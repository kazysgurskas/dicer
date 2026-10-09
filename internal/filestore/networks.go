// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import (
	"maps"
	"path/filepath"
	"slices"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/naming"
	"github.com/konradasb/dicer/internal/network"
)

// networksDir returns the directory every network's definition is kept in.
func (s *Store) networksDir() string {
	return filepath.Join(s.dataDir, "networks")
}

// networkPath returns the file holding the named network's definition.
func (s *Store) networkPath(name string) string {
	return filepath.Join(s.networksDir(), name+".yaml")
}

// loadNetworks reads every network's definition into memory.
func (s *Store) loadNetworks() error {
	names, err := definitionFiles(s.networksDir())
	if err != nil {
		return err
	}
	for _, name := range names {
		var v network.Network
		path := s.networkPath(name)
		if s.readDefinition(path, &v) && s.isWhereNamed(path, name, v.Name) {
			s.putNetwork(v)
		}
	}
	return nil
}

// CreateNetwork records a new network definition, refusing one whose name is
// taken.
func (s *Store) CreateNetwork(v network.Network) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := naming.Validate(v.Name); err != nil {
		return err
	}
	if _, err := s.Network(v.Name); err == nil {
		return errdefs.Exists("network %q already exists", v.Name)
	}

	if err := writeDefinition(s.networkPath(v.Name), v); err != nil {
		return err
	}
	s.putNetwork(v)
	return nil
}

// Network returns a network by name or ID.
func (s *Store) Network(nameOrID string) (network.Network, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if v, ok := s.networks[nameOrID]; ok {
		return v, nil
	}
	if name, ok := s.networkNamesByID[nameOrID]; ok {
		return s.networks[name], nil
	}
	return network.Network{}, errdefs.NotFound("no network %q", nameOrID)
}

// DeleteNetwork removes a network definition, refusing one an instance or a
// snapshot is on.
func (s *Store) DeleteNetwork(nameOrID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	v, err := s.Network(nameOrID)
	if err != nil {
		return err
	}
	for _, i := range s.Instances() {
		if i.NetworkName == v.Name {
			return errdefs.InvalidState("network %q is in use by instance %q", v.Name, i.Name)
		}
	}
	for _, snapshot := range s.Snapshots() {
		if snapshot.Instance.NetworkName == v.Name {
			return errdefs.InvalidState("network %q is in use by snapshot %q: delete the snapshot first",
				v.Name, snapshot.Name)
		}
	}

	if err := removeDefinition(s.networkPath(v.Name)); err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.networks, v.Name)
	delete(s.networkNamesByID, v.ID)
	s.mu.Unlock()
	return nil
}

// Networks returns every network, sorted by name.
func (s *Store) Networks() []network.Network {
	s.mu.RLock()
	defer s.mu.RUnlock()

	networks := make([]network.Network, 0, len(s.networks))
	for _, name := range slices.Sorted(maps.Keys(s.networks)) {
		networks = append(networks, s.networks[name])
	}
	return networks
}

// putNetwork keeps v in memory, under its name and its ID.
func (s *Store) putNetwork(v network.Network) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.networks[v.Name] = v
	if v.ID != "" {
		s.networkNamesByID[v.ID] = v.Name
	}
}
