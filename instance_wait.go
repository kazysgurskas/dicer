// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"errors"
	"io"

	"google.golang.org/grpc"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// UnknownExitCode is the status Instances.Wait returns for a guest that
// ended without reporting one, as docker wait does.
const UnknownExitCode = 125

// WaitOptions say which instance a wait is for, and what it waits for.
type WaitOptions struct {
	// ID, if set, is the instance to wait for, which tells it apart from a
	// later one given the same name. Empty means whichever instance has the
	// name.
	ID string

	// Condition is what to wait for. Empty means WaitConditionStopped.
	Condition WaitCondition

	// NextStop waits for the instance's next stop, even if it is stopped
	// now: for a caller about to start it, with Instances.Waiter.
	NextStop bool
}

// WaitCondition is what a wait waits for an instance to do.
type WaitCondition string

// The wait conditions.
const (
	// WaitConditionStopped waits for the instance to stop.
	WaitConditionStopped WaitCondition = "stopped"

	// WaitConditionHealthy waits for the running instance's health check to
	// pass. The wait ends at once if the check has passed already. It goes on
	// while the instance is unhealthy, because a later probe or a restart can
	// make it healthy. It fails with ErrFailedPrecondition if the instance has
	// no health check, is not running, or stops first.
	WaitConditionHealthy WaitCondition = "healthy"
)

var waitConditions = enum[WaitCondition, dicerdv1.WaitCondition]{"wait condition", map[WaitCondition]dicerdv1.WaitCondition{
	WaitConditionStopped: dicerdv1.WaitCondition_WAIT_CONDITION_STOPPED,
	WaitConditionHealthy: dicerdv1.WaitCondition_WAIT_CONDITION_HEALTHY,
}}

// Waiter is a wait for an instance that the daemon has begun. See
// Instances.Waiter.
type Waiter struct {
	stream grpc.ServerStreamingClient[dicerdv1.WaitInstanceResponse]
	cancel context.CancelFunc
}

// Waiter begins waiting for an instance to stop, or for what opts.Condition
// says, and returns once the daemon is waiting. An instance started after it
// returns cannot end unheard, even if it is deleted as it stops, so a caller
// that starts an instance and waits for it makes its Waiter, with NextStop,
// first. The caller must Close it. ctx bounds the whole wait.
func (s *Instances) Waiter(ctx context.Context, name string, opts WaitOptions) (*Waiter, error) {
	condition, err := waitConditions.toProto(opts.Condition)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	stream, err := s.api.WaitInstance(ctx, &dicerdv1.WaitInstanceRequest{
		Name: name, Id: opts.ID, Condition: condition, NextStop: opts.NextStop,
	})
	if err != nil {
		cancel()
		return nil, fromStatus(err)
	}
	// The daemon sends the headers once it is waiting.
	if _, err := stream.Header(); err != nil {
		cancel()
		return nil, fromStatus(err)
	}

	return &Waiter{stream: stream, cancel: cancel}, nil
}

// Wait returns the status the instance's guest ended with: its workload's
// exit code, 0 for a guest that powered itself off or was stopped, or
// UnknownExitCode for one that failed without saying how. An instance its
// restart policy starts again has not stopped, so the wait goes on. A wait
// for WaitConditionHealthy returns 0 once the instance is healthy.
func (w *Waiter) Wait() (int, error) {
	resp, err := w.stream.Recv()
	if errors.Is(err, io.EOF) {
		return 0, errors.New("the daemon ended the wait without saying how the instance stopped")
	}
	if err != nil {
		return 0, fromStatus(err)
	}

	switch {
	case resp.ExitCode != nil:
		return int(resp.GetExitCode()), nil
	case resp.GetState() == dicerdv1.InstanceState_INSTANCE_STATE_FAILED:
		return UnknownExitCode, nil
	default:
		return 0, nil
	}
}

// Close ends the wait.
func (w *Waiter) Close() error {
	w.cancel()
	return nil
}

// Wait waits for an instance to stop, or for what opts.Condition says, and
// returns the status its guest ended with, as Waiter.Wait does. Without
// opts.NextStop, an instance that has already stopped is not waited for: its
// last status is returned at once.
// One that has been deleted is not found, so a caller that wants the status
// of an instance deleted as it stops, as RemoveOnExit does, waits for it
// with a Waiter made before it starts.
func (s *Instances) Wait(ctx context.Context, name string, opts WaitOptions) (int, error) {
	w, err := s.Waiter(ctx, name, opts)
	if err != nil {
		return 0, err
	}
	defer func() { _ = w.Close() }()

	return w.Wait()
}
