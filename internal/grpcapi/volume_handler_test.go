// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/volume"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// A volume an instance mounts cannot be deleted; once nothing mounts it, it
// can.
func TestDeleteVolumeRefusesOneInUse(t *testing.T) {
	s, store := newTestServer(t)
	if err := store.CreateVolume(volume.Volume{ID: "v-1", Name: "data", SizeBytes: 10 << 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateInstance(instance.Spec{
		ID: "i-1", Name: "db", KernelName: "default", NetworkName: "default",
		Mounts: []instance.Mount{{Type: instance.MountTypeVolume, Source: "data", Target: "/data"}},
	}); err != nil {
		t.Fatal(err)
	}

	_, err := s.DeleteVolume(t.Context(), &dicerdv1.DeleteVolumeRequest{Name: "data"})
	wantClass(t, err, errdefs.ErrInvalidState)

	if err := store.DeleteInstance("db"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteVolume(t.Context(), &dicerdv1.DeleteVolumeRequest{Name: "data"}); err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}
}
