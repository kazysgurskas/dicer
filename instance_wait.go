// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"cmp"
	"context"
	"errors"
	"io"
	"strconv"
	"sync"
)

// UnknownExitCode is the status Instances.Wait returns for a guest that
// ended without reporting one, as docker wait does.
const UnknownExitCode = 125

// WaitOptions say which instance Instances.Wait waits for.
type WaitOptions struct {
	// ID, if set, is the instance to wait for, which tells it apart from a
	// later one given the same name. Empty means whichever instance has the
	// name.
	ID string
}

// Wait waits for an instance to stop, and returns the status its guest ended
// with: its workload's exit code, 0 for a guest that powered itself off, or
// UnknownExitCode for one that ended without saying how.
//
// An instance that has already stopped is not waited for: its last status
// is returned at once, even if it has been deleted since. An instance its
// restart policy starts again has not stopped, so the wait goes on.
func (s *Instances) Wait(ctx context.Context, name string, opts WaitOptions) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := newEventStream(ctx, s.api, EventOptions{Kind: EventKindInstance, Name: name, Follow: true})
	if err != nil {
		return 0, err
	}
	defer func() { _ = stream.Close() }()

	w := &instanceWaiter{
		id:       opts.ID,
		ends:     make(map[string]*instanceEnding),
		stopped:  make(chan struct{}, 1),
		caughtUp: make(chan struct{}),
		failed:   make(chan error, 1),
	}
	go func() { w.failed <- w.follow(stream) }()

	// Wait until the stream reports what happens next before reading the
	// instance, so that a stop between the two cannot be missed.
	select {
	case <-w.caughtUp:
	case err := <-w.failed:
		return 0, eventStreamEnded(ctx, err)
	case <-ctx.Done():
		return 0, ctx.Err()
	}

	for {
		switch code, stopped, err := w.status(ctx, s, name); {
		case err != nil:
			return 0, err
		case stopped:
			return code, nil
		}

		select {
		case <-w.stopped:
		case err := <-w.failed:
			return 0, eventStreamEnded(ctx, err)
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

// instanceWaiter watches the events of the instances that had a name for
// the one thing Wait cares about: that the one waited for is no longer
// running, and what it ended with.
type instanceWaiter struct {
	stopped  chan struct{}
	caughtUp chan struct{}
	failed   chan error

	// id is the instance waited for, once known.
	id string

	// mu guards what the stream reports against the goroutine reading it.
	mu sync.Mutex

	// ends is how each instance last ended, by ID, as its events told.
	ends map[string]*instanceEnding

	// latest is the instance the latest event was about: for a name no
	// instance has now, the last one that had it.
	latest string
}

// instanceEnding is how an instance ended, as its events told.
type instanceEnding struct {
	// code is the exit code reported, if one was.
	code *int

	// died is an end that reported no code of its own.
	died bool

	// deleted is set once the instance is deleted.
	deleted bool
}

// exitCode is the exit status the ending stands for.
func (e *instanceEnding) exitCode() int {
	switch {
	case e.code != nil:
		return *e.code
	case e.died:
		return UnknownExitCode
	default:
		return 0
	}
}

// follow reads the stream's events until it ends, closing caughtUp once the
// history has been read.
func (w *instanceWaiter) follow(stream *EventStream) error {
	closeCaughtUp := sync.OnceFunc(func() { close(w.caughtUp) })
	for {
		batch, err := stream.Next()
		if err != nil {
			return err
		}
		for _, e := range batch.Events {
			w.observe(e)
		}
		if batch.CaughtUp {
			closeCaughtUp()
		}
	}
}

// observe records one of the instances' events.
func (w *instanceWaiter) observe(e Event) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.latest = e.ID

	switch e.Action {
	case EventActionExited, EventActionDied, EventActionStopped:
		end := &instanceEnding{died: e.Action == EventActionDied}
		if code, err := strconv.Atoi(e.Attributes["exit_code"]); err == nil {
			end.code = &code
		}
		w.ends[e.ID] = end
	case EventActionDeleted:
		if w.ends[e.ID] == nil {
			w.ends[e.ID] = &instanceEnding{}
		}
		w.ends[e.ID].deleted = true
	default:
		// A start, a restart, a health verdict: the instance is running, or
		// is about to be, and whatever it ended with before is past.
		delete(w.ends, e.ID)
		return
	}

	select {
	case w.stopped <- struct{}{}:
	default: // one wake-up is enough; the instance is read either way
	}
}

// status returns what the instance ended with, and whether it has ended.
func (w *instanceWaiter) status(ctx context.Context, instances *Instances, name string) (int, bool, error) {
	instance, err := instances.Get(ctx, name)
	switch {
	case err == nil && (w.id == "" || instance.ID == w.id):
		w.id = instance.ID
		code, stopped := endedWith(instance)
		return code, stopped, nil
	case err == nil, errors.Is(err, ErrNotFound):
		// Gone, or the name another instance's now.
		if code, ok := w.deletedExitCode(); ok {
			return code, true, nil
		}
		if w.id != "" {
			// Deleted, and the event saying so is on its way.
			return 0, false, nil
		}
		return 0, false, err
	default:
		return 0, false, err
	}
}

// deletedExitCode returns the status the instance waited for ended with, if
// its events say it is deleted. Before the instance is known, it is the one
// that had the name last.
func (w *instanceWaiter) deletedExitCode() (int, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	end := w.ends[cmp.Or(w.id, w.latest)]
	if end == nil || !end.deleted {
		return 0, false
	}

	return end.exitCode(), true
}

// endedWith returns the status a stopped instance ended with, and whether it
// has stopped.
func endedWith(instance Instance) (int, bool) {
	switch {
	case instance.State != InstanceStateStopped && instance.State != InstanceStateFailed:
		return 0, false
	case instance.ExitCode != nil:
		return *instance.ExitCode, true
	case instance.State == InstanceStateFailed:
		return UnknownExitCode, true
	default:
		return 0, true
	}
}

// eventStreamEnded returns the error for an event stream that ended during
// a wait.
func eventStreamEnded(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case err == nil, errors.Is(err, io.EOF):
		return errors.New("the daemon stopped reporting events before the instance stopped")
	default:
		return err
	}
}
