// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/naming"
)

// snapshotsDir returns the directory every snapshot is kept in.
func (s *Store) snapshotsDir() string {
	return filepath.Join(s.dataDir, "snapshots")
}

// SnapshotDir returns the directory holding a snapshot's files.
func (s *Store) SnapshotDir(name string) string {
	return filepath.Join(s.snapshotsDir(), name)
}

// snapshotPath returns the file holding the named snapshot's definition.
func (s *Store) snapshotPath(name string) string {
	return filepath.Join(s.SnapshotDir(name), configFile)
}

// loadSnapshots reads every snapshot's definition into memory.
func (s *Store) loadSnapshots() error {
	names, err := s.definitionDirs(s.snapshotsDir())
	if err != nil {
		return err
	}
	for _, name := range names {
		var v instance.Snapshot
		path := s.snapshotPath(name)
		if s.readDefinition(path, &v) && s.isWhereNamed(path, name, v.Name) {
			s.putSnapshot(v)
		}
	}
	return nil
}

// StageSnapshot returns a new, empty directory beside the snapshots for a
// snapshot's files to be written in before CreateSnapshot moves it into
// place. One a crash leaves behind is removed when the store next loads.
func (s *Store) StageSnapshot() (string, error) {
	dir, err := os.MkdirTemp(s.snapshotsDir(), stagingPrefix)
	if err != nil {
		return "", fmt.Errorf("create snapshot staging directory: %w", err)
	}
	return dir, nil
}

// CreateSnapshot records a new snapshot whose files are in staged, a
// directory from StageSnapshot, moving it into place. A snapshot is thus
// either whole or absent. It refuses one whose name is taken, or whose
// instance names a kernel, network or volume that does not exist.
func (s *Store) CreateSnapshot(v instance.Snapshot, staged string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := naming.Validate(v.Name); err != nil {
		return err
	}
	if _, err := s.Snapshot(v.Name); err == nil {
		return errdefs.Exists("snapshot %q already exists", v.Name)
	}
	if err := s.checkReferences(v.Instance); err != nil {
		return err
	}

	if err := writeDefinition(filepath.Join(staged, configFile), v); err != nil {
		return err
	}
	if err := os.Rename(staged, s.SnapshotDir(v.Name)); err != nil {
		return fmt.Errorf("move %s into place: %w", staged, err)
	}
	s.putSnapshot(v)
	return nil
}

// Snapshot returns a snapshot by name or ID.
func (s *Store) Snapshot(nameOrID string) (instance.Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if v, ok := s.snapshots[nameOrID]; ok {
		return v, nil
	}
	if name, ok := s.snapshotNamesByID[nameOrID]; ok {
		return s.snapshots[name], nil
	}
	return instance.Snapshot{}, errdefs.NotFound("no snapshot %q", nameOrID)
}

// Snapshots returns every snapshot, sorted by name.
func (s *Store) Snapshots() []instance.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshots := make([]instance.Snapshot, 0, len(s.snapshots))
	for _, name := range slices.Sorted(maps.Keys(s.snapshots)) {
		snapshots = append(snapshots, s.snapshots[name])
	}
	return snapshots
}

// DeleteSnapshot removes a snapshot and its files.
func (s *Store) DeleteSnapshot(nameOrID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	v, err := s.Snapshot(nameOrID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(s.SnapshotDir(v.Name)); err != nil {
		return fmt.Errorf("remove %s: %w", s.SnapshotDir(v.Name), err)
	}

	s.mu.Lock()
	delete(s.snapshots, v.Name)
	delete(s.snapshotNamesByID, v.ID)
	s.mu.Unlock()
	return nil
}

// putSnapshot keeps v in memory, under its name and its ID.
func (s *Store) putSnapshot(v instance.Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.snapshots[v.Name] = v
	if v.ID != "" {
		s.snapshotNamesByID[v.ID] = v.Name
	}
}
