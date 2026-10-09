// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import (
	"maps"
	"path/filepath"
	"slices"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/naming"
	"github.com/konradasb/dicer/internal/volume"
)

// volumesDir returns the directory every volume's definition is kept in.
func (s *Store) volumesDir() string {
	return filepath.Join(s.dataDir, "volumes")
}

// volumePath returns the file holding the named volume's definition.
func (s *Store) volumePath(name string) string {
	return filepath.Join(s.volumesDir(), name+".yaml")
}

// loadVolumes reads every volume's definition into memory.
func (s *Store) loadVolumes() error {
	names, err := definitionFiles(s.volumesDir())
	if err != nil {
		return err
	}
	for _, name := range names {
		var v volume.Volume
		path := s.volumePath(name)
		if s.readDefinition(path, &v) && s.isWhereNamed(path, name, v.Name) {
			s.putVolume(v)
		}
	}
	return nil
}

// CreateVolume records a new volume definition, refusing one whose name is
// taken.
func (s *Store) CreateVolume(v volume.Volume) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := naming.Validate(v.Name); err != nil {
		return err
	}
	if _, err := s.Volume(v.Name); err == nil {
		return errdefs.Exists("volume %q already exists", v.Name)
	}

	if err := writeDefinition(s.volumePath(v.Name), v); err != nil {
		return err
	}
	s.putVolume(v)
	return nil
}

// Volume returns a volume by name or ID.
func (s *Store) Volume(nameOrID string) (volume.Volume, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if v, ok := s.volumes[nameOrID]; ok {
		return v, nil
	}
	if name, ok := s.volumeNamesByID[nameOrID]; ok {
		return s.volumes[name], nil
	}
	return volume.Volume{}, errdefs.NotFound("no volume %q", nameOrID)
}

// DeleteVolume removes a volume definition, refusing one an instance or a
// snapshot mounts. The backing disk is the volume manager's to delete.
func (s *Store) DeleteVolume(nameOrID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	v, err := s.Volume(nameOrID)
	if err != nil {
		return err
	}
	for _, i := range s.Instances() {
		if _, ok := i.VolumeMount(v.Name); ok {
			return errdefs.InvalidState("volume %q is in use by instance %q", v.Name, i.Name)
		}
	}
	for _, snapshot := range s.Snapshots() {
		if _, ok := snapshot.Instance.VolumeMount(v.Name); ok {
			return errdefs.InvalidState("volume %q is in use by snapshot %q: delete the snapshot first",
				v.Name, snapshot.Name)
		}
	}

	if err := removeDefinition(s.volumePath(v.Name)); err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.volumes, v.Name)
	delete(s.volumeNamesByID, v.ID)
	s.mu.Unlock()
	return nil
}

// Volumes returns every volume, sorted by name.
func (s *Store) Volumes() []volume.Volume {
	s.mu.RLock()
	defer s.mu.RUnlock()

	volumes := make([]volume.Volume, 0, len(s.volumes))
	for _, name := range slices.Sorted(maps.Keys(s.volumes)) {
		volumes = append(volumes, s.volumes[name])
	}
	return volumes
}

// putVolume keeps v in memory, under its name and its ID.
func (s *Store) putVolume(v volume.Volume) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.volumes[v.Name] = v
	if v.ID != "" {
		s.volumeNamesByID[v.ID] = v.Name
	}
}
