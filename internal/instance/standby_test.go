// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
	"github.com/konradasb/dicer/internal/hypervisor"
	"github.com/konradasb/dicer/internal/network"
	"github.com/konradasb/dicer/internal/volume"
)

// TestStandbyFreesTheHostAndStartResumes checks that standby ends the VMM,
// so that the instance holds no CPU or memory, and that a start resumes the
// guest frozen on disk rather than booting it afresh.
func TestStandbyFreesTheHostAndStartResumes(t *testing.T) {
	h := newHarness(t)
	h.start(t)
	vmm := h.starter.vmm()

	if err := h.manager.Standby(t.Context(), h.instance.Name); err != nil {
		t.Fatalf("Standby: %v", err)
	}

	status := h.status(t)
	if status.State != StateStandby {
		t.Errorf("state = %s, want %s", status.State, StateStandby)
	}
	if held := status.HeldResources(); held != (Resources{}) {
		t.Errorf("an instance on standby holds %s, want nothing", held)
	}
	select {
	case <-vmm.Done():
	default:
		t.Error("the VMM is still running")
	}
	for _, f := range []string{standbyFile, "vmstate"} {
		if _, err := os.Stat(filepath.Join(h.manager.standbyDir(h.instance), f)); err != nil {
			t.Errorf("standby is missing %s: %v", f, err)
		}
	}
	if _, ok := h.events.last(event.ActionStandby); !ok {
		t.Error("no standby event was recorded")
	}

	if err := h.manager.start(t.Context(), h.instance); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if len(h.starter.restoredFrom) != 1 || h.starter.restoredFrom[0] != h.manager.standbyDir(h.instance) {
		t.Errorf("restored from %v, want the standby directory", h.starter.restoredFrom)
	}
	if status := h.status(t); status.State != StateRunning {
		t.Errorf("state = %s, want %s", status.State, StateRunning)
	}
	if _, err := os.Stat(h.manager.standbyDir(h.instance)); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the standby is still there after the instance resumed")
	}
	if h.agent.clockSets != 1 || len(h.agent.identities) != 0 {
		t.Errorf("clock set %d times and %d identities given, want 1 and none", h.agent.clockSets, len(h.agent.identities))
	}
}

// TestStandbyOutlivesTheRuntimeStatus checks that an instance is still on
// standby once its runtime status is gone, as a host reboot takes it.
// TestStandbyReplacesAStaleOne checks that a standby left in place by a
// resume that could not remove it does not stop the instance going on
// standby again.
func TestStandbyReplacesAStaleOne(t *testing.T) {
	h := newHarness(t)
	h.start(t)
	stale := filepath.Join(h.manager.standbyDir(h.instance), "stale")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := h.manager.Standby(t.Context(), h.instance.Name); err != nil {
		t.Fatalf("Standby: %v", err)
	}
	if status := h.status(t); status.State != StateStandby {
		t.Errorf("state = %s, want %s", status.State, StateStandby)
	}
	if _, err := os.Stat(stale); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the stale standby is still there: %v", err)
	}
}

func TestStandbyOutlivesTheRuntimeStatus(t *testing.T) {
	h := newHarness(t)
	h.start(t)
	if err := h.manager.Standby(t.Context(), h.instance.Name); err != nil {
		t.Fatal(err)
	}

	if err := h.manager.removeRuntimeDir(h.instance.ID); err != nil {
		t.Fatal(err)
	}

	if status := h.status(t); status.State != StateStandby {
		t.Errorf("state = %s, want %s", status.State, StateStandby)
	}
}

// TestStandbyKeepsItsPortsAndVolumes checks that no other instance can take
// the host port or writable volume an instance on standby resumes with.
func TestStandbyKeepsItsPortsAndVolumes(t *testing.T) {
	tests := []struct {
		name  string
		share func(*Spec)
	}{
		{"host port", func(s *Spec) {
			s.Ports = []network.PortMapping{{HostPort: 8080, GuestPort: 80}}
		}},
		{"writable volume", func(s *Spec) {
			s.Mounts = []Mount{{Type: MountTypeVolume, Source: "data", Target: "/data"}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			volumes := fakeVolumes{dir: t.TempDir()}
			h.manager.volumes = volumes
			h.store.volumes["data"] = volume.Volume{ID: "vol-data", Name: "data"}
			disk := volumes.Path(volume.Volume{ID: "vol-data"})
			if err := os.MkdirAll(filepath.Dir(disk), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(disk, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			tt.share(&h.instance)
			h.store.instances[h.instance.Name] = h.instance
			h.start(t)
			if err := h.manager.Standby(t.Context(), h.instance.Name); err != nil {
				t.Fatal(err)
			}

			other := seedInstance(t, h.store, "other")
			tt.share(&other)
			h.store.instances[other.Name] = other

			if err := h.manager.start(t.Context(), other); !errors.Is(err, errdefs.ErrInvalidState) {
				t.Errorf("Start of an instance sharing the %s = %v, want ErrInvalidState", tt.name, err)
			}
		})
	}
}

// TestStopDiscardsStandby checks that stopping an instance on standby throws
// away its frozen guest, so that the next start boots it afresh.
func TestStopDiscardsStandby(t *testing.T) {
	h := newHarness(t)
	h.start(t)
	if err := h.manager.Standby(t.Context(), h.instance.Name); err != nil {
		t.Fatal(err)
	}

	if err := h.manager.stop(t.Context(), h.instance); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if status := h.status(t); status.State != StateStopped {
		t.Errorf("state = %s, want %s", status.State, StateStopped)
	}

	if err := h.manager.start(t.Context(), h.instance); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(h.starter.restoredFrom) != 0 {
		t.Error("a stopped instance was resumed from a discarded standby")
	}
}

func TestStandbyRejections(t *testing.T) {
	t.Run("stopped instance", func(t *testing.T) {
		h := newHarness(t)

		if err := h.manager.Standby(t.Context(), h.instance.Name); !errors.Is(err, errdefs.ErrInvalidState) {
			t.Errorf("Standby of a stopped instance = %v, want ErrInvalidState", err)
		}
	})

	t.Run("hypervisor without snapshots", func(t *testing.T) {
		h := newHarness(t)
		h.start(t)
		h.hv.capabilities = hypervisor.Capabilities{SupportsPause: true}

		if err := h.manager.Standby(t.Context(), h.instance.Name); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("Standby = %v, want ErrUnsupported", err)
		}
	})

	t.Run("instance on standby changed", func(t *testing.T) {
		h := newHarness(t)
		h.start(t)
		if err := h.manager.Standby(t.Context(), h.instance.Name); err != nil {
			t.Fatal(err)
		}

		changed := h.instance
		changed.VCPUs++
		if err := h.manager.Update(t.Context(), changed); !errors.Is(err, errdefs.ErrInvalidState) {
			t.Errorf("Update of an instance on standby = %v, want ErrInvalidState", err)
		}
	})
}

// TestFailedStandbyKeepsTheGuestRunning checks that a standby that cannot
// freeze the guest leaves it running as it was, with nothing frozen.
func TestFailedStandbyKeepsTheGuestRunning(t *testing.T) {
	h := newHarness(t)
	h.start(t)
	h.hv.snapshotErr = errors.New("out of disk")

	if err := h.manager.Standby(t.Context(), h.instance.Name); err == nil {
		t.Fatal("Standby succeeded despite the hypervisor failing")
	}

	if status := h.status(t); status.State != StateRunning {
		t.Errorf("state = %s, want %s", status.State, StateRunning)
	}
	if h.hv.paused != 1 || h.hv.resumed != 1 {
		t.Errorf("paused %d times and resumed %d, want 1 and 1", h.hv.paused, h.hv.resumed)
	}
	if h.manager.onStandby(h.instance) {
		t.Error("a failed standby left a frozen guest behind")
	}
}

// TestStandbyWaitsForMemoryWithTheGuestRunning checks that an instance woken
// moments ago, whose memory is still being restored, goes on standby once it
// has been, and runs while it waits.
func TestStandbyWaitsForMemoryWithTheGuestRunning(t *testing.T) {
	h := newHarness(t)
	h.start(t)
	h.hv.restoringSnapshots = 1

	if err := h.manager.Standby(t.Context(), h.instance.Name); err != nil {
		t.Fatalf("Standby: %v", err)
	}

	if h.hv.paused != 2 || h.hv.resumed != 1 {
		t.Errorf("paused %d times and resumed %d, want 2 and 1", h.hv.paused, h.hv.resumed)
	}
	if !h.manager.onStandby(h.instance) {
		t.Error("instance is not on standby")
	}
}
