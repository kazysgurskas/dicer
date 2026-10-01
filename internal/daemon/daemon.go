// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

// Package daemon assembles and runs dicerd: it loads the configuration,
// wires the services together and manages the process lifecycle.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/konradasb/dicer/internal/dns"
	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
	"github.com/konradasb/dicer/internal/filestore"
	"github.com/konradasb/dicer/internal/hostfs"
	"github.com/konradasb/dicer/internal/hostinfo"
	"github.com/konradasb/dicer/internal/hostnet"
	"github.com/konradasb/dicer/internal/hypervisor"
	"github.com/konradasb/dicer/internal/image"
	"github.com/konradasb/dicer/internal/initrd"
	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/kernel"
	"github.com/konradasb/dicer/internal/metric"
	"github.com/konradasb/dicer/internal/network"
	"github.com/konradasb/dicer/internal/registry"
	"github.com/konradasb/dicer/internal/token"
	"github.com/konradasb/dicer/internal/version"
	"github.com/konradasb/dicer/internal/virtiofs"
	"github.com/konradasb/dicer/internal/volume"
)

// daemon is the Dicer host daemon. It manages one machine's virtual machines.
type daemon struct {
	cfg    *Config
	logger *slog.Logger

	store           *filestore.Store
	networkManager  *network.Manager
	instanceManager *instance.Manager
	hostNetwork     *hostnet.Host
	// dnsServers serves each network's guests their nameserver. Nil if
	// the configuration turns it off.
	dnsServers    *dns.Servers
	imageManager  *image.Manager
	kernelManager *kernel.Manager
	volumeManager *volume.Manager
	tokenManager  *token.Manager
	initrdManager *initrd.Manager

	// starters launch VMMs, one for each hypervisor version this daemon
	// carries, by type with the default version first.
	starters map[hypervisor.Type][]hypervisor.Starter

	metrics *metric.Registry
	events  *event.Log
}

// newDaemon returns a daemon for cfg, which must be valid, as loadConfig
// returns it. Nothing is opened or started until Run.
func newDaemon(cfg *Config) (*daemon, error) {
	level, err := cfg.logLevel()
	if err != nil {
		return nil, err
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	d := &daemon{cfg: cfg, logger: logger}
	d.metrics = d.newMetrics()

	return d, nil
}

// Run starts the daemon and blocks until the context is cancelled.
func (d *daemon) Run(ctx context.Context) error {
	d.logger.Info("starting Dicer",
		"version", version.Version, "commit", version.Commit, "date", version.BuildDate)

	if err := d.openStore(); err != nil {
		return err
	}
	if err := d.openEvents(); err != nil {
		return err
	}
	if err := d.openNetworks(); err != nil {
		return err
	}

	if err := d.initServices(); err != nil {
		return err
	}
	d.registerMetrics()

	// Deferred first so it runs last: the instance manager records events
	// until it is closed.
	defer func() {
		if err := d.events.Close(); err != nil {
			d.logger.Warn("not every event reached the events file", "error", err)
		}
	}()
	defer d.hostNetwork.Close()

	// Deferred before the instance manager's close, so it runs after: the
	// instance manager stops networks' DNS servers until it is closed.
	if d.dnsServers != nil {
		defer d.dnsServers.Close()
	}

	// Reconcile recorded state with what is running before serving.
	d.instanceManager.Recover(ctx)
	defer d.instanceManager.Close()
	d.instanceManager.WarnDeprecatedHypervisorVersions(ctx)

	// Created before serving, so that no request finds either missing.
	if err := d.networkManager.EnsureDefault(d.cfg.Network.DefaultSubnet); err != nil {
		if errors.Is(err, errdefs.ErrExists) {
			return fmt.Errorf("%w; set network.default_subnet to a free subnet", err)
		}
		return err
	}
	if err := d.kernelManager.EnsureDefault(); err != nil {
		return err
	}

	listeners, err := d.listen(ctx)
	if err != nil {
		return err
	}

	serveErr := make(chan error, len(listeners))
	for _, l := range listeners {
		go func() {
			d.logger.Info("serving the API", "transport", l.transport, "address", l.address)
			if err := l.server.Serve(l.listener); err != nil {
				serveErr <- fmt.Errorf("serve the API on %s: %w", l.address, err)
			}
		}()
	}

	// Background work is waited for before the instance manager and events
	// log are closed.
	ctx, cancel := context.WithCancel(ctx)
	var background sync.WaitGroup
	defer background.Wait()
	defer cancel()

	metricsErr := make(chan error, 1)
	background.Go(func() {
		if err := d.serveMetrics(ctx); err != nil {
			metricsErr <- err
		}
	})

	background.Go(func() { d.hostNetwork.WatchFirewalld(ctx) })

	// Started after the API is up so slow boots do not delay it.
	background.Go(func() { d.instanceManager.StartOnBoot(ctx) })
	background.Go(func() { d.instanceManager.StandbyIdle(ctx) })

	// Garbage collection needs recovery to know which images are in use.
	if policy := d.cfg.Images.gcPolicy(); policy.Enabled() {
		background.Go(func() { d.imageManager.RunGC(ctx, policy, d.cfg.Images.GCInterval, d.instanceManager.ImagesInUse) })
	}

	select {
	case err := <-serveErr:
		return err
	case err := <-metricsErr:
		return err
	case <-ctx.Done():
	}

	d.logger.Info("shutting down")
	stopServers(listeners, apiDrainTimeout)

	// Running VMs outlive the daemon; the next start re-adopts them.
	d.logger.Info("stopped, leaving running instances alone")
	return nil
}

// apiDrainTimeout bounds how long a shutdown waits for calls in flight, such
// as followed logs or exec sessions.
const apiDrainTimeout = 10 * time.Second

// stopServers stops every server concurrently with stopServer.
func stopServers(listeners []listener, timeout time.Duration) {
	var wg sync.WaitGroup
	for _, l := range listeners {
		wg.Go(func() { stopServer(l.server, timeout) })
	}
	wg.Wait()
}

// stopServer stops server gracefully, forcing it after timeout.
func stopServer(server *grpc.Server, timeout time.Duration) {
	stopped := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(timeout):
		server.Stop()
		<-stopped
	}
}

// openStore opens the store of instance, snapshot, network, volume, kernel and
// token definitions.
func (d *daemon) openStore() error {
	var err error
	d.store, err = filestore.New(filestore.Config{
		DataDir: d.cfg.DataDir,
		Logger:  d.logger,
	})
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}

	return nil
}

// openEvents opens the event log, which the managers record into.
func (d *daemon) openEvents() error {
	var err error
	d.events, err = event.Open(event.Config{
		File:     filepath.Join(d.cfg.DataDir, eventsFile),
		MaxCount: d.cfg.Events.MaxCount,
		MaxAge:   d.cfg.Events.MaxAge,
		Logger:   d.logger,
	})
	if err != nil {
		return fmt.Errorf("open events: %w", err)
	}
	return nil
}

// openNetworks opens the network manager, which defines the networks and
// keeps their address allocations.
func (d *daemon) openNetworks() error {
	var err error
	d.networkManager, err = network.NewManager(network.Config{
		Dir:         filepath.Join(d.cfg.DataDir, "allocations"),
		Store:       d.store,
		HostSubnets: hostnet.Subnets,
		Events:      d.events,
		Logger:      d.logger,
	})
	if err != nil {
		return fmt.Errorf("open network manager: %w", err)
	}

	return nil
}

// eventsFile is the events log, in the data directory.
const eventsFile = "events.jsonl"

// initServices creates the managers the API and the instance manager use,
// and the instance manager itself.
func (d *daemon) initServices() error {
	auths := make(map[string]registry.Auth, len(d.cfg.Registries))
	for host, r := range d.cfg.Registries {
		auths[host] = r.auth()
	}
	registryClient, err := registry.NewClient(d.cfg.DataDir,
		registry.WithLogger(d.logger), registry.WithKeychain(registry.NewKeychain(auths)))
	if err != nil {
		return fmt.Errorf("create registry client: %w", err)
	}

	d.imageManager, err = image.NewManager(image.Config{
		DataDir:            d.cfg.DataDir,
		MaxConcurrentPulls: 1,
		Registry:           registryClient,
		Events:             d.events,
		Logger:             d.logger,
	})
	if err != nil {
		return fmt.Errorf("create image manager: %w", err)
	}

	d.hostNetwork = hostnet.NewHost(hostnet.Config{
		UplinkInterface:         d.cfg.Network.UplinkInterface,
		UploadBurstMultiplier:   d.cfg.Network.UploadBurstMultiplier,
		DownloadBurstMultiplier: d.cfg.Network.DownloadBurstMultiplier,
		ListenerPort:            d.cfg.Server.ListenerPort(),
		Logger:                  d.logger,
	})

	d.kernelManager, err = kernel.NewManager(kernel.Config{
		DataDir: d.cfg.DataDir,
		Store:   d.store,
		Events:  d.events,
		Logger:  d.logger,
	})
	if err != nil {
		return fmt.Errorf("create kernel manager: %w", err)
	}

	d.initrdManager, err = initrd.NewManager(initrd.Config{
		Puller:  registryClient,
		DataDir: d.cfg.DataDir,
		Logger:  d.logger,
	})
	if err != nil {
		return fmt.Errorf("create initrd manager: %w", err)
	}

	d.tokenManager = token.NewManager(token.Config{Store: d.store})

	d.volumeManager = volume.NewManager(volume.Config{
		DataDir: d.cfg.DataDir,
		Store:   d.store,
		Events:  d.events,
		Logger:  d.logger,
	})

	d.starters, err = buildStarters(d.cfg.DataDir)
	if err != nil {
		return fmt.Errorf("build hypervisor starters: %w", err)
	}

	capacity, err := d.hostCapacity()
	if err != nil {
		return err
	}

	// virtiofsd is set up only where some directory may be mounted. Without
	// it, instances cannot mount directories, and the rest of the daemon
	// works.
	var shares *virtiofs.Daemon
	if len(d.cfg.Mounts.AllowedDirectories) > 0 {
		if shares, err = d.newDirectoryShares(); err != nil {
			d.logger.Warn("instances cannot mount host directories", "reason", err)
		}
	}

	instanceCfg := instance.Config{
		Store:       d.store,
		Networks:    d.networkManager,
		RunDir:      d.cfg.RunDir,
		Images:      d.imageManager,
		Kernels:     d.kernelManager,
		Volumes:     d.volumeManager,
		Initrds:     d.initrdManager,
		HostNetwork: d.hostNetwork,
		Starters:    d.starters,
		Capacity:    capacity,
		Events:      d.events,
		Logger:      d.logger,

		AllowedDirectories: hostfs.AllowedDirectories(d.cfg.Mounts.AllowedDirectories),
	}
	if d.cfg.Network.DNS {
		// The servers ask the instance manager about the networks'
		// instances, and it starts and stops the servers.
		d.dnsServers = dns.NewServers(dns.Config{
			Resolver:           instanceNames{d},
			DefaultNameservers: []string{network.DefaultNameserver},
			Logger:             d.logger,
		})

		instanceCfg.DNSServers = d.dnsServers
	}
	if shares != nil {
		instanceCfg.Shares = shares
	}
	d.instanceManager = instance.NewManager(instanceCfg)

	return nil
}

// instanceNames answers the DNS servers' questions about the networks'
// instances, from the instance manager, which is made after them.
type instanceNames struct{ d *daemon }

// LookupHost asks the instance manager.
func (n instanceNames) LookupHost(network, name string) []netip.Addr {
	return n.d.instanceManager.LookupHost(network, name)
}

// LookupAddr asks the instance manager.
func (n instanceNames) LookupAddr(network string, addr netip.Addr) []string {
	return n.d.instanceManager.LookupAddr(network, addr)
}

// hostCapacity reads the host's CPUs and memory once and works out what
// instances may be given.
func (d *daemon) hostCapacity() (instance.Capacity, error) {
	cpus, err := hostinfo.CPUCount()
	if err != nil {
		return instance.Capacity{}, fmt.Errorf("read the host's CPUs: %w", err)
	}
	memory, err := hostinfo.MemoryTotal()
	if err != nil {
		return instance.Capacity{}, fmt.Errorf("read the host's memory: %w", err)
	}

	capacity, err := d.cfg.Resources.capacity(cpus, memory)
	if err != nil {
		return instance.Capacity{}, err
	}

	allocatable := capacity.Allocatable()
	d.logger.Info("admission capacity",
		"cpus", cpus, "memory_bytes", memory,
		"cpu_overcommit", capacity.CPUOvercommit, "memory_overcommit", capacity.MemoryOvercommit,
		"reserved_memory_bytes", capacity.ReservedMemoryBytes,
		"allocatable_vcpus", allocatable.VCPUs, "allocatable_memory_bytes", allocatable.MemoryBytes)

	return capacity, nil
}

// newDirectoryShares returns what shares host directories with guests: the
// virtiofsd dicerd embeds, extracted beside the hypervisors. It is run in the
// host's mount namespace, which has the data directory where the daemon's
// does.
func (d *daemon) newDirectoryShares() (*virtiofs.Daemon, error) {
	path, err := virtiofs.Extract(filepath.Join(d.cfg.DataDir, "bin", "virtiofsd", virtiofs.Version, "virtiofsd"))
	if err != nil {
		return nil, err
	}
	return virtiofs.New(path)
}
