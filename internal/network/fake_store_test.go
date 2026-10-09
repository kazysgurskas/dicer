// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package network

import (
	"sort"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
)

// fakeStore is a Store of the networks it holds, by name. It refuses to
// delete those in inUse, as a store does those an instance is on.
type fakeStore struct {
	networks map[string]Network
	inUse    map[string]bool
}

// newFakeStore returns a fakeStore holding networks.
func newFakeStore(networks ...Network) *fakeStore {
	s := &fakeStore{networks: map[string]Network{}, inUse: map[string]bool{}}
	for _, n := range networks {
		s.networks[n.Name] = n
	}
	return s
}

func (s *fakeStore) CreateNetwork(n Network) error {
	if _, ok := s.networks[n.Name]; ok {
		return errdefs.Exists("network %q already exists", n.Name)
	}
	s.networks[n.Name] = n
	return nil
}

func (s *fakeStore) Network(nameOrID string) (Network, error) {
	for _, n := range s.networks {
		if n.Name == nameOrID || n.ID == nameOrID {
			return n, nil
		}
	}
	return Network{}, errdefs.NotFound("no network %q", nameOrID)
}

func (s *fakeStore) Networks() []Network {
	networks := make([]Network, 0, len(s.networks))
	for _, n := range s.networks {
		networks = append(networks, n)
	}
	sort.Slice(networks, func(i, j int) bool { return networks[i].Name < networks[j].Name })
	return networks
}

func (s *fakeStore) DeleteNetwork(nameOrID string) error {
	n, err := s.Network(nameOrID)
	if err != nil {
		return err
	}
	if s.inUse[n.Name] {
		return errdefs.InvalidState("network %q is in use by instance %q", n.Name, "web")
	}
	delete(s.networks, n.Name)
	return nil
}

// fakeRecorder keeps the events it is given.
type fakeRecorder struct{ events []event.Event }

func (r *fakeRecorder) Record(e event.Event) { r.events = append(r.events, e) }
