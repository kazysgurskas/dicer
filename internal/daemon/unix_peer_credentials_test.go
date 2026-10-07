// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"

	"github.com/konradasb/dicer"
	"github.com/konradasb/dicer/internal/grpcapi"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// TestUnixPeerCredentialsNameTheCaller checks that a call on the Unix socket
// carries the user and process that made it, for the audit.
func TestUnixPeerCredentialsNameTheCaller(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dicer.sock")
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}

	peers := make(chan peer.Peer, 1)
	s := grpc.NewServer(grpc.Creds(unixPeerCredentials{}), grpc.UnaryInterceptor(
		func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			if p, ok := peer.FromContext(ctx); ok {
				peers <- *p
			}
			return handler(ctx, req)
		}))
	dicerdv1.RegisterDaemonServiceServer(s, hostInfoServer{})
	go func() { _ = s.Serve(listener) }()
	t.Cleanup(s.Stop)

	c, err := dicer.NewClient(dicer.WithAddress("unix://" + path))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.GetHostInfo(t.Context(), &dicerdv1.GetHostInfoRequest{}); err != nil {
		t.Fatalf("GetHostInfo: %v", err)
	}

	p := <-peers
	got, ok := p.AuthInfo.(grpcapi.UnixPeer)
	if !ok {
		t.Fatalf("auth info = %#v, want a UnixPeer", p.AuthInfo)
	}
	if got.UID != os.Getuid() || got.PID != os.Getpid() {
		t.Errorf("peer = uid %d, pid %d; want uid %d, pid %d", got.UID, got.PID, os.Getuid(), os.Getpid())
	}
}
