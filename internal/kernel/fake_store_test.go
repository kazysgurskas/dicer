// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package kernel

import (
	"sort"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
)

// fakeStore is a Store of the kernels it holds, by name. It refuses to
// delete those in inUse, as a store does those an instance boots.
type fakeStore struct {
	kernels map[string]Kernel
	inUse   map[string]bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{kernels: map[string]Kernel{}, inUse: map[string]bool{}}
}

func (s *fakeStore) CreateKernel(k Kernel) error {
	if _, ok := s.kernels[k.Name]; ok {
		return errdefs.Exists("kernel %q already exists", k.Name)
	}
	s.kernels[k.Name] = k
	return nil
}

func (s *fakeStore) Kernel(nameOrID string) (Kernel, error) {
	for _, k := range s.kernels {
		if k.Name == nameOrID || k.ID == nameOrID {
			return k, nil
		}
	}
	return Kernel{}, errdefs.NotFound("no kernel %q", nameOrID)
}

func (s *fakeStore) Kernels() []Kernel {
	kernels := make([]Kernel, 0, len(s.kernels))
	for _, k := range s.kernels {
		kernels = append(kernels, k)
	}
	sort.Slice(kernels, func(i, j int) bool { return kernels[i].Name < kernels[j].Name })
	return kernels
}

func (s *fakeStore) UpdateKernel(k Kernel) error {
	if _, ok := s.kernels[k.Name]; !ok {
		return errdefs.NotFound("no kernel %q", k.Name)
	}
	s.kernels[k.Name] = k
	return nil
}

func (s *fakeStore) DeleteKernel(nameOrID string) error {
	k, err := s.Kernel(nameOrID)
	if err != nil {
		return err
	}
	if s.inUse[k.Name] {
		return errdefs.InvalidState("kernel %q is in use by instance %q", k.Name, "web")
	}
	delete(s.kernels, k.Name)
	return nil
}

// fakeRecorder keeps the events it is given.
type fakeRecorder struct{ events []event.Event }

func (r *fakeRecorder) Record(e event.Event) { r.events = append(r.events, e) }
