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

// InstanceDir returns an instance's persistent directory, which holds its
// definition and its overlay disk, and is removed when the instance is
// deleted.
func (s *Store) InstanceDir(name string) string {
	return filepath.Join(s.dataDir, "instances", name)
}

// instancePath returns the file holding the named instance's definition.
func (s *Store) instancePath(name string) string {
	return filepath.Join(s.InstanceDir(name), configFile)
}

// loadInstances reads every instance's definition into memory.
func (s *Store) loadInstances() error {
	names, err := s.definitionDirs(filepath.Join(s.dataDir, "instances"))
	if err != nil {
		return err
	}
	for _, name := range names {
		var v instance.Spec
		path := s.instancePath(name)
		if s.readDefinition(path, &v) && s.isWhereNamed(path, name, v.Name) {
			s.putInstance(v)
		}
	}
	return nil
}

// CreateInstance records a new instance definition, refusing one whose name
// is taken or that names a kernel, network or volume that does not exist.
func (s *Store) CreateInstance(v instance.Spec) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := naming.Validate(v.Name); err != nil {
		return err
	}
	if _, err := s.Instance(v.Name); err == nil {
		return errdefs.Exists("instance %q already exists", v.Name)
	}
	if err := s.checkReferences(v); err != nil {
		return err
	}

	if err := os.MkdirAll(s.InstanceDir(v.Name), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", s.InstanceDir(v.Name), err)
	}
	if err := writeDefinition(s.instancePath(v.Name), v); err != nil {
		return err
	}
	s.putInstance(v)
	return nil
}

// Instance returns an instance by name or ID.
func (s *Store) Instance(nameOrID string) (instance.Spec, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if v, ok := s.instances[nameOrID]; ok {
		return v, nil
	}
	if name, ok := s.instanceNamesByID[nameOrID]; ok {
		return s.instances[name], nil
	}
	return instance.Spec{}, errdefs.NotFound("no instance %q", nameOrID)
}

// UpdateInstance overwrites an existing instance definition, refusing one
// that names a kernel, network or volume that does not exist.
func (s *Store) UpdateInstance(v instance.Spec) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if _, err := s.Instance(v.Name); err != nil {
		return err
	}
	if err := s.checkReferences(v); err != nil {
		return err
	}

	if err := writeDefinition(s.instancePath(v.Name), v); err != nil {
		return err
	}
	s.putInstance(v)
	return nil
}

// RenameInstance moves an instance to a new name, taking its directory, with
// its overlay disk, console log and snapshots, with it.
func (s *Store) RenameInstance(nameOrID string, renamed instance.Spec) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	current, err := s.Instance(nameOrID)
	if err != nil {
		return err
	}
	if err := s.checkReferences(renamed); err != nil {
		return err
	}
	if err := naming.Validate(renamed.Name); err != nil {
		return err
	}
	if renamed.Name == current.Name {
		return nil
	}
	if _, err := s.Instance(renamed.Name); err == nil {
		return errdefs.Exists("instance %q already exists", renamed.Name)
	}

	// The directory moves first: a rename that fails halfway must leave the
	// definition where its files are, not where they are not.
	from, to := s.InstanceDir(current.Name), s.InstanceDir(renamed.Name)
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("rename %s to %s: %w", from, to, err)
	}
	if err := writeDefinition(s.instancePath(renamed.Name), renamed); err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.instances, current.Name)
	s.mu.Unlock()
	s.putInstance(renamed)
	return nil
}

// DeleteInstance removes an instance and its directory, including its
// overlay disk.
func (s *Store) DeleteInstance(nameOrID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	v, err := s.Instance(nameOrID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(s.InstanceDir(v.Name)); err != nil {
		return fmt.Errorf("remove %s: %w", s.InstanceDir(v.Name), err)
	}

	s.mu.Lock()
	delete(s.instances, v.Name)
	delete(s.instanceNamesByID, v.ID)
	s.mu.Unlock()
	return nil
}

// Instances returns every instance, sorted by name.
func (s *Store) Instances() []instance.Spec {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instances := make([]instance.Spec, 0, len(s.instances))
	for _, name := range slices.Sorted(maps.Keys(s.instances)) {
		instances = append(instances, s.instances[name])
	}
	return instances
}

// MatchingInstances returns the instances match reports true for, in no
// particular order: it copies only those, and sorts none. match must not
// call back into the Store.
func (s *Store) MatchingInstances(match func(instance.Spec) bool) []instance.Spec {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matching []instance.Spec
	for _, v := range s.instances {
		if match(v) {
			matching = append(matching, v)
		}
	}
	return matching
}

// putInstance keeps v in memory, under its name and its ID.
func (s *Store) putInstance(v instance.Spec) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.instances[v.Name] = v
	if v.ID != "" {
		s.instanceNamesByID[v.ID] = v.Name
	}
}

// checkReferences returns an invalid argument error unless the kernel,
// network and volumes spec names exist. An instance's definition names them,
// and so does the one a snapshot keeps of its instance.
func (s *Store) checkReferences(spec instance.Spec) error {
	if _, err := s.Kernel(spec.KernelName); err != nil {
		return errdefs.InvalidArgument("%v", err)
	}
	if _, err := s.Network(spec.NetworkName); err != nil {
		return errdefs.InvalidArgument("%v", err)
	}
	for _, m := range spec.Mounts {
		if m.Type != instance.MountTypeVolume {
			continue
		}
		if _, err := s.Volume(m.Source); err != nil {
			return errdefs.InvalidArgument("%v", err)
		}
	}
	return nil
}
