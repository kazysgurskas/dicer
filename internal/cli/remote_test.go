// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/konradasb/dicer"
	"github.com/konradasb/dicer/internal/cli/remote"
	"github.com/konradasb/dicer/internal/filestore"
	"github.com/konradasb/dicer/internal/grpcserver"
	"github.com/konradasb/dicer/internal/kernel"
	"github.com/konradasb/dicer/internal/network"
	"github.com/konradasb/dicer/internal/token"
)

// serveDaemon serves the API on loopback, as dicerd does with server.listen
// set: over TLS with a certificate of its own, to calls with a token. It
// returns the address to reach it at and a token it accepts.
func serveDaemon(t *testing.T) (address, value string) {
	t.Helper()

	logger := slog.New(slog.DiscardHandler)
	store, err := filestore.New(filestore.Config{
		DataDir: filepath.Join(t.TempDir(), "data"),
		Logger:  logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	cert, fingerprint := newTestCertificate(t)
	tokenManager := token.NewManager(token.Config{Store: store})
	_, secret, err := tokenManager.Create("laptop", "", []token.Scope{token.ScopeAll})
	if err != nil {
		t.Fatal(err)
	}

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	networkManager, err := network.NewManager(network.Config{
		Dir: filepath.Join(t.TempDir(), "allocations"), Store: store, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	kernelManager, err := kernel.NewManager(kernel.Config{
		DataDir: filepath.Join(t.TempDir(), "data"), Store: store, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := grpcserver.NewServer(grpcserver.Config{
		NetworkManager:   networkManager,
		KernelManager:    kernelManager,
		TokenManager:     tokenManager,
		ListenAddress:    listener.Addr().String(),
		Fingerprint:      fingerprint,
		TokenFingerprint: fingerprint,
		Logger:           logger,
	})
	grpcServer := grpcserver.NewTCPServer(server, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}})

	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	return listener.Addr().String(), token.Format(secret, fingerprint)
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

// TestRemoteCreateWithTheTokenGiven checks the command token create prints:
// the token on the command line, read from nowhere else.
func TestRemoteCreateWithTheTokenGiven(t *testing.T) {
	isolateConfig(t)
	address, value := serveDaemon(t)

	if out, err := runWithInput(t, "not this", "remote", "create", "prod", address, "--token", value); err != nil {
		t.Fatalf("remote create --token: %v\n%s", err, out)
	}
	if out, err := run(t, "--remote", "prod", "network", "list"); err != nil {
		t.Errorf("network list on the new remote: %v\n%s", err, out)
	}
}

// The whole of setting up a remote: an address and a token are recorded,
// and commands go to it.
func TestRemoteCreateAndCommandsUseIt(t *testing.T) {
	isolateConfig(t)

	address, value := serveDaemon(t)

	out, err := runWithInput(t, value+"\n", "remote", "create", "prod", address)
	if err != nil {
		t.Fatalf("remote create: %v\n%s", err, out)
	}

	// Reachable by name, as this client.
	if out, err := run(t, "--remote", "prod", "network", "list"); err != nil {
		t.Fatalf("network list on the new remote: %v\n%s", err, out)
	}

	// And once it is current, without naming it.
	if out, err := run(t, "remote", "use", "prod"); err != nil {
		t.Fatalf("remote use: %v\n%s", err, out)
	}
	if out, err := run(t, "network", "list"); err != nil {
		t.Fatalf("network list on the current remote: %v\n%s", err, out)
	}

	out, err = run(t, "remote", "list")
	if err != nil {
		t.Fatalf("remote list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "token") {
		t.Errorf("remote list = %q, want it to say prod is reached with a token", out)
	}

	// The records say how each remote is reached, and never with what.
	out, err = run(t, "remote", "list", "--format", "json")
	if err != nil {
		t.Fatalf("remote list --format json: %v\n%s", err, out)
	}
	var records []remoteRecord
	if err := json.Unmarshal([]byte(out), &records); err != nil {
		t.Fatalf("not a JSON array of remotes: %v\n%s", err, out)
	}
	prod := slices.IndexFunc(records, func(r remoteRecord) bool { return r.Name == "prod" })
	if prod < 0 || records[prod].Auth != "token" || !records[prod].Current || records[prod].Address != address {
		t.Errorf("records = %+v, want prod, current, reached with a token", records)
	}
	if strings.Contains(out, value) {
		t.Errorf("remote list --format json wrote the token:\n%s", out)
	}
}

func TestRemoteCreateRefusesATCPAddressWithoutAToken(t *testing.T) {
	isolateConfig(t)

	for _, input := range []string{"", "hunter2"} {
		out, err := runWithInput(t, input, "remote", "create", "prod", "192.0.2.1:7443")
		if err == nil {
			t.Errorf("remote create with %q as the token succeeded:\n%s", input, out)
		}
	}
}

// TestTokenFromTheEnvironment checks that $DICER_TOKEN reaches a daemon with
// nothing configured, as in CI, and takes the place of a remote's token.
func TestTokenFromTheEnvironment(t *testing.T) {
	isolateConfig(t)
	address, value := serveDaemon(t)

	t.Setenv(remoteEnv, address)
	if out, err := run(t, "network", "list"); err == nil || !strings.Contains(err.Error(), tokenEnv) {
		t.Errorf("a TCP address without a token = %v, want an error naming $%s\n%s", err, tokenEnv, out)
	}

	t.Setenv(tokenEnv, value)
	if out, err := run(t, "network", "list"); err != nil {
		t.Errorf("network list with $%s: %v\n%s", tokenEnv, err, out)
	}

	// A wrong one in the environment wins over a right one configured.
	t.Setenv(remoteEnv, "")
	if out, err := runWithInput(t, value, "remote", "create", "prod", address); err != nil {
		t.Fatalf("remote create: %v\n%s", err, out)
	}
	_, fingerprint, _ := token.Parse(value)
	t.Setenv(tokenEnv, token.Format(token.NewSecret(), fingerprint))
	if _, err := run(t, "--remote", "prod", "network", "list"); !errors.Is(err, dicer.ErrUnauthenticated) {
		t.Errorf("a call with another token = %v, want ErrUnauthenticated", err)
	}
}

// TestTokenCommands makes, lists, rotates and deletes tokens over the
// daemon's TCP listener, as an operator on another machine can.
func TestTokenCommands(t *testing.T) {
	isolateConfig(t)
	address, value := serveDaemon(t)
	t.Setenv(remoteEnv, address)
	t.Setenv(tokenEnv, value)

	// The token alone goes to standard output, for a script to capture, and
	// what to do with it to standard error.
	cmd := NewCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"token", "create", "ci", "--scopes", "instances:write,kernels:read"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("token create: %v\n%s", err, stderr.String())
	}
	issued := strings.TrimSuffix(stdout.String(), "\n")
	if _, _, err := token.Parse(issued); err != nil {
		t.Errorf("token create's output = %q, want the token alone: %v", stdout.String(), err)
	}
	// The command to run on another machine, complete: the host's name, the
	// address this command reached it at, and the token.
	hostname, _ := os.Hostname()
	hostname, _, _ = strings.Cut(hostname, ".")
	if want := "dicer remote create " + hostname + " " + address + " --token " + issued; !strings.Contains(stderr.String(), want) {
		t.Errorf("token create = %q, want it to say to run %q", stderr.String(), want)
	}

	out, err := run(t, "token", "create", "deploy", "--format", "json")
	if err != nil || !strings.Contains(out, `"value": "dicer_`) {
		t.Errorf("token create --format json = %v\n%s, want the token's value in it", err, out)
	}

	out, err = run(t, "token", "list")
	if err != nil {
		t.Fatalf("token list: %v\n%s", err, out)
	}
	for _, want := range []string{"ci", "instances:write,kernels:read", "deploy", "laptop", "*"} {
		if !strings.Contains(out, want) {
			t.Errorf("token list = %q, missing %q", out, want)
		}
	}

	// The new token does what its scopes allow, and nothing else, until it
	// is rotated.
	t.Setenv(tokenEnv, issued)
	if out, err := run(t, "kernel", "list"); err != nil {
		t.Errorf("kernel list with kernels:read: %v\n%s", err, out)
	}
	if _, err := run(t, "network", "list"); !errors.Is(err, dicer.ErrPermissionDenied) {
		t.Errorf("network list without networks:read = %v, want ErrPermissionDenied", err)
	}

	t.Setenv(tokenEnv, value)
	if out, err := run(t, "token", "rotate", "ci"); err != nil {
		t.Fatalf("token rotate: %v\n%s", err, out)
	}
	t.Setenv(tokenEnv, issued)
	if _, err := run(t, "kernel", "list"); !errors.Is(err, dicer.ErrUnauthenticated) {
		t.Errorf("a call with the token rotated away = %v, want ErrUnauthenticated", err)
	}

	t.Setenv(tokenEnv, value)
	if out, err := run(t, "token", "delete", "ci", "deploy"); err != nil {
		t.Fatalf("token delete: %v\n%s", err, out)
	}
	if out, _ := run(t, "token", "list", "-q"); strings.TrimSpace(out) != "laptop" {
		t.Errorf("token list -q after the delete = %q, want laptop alone", out)
	}
}

func TestRemoteCreateFromSocketAddress(t *testing.T) {
	isolateConfig(t)

	if out, err := run(t, "remote", "create", "test", "unix:///run/dicer-test/dicer.sock"); err != nil {
		t.Fatalf("remote create: %v\n%s", err, out)
	}

	out, err := run(t, "remote", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{remote.Local, "test", "unix:///run/dicer-test/dicer.sock"} {
		if !strings.Contains(out, want) {
			t.Errorf("remote list = %q, missing %q", out, want)
		}
	}
}

func TestResolveTargetPrecedence(t *testing.T) {
	dir := isolateConfig(t)

	cfg, _ := remote.Load(dir)
	_ = cfg.Create("current", remote.Remote{Address: "unix:///run/current.sock"})
	_ = cfg.Create("env", remote.Remote{Address: "unix:///run/env.sock"})
	_ = cfg.Create("flag", remote.Remote{Address: "unix:///run/flag.sock"})

	resolve := func(args ...string) string {
		t.Helper()

		cmd := NewCommand()
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatal(err)
		}
		target, err := resolveTarget(cmd)
		if err != nil {
			t.Fatalf("resolveTarget(%v): %v", args, err)
		}
		return target.name
	}
	save := func() {
		t.Helper()
		if err := cfg.Save(dir); err != nil {
			t.Fatal(err)
		}
	}

	save()
	if got := resolve(); got != remote.Local {
		t.Errorf("with nothing chosen: %q, want %q", got, remote.Local)
	}

	_ = cfg.Use("current")
	save()
	if got := resolve(); got != "current" {
		t.Errorf("with a current remote: %q, want current", got)
	}

	t.Setenv(remoteEnv, "env")
	if got := resolve(); got != "env" {
		t.Errorf("with $%s: %q, want env", remoteEnv, got)
	}

	if got := resolve("--remote", "flag"); got != "flag" {
		t.Errorf("with --remote: %q, want flag", got)
	}

	// A socket address needs nothing configured.
	if got := resolve("-r", "unix:///run/adhoc.sock"); got != "unix:///run/adhoc.sock" {
		t.Errorf("with an address: %q", got)
	}
}
