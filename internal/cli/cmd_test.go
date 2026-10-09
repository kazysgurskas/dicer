// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	"github.com/konradasb/dicer"
	"github.com/konradasb/dicer/internal/cli/remote"
	"github.com/konradasb/dicer/internal/grpcapi"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// The API's values the fake daemons in these tests report, named as the CLI
// shows them.
const (
	stateStopped    = dicerdv1.InstanceState_INSTANCE_STATE_STOPPED
	stateStarting   = dicerdv1.InstanceState_INSTANCE_STATE_STARTING
	stateRunning    = dicerdv1.InstanceState_INSTANCE_STATE_RUNNING
	statePaused     = dicerdv1.InstanceState_INSTANCE_STATE_PAUSED
	stateStandby    = dicerdv1.InstanceState_INSTANCE_STATE_STANDBY
	stateRestarting = dicerdv1.InstanceState_INSTANCE_STATE_RESTARTING
	stateFailed     = dicerdv1.InstanceState_INSTANCE_STATE_FAILED

	healthStarting  = dicerdv1.HealthStatus_HEALTH_STATUS_STARTING
	healthHealthy   = dicerdv1.HealthStatus_HEALTH_STATUS_HEALTHY
	healthUnhealthy = dicerdv1.HealthStatus_HEALTH_STATUS_UNHEALTHY
)

func TestExecute(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantOut  string
	}{
		{"success", nil, 0, ""},
		{"plain error", errors.New("boom"), 1, "Error: boom\n"},
		{"exit status passes through silently", &exitError{code: 42}, 42, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{
				Use:           "test",
				SilenceErrors: true,
				SilenceUsage:  true,
				RunE:          func(*cobra.Command, []string) error { return tt.err },
			}
			cmd.SetArgs(nil)

			var stderr bytes.Buffer
			if code := exitStatus(cmd, &stderr); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if got := stderr.String(); got != tt.wantOut {
				t.Errorf("stderr = %q, want %q", got, tt.wantOut)
			}
		})
	}
}

// TestADaemonsErrorIsPrintedAsItsMessage checks that a failure the daemon
// reports is printed as its message alone, with what the CLI adds to it.
func TestADaemonsErrorIsPrintedAsItsMessage(t *testing.T) {
	serveFakeDaemon(t, newFakeInstanceDaemon(fakeInstances()...))

	cmd := NewCommand()
	cmd.SetArgs([]string{"inspect", "wbe"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	var stderr bytes.Buffer
	if code := exitStatus(cmd, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if got := stderr.String(); !strings.HasPrefix(got, "Error: no instance") ||
		!strings.HasSuffix(got, " (did you mean web?)\n") {
		t.Errorf("stderr = %q, want the daemon's message and a suggestion", got)
	}
}

// run executes the dicer command line with args and returns what it printed.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()

	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(t.Context())
	return out.String(), err
}

// runWithInput runs the CLI as run does, with input on standard input.
func runWithInput(t *testing.T, input string, args ...string) (string, error) {
	t.Helper()

	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(input))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(t.Context())
	return out.String(), err
}

// isolateConfig gives the test a configuration directory of its own, and
// clears whatever remote the environment names.
func isolateConfig(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv(remote.ConfigDirEnv, dir)
	t.Setenv(remoteEnv, "")
	t.Setenv(tokenEnv, "")

	return dir
}

// serveFakeDaemon serves srv on a socket and aims commands at it through
// $DICER_REMOTE, as a completion, which takes no --remote, needs. It returns
// the socket's address.
func serveFakeDaemon(t *testing.T, srv dicerdv1.DaemonServiceServer) string {
	t.Helper()
	isolateConfig(t)

	// A short directory: a socket path is limited to about 100 bytes, and
	// a test's temporary directory can be most of that on its own.
	dir, err := os.MkdirTemp("", "dicer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socket := filepath.Join(dir, "d.sock")
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}

	server := newTestServer()
	dicerdv1.RegisterDaemonServiceServer(server, srv)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	address := "unix://" + socket
	t.Setenv(remoteEnv, address)
	return address
}

// clientOf serves srv as serveFakeDaemon does, and returns a client of it.
func clientOf(t *testing.T, srv dicerdv1.DaemonServiceServer) *dicer.Client {
	t.Helper()

	c, err := dicer.NewClient(dicer.WithAddress(serveFakeDaemon(t, srv)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })

	return c
}

// stateWord is an instance state as the daemon names it in a message:
// "running".
func stateWord(s dicerdv1.InstanceState) string {
	return strings.ToLower(strings.TrimPrefix(s.String(), "INSTANCE_STATE_"))
}

// newTestServer returns a gRPC server that sends errors as dicerd does.
func newTestServer() *grpc.Server {
	return grpc.NewServer(
		grpc.ChainUnaryInterceptor(grpcapi.UnaryStatusInterceptor),
		grpc.ChainStreamInterceptor(grpcapi.StreamStatusInterceptor),
	)
}
