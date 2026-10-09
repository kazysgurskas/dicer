// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"errors"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
)

// Every operation on an instance takes its name or ID, and refuses one that
// names no instance.
func TestOperationsOnAnUnknownInstanceAreNotFound(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()

	for name, call := range map[string]func() error{
		"Start":   func() error { return h.manager.Start(ctx, "ghost") },
		"Stop":    func() error { return h.manager.Stop(ctx, "ghost") },
		"Pause":   func() error { return h.manager.Pause(ctx, "ghost") },
		"Resume":  func() error { return h.manager.Resume(ctx, "ghost") },
		"Standby": func() error { return h.manager.Standby(ctx, "ghost") },
		"Resize":  func() error { return h.manager.Resize(ctx, "ghost", Resources{VCPUs: 1}) },
		"Delete":  func() error { return h.manager.Delete(ctx, "ghost", false) },
		"Rename": func() error {
			_, err := h.manager.Rename(ctx, "ghost", "other")
			return err
		},
		"CreateSnapshot": func() error {
			_, err := h.manager.CreateSnapshot(ctx, "ghost", "")
			return err
		},
		"DeleteSnapshot": func() error { return h.manager.DeleteSnapshot(ctx, "ghost") },
		"Status": func() error {
			_, err := h.manager.Status("ghost")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, errdefs.ErrNotFound) {
				t.Errorf("%s = %v, want a not found error", name, err)
			}
		})
	}
}

// An operation acts on the definition as it is once it holds the
// instance's lock, found by name or by ID.
func TestOperationsFindAnInstanceByNameOrID(t *testing.T) {
	h := newHarness(t)

	if err := h.manager.Start(t.Context(), h.instance.Name); err != nil {
		t.Fatalf("Start by name: %v", err)
	}
	if err := h.manager.Stop(t.Context(), h.instance.ID); err != nil {
		t.Fatalf("Stop by ID: %v", err)
	}
	if status, err := h.manager.Status(h.instance.Name); err != nil || status.State != StateStopped {
		t.Errorf("Status = %+v, %v; want stopped", status, err)
	}
}
