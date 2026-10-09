// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import (
	"errors"
	"strings"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/volume"
)

// withVolume returns spec mounting the volume named name.
func withVolume(spec instance.Spec, name string) instance.Spec {
	spec.Mounts = []instance.Mount{{Type: instance.MountTypeVolume, Source: name, Target: "/data"}}
	return spec
}

// An instance cannot name a kernel, network or volume that does not exist,
// whether it is created, updated or renamed.
func TestDefinitionsNameOnlyWhatExists(t *testing.T) {
	missingKernel := testInstance("web")
	missingKernel.KernelName = "gone"
	missingNetwork := testInstance("web")
	missingNetwork.NetworkName = "gone"
	missingVolume := withVolume(testInstance("web"), "gone")

	for name, spec := range map[string]instance.Spec{
		"kernel":  missingKernel,
		"network": missingNetwork,
		"volume":  missingVolume,
	} {
		t.Run(name, func(t *testing.T) {
			s := newTestStore(t)
			if err := s.CreateInstance(spec); !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("CreateInstance = %v, want an invalid argument error", err)
			}

			if err := s.CreateInstance(testInstance("web")); err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateInstance(spec); !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("UpdateInstance = %v, want an invalid argument error", err)
			}
			if err := s.RenameInstance("web", spec); !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("RenameInstance = %v, want an invalid argument error", err)
			}
		})
	}
}

// A kernel, network or volume an instance or a snapshot uses cannot be
// deleted. One nothing uses can.
func TestWhatIsInUseCannotBeDeleted(t *testing.T) {
	tests := []struct {
		name   string
		delete func(*Store) error
	}{
		{"kernel", func(s *Store) error { return s.DeleteKernel("default") }},
		{"network", func(s *Store) error { return s.DeleteNetwork("default") }},
		{"volume", func(s *Store) error { return s.DeleteVolume("data") }},
	}

	for _, tt := range tests {
		t.Run(tt.name+" used by an instance", func(t *testing.T) {
			s := newTestStore(t)
			createVolume(t, s, "data")
			if err := s.CreateInstance(withVolume(testInstance("web"), "data")); err != nil {
				t.Fatal(err)
			}

			err := tt.delete(s)
			if !errors.Is(err, errdefs.ErrInvalidState) || !strings.Contains(err.Error(), `instance "web"`) {
				t.Errorf("delete = %v, want an invalid state error naming the instance", err)
			}
		})

		t.Run(tt.name+" used by a snapshot", func(t *testing.T) {
			s := newTestStore(t)
			createVolume(t, s, "data")
			staged, err := s.StageSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			snapshot := instance.Snapshot{ID: "snap-1", Name: "before", Instance: withVolume(testInstance("web"), "data")}
			if err := s.CreateSnapshot(snapshot, staged); err != nil {
				t.Fatal(err)
			}

			err = tt.delete(s)
			if !errors.Is(err, errdefs.ErrInvalidState) || !strings.Contains(err.Error(), `snapshot "before"`) {
				t.Errorf("delete = %v, want an invalid state error naming the snapshot", err)
			}
		})

		t.Run(tt.name+" used by nothing", func(t *testing.T) {
			s := newTestStore(t)
			createVolume(t, s, "data")

			if err := tt.delete(s); err != nil {
				t.Errorf("delete = %v, want nil", err)
			}
		})
	}
}

// createVolume defines a volume named name.
func createVolume(t *testing.T, s *Store, name string) {
	t.Helper()

	if err := s.CreateVolume(volume.Volume{ID: "volume-" + name, Name: name, SizeBytes: 1 << 20}); err != nil {
		t.Fatal(err)
	}
}
