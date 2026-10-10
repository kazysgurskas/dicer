// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/konradasb/dicer/internal/errdefs"
)

// waitInBackground waits for the harness instance to stop, and returns where
// the status it stopped with arrives.
func waitInBackground(t *testing.T, h *harness, opts WaitOptions) <-chan Status {
	t.Helper()

	w, err := h.manager.Waiter(h.instance.Name, opts)
	if err != nil {
		t.Fatalf("Waiter: %v", err)
	}
	t.Cleanup(w.Close)

	stopped := make(chan Status, 1)
	go func() {
		status, err := w.Wait(t.Context())
		if err != nil {
			t.Errorf("Wait: %v", err)
		}
		stopped <- status
	}()
	return stopped
}

// stopWithin returns the status the instance stopped with, failing the test
// if it does not arrive in time.
func stopWithin(t *testing.T, stopped <-chan Status) Status {
	t.Helper()

	select {
	case status := <-stopped:
		return status
	case <-time.After(10 * time.Second):
		t.Fatal("the wait did not end")
		return Status{}
	}
}

// assertStillWaiting fails the test if the wait has ended.
func assertStillWaiting(t *testing.T, stopped <-chan Status) {
	t.Helper()

	select {
	case status := <-stopped:
		t.Fatalf("the wait ended with %s, want it to go on", status.State)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWaitForAStoppedInstanceReturnsAtOnce(t *testing.T) {
	h := newHarness(t)
	h.define()

	if status := stopWithin(t, waitInBackground(t, h, WaitOptions{})); status.State != StateStopped {
		t.Errorf("state = %s, want %s", status.State, StateStopped)
	}
}

func TestWaitReturnsTheExitCode(t *testing.T) {
	h := newHarness(t)
	h.start(t)
	stopped := waitInBackground(t, h, WaitOptions{})
	assertStillWaiting(t, stopped)

	h.exit(t, 3)

	// A workload that exits with an error has failed.
	status := stopWithin(t, stopped)
	if status.State != StateFailed || status.ExitCode == nil || *status.ExitCode != 3 {
		t.Errorf("stopped with %s, exit code %v; want Failed with 3", status.State, status.ExitCode)
	}
}

// TestWaitForNextStopHearsAnInstanceStartedAfterIt checks that a wait made
// before an instance is started hears it end, even when the instance is
// deleted as it stops, as dicer run --rm needs.
func TestWaitForNextStopHearsAnInstanceStartedAfterIt(t *testing.T) {
	h := newHarness(t)
	h.instance.RemoveOnExit = true
	h.define()
	stopped := waitInBackground(t, h, WaitOptions{NextStop: true})
	assertStillWaiting(t, stopped)

	h.start(t)
	h.exit(t, 7)

	if status := stopWithin(t, stopped); status.ExitCode == nil || *status.ExitCode != 7 {
		t.Errorf("exit code = %v, want 7", status.ExitCode)
	}
}

func TestWaitGoesOnThroughARestart(t *testing.T) {
	h := newHarness(t)
	h.restartAtOnce()
	h.setRestart(t, RestartPolicy{Mode: RestartModeAlways})
	h.start(t)
	stopped := waitInBackground(t, h, WaitOptions{})

	h.exit(t, 1)
	h.waitForVMMs(t, 2)
	h.waitForState(t, StateRunning)
	assertStillWaiting(t, stopped)

	if err := h.manager.stop(t.Context(), h.instance); err != nil {
		t.Fatal(err)
	}
	if status := stopWithin(t, stopped); status.State != StateStopped {
		t.Errorf("state = %s, want %s", status.State, StateStopped)
	}
}

// TestWaitEndsWhenARunningInstanceIsDeleted checks that a wait ends when the
// instance is deleted from under it.
func TestWaitEndsWhenARunningInstanceIsDeleted(t *testing.T) {
	h := newHarness(t)
	h.start(t)
	stopped := waitInBackground(t, h, WaitOptions{})

	if err := h.manager.delete(t.Context(), h.instance, true); err != nil {
		t.Fatal(err)
	}
	if status := stopWithin(t, stopped); status.State != StateStopped {
		t.Errorf("state = %s, want %s", status.State, StateStopped)
	}
}

func TestWaitForAnotherInstanceOfTheNameIsNotFound(t *testing.T) {
	h := newHarness(t)
	h.define()

	_, err := h.manager.Waiter(h.instance.Name, WaitOptions{ID: "another"})
	if !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("Waiter = %v, want ErrNotFound", err)
	}
}

func TestWaitGivesUpWithItsContext(t *testing.T) {
	h := newHarness(t)
	h.start(t)
	w, err := h.manager.Waiter(h.instance.Name, WaitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := w.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Wait = %v, want the context's error", err)
	}
}
