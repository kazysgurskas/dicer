// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dns

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"sync"

	"github.com/konradasb/dicer/internal/types"
)

// Port is the port guests ask on.
const Port = 53

// Config configures [Servers].
type Config struct {
	// Resolver knows the networks' instances.
	Resolver Resolver
	// DefaultUpstreams are the nameservers a network that names none
	// forwards to.
	DefaultUpstreams []string
	// Port overrides the port listened on, for tests. Zero means [Port].
	Port int
	// Logger is optional.
	Logger *slog.Logger
}

// Servers runs a server for each network that has one.
type Servers struct {
	cfg    Config
	logger *slog.Logger

	mu      sync.Mutex
	servers map[string]*running
}

// running is a network's server, and what it was started with.
type running struct {
	server  *server
	listen  string
	network network
}

// NewServers returns servers for networks, none of them started.
func NewServers(cfg Config) *Servers {
	if cfg.Port == 0 {
		cfg.Port = Port
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	return &Servers{
		cfg:     cfg,
		logger:  cfg.Logger.With("component", "dns"),
		servers: make(map[string]*running),
	}
}

// Serve starts a server for nw on its gateway address, unless one is
// already serving it as it is: it is restarted if the network's gateway or
// upstreams have changed. The gateway address must already be on the host,
// on the network's bridge.
func (s *Servers) Serve(ctx context.Context, nw types.Network) error {
	want, listenAddr, err := s.describe(nw)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if r, ok := s.servers[nw.Name]; ok {
		if r.listen == listenAddr && slices.Equal(r.network.upstreams, want.upstreams) &&
			r.network.gateway == want.gateway &&
			r.network.subnet == want.subnet && r.network.local == want.local {
			return nil
		}
		r.server.close()
		delete(s.servers, nw.Name)
	}

	srv, err := listen(ctx, listenAddr, want, s.cfg.Resolver, s.logger)
	if err != nil {
		return fmt.Errorf("serve DNS for network %q on %s: %w", nw.Name, listenAddr, err)
	}
	s.servers[nw.Name] = &running{server: srv, listen: listenAddr, network: want}

	s.logger.InfoContext(ctx, "serving DNS", "network", nw.Name, "address", srv.addr(), "upstreams", want.upstreams)
	return nil
}

// describe works out what a network's server is: what it serves and where.
func (s *Servers) describe(nw types.Network) (network, string, error) {
	subnet, err := netip.ParsePrefix(nw.Subnet)
	if err != nil {
		return network{}, "", fmt.Errorf("network %q: subnet: %w", nw.Name, err)
	}
	gateway, err := netip.ParseAddr(nw.Gateway)
	if err != nil {
		return network{}, "", fmt.Errorf("network %q: gateway: %w", nw.Name, err)
	}

	nameservers := nw.Nameservers
	if len(nameservers) == 0 {
		nameservers = s.cfg.DefaultUpstreams
	}
	upstreams := make([]string, 0, len(nameservers))
	for _, ns := range nameservers {
		upstreams = append(upstreams, net.JoinHostPort(ns, strconv.Itoa(Port)))
	}

	return network{
		name:      nw.Name,
		subnet:    subnet.Masked(),
		gateway:   gateway,
		upstreams: upstreams,
		local:     !nw.Isolated,
	}, net.JoinHostPort(gateway.String(), strconv.Itoa(s.cfg.Port)), nil
}

// Stop stops a network's server, if it has one.
func (s *Servers) Stop(networkName string) {
	s.mu.Lock()
	r, ok := s.servers[networkName]
	delete(s.servers, networkName)
	s.mu.Unlock()

	if ok {
		r.server.close()
		s.logger.Info("stopped serving DNS", "network", networkName)
	}
}

// Close stops every server.
func (s *Servers) Close() {
	s.mu.Lock()
	servers := s.servers
	s.servers = make(map[string]*running)
	s.mu.Unlock()

	for _, r := range servers {
		r.server.close()
	}
}
