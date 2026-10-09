// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package network

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
)

// newDefiningManager returns a Manager over a fake store, recording into a
// fake recorder, on a host whose own subnet is 192.168.1.0/24.
func newDefiningManager(t *testing.T) (*Manager, *fakeStore, *fakeRecorder) {
	t.Helper()

	m := newTestManager(t)
	store, events := newFakeStore(), &fakeRecorder{}
	m.store, m.events = store, events
	m.hostSubnets = func() ([]netip.Prefix, error) {
		return []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}, nil
	}
	return m, store, events
}

func TestCreateDefinesANetwork(t *testing.T) {
	m, store, events := newDefiningManager(t)

	n, err := m.Create(Spec{Name: "lan", Subnet: "10.9.0.0/24", Isolated: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got, err := store.Network("lan"); err != nil || got.ID != n.ID || got.Gateway != "10.9.0.1" {
		t.Errorf("stored network = %+v, %v; want %+v", got, err, n)
	}
	want := "Created network with subnet 10.9.0.0/24, gateway 10.9.0.1; isolated: its instances cannot reach each other"
	if len(events.events) != 1 || events.events[0].Action != event.ActionCreated || events.events[0].Message != want {
		t.Fatalf("events = %+v, want one creation: %q", events.events, want)
	}
	if e := events.events[0]; e.Kind != event.KindNetwork || e.ID != n.ID ||
		e.Attributes["subnet"] != "10.9.0.0/24" || e.Attributes["gateway"] != "10.9.0.1" {
		t.Errorf("event = %+v, want network lan with its subnet and gateway", e)
	}
}

func TestCreateRefusesWhatWouldClash(t *testing.T) {
	tests := []struct {
		name string
		spec Spec
		want error
	}{
		{"a name taken", Spec{Name: "lan", Subnet: "10.8.0.0/24"}, errdefs.ErrExists},
		{"another network's subnet", Spec{Name: "other", Subnet: "10.9.0.128/25"}, errdefs.ErrExists},
		{"the host's subnet", Spec{Name: "other", Subnet: "192.168.1.0/24"}, errdefs.ErrExists},
		{"no subnet", Spec{Name: "other"}, errdefs.ErrInvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, _ := newDefiningManager(t)
			if _, err := m.Create(Spec{Name: "lan", Subnet: "10.9.0.0/24"}); err != nil {
				t.Fatal(err)
			}

			if _, err := m.Create(tt.spec); !errors.Is(err, tt.want) {
				t.Errorf("Create = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestDeleteRemovesTheDefinitionAndTheAllocations(t *testing.T) {
	m, store, events := newDefiningManager(t)
	n, err := m.Create(Spec{Name: "lan", Subnet: "10.9.0.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Allocate(n, "i-1", ""); err != nil {
		t.Fatal(err)
	}

	if err := m.Delete("lan"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Network("lan"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("the definition is still stored: %v", err)
	}
	if allocations, _ := m.List("lan"); len(allocations) != 0 {
		t.Errorf("allocations = %+v after Delete, want none", allocations)
	}
	if last := events.events[len(events.events)-1]; last.Action != event.ActionDeleted ||
		last.Message != "Deleted network with subnet 10.9.0.0/24" {
		t.Errorf("last event = %+v, want the deletion", last)
	}
}

func TestDeleteRefusesTheDefaultNetworkAndOneInUse(t *testing.T) {
	m, store, _ := newDefiningManager(t)
	if err := m.EnsureDefault("10.250.0.0/16"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(Spec{Name: "lan", Subnet: "10.9.0.0/24"}); err != nil {
		t.Fatal(err)
	}
	store.inUse["lan"] = true

	if err := m.Delete(DefaultName); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Delete of the default network = %v, want an invalid argument error", err)
	}
	if err := m.Delete("lan"); !errors.Is(err, errdefs.ErrInvalidState) {
		t.Errorf("Delete of a network in use = %v, want an invalid state error", err)
	}
}

func TestDefaultNetworkIsCreatedOnce(t *testing.T) {
	m, store, _ := newDefiningManager(t)

	if err := m.EnsureDefault("10.250.0.0/16"); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	n, err := store.Network(DefaultName)
	if err != nil || n.Subnet != "10.250.0.0/16" || n.Gateway != "10.250.0.1" {
		t.Fatalf("default network = %+v, %v; want it on 10.250.0.0/16", n, err)
	}

	// A later start leaves it as it is, even when the subnet configured has
	// changed.
	if err := m.EnsureDefault("10.251.0.0/16"); err != nil {
		t.Fatalf("EnsureDefault again: %v", err)
	}
	if again, _ := store.Network(DefaultName); again.ID != n.ID || again.Subnet != n.Subnet {
		t.Errorf("default network = %+v after a second start, want %+v", again, n)
	}
}

func TestDefaultNetworkRefusesATakenSubnet(t *testing.T) {
	m, store, _ := newDefiningManager(t)
	if err := store.CreateNetwork(Network{ID: "n-1", Name: "lan", Subnet: "10.250.1.0/24"}); err != nil {
		t.Fatal(err)
	}

	if err := m.EnsureDefault("10.250.0.0/16"); !errors.Is(err, errdefs.ErrExists) {
		t.Errorf("EnsureDefault = %v, want the overlap refused", err)
	}
}
