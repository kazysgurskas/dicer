// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"context"

	"github.com/konradasb/dicer/internal/errdefs"
)

// WaitOptions say which stop of an instance a Waiter waits for.
type WaitOptions struct {
	// ID, if set, is the instance to wait for, which tells it apart from a
	// later one given the same name.
	ID string

	// NextStop waits for the instance's next stop, even if it is stopped
	// now: for a caller about to start it.
	NextStop bool
}

// Waiter waits for an instance to stop. It hears every stop from the moment
// it is made, so one made before the instance is started cannot miss how it
// ends, even if the instance is deleted as it stops.
type Waiter struct {
	manager  *Manager
	instance Spec
	opts     WaitOptions

	// stopped receives the status of the instance's first stop.
	stopped chan Status
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

	w := &Waiter{manager: m, instance: instance, opts: opts, stopped: make(chan Status, 1)}

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
// that is stopped already is not waited for.
func (w *Waiter) Wait(ctx context.Context) (Status, error) {
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

// Close stops the waiter hearing the instance's stops.
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

// isStopped reports whether an instance in state has stopped, as a waiter
// means it: Stopped or Failed.
func isStopped(state State) bool {
	return state == StateStopped || state == StateFailed
}
