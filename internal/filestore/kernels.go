// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import (
	"maps"
	"path/filepath"
	"slices"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/kernel"
	"github.com/konradasb/dicer/internal/naming"
)

// kernelsDir returns the directory every kernel's definition is kept in.
func (s *Store) kernelsDir() string {
	return filepath.Join(s.dataDir, "kernels")
}

// kernelPath returns the file holding the named kernel's definition.
func (s *Store) kernelPath(name string) string {
	return filepath.Join(s.kernelsDir(), name+".yaml")
}

// loadKernels reads every kernel's definition into memory.
func (s *Store) loadKernels() error {
	names, err := definitionFiles(s.kernelsDir())
	if err != nil {
		return err
	}
	for _, name := range names {
		var v kernel.Kernel
		path := s.kernelPath(name)
		if s.readDefinition(path, &v) && s.isWhereNamed(path, name, v.Name) {
			s.putKernel(v)
		}
	}
	return nil
}

// CreateKernel records a new kernel definition, refusing one whose name is
// taken.
func (s *Store) CreateKernel(v kernel.Kernel) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := naming.Validate(v.Name); err != nil {
		return err
	}
	if _, err := s.Kernel(v.Name); err == nil {
		return errdefs.Exists("kernel %q already exists", v.Name)
	}

	if err := writeDefinition(s.kernelPath(v.Name), v); err != nil {
		return err
	}
	s.putKernel(v)
	return nil
}

// Kernel returns a kernel by name or ID.
func (s *Store) Kernel(nameOrID string) (kernel.Kernel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if v, ok := s.kernels[nameOrID]; ok {
		return v, nil
	}
	if name, ok := s.kernelNamesByID[nameOrID]; ok {
		return s.kernels[name], nil
	}
	return kernel.Kernel{}, errdefs.NotFound("no kernel %q", nameOrID)
}

// UpdateKernel replaces a kernel definition.
func (s *Store) UpdateKernel(v kernel.Kernel) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if _, err := s.Kernel(v.Name); err != nil {
		return err
	}

	if err := writeDefinition(s.kernelPath(v.Name), v); err != nil {
		return err
	}
	s.putKernel(v)
	return nil
}

// DeleteKernel removes a kernel definition, refusing one an instance or a
// snapshot boots. The binary is the kernel manager's to delete.
func (s *Store) DeleteKernel(nameOrID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	v, err := s.Kernel(nameOrID)
	if err != nil {
		return err
	}
	for _, i := range s.Instances() {
		if i.KernelName == v.Name {
			return errdefs.InvalidState("kernel %q is in use by instance %q", v.Name, i.Name)
		}
	}
	for _, snapshot := range s.Snapshots() {
		if snapshot.Instance.KernelName == v.Name {
			return errdefs.InvalidState("kernel %q is in use by snapshot %q: delete the snapshot first",
				v.Name, snapshot.Name)
		}
	}

	if err := removeDefinition(s.kernelPath(v.Name)); err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.kernels, v.Name)
	delete(s.kernelNamesByID, v.ID)
	s.mu.Unlock()
	return nil
}

// Kernels returns every kernel, sorted by name.
func (s *Store) Kernels() []kernel.Kernel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	kernels := make([]kernel.Kernel, 0, len(s.kernels))
	for _, name := range slices.Sorted(maps.Keys(s.kernels)) {
		kernels = append(kernels, s.kernels[name])
	}
	return kernels
}

// putKernel keeps v in memory, under its name and its ID.
func (s *Store) putKernel(v kernel.Kernel) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.kernels[v.Name] = v
	if v.ID != "" {
		s.kernelNamesByID[v.ID] = v.Name
	}
}
