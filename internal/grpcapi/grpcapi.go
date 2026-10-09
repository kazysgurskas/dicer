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

	"github.com/konradasb/dicer/internal/event"
	"github.com/konradasb/dicer/internal/hypervisor"
	"github.com/konradasb/dicer/internal/image"
	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/kernel"
	"github.com/konradasb/dicer/internal/network"
	"github.com/konradasb/dicer/internal/token"
	"github.com/konradasb/dicer/internal/volume"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// Config holds the dependencies for creating a Server.
type Config struct {
	NetworkManager  *network.Manager
	InstanceManager *instance.Manager

	Starters      map[hypervisor.Type][]hypervisor.Starter
	ImageManager  *image.Manager
	KernelManager *kernel.Manager
	VolumeManager *volume.Manager
	TokenManager  *token.Manager

	// Events is the event log GetEvents reads.
	Events *event.Log

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

	// DataDir is the data directory, whose disk GetResources reports on.
	DataDir string

	// Version is the daemon's version, as GetHostInfo reports it.
	Version string
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

	metrics metrics
}

// NewServer creates a Server with the given configuration.
func NewServer(cfg Config) *Server {
	return &Server{
		instanceHandler: instanceHandler{
			instanceManager: cfg.InstanceManager,

			statsInterval: instanceStatsInterval,
		},
		snapshotHandler: snapshotHandler{instanceManager: cfg.InstanceManager},
		networkHandler: networkHandler{
			networkManager:  cfg.NetworkManager,
			instanceManager: cfg.InstanceManager,
		},
		volumeHandler: volumeHandler{volumeManager: cfg.VolumeManager},
		kernelHandler: kernelHandler{kernelManager: cfg.KernelManager},
		tokenHandler: tokenHandler{
			tokenManager: cfg.TokenManager,
			servesTCP:    cfg.ListenAddress != "",
			fingerprint:  cfg.TokenFingerprint,
		},
		imageHandler: imageHandler{
			instanceManager: cfg.InstanceManager,
			imageManager:    cfg.ImageManager,
		},
		resourceHandler: resourceHandler{
			volumeManager:   cfg.VolumeManager,
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
		metrics:       newMetrics(),
	}
}

// Register registers the server's services with a gRPC server.
func (s *Server) Register(gs *grpc.Server) {
	dicerdv1.RegisterDaemonServiceServer(gs, s)
}
