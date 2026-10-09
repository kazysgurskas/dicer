// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/konradasb/dicer/internal/token"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// TestNewClientOverASocket checks the local case end to end: a client with
// the socket's address reaches the server behind it.
func TestNewClientOverASocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dicer.sock")
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, health.NewServer())
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	c, err := NewClient(WithAddress("unix://" + path))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = c.Close() }()

	if _, err := healthpb.NewHealthClient(c.conn).Check(t.Context(), &healthpb.HealthCheckRequest{}); err != nil {
		t.Errorf("a call over the socket failed: %v", err)
	}
}

// TestNewClientWithNoSocket checks that a socket that is not there is reported
// at once, rather than by the first call, and names what is missing.
func TestNewClientWithNoSocket(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "dicer.sock")

	c, err := NewClient(WithAddress("unix://" + missing))
	if err == nil {
		_ = c.Close()
		t.Fatal("NewClient succeeded")
	}
	if !strings.Contains(err.Error(), "is dicerd running?") || !strings.Contains(err.Error(), missing) {
		t.Errorf("NewClient = %v, want it to name the missing socket", err)
	}
}

// TestNewClientDefaultsToTheLocalDaemon checks that a client goes to the
// local daemon's socket unless told otherwise.
func TestNewClientDefaultsToTheLocalDaemon(t *testing.T) {
	if DefaultAddress != "unix:///run/dicer/dicer.sock" {
		t.Errorf("DefaultAddress = %q", DefaultAddress)
	}

	c, err := NewClient()
	if err == nil {
		_ = c.Close()
		t.Skip("a daemon is running on this machine")
	}
	if !strings.Contains(err.Error(), "/run/dicer/dicer.sock") {
		t.Errorf("NewClient() = %v, want it to name the default socket", err)
	}
}

// TestNewClientOverTCP checks that a TCP address is connected to lazily, so
// a client is made without a daemon there.
func TestNewClientOverTCP(t *testing.T) {
	secret := token.NewSecret()
	pinned := token.Format(secret, strings.Repeat("ab", 32))
	unpinned := token.Format(secret, "")

	tests := []struct {
		name string
		opts []Option
	}{
		{"a token with a fingerprint", []Option{WithAddress("192.0.2.1:7443"), WithToken(pinned)}},
		{"a token without one", []Option{WithAddress("dns:///dicer.example.com:7443"), WithToken(unpinned)}},
		{"a token and TLS", []Option{
			WithAddress("192.0.2.1:7443"),
			WithToken(unpinned),
			WithTLS(&tls.Config{MinVersion: tls.VersionTLS13}),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewClient(tt.opts...)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			_ = c.Close()
		})
	}
}

// TestNewClientRefusesWhatCannotWork checks that a client that could only be
// refused, or could only send its token in the clear, is not made.
func TestNewClientRefusesWhatCannotWork(t *testing.T) {
	// NewClient only looks for the socket, so a file will do.
	socket := filepath.Join(t.TempDir(), "dicer.sock")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	valid := token.Format(token.NewSecret(), "")

	tests := []struct {
		name string
		opts []Option
		want string
	}{
		{"TCP without a token", []Option{WithAddress("192.0.2.1:7443")}, "needs a token"},
		{"TCP with TLS but no token", []Option{
			WithAddress("192.0.2.1:7443"), WithTLS(&tls.Config{MinVersion: tls.VersionTLS13}),
		}, "needs a token"},
		{"a token that is not one", []Option{WithAddress("192.0.2.1:7443"), WithToken("ghp_abc")}, "invalid token"},
		{"a token for a socket", []Option{WithAddress("unix://" + socket), WithToken(valid)}, "not its socket"},
		{"a token and the default address", []Option{WithToken(valid)}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewClient(tt.opts...)
			if err == nil {
				_ = c.Close()
				t.Fatal("NewClient succeeded")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("NewClient = %v, want it to say %q", err, tt.want)
			}
		})
	}
}

// TestTokenIsSentToTheDaemonItWasMadeFor checks both halves of a token: the
// client sends it with every call, and sends nothing to a daemon whose
// certificate is not the one its fingerprint pins.
func TestTokenIsSentToTheDaemonItWasMadeFor(t *testing.T) {
	daemon := &fakeDaemon{}
	address, value := serve(t, daemon)

	c, err := NewClient(WithAddress(address), WithToken(value))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.Instances.Get(t.Context(), "web"); !errors.Is(err, ErrUnimplemented) {
		t.Errorf("a call with the token = %v, want the daemon's answer", err)
	}

	// The same secret, pinned to another daemon.
	secret, _, _ := token.Parse(value)
	impostor, err := NewClient(WithAddress(address), WithToken(token.Format(secret, strings.Repeat("ab", 32))))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = impostor.Close() })
	_, err = impostor.Instances.Get(t.Context(), "web")
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "certificate fingerprint mismatch") {
		t.Errorf("a call to a daemon of another fingerprint = %v, want ErrUnavailable saying why", err)
	}

	// A token the daemon does not know is refused by it.
	stranger, err := NewClient(WithAddress(address), WithToken(token.Format(token.NewSecret(), daemon.fingerprint)))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = stranger.Close() })
	if _, err := stranger.Instances.Get(t.Context(), "web"); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a call with another token = %v, want ErrUnauthenticated", err)
	}
}

// TestKeepaliveGivesUpOnADaemonThatStopsAnswering checks that a call in
// flight fails once the daemon stops answering without closing the
// connection, rather than waiting for as long as its context lets it.
func TestKeepaliveGivesUpOnADaemonThatStopsAnswering(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for a keepalive ping, which gRPC sends 10s apart at the soonest")
	}

	daemon := &stuckDaemon{called: make(chan struct{})}
	address, value := serve(t, daemon)
	blackhole := newBlackhole(t, address)

	c, err := NewClient(WithAddress(blackhole.address()), WithToken(value), WithKeepalive(10*time.Second, time.Second))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := c.HostInfo(ctx)
		done <- err
	}()

	<-daemon.called
	blackhole.drop()

	select {
	case err := <-done:
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("call = %v, want ErrUnavailable", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the call still waits 30s after the daemon stopped answering")
	}
}

// stuckDaemon answers GetHostInfo never, as a daemon on a frozen host.
type stuckDaemon struct {
	dicerdv1.UnimplementedDaemonServiceServer

	// called is closed once the call has arrived.
	called chan struct{}
}

// GetHostInfo waits for the call to be given up.
func (d *stuckDaemon) GetHostInfo(ctx context.Context, _ *dicerdv1.GetHostInfoRequest) (*dicerdv1.GetHostInfoResponse, error) {
	close(d.called)
	<-ctx.Done()
	return nil, ctx.Err()
}

// serve serves daemon on loopback as a daemon's TCP listener does: over TLS
// with a certificate of its own, to calls with its token. It returns the
// address to reach it at and the token's value. A daemon that wants to know
// its fingerprint, as fakeDaemon does, is told it.
func serve(t *testing.T, daemon dicerdv1.DaemonServiceServer) (address, value string) {
	t.Helper()

	cert, fingerprint := newTestCertificate(t)
	if d, ok := daemon.(*fakeDaemon); ok {
		d.fingerprint = fingerprint
	}
	value = token.Format(token.NewSecret(), fingerprint)

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	checkToken := func(ctx context.Context) error {
		md, _ := metadata.FromIncomingContext(ctx)
		if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer "+value {
			return status.Error(codes.Unauthenticated, "no such token")
		}
		return nil
	}
	server := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}})),
		grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			if err := checkToken(ctx); err != nil {
				return nil, err
			}
			return handler(ctx, req)
		}),
		grpc.StreamInterceptor(func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			if err := checkToken(ss.Context()); err != nil {
				return err
			}
			return handler(srv, ss)
		}),
	)
	dicerdv1.RegisterDaemonServiceServer(server, daemon)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	return listener.Addr().String(), value
}

// newTestCertificate makes a certificate like the one a daemon makes for
// itself, and returns it with its fingerprint.
func newTestCertificate(t *testing.T) (tls.Certificate, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "dicerd"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, token.Fingerprint(leaf)
}

// blackhole forwards TCP connections to a target until told to drop, after
// which it keeps them open and discards whatever either end sends: what a
// network that stopped carrying packets, or a frozen host, looks like.
type blackhole struct {
	listener net.Listener
	target   string
	dropping atomic.Bool
}

// newBlackhole starts a blackhole forwarding to target, closed when the test
// ends.
func newBlackhole(t *testing.T, target string) *blackhole {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	b := &blackhole{listener: listener, target: target}
	go b.accept(t)

	return b
}

// address returns the address clients connect to.
func (b *blackhole) address() string { return b.listener.Addr().String() }

// drop stops carrying bytes, leaving the connections open.
func (b *blackhole) drop() { b.dropping.Store(true) }

// accept forwards each connection it is given until the listener closes.
func (b *blackhole) accept(t *testing.T) {
	for {
		client, err := b.listener.Accept()
		if err != nil {
			return
		}
		server, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", b.target)
		if err != nil {
			_ = client.Close()
			continue
		}
		t.Cleanup(func() {
			_ = client.Close()
			_ = server.Close()
		})

		go b.carry(server, client)
		go b.carry(client, server)
	}
}

// carry copies from src to dst until src closes, discarding once dropping.
func (b *blackhole) carry(dst io.Writer, src io.Reader) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 && !b.dropping.Load() {
			if _, err := dst.Write(buf[:n]); err != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}
