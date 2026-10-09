// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	"github.com/konradasb/dicer/internal/grpcapi"
	"github.com/konradasb/dicer/internal/version"
)

// The transports the API is served on.
const (
	transportUnix = "unix"
	transportTCP  = "tcp"
)

// apiListener is one transport the API is served on, and the server behind
// it.
type apiListener struct {
	transport string
	address   string
	listener  net.Listener
	server    *grpc.Server
}

// listenAPI opens every configured transport, each with its own server since
// credentials are per server.
func (d *daemon) listenAPI(ctx context.Context) (listeners []apiListener, err error) {
	defer func() {
		if err != nil {
			for _, l := range listeners {
				_ = l.listener.Close()
			}
		}
	}()

	socket, err := listenSocket(ctx, d.cfg.Server.Socket)
	if err != nil {
		return nil, err
	}

	var listenAddress, fingerprint string
	var tcp net.Listener
	var tlsConfig *tls.Config

	if d.cfg.Server.ServesTCP() {
		tlsConfig, fingerprint, err = serverTLSConfig(d.cfg.Server, d.cfg.DataDir, d.logger)
		if err != nil {
			_ = socket.Close()
			return nil, err
		}
		tcp, err = (&net.ListenConfig{}).Listen(ctx, "tcp", d.cfg.Server.Listen)
		if err != nil {
			_ = socket.Close()
			return nil, fmt.Errorf("listen on %s: %w", d.cfg.Server.Listen, err)
		}

		listenAddress = tcp.Addr().String()
	}

	// Tokens carry the fingerprint of the daemon's own certificate, not of
	// one clients verify for themselves.
	tokenFingerprint := fingerprint
	if d.cfg.Server.CrtFile != "" {
		tokenFingerprint = ""
	}

	api := grpcapi.NewServer(grpcapi.Config{
		Starters:      d.starters,
		ListenAddress: listenAddress,
		HostAddresses: func() ([]netip.Addr, error) {
			return d.hostNetwork.Addresses(d.networkManager.Networks())
		},
		Fingerprint:      fingerprint,
		TokenFingerprint: tokenFingerprint,
		NetworkManager:   d.networkManager,
		InstanceManager:  d.instanceManager,
		ImageManager:     d.imageManager,
		KernelManager:    d.kernelManager,
		VolumeManager:    d.volumeManager,
		TokenManager:     d.tokenManager,
		Events:           d.events,
		DataDir:          d.cfg.DataDir,
		Version:          version.Version,
	})
	d.metrics.Register(api)

	listeners = make([]apiListener, 0, 2)
	//nolint:contextcheck // interceptors run with each call's own context
	listeners = append(listeners, apiListener{
		transport: transportUnix,
		address:   d.cfg.Server.Socket.Path,
		listener:  socket,
		server:    d.newGRPCServer(api, nil, grpc.Creds(unixPeerCredentials{})),
	})

	if tcp == nil {
		return listeners, nil
	}

	d.logger.Info("the API is served over TCP, to clients with a token",
		"listen", listenAddress, "fingerprint", fingerprint, "tokens", len(d.tokenManager.Tokens()))

	authentication := grpcapi.NewAuthentication(d.tokenManager, d.logger)
	return append(listeners, apiListener{
		transport: transportTCP,
		address:   listenAddress,
		listener:  tcp,
		server:    d.newGRPCServer(api, authentication, grpc.Creds(credentials.NewTLS(tlsConfig))), //nolint:contextcheck // interceptors run with each call's own context
	}), nil
}

// newGRPCServer builds an API server with metrics and audit interceptors,
// which see the status each call ends with, and inside them the conversion
// of handlers' errors to statuses. With authentication, only calls with a
// token reach the audit, and innermost the token's scopes must allow the
// call, so that a refused call is audited too. It keeps connections alive
// as server.keepalive says.
func (d *daemon) newGRPCServer(
	api *grpcapi.Server, authentication *grpcapi.Authentication, opts ...grpc.ServerOption,
) *grpc.Server {
	audit := grpcapi.NewAudit(d.logger)

	unary := []grpc.UnaryServerInterceptor{api.UnaryMetricsInterceptor()}
	stream := []grpc.StreamServerInterceptor{api.StreamMetricsInterceptor()}
	if authentication != nil {
		unary = append(unary, authentication.UnaryInterceptor())
		stream = append(stream, authentication.StreamInterceptor())
	}
	unary = append(unary, audit.UnaryInterceptor(), grpcapi.UnaryStatusInterceptor)
	stream = append(stream, audit.StreamInterceptor(), grpcapi.StreamStatusInterceptor)
	if authentication != nil {
		unary = append(unary, grpcapi.UnaryAuthorizationInterceptor)
		stream = append(stream, grpcapi.StreamAuthorizationInterceptor)
	}

	opts = append(opts, keepaliveOptions(d.cfg.Server.Keepalive)...)
	s := grpc.NewServer(append(opts,
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(stream...),
	)...)
	api.Register(s)

	return s
}

// keepaliveOptions returns the server options that ping quiet clients and
// let clients ping, as cfg says.
func keepaliveOptions(cfg KeepaliveConfig) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: cfg.Interval, Timeout: cfg.Timeout}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: cfg.MinClientInterval, PermitWithoutStream: true}),
	}
}

// listenSocket opens the API socket, removing a stale one left by a previous
// run.
func listenSocket(ctx context.Context, cfg SocketConfig) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o750); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}

	if err := removeStaleSocket(ctx, cfg.Path); err != nil {
		return nil, err
	}

	// Create the socket owner-only, then chmod, so nobody can connect
	// before its mode is set.
	oldUmask := unix.Umask(0o177)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "unix", cfg.Path)
	unix.Umask(oldUmask)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", cfg.Path, err)
	}

	// Its group before its mode, for the same reason.
	gid, err := cfg.gid()
	if err == nil && gid >= 0 {
		err = os.Chown(cfg.Path, -1, gid)
	}
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("set socket group: %w", err)
	}

	mode := cfg.Mode
	if mode == 0 {
		mode = defaultSocketMode
	}
	if err := os.Chmod(cfg.Path, os.FileMode(mode)); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("set socket permissions: %w", err)
	}

	return listener, nil
}

// removeStaleSocket removes a socket left by an unclean shutdown. It refuses
// to remove a non-socket or a socket another daemon still answers on.
func removeStaleSocket(ctx context.Context, path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check for a stale socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a socket", path)
	}

	dialCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if conn, err := (&net.Dialer{}).DialContext(dialCtx, "unix", path); err == nil {
		_ = conn.Close()
		return fmt.Errorf("another daemon is already serving on %s", path)
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return nil
}
