// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/konradasb/dicer/internal/errdefs"
	diceragentv1 "github.com/konradasb/dicer/proto/diceragent/v1"
)

// TestAgentWaitsForBootingGuest checks that a request to a guest whose agent
// is not listening yet waits for it, rather than failing.
func TestAgentWaitsForBootingGuest(t *testing.T) {
	manager, store, _ := newTestManager(t)
	instance := seedInstance(t, store, "web")
	vsockPath := writeRunningStatus(t, manager, instance)

	go func() {
		time.Sleep(300 * time.Millisecond)
		serveFakeAgent(t, vsockPath)
	}()

	agent, closeAgent, err := manager.agent(t.Context(), instance)
	if err != nil {
		t.Fatalf("Agent: %v", err)
	}
	defer closeAgent()

	if _, err := agent.ListProcesses(t.Context(), &diceragentv1.ListProcessesRequest{}); err != nil {
		t.Errorf("ListProcesses: %v", err)
	}
}

// TestAgentStopsWaitingForStoppedInstance checks that a request waiting for a
// guest's agent fails as soon as the instance stops, rather than waiting out
// the timeout.
func TestAgentStopsWaitingForStoppedInstance(t *testing.T) {
	manager, store, _ := newTestManager(t)
	instance := seedInstance(t, store, "job")
	writeRunningStatus(t, manager, instance)

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		time.Sleep(300 * time.Millisecond)
		if err := manager.writeStatus(Status{
			InstanceID: instance.ID, State: StateStopped,
		}); err != nil {
			t.Errorf("writeStatus: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, _, err := manager.agent(ctx, instance)
	if !errors.Is(err, errdefs.ErrInvalidState) {
		t.Errorf("Agent = %v, want an invalid state error", err)
	}
	<-stopped
}

// writeRunningStatus records instance running, with a vsock socket that
// nothing listens on yet, and returns the socket's path.
func writeRunningStatus(t *testing.T, manager *Manager, instance Spec) string {
	t.Helper()

	vsockPath := filepath.Join(t.TempDir(), "sock") // short: macOS caps a socket path at 104 bytes
	if err := manager.writeStatus(Status{
		InstanceID: instance.ID, State: StateRunning, VsockPath: vsockPath,
	}); err != nil {
		t.Fatalf("writeStatus: %v", err)
	}

	return vsockPath
}

// serveFakeAgent serves a guest agent behind a fake of the VMM's vsock proxy
// at vsockPath until the test ends.
func serveFakeAgent(t *testing.T, vsockPath string) {
	t.Helper()

	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "unix", vsockPath)
	if err != nil {
		t.Errorf("listen: %v", err)
		return
	}

	server := grpc.NewServer()
	diceragentv1.RegisterAgentServiceServer(server, fakeAgent{})
	t.Cleanup(server.Stop)

	go func() { _ = server.Serve(vsockProxyListener{l}) }()
}

// fakeAgent is a guest agent that lists no processes.
type fakeAgent struct {
	diceragentv1.UnimplementedAgentServiceServer
}

func (fakeAgent) ListProcesses(
	context.Context, *diceragentv1.ListProcessesRequest,
) (*diceragentv1.ListProcessesResponse, error) {
	return &diceragentv1.ListProcessesResponse{}, nil
}

// vsockProxyListener answers the CONNECT handshake of each connection it
// accepts, as the VMM's vsock proxy does.
type vsockProxyListener struct {
	net.Listener
}

// Accept implements net.Listener. A connection whose handshake fails is
// dropped, not returned as an error, which would end the server.
func (l vsockProxyListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}

		// The dialer waits for the reply before sending more, so nothing
		// past the line is buffered.
		if _, err := bufio.NewReader(conn).ReadString('\n'); err == nil {
			if _, err := io.WriteString(conn, "OK 1073741824\n"); err == nil {
				return conn, nil
			}
		}
		_ = conn.Close()
	}
}
