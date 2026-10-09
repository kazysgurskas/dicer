// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package volume

import (
	"sort"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
)

// fakeStore is a Store of the volumes it holds, by name. It refuses to
// delete those in inUse, as a store does those an instance mounts, and to
// create any while failCreate is set.
type fakeStore struct {
	volumes    map[string]Volume
	inUse      map[string]bool
	failCreate error
}

func newFakeStore() *fakeStore {
	return &fakeStore{volumes: map[string]Volume{}, inUse: map[string]bool{}}
}

func (s *fakeStore) CreateVolume(v Volume) error {
	if s.failCreate != nil {
		return s.failCreate
	}
	if _, ok := s.volumes[v.Name]; ok {
		return errdefs.Exists("volume %q already exists", v.Name)
	}
	s.volumes[v.Name] = v
	return nil
}

func (s *fakeStore) Volume(nameOrID string) (Volume, error) {
	for _, v := range s.volumes {
		if v.Name == nameOrID || v.ID == nameOrID {
			return v, nil
		}
	}
	return Volume{}, errdefs.NotFound("no volume %q", nameOrID)
}

func (s *fakeStore) Volumes() []Volume {
	volumes := make([]Volume, 0, len(s.volumes))
	for _, v := range s.volumes {
		volumes = append(volumes, v)
	}
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].Name < volumes[j].Name })
	return volumes
}

func (s *fakeStore) DeleteVolume(nameOrID string) error {
	v, err := s.Volume(nameOrID)
	if err != nil {
		return err
	}
	if s.inUse[v.Name] {
		return errdefs.InvalidState("volume %q is in use by instance %q", v.Name, "db")
	}
	delete(s.volumes, v.Name)
	return nil
}

// fakeRecorder keeps the events it is given.
type fakeRecorder struct{ events []event.Event }

func (r *fakeRecorder) Record(e event.Event) { r.events = append(r.events, e) }
