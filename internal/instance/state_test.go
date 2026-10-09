// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"errors"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
)

func TestTransitionRejectsIllegalMove(t *testing.T) {
	manager, definitions, _ := newTestManager(t)
	instance := seedInstance(t, definitions, "web")

	// Stopped -> Paused is not a legal move; it must be refused before
	// anything touches the host.
	if err := manager.transition(instance, StatePaused); !errors.Is(err, errdefs.ErrInvalidState) {
		t.Errorf("error = %v, want ErrInvalidState", err)
	}

	if err := manager.transition(instance, StateStarting); err != nil {
		t.Errorf("Stopped -> Starting should be allowed: %v", err)
	}
}

// transition moves the state and nothing else: the recorded process details
// belong to whoever started the process.
func TestTransitionKeepsProcessDetails(t *testing.T) {
	h := newHarness(t)
	h.running(t)

	if err := h.manager.transition(h.instance, StatePaused); err != nil {
		t.Fatalf("transition: %v", err)
	}

	status := h.status(t)
	if status.State != StatePaused {
		t.Errorf("state = %s, want Paused", status.State)
	}
	if status.VMMPID == nil || status.HypervisorVersion != testHypervisorVersion {
		t.Errorf("status = %+v, want the process details kept", status)
	}
}

// fail records why, and forgets the process: there is no longer one.
func TestFailClearsProcessDetails(t *testing.T) {
	h := newHarness(t)
	h.running(t)

	h.manager.fail(h.instance.ID, errors.New("boom"))

	status := h.status(t)
	if status.State != StateFailed || status.StateError != "boom" {
		t.Errorf("status = %+v, want Failed/boom", status)
	}
	if status.VMMPID != nil {
		t.Errorf("VMMPID = %d, want it cleared", *status.VMMPID)
	}
}
