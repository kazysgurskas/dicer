// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/kernel"
)

// An update replaces a kernel's definition on disk as well as in memory. A
// kernel that does not exist cannot be updated.
func TestUpdateKernelReplacesItsDefinition(t *testing.T) {
	cfg := Config{DataDir: filepath.Join(t.TempDir(), "data")}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	addDefaults(t, s)

	const checksum = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if err := s.UpdateKernel(kernel.Kernel{ID: "kernel-default", Name: "default", SHA256: checksum}); err != nil {
		t.Fatalf("UpdateKernel: %v", err)
	}
	reopened, err := New(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := reopened.Kernel("kernel-default")
	if err != nil || got.SHA256 != checksum {
		t.Errorf("Kernel after reopen = %+v, %v; want the updated checksum", got, err)
	}

	if err := s.UpdateKernel(kernel.Kernel{ID: "kernel-gone", Name: "gone"}); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("UpdateKernel of a missing kernel = %v, want ErrNotFound", err)
	}
}

// A kernel an instance or a snapshot boots cannot be deleted. One nothing
// boots can.
func TestKernelInUseCannotBeDeleted(t *testing.T) {
	checkInUseCannotBeDeleted(t, func(s *Store) error { return s.DeleteKernel("default") })
}
