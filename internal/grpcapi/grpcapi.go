// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

// Package grpcapi implements the DaemonService gRPC service and the
// interceptors in front of it: those that authenticate a call's token,
// authorize it by the token's scopes, and audit it.
//
// Handlers validate the request, call the store or the instance manager, and
// convert the result. They take their dependencies as concrete types.
package grpcapi

import (
	"net/netip"

	"google.golang.org/grpc"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/events"
	"github.com/konradasb/dicer/internal/filestore"
	"github.com/konradasb/dicer/internal/hypervisor"
	"github.com/konradasb/dicer/internal/image"
	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/kernel"
	"github.com/konradasb/dicer/internal/network"
	"github.com/konradasb/dicer/internal/volume"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// Config holds the dependencies for creating a Server.
type Config struct {
	Store           *filestore.Store
	NetworkManager  *network.Manager
	InstanceManager *instance.Manager

	Starters      map[hypervisor.Type][]hypervisor.Starter
	ImageManager  *image.Manager
	KernelManager *kernel.Manager
	VolumeManager *volume.Manager

	// Events is the event log GetEvents reads and the handlers record to.
	// Nil records nothing.
	Events *events.Log

	// ListenAddress is the address the TCP listener is bound to, such as
	// [::]:9000, or empty if the API is not served over TCP.
	ListenAddress string

	// HostAddresses returns the host's own addresses, which a TCP listener
	// on all of them is reached at. Nil reports the listener's address as it
	// is.
	HostAddresses func() ([]netip.Addr, error)

	// Fingerprint is the fingerprint of the certificate the daemon is served
	// with over TCP, or empty if it is not.
	Fingerprint string

	// TokenFingerprint is the fingerprint every token carries, for clients
	// to check the daemon by: Fingerprint, or empty for a certificate
	// clients verify for themselves.
	TokenFingerprint string

	// HostSubnets returns the subnets of the host's interfaces, which a new
	// network may not overlap. Nil skips the check.
	HostSubnets func() ([]netip.Prefix, error)

	// DataDir is the data directory, whose disk GetResources reports on.
	DataDir string

	// Version is the daemon's version, as GetHostInfo reports it.
	Version string
}

// recorder records what happens to the resources the API changes.
type recorder interface {
	Record(e events.Event)
}

// discardRecorder is the recorder used when no event log is configured.
type discardRecorder struct{}

func (discardRecorder) Record(events.Event) {}

// recorderOf returns log as a recorder, or one that discards if log is nil.
func recorderOf(log *events.Log) recorder {
	if log == nil {
		return discardRecorder{}
	}

	return log
}

// Server implements dicerdv1.DaemonServiceServer through its embedded
// handlers.
type Server struct {
	instanceHandler
	snapshotHandler
	networkHandler
	volumeHandler
	kernelHandler
	tokenHandler
	imageHandler
	hostHandler
	resourceHandler
	eventsHandler
}

// NewServer creates a Server with the given configuration.
func NewServer(cfg Config) *Server {
	return &Server{
		instanceHandler: instanceHandler{
			store:           cfg.Store,
			instanceManager: cfg.InstanceManager,

			statsInterval: instanceStatsInterval,
		},
		snapshotHandler: snapshotHandler{store: cfg.Store, instanceManager: cfg.InstanceManager},
		networkHandler: networkHandler{
			store:          cfg.Store,
			networkManager: cfg.NetworkManager,
			hostSubnets:    cfg.HostSubnets,
			events:         recorderOf(cfg.Events),
		},
		volumeHandler: volumeHandler{
			store:         cfg.Store,
			volumeManager: cfg.VolumeManager,
			events:        recorderOf(cfg.Events),
		},
		kernelHandler: kernelHandler{
			store:         cfg.Store,
			kernelManager: cfg.KernelManager,
			events:        recorderOf(cfg.Events),
		},
		tokenHandler: tokenHandler{
			store:       cfg.Store,
			servesTCP:   cfg.ListenAddress != "",
			fingerprint: cfg.TokenFingerprint,
		},
		imageHandler: imageHandler{
			store:           cfg.Store,
			instanceManager: cfg.InstanceManager,
			imageManager:    cfg.ImageManager,
		},
		resourceHandler: resourceHandler{
			store:           cfg.Store,
			instanceManager: cfg.InstanceManager,
			dataDir:         cfg.DataDir,
		},
		hostHandler: hostHandler{
			version:       cfg.Version,
			starters:      cfg.Starters,
			listenAddress: cfg.ListenAddress,
			hostAddresses: cfg.HostAddresses,
			fingerprint:   cfg.Fingerprint,
		},
		eventsHandler: eventsHandler{events: cfg.Events},
	}
}

// Register registers the server's services with a gRPC server.
func (s *Server) Register(gs *grpc.Server) {
	dicerdv1.RegisterDaemonServiceServer(gs, s)
}

// refuseInUse returns an ErrInvalidState error naming the first instance
// for which inUse is true, or nil. what reads like `kernel "k" is in use`.
func refuseInUse(store *filestore.Store, what string, inUse func(instance.Spec) bool) error {
	for _, instance := range store.Instances() {
		if inUse(instance) {
			return errdefs.InvalidState("%s by instance %q", what, instance.Name)
		}
	}
	return nil
}
