// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"errors"
	"slices"
	"testing"

	"github.com/konradasb/dicer/internal/network"
)

func TestStartPublishesPortsAndStopUnpublishes(t *testing.T) {
	h := newHarness(t)
	h.withPorts(network.PortMapping{HostPort: 8080, GuestPort: 80})
	h.start(t)

	allocation, err := h.manager.allocationOf(h.instance)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := h.hostNetwork.published[h.instance.ID]
	if !ok || got.ip != allocation.IP || !slices.Equal(got.ports, h.instance.Ports) {
		t.Fatalf("published = %+v, want %v at the instance's address %s", got, h.instance.Ports, allocation.IP)
	}

	if err := h.manager.stop(t.Context(), h.instance); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, ok := h.hostNetwork.published[h.instance.ID]; ok {
		t.Error("ports are still published after stop")
	}
}

func TestStartWithoutPortsPublishesNothing(t *testing.T) {
	h := newHarness(t)
	h.start(t)

	if len(h.hostNetwork.published) != 0 {
		t.Errorf("published = %+v, want nothing for an instance without ports", h.hostNetwork.published)
	}
}

func TestCrashUnpublishesPorts(t *testing.T) {
	h := newHarness(t)
	h.withPorts(network.PortMapping{HostPort: 8080, GuestPort: 80})
	h.start(t)

	h.crash(t)
	h.waitForState(t, StateFailed)

	if _, ok := h.hostNetwork.published[h.instance.ID]; ok {
		t.Error("ports are still published after the VMM crashed")
	}
}

func TestFailedPublishUndoesNetwork(t *testing.T) {
	h := newHarness(t)
	h.withPorts(network.PortMapping{HostPort: 22, GuestPort: 22})
	h.hostNetwork.publishErr = errors.New("host port is in use")

	if err := h.manager.start(t.Context(), h.instance); err == nil {
		t.Fatal("Start succeeded although its ports could not be published")
	}
	if !slices.Contains(h.hostNetwork.removedTAPs, h.instance.ID) {
		t.Errorf("removed TAPs = %v, want the instance's TAP gone after the failed start", h.hostNetwork.removedTAPs)
	}
}

func TestRestoreSnapshotPublishesPorts(t *testing.T) {
	h := newHarness(t)
	h.withPorts(network.PortMapping{HostPort: 8080, GuestPort: 80})
	h.running(t)

	snapshot, err := h.manager.createSnapshot(t.Context(), h.instance, "good")
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	h.stopped(t)

	if _, err := h.manager.restoreSnapshot(t.Context(), snapshot); err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}
	if _, ok := h.hostNetwork.published[h.instance.ID]; !ok {
		t.Error("ports were not published for the restored instance")
	}
}
