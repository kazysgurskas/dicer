// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"context"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/health"
)

// WaitCondition is what a Waiter waits for an instance to do.
type WaitCondition int

// The wait conditions.
const (
	// WaitConditionStopped waits for the instance to stop.
	WaitConditionStopped WaitCondition = iota

	// WaitConditionHealthy waits for the running instance's health check to
	// pass.
	WaitConditionHealthy
)

// WaitOptions say what a Waiter waits for.
type WaitOptions struct {
	// ID, if set, is the instance to wait for, which tells it apart from a
	// later one given the same name.
	ID string

	// Condition is what to wait for. The zero value waits for a stop.
	Condition WaitCondition

	// NextStop waits for the instance's next stop, even if it is stopped
	// now: for a caller about to start it.
	NextStop bool
}

// Waiter waits for an instance to stop, or to be healthy. It hears every
// stop and every passing health check from the moment it is made, so one
// made before the instance is started cannot miss how it ends, even if the
// instance is deleted as it stops.
type Waiter struct {
	manager  *Manager
	instance Spec
	opts     WaitOptions

	// stopped receives the status of the instance's first stop.
	stopped chan Status

	// healthy receives a value once the instance's health check passes.
	healthy chan struct{}
}

// Waiter returns a waiter for an instance, which the caller must Close. An
// instance that does not exist, or no longer has opts.ID, is an
// errdefs.ErrNotFound error.
func (m *Manager) Waiter(nameOrID string, opts WaitOptions) (*Waiter, error) {
	instance, err := m.store.Instance(nameOrID)
	if err != nil {
		return nil, err
	}
	if opts.ID != "" && instance.ID != opts.ID {
		return nil, errdefs.NotFound("instance %q is no longer the one with ID %s", nameOrID, opts.ID)
	}

	w := &Waiter{
		manager: m, instance: instance, opts: opts,
		stopped: make(chan Status, 1), healthy: make(chan struct{}, 1),
	}

	m.waitersMu.Lock()
	defer m.waitersMu.Unlock()
	if m.waiters[instance.ID] == nil {
		m.waiters[instance.ID] = make(map[*Waiter]struct{})
	}
	m.waiters[instance.ID][w] = struct{}{}

	return w, nil
}

// Wait returns the status the instance stopped with: Stopped or Failed. A
// restart is not a stop, and nor is standby. Without NextStop, an instance
// that is stopped already is not waited for. With WaitConditionHealthy, it
// waits as waitHealthy does.
func (w *Waiter) Wait(ctx context.Context) (Status, error) {
	if w.opts.Condition == WaitConditionHealthy {
		return w.waitHealthy(ctx)
	}

	if !w.opts.NextStop {
		// Read after the waiter was made, so a stop between the two is
		// heard either way.
		status, err := w.manager.statusOf(w.instance)
		if err != nil {
			return Status{}, err
		}
		if isStopped(status.State) {
			return status, nil
		}
	}

	select {
	case status := <-w.stopped:
		return status, nil
	case <-ctx.Done():
		return Status{}, ctx.Err()
	}
}

// waitHealthy returns the status of the running instance once its health
// check passes. It returns at once if the check has passed already, and goes
// on waiting while the instance is unhealthy, because a later probe or a
// restart can make it healthy. It returns an errdefs.ErrInvalidState error if
// the instance has no health check, is not running, or stops first.
func (w *Waiter) waitHealthy(ctx context.Context) (Status, error) {
	// Read after the waiter was made, so a passing check between the two is
	// heard either way.
	status, err := w.manager.statusOf(w.instance)
	if err != nil {
		return Status{}, err
	}
	if status.State != StateRunning {
		return Status{}, errdefs.InvalidState("instance %q is %s, so it cannot become healthy", w.instance.Name, status.State)
	}
	if status.HealthCheck == nil {
		return Status{}, errdefs.InvalidState("instance %q has no health check to wait for", w.instance.Name)
	}
	if _, verdict, ok := w.manager.healthOf(w.instance); ok && verdict.Status == health.StatusHealthy {
		return status, nil
	}

	select {
	case <-w.healthy:
		return w.manager.statusOf(w.instance)
	case <-w.stopped:
		return Status{}, errdefs.InvalidState("instance %q stopped before it was healthy", w.instance.Name)
	case <-ctx.Done():
		return Status{}, ctx.Err()
	}
}

// Close stops the waiter hearing the instance's stops and health.
func (w *Waiter) Close() {
	m := w.manager
	m.waitersMu.Lock()
	defer m.waitersMu.Unlock()

	delete(m.waiters[w.instance.ID], w)
	if len(m.waiters[w.instance.ID]) == 0 {
		delete(m.waiters, w.instance.ID)
	}
}

// notifyWaiters tells the waiters of the instance status is of that it has
// stopped with status. A waiter that has heard of an earlier stop keeps
// that one.
func (m *Manager) notifyWaiters(status Status) {
	m.waitersMu.Lock()
	defer m.waitersMu.Unlock()

	for w := range m.waiters[status.InstanceID] {
		select {
		case w.stopped <- status:
		default:
		}
	}
}

// notifyHealthy tells the waiters of an instance that its health check has
// passed.
func (m *Manager) notifyHealthy(instanceID string) {
	m.waitersMu.Lock()
	defer m.waitersMu.Unlock()

	for w := range m.waiters[instanceID] {
		select {
		case w.healthy <- struct{}{}:
		default:
		}
	}
}

// isStopped reports whether an instance in state has stopped, as a waiter
// means it: Stopped or Failed.
func isStopped(state State) bool {
	return state == StateStopped || state == StateFailed
}
