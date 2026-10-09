// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"errors"
	"sync"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/network"
)

// testCapacity is a host with 4 CPUs and 8GiB, overcommitted as the daemon
// does by default: 16 vCPUs, and 7GiB once 1GiB is reserved.
var testCapacity = Capacity{
	Host:                Resources{VCPUs: 4, MemoryBytes: 8 << 30},
	ReservedMemoryBytes: 1 << 30,
	CPUOvercommit:       4,
	MemoryOvercommit:    1,
}

// admitHarness is a harness on a host with testCapacity, and a second
// instance to compete with.
func admitHarness(t *testing.T) (*harness, Spec) {
	t.Helper()

	h := newHarness(t)
	h.manager.capacity = testCapacity

	store, ok := h.manager.store.(*fakeStore)
	if !ok {
		t.Fatal("harness store is not the fake")
	}
	other := seedInstance(t, store, "other")

	return h, other
}

// holding records instance as in state, holding r, as admission would have.
func holding(t *testing.T, manager *Manager, instance Spec, state State, r Resources) {
	t.Helper()

	if err := manager.writeStatus(Status{
		InstanceID: instance.ID, State: state, VCPUs: r.VCPUs, MemoryBytes: r.MemoryBytes,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStartRefusedWhenTheHostIsFull(t *testing.T) {
	h, other := admitHarness(t)
	h.instance.VCPUs, h.instance.MemoryBytes = 1, 2<<30
	h.define()

	// 6 of the 7GiB are held: 2 more do not fit.
	holding(t, h.manager, other, StateRunning, Resources{VCPUs: 1, MemoryBytes: 6 << 30})

	err := h.manager.start(t.Context(), h.instance)
	if !errors.Is(err, errdefs.ErrResourceExhausted) {
		t.Fatalf("Start = %v, want ErrResourceExhausted", err)
	}

	// A refusal changes nothing: the instance was never started, so it is
	// not Failed either.
	if status := h.status(t); status.State != StateStopped {
		t.Errorf("state after a refused start = %s, want Stopped", status.State)
	}
	if h.starter.vmmCount() != 0 {
		t.Error("a VMM was started for a refused instance")
	}
}

func TestStartAdmittedWhenItFits(t *testing.T) {
	h, other := admitHarness(t)
	h.instance.VCPUs, h.instance.MemoryBytes = 1, 1<<30
	h.define()

	holding(t, h.manager, other, StateRunning, Resources{VCPUs: 1, MemoryBytes: 6 << 30})

	if err := h.manager.start(t.Context(), h.instance); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// What it holds is recorded, and is what it asked for.
	status := h.status(t)
	if got := status.HeldResources(); got != h.instance.Resources() {
		t.Errorf("held = %+v, want %+v", got, h.instance.Resources())
	}
}

// Only instances with a VMM, or about to have one, hold anything.
func TestWhatHoldsResources(t *testing.T) {
	for _, tt := range []struct {
		state State
		holds bool
	}{
		{StateStarting, true},
		{StateRunning, true},
		{StatePaused, true},
		{StateStopping, false},
		{StateStopped, false},
		{StateFailed, false},
	} {
		t.Run(string(tt.state), func(t *testing.T) {
			h, other := admitHarness(t)
			h.instance.VCPUs, h.instance.MemoryBytes = 1, 2<<30
			h.define()
			holding(t, h.manager, other, tt.state, Resources{VCPUs: 1, MemoryBytes: 6 << 30})

			err := h.manager.start(t.Context(), h.instance)
			if refused := errors.Is(err, errdefs.ErrResourceExhausted); refused != tt.holds {
				t.Errorf("another instance %s: Start = %v, want refused %v", tt.state, err, tt.holds)
			}
		})
	}
}

// A restored guest comes back with the memory it was snapshotted with, and
// is admitted on that, not on what the definition says now.
func TestRestoreAdmittedOnTheSnapshotsMemory(t *testing.T) {
	h, other := admitHarness(t)
	h.instance.MemoryBytes = 1 << 30
	h.start(t)

	if _, err := h.manager.createSnapshot(t.Context(), h.instance, "big"); err != nil {
		t.Fatal(err)
	}
	if err := h.manager.stop(t.Context(), h.instance); err != nil {
		t.Fatal(err)
	}

	// Make the snapshot as if taken with 4GiB, and fill the host so
	// only 1GiB is left: enough for the definition, not for the snapshot.
	snapshot := h.store.snapshots["big"]
	snapshot.MemoryBytes = 4 << 30
	h.store.snapshots["big"] = snapshot
	holding(t, h.manager, other, StateRunning, Resources{VCPUs: 1, MemoryBytes: 6 << 30})

	_, err := h.manager.restoreSnapshot(t.Context(), snapshot)
	if !errors.Is(err, errdefs.ErrResourceExhausted) {
		t.Errorf("RestoreSnapshot = %v, want ErrResourceExhausted", err)
	}
}

// Two starts competing for the last room on the host: exactly one is let in.
func TestConcurrentStartsCannotBothTakeTheLastRoom(t *testing.T) {
	h, other := admitHarness(t)
	h.instance.VCPUs, h.instance.MemoryBytes = 1, 1<<30
	h.define()
	other.VCPUs, other.MemoryBytes = 1, 1<<30

	store, _ := h.manager.store.(*fakeStore)
	third := seedInstance(t, store, "third")
	holding(t, h.manager, third, StateRunning, Resources{VCPUs: 1, MemoryBytes: 6 << 30})

	// The two starts never boot: admission is all that is under test, so
	// each is refused or admitted and then left Starting.
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i, instance := range []Spec{h.instance, other} {
		wg.Go(func() {
			lock := h.manager.lock(instance.ID)
			lock.Lock()
			defer lock.Unlock()
			results[i] = h.manager.admit(instance, instance.Resources())
		})
	}
	wg.Wait()

	admitted := 0
	for _, err := range results {
		switch {
		case err == nil:
			admitted++
		case !errors.Is(err, errdefs.ErrResourceExhausted):
			t.Errorf("admit = %v", err)
		}
	}
	if admitted != 1 {
		t.Errorf("%d starts admitted into room for one", admitted)
	}
}

func TestCheckResources(t *testing.T) {
	manager, _, _ := newTestManager(t)
	manager.capacity = testCapacity

	if err := manager.CheckResources(Resources{VCPUs: 4, MemoryBytes: 7 << 30}); err != nil {
		t.Errorf("the whole host: %v", err)
	}
	// More vCPUs than CPUs is never useful to one instance, overcommit or
	// not.
	if err := manager.CheckResources(Resources{VCPUs: 5, MemoryBytes: 1 << 30}); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("5 vCPUs on 4 CPUs: %v, want ErrInvalidArgument", err)
	}
	if err := manager.CheckResources(Resources{VCPUs: 1, MemoryBytes: 8 << 30}); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("more memory than allocatable: %v, want ErrInvalidArgument", err)
	}

	// Without a capacity, nothing is refused.
	manager.capacity = Capacity{}
	if err := manager.CheckResources(Resources{VCPUs: 1000, MemoryBytes: 1 << 50}); err != nil {
		t.Errorf("unlimited: %v", err)
	}
}

// The refusal says what was asked for and what is left, in the units sizes
// are given in.
func TestRefusalExplainsItself(t *testing.T) {
	h, other := admitHarness(t)
	h.instance.VCPUs, h.instance.MemoryBytes = 1, 2<<30
	h.define()
	holding(t, h.manager, other, StateRunning, Resources{VCPUs: 1, MemoryBytes: 6 << 30})

	err := h.manager.start(t.Context(), h.instance)

	want := `instance "web" needs 1 vCPU, 2 GiB, but 1 vCPU, 6 GiB of the 16 vCPU, 7 GiB ` +
		`this host allows is committed`
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}

// dataMount mounts the volume data at /data.
func dataMount(readOnly bool) []Mount {
	return []Mount{{Type: MountTypeVolume, Source: "data", Target: "/data", ReadOnly: readOnly}}
}

// seedVolumeHolder defines another instance mounting data, recorded as in
// state.
func (h *harness) seedVolumeHolder(t *testing.T, state State, readOnly bool) {
	t.Helper()

	other := seedInstance(t, h.store, "other")
	other.Mounts = dataMount(readOnly)
	h.store.instances[other.Name] = other

	if err := h.manager.writeStatus(Status{InstanceID: other.ID, State: state}); err != nil {
		t.Fatalf("writeStatus: %v", err)
	}
}

func TestAdmitVolumeSharing(t *testing.T) {
	const rw, ro = false, true
	tests := []struct {
		name    string
		mine    bool // read-only
		theirs  bool
		state   State
		refused bool
	}{
		{"read-write beside running read-write", rw, rw, StateRunning, true},
		{"read-write beside starting read-write", rw, rw, StateStarting, true},
		{"read-write beside stopping read-write", rw, rw, StateStopping, true},
		{"read-write beside running read-only", rw, ro, StateRunning, true},
		{"read-only beside paused read-write", ro, rw, StatePaused, true},
		{"read-only beside running read-only", ro, ro, StateRunning, false},
		{"read-write beside stopped read-write", rw, rw, StateStopped, false},
		{"read-write beside failed read-write", rw, rw, StateFailed, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.instance.Mounts = dataMount(tt.mine)
			h.store.instances[h.instance.Name] = h.instance
			h.seedVolumeHolder(t, tt.state, tt.theirs)

			err := h.manager.admit(h.instance, h.instance.Resources())
			if refused := errors.Is(err, errdefs.ErrInvalidState); refused != tt.refused {
				t.Fatalf("admit = %v, want refused %v", err, tt.refused)
			}
			if tt.refused {
				if status := h.status(t); status.State != StateStopped {
					t.Errorf("state = %s, want a refused instance left %s", status.State, StateStopped)
				}
			}
		})
	}
}

// seedRunning defines another instance, recorded as in state.
func (h *harness) seedRunning(t *testing.T, name string, state State, ports ...network.PortMapping) Spec {
	t.Helper()

	other := seedInstance(t, h.store, name)
	other.Ports = ports
	h.store.instances[name] = other

	if err := h.manager.writeStatus(Status{InstanceID: other.ID, State: state}); err != nil {
		t.Fatalf("writeStatus: %v", err)
	}
	return other
}

func TestStartRefusesPortHeldByAnotherInstance(t *testing.T) {
	for _, state := range []State{StateRunning, StatePaused, StateStarting, StateStopping} {
		t.Run(string(state), func(t *testing.T) {
			h := newHarness(t)
			h.withPorts(network.PortMapping{HostIP: "192.0.2.1", HostPort: 8080, GuestPort: 80})
			h.seedRunning(t, "db", state, network.PortMapping{HostPort: 8080, GuestPort: 5432})

			err := h.manager.start(t.Context(), h.instance)
			if !errors.Is(err, errdefs.ErrInvalidState) {
				t.Fatalf("Start = %v, want a refusal for the port %s instance holds", err, state)
			}
			if len(h.hostNetwork.published) != 0 {
				t.Errorf("published = %+v, want nothing", h.hostNetwork.published)
			}
			// Refused at admission: the instance was never Starting, so
			// it is left as it was rather than Failed.
			if status := h.status(t); status.State != StateStopped {
				t.Errorf("state = %s, want %s", status.State, StateStopped)
			}
		})
	}
}

func TestStartAllowsPortsThatDoNotClash(t *testing.T) {
	h := newHarness(t)
	h.withPorts(network.PortMapping{HostPort: 8080, GuestPort: 80})
	// A different port, a different protocol, and the same port on an
	// instance that is not running.
	h.seedRunning(t, "db", StateRunning, network.PortMapping{HostPort: 5432, GuestPort: 5432})
	h.seedRunning(t, "dns", StateRunning, network.PortMapping{HostPort: 8080, GuestPort: 80, Protocol: "udp"})
	h.seedRunning(t, "old", StateStopped, network.PortMapping{HostPort: 8080, GuestPort: 80})

	h.start(t)
}
