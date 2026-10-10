// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/konradasb/dicer/internal/errdefs"
)

func TestUpdateReleasesTheAddressOfAMovedInstance(t *testing.T) {
	h := newHarness(t)
	h.start(t)
	if err := h.manager.stop(t.Context(), h.instance); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := h.manager.allocationOf(h.instance); err != nil {
		t.Fatalf("a stopped instance keeps its address: %v", err)
	}

	moved := h.instance
	moved.StaticIP = "10.0.0.200"
	if err := h.manager.Update(t.Context(), moved); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if _, err := h.manager.allocationOf(h.instance); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("Address = %v, want the old address released for the new static IP", err)
	}
}

// TestUpdateRefusesADefinitionReadBeforeARename checks that an update made
// from a definition read before the instance was renamed does not write it
// back under the old name.
func TestUpdateRefusesADefinitionReadBeforeARename(t *testing.T) {
	h := newHarness(t)
	stale := h.instance
	if _, err := h.manager.Rename(t.Context(), h.instance.ID, "api"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	stale.VCPUs++
	if err := h.manager.Update(t.Context(), stale); !errors.Is(err, errdefs.ErrInvalidState) {
		t.Errorf("Update = %v, want a refusal of the definition read before the rename", err)
	}
	if _, err := h.store.Instance(stale.Name); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("an instance %q exists after the update: %v", stale.Name, err)
	}
}

func TestUpdateRefusesARunningInstance(t *testing.T) {
	h := newHarness(t)
	h.start(t)

	changed := h.instance
	changed.VCPUs++
	if err := h.manager.Update(t.Context(), changed); !errors.Is(err, errdefs.ErrInvalidState) {
		t.Fatalf("Update = %v, want a refusal while the instance runs", err)
	}
}

// A restart policy is read when the instance ends, so changing it alone is
// fine while it runs -- and takes effect for the next end.
func TestUpdateChangesTheRestartPolicyOfARunningInstance(t *testing.T) {
	h := newHarness(t)
	h.restartAtOnce()
	h.start(t)

	changed, err := h.store.Instance(h.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	changed.Restart = RestartPolicy{Mode: RestartModeAlways}
	changed.UpdatedAt = changed.UpdatedAt.Add(time.Second)
	if err := h.manager.Update(t.Context(), changed); err != nil {
		t.Fatalf("Update = %v, want the restart policy changed", err)
	}

	h.crash(t)
	h.waitForVMMs(t, 2)
	h.waitForState(t, StateRunning)
}

// An overlay disk grows to a larger disk_bytes at the next start, but cannot
// shrink, so an update to less than it has is refused.
func TestUpdateRefusesToShrinkTheOverlayDisk(t *testing.T) {
	tests := []struct {
		name       string
		diskBytes  int64
		hasOverlay bool
		want       error
	}{
		{name: "larger", diskBytes: 64 << 20, hasOverlay: true},
		{name: "smaller", diskBytes: 512 << 10, hasOverlay: true, want: errdefs.ErrInvalidArgument},
		{name: "smaller before the first start", diskBytes: 512 << 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			if !tt.hasOverlay {
				if err := os.Remove(h.overlay); err != nil {
					t.Fatal(err)
				}
			}

			changed := h.instance
			changed.DiskBytes = tt.diskBytes
			if err := h.manager.Update(t.Context(), changed); !errors.Is(err, tt.want) {
				t.Errorf("Update = %v, want %v", err, tt.want)
			}
		})
	}
}
