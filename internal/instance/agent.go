// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"context"
	"fmt"
	"net"
	"time"

	"google.golang.org/grpc"
	grpcbackoff "google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/guest"
	"github.com/konradasb/dicer/internal/hypervisor"
	diceragentv1 "github.com/konradasb/dicer/proto/diceragent/v1"
)

// guestSyncTimeout bounds how long a stop waits for the guest to flush its
// filesystems before ending its VMM regardless.
const guestSyncTimeout = 10 * time.Second

// agentReadyTimeout bounds how long a request waits for a running guest's
// agent, which starts a moment after the guest does.
const agentReadyTimeout = 30 * time.Second

// agent is Agent, for an instance its caller has looked up.
func (m *Manager) agent(ctx context.Context, instance Spec) (diceragentv1.AgentServiceClient, func(), error) {
	status, err := m.statusOf(instance)
	if err != nil {
		return nil, nil, err
	}
	if status.State != StateRunning {
		return nil, nil, errdefs.InvalidState("instance %q is %s, not running", instance.Name, status.State.Lowercase())
	}

	conn, err := dialAgent(status.VsockPath)
	if err != nil {
		return nil, nil, err
	}
	if err := m.waitForAgent(ctx, instance, conn); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}

	return diceragentv1.NewAgentServiceClient(conn), func() { _ = conn.Close() }, nil
}

// Agent connects to a running instance's guest agent over vsock. If the guest
// is still booting, Agent waits up to agentReadyTimeout for its agent to
// answer. It returns an ErrUnavailable error if the agent does not answer in
// time, and an ErrInvalidState one if the instance is not running or stops.
// The returned function closes the connection.
func (m *Manager) Agent(ctx context.Context, nameOrID string) (diceragentv1.AgentServiceClient, func(), error) {
	instance, err := m.store.Instance(nameOrID)
	if err != nil {
		return nil, nil, err
	}
	return m.agent(ctx, instance)
}

// waitForAgent waits until conn reaches the instance's guest agent. It gives
// up once the instance stops running, or after agentReadyTimeout.
func (m *Manager) waitForAgent(ctx context.Context, instance Spec, conn *grpc.ClientConn) error {
	readyCtx, cancel := context.WithTimeout(ctx, agentReadyTimeout)
	defer cancel()

	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return nil
		}
		conn.Connect()

		// A connection that keeps failing may not change state, so the
		// instance is checked at least once a second.
		changeCtx, cancelChange := context.WithTimeout(readyCtx, time.Second)
		conn.WaitForStateChange(changeCtx, state)
		cancelChange()

		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case readyCtx.Err() != nil:
			return errdefs.Unavailable("the guest agent of instance %q did not answer within %s; "+
				"see 'dicer logs %s' for how the guest booted", instance.Name, agentReadyTimeout, instance.Name)
		}

		status, err := m.statusOf(instance)
		if err != nil {
			return err
		}
		if status.State != StateRunning {
			return errdefs.InvalidState("instance %q is %s, not running", instance.Name, status.State.Lowercase())
		}
	}
}

// dialAgent returns a connection to the guest agent behind vsockPath. The
// connection is made on first use. Failed attempts are retried quickly,
// because an agent that is not listening yet is usually about to.
func dialAgent(vsockPath string) (*grpc.ClientConn, error) {
	retry := grpcbackoff.DefaultConfig
	retry.BaseDelay = 100 * time.Millisecond
	retry.MaxDelay = time.Second

	conn, err := grpc.NewClient("passthrough:///agent",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return hypervisor.DialVsock(ctx, vsockPath, guest.AgentPort)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		// gRPC's default; left at zero, it would give each attempt no time.
		grpc.WithConnectParams(grpc.ConnectParams{Backoff: retry, MinConnectTimeout: 20 * time.Second}),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to guest agent: %w", err)
	}

	return conn, nil
}

// syncGuest asks a running guest to flush its filesystems before its VMM is
// ended. It is best effort.
func (m *Manager) syncGuest(ctx context.Context, instance Spec, status Status) {
	if status.State != StateRunning || status.VsockPath == "" {
		return
	}

	conn, err := dialAgent(status.VsockPath)
	if err != nil {
		m.logger.WarnContext(ctx, "cannot reach guest agent to flush the guest's disks",
			"instance", instance.Name, "error", err)
		return
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(ctx, guestSyncTimeout)
	defer cancel()

	if _, err := diceragentv1.NewAgentServiceClient(conn).Sync(ctx, &diceragentv1.SyncRequest{}); err != nil {
		m.logger.WarnContext(ctx, "guest did not flush its disks before stopping",
			"instance", instance.Name, "error", err)
	}
}

// restoredAgentTimeout bounds how long a restored guest's agent is waited
// for, and given, to answer: a snapshot taken as the guest booted may have
// caught it before its agent was serving.
const restoredAgentTimeout = 10 * time.Second

// setGuestClock sets the clock of the guest behind vsockPath to t.
func setGuestClock(ctx context.Context, vsockPath string, t time.Time) error {
	conn, err := dialAgent(vsockPath)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(ctx, restoredAgentTimeout)
	defer cancel()

	req := &diceragentv1.SetClockRequest{Time: timestamppb.New(t)}
	_, err = diceragentv1.NewAgentServiceClient(conn).SetClock(ctx, req, grpc.WaitForReady(true))
	return err
}

// setGuestIdentity gives the guest behind vsockPath the identity req
// describes.
func setGuestIdentity(ctx context.Context, vsockPath string, req *diceragentv1.SetIdentityRequest) error {
	conn, err := dialAgent(vsockPath)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(ctx, restoredAgentTimeout)
	defer cancel()

	_, err = diceragentv1.NewAgentServiceClient(conn).SetIdentity(ctx, req, grpc.WaitForReady(true))
	return err
}

// agentCallTimeout bounds a quick request to the guest agent.
const agentCallTimeout = 5 * time.Second

// shutdownGuest asks the guest behind vsockPath to shut down, without waiting
// for it to.
func shutdownGuest(ctx context.Context, vsockPath string) error {
	conn, err := dialAgent(vsockPath)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(ctx, agentCallTimeout)
	defer cancel()

	_, err = diceragentv1.NewAgentServiceClient(conn).Shutdown(ctx, &diceragentv1.ShutdownRequest{})
	return err
}
