// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/instance"
)

func TestStagedSnapshotIsMovedIntoPlace(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DataDir: filepath.Join(dir, "data")}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	addDefaults(t, s)

	staged, err := s.StageSnapshot()
	if err != nil {
		t.Fatalf("StageSnapshot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staged, "overlay.img"), []byte("disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := instance.Snapshot{ID: "id-snap", Name: "snap", Kind: instance.SnapshotKindDisk, Instance: testInstance("web")}
	if err := s.CreateSnapshot(snapshot, staged); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	if _, err := os.Stat(filepath.Join(s.SnapshotDir("snap"), "overlay.img")); err != nil {
		t.Errorf("the staged file is not in the snapshot's directory: %v", err)
	}
	if _, err := os.Stat(staged); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the staging directory is still there")
	}

	reopened, err := New(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := reopened.Snapshot("id-snap")
	if err != nil {
		t.Fatalf("Snapshot after reopen: %v", err)
	}
	if got.Instance.Name != "web" {
		t.Errorf("Instance.Name = %q, want the instance it was taken from", got.Instance.Name)
	}
}

func TestCreateSnapshotOfATakenNameFails(t *testing.T) {
	s := newTestStore(t)

	for i, want := range []error{nil, errdefs.ErrExists} {
		staged, err := s.StageSnapshot()
		if err != nil {
			t.Fatalf("StageSnapshot: %v", err)
		}
		err = s.CreateSnapshot(instance.Snapshot{ID: "id-" + strconv.Itoa(i), Name: "snap", Instance: testInstance("web")}, staged)
		if !errors.Is(err, want) {
			t.Errorf("CreateSnapshot %d = %v, want %v", i, err, want)
		}
	}
}

// Deleting a snapshot removes its directory, with the files taken with it,
// and frees its name.
func TestDeleteSnapshotRemovesItsDirectory(t *testing.T) {
	s := newTestStore(t)

	staged, err := s.StageSnapshot()
	if err != nil {
		t.Fatalf("StageSnapshot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staged, "overlay.img"), []byte("disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := instance.Snapshot{ID: "id-snap", Name: "snap", Kind: instance.SnapshotKindDisk, Instance: testInstance("web")}
	if err := s.CreateSnapshot(snapshot, staged); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	if err := s.DeleteSnapshot("id-snap"); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}
	if _, err := os.Stat(s.SnapshotDir("snap")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the snapshot's directory is still there")
	}
	for _, key := range []string{"snap", "id-snap"} {
		if _, err := s.Snapshot(key); !errors.Is(err, errdefs.ErrNotFound) {
			t.Errorf("Snapshot(%q) after delete = %v, want ErrNotFound", key, err)
		}
	}
	if err := s.DeleteSnapshot("snap"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("second DeleteSnapshot = %v, want ErrNotFound", err)
	}

	staged, err = s.StageSnapshot()
	if err != nil {
		t.Fatalf("StageSnapshot: %v", err)
	}
	snapshot.ID = "id-snap-2"
	if err := s.CreateSnapshot(snapshot, staged); err != nil {
		t.Errorf("CreateSnapshot of the deleted snapshot's name = %v, want nil", err)
	}
}
