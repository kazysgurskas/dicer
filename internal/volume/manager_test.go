// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package volume

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
)

// newTestManager returns a Manager over a fake store, recording into a fake
// recorder, whose disks are empty directories.
func newTestManager(t *testing.T) (*Manager, *fakeStore, *fakeRecorder) {
	t.Helper()

	store, events := newFakeStore(), &fakeRecorder{}
	m := NewManager(Config{DataDir: t.TempDir(), Store: store, Events: events})
	m.createDisk = func(_ context.Context, path string, _ int64) error {
		return os.MkdirAll(filepath.Dir(path), 0o750)
	}
	return m, store, events
}

func TestCreateMakesAVolume(t *testing.T) {
	m, store, events := newTestManager(t)

	volume, err := m.Create(t.Context(), "my-vol", 1024)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if volume.ID == "" || volume.Name != "my-vol" || volume.SizeBytes != 1024 || volume.Path == "" ||
		volume.CreatedAt.IsZero() {
		t.Errorf("Create() = %+v, want a volume of 1024 bytes named my-vol, with an ID, path and time", volume)
	}
	if got, err := store.Volume("my-vol"); err != nil || got != volume {
		t.Errorf("stored volume = %+v, %v, want %+v", got, err, volume)
	}
	wantEvent(t, events, volume, event.ActionCreated, "Created volume of 1 KiB, formatted ext4")
}

// A volume that could never be used is refused before any disk is made.
func TestCreateRefusesAnInvalidVolume(t *testing.T) {
	tests := []struct {
		name      string
		volume    string
		sizeBytes int64
	}{
		{"no size", "data", 0},
		{"a negative size", "data", -1},
		{"an invalid name", "data_1", 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, _ := newTestManager(t)
			m.createDisk = func(context.Context, string, int64) error {
				t.Error("a disk was made for an invalid volume")
				return nil
			}

			if _, err := m.Create(t.Context(), tt.volume, tt.sizeBytes); !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Create() error = %v, want an invalid argument error", err)
			}
		})
	}
}

func TestCreateRefusesATakenName(t *testing.T) {
	m, _, _ := newTestManager(t)
	if _, err := m.Create(t.Context(), "data", 1024); err != nil {
		t.Fatal(err)
	}
	m.createDisk = func(context.Context, string, int64) error {
		t.Error("a disk was made for a name already taken")
		return nil
	}

	if _, err := m.Create(t.Context(), "data", 1024); !errors.Is(err, errdefs.ErrExists) {
		t.Errorf("Create() error = %v, want an exists error", err)
	}
}

func TestCreateFailsWithItsDisk(t *testing.T) {
	m, _, _ := newTestManager(t)
	diskErr := errors.New("mkfs failed")
	m.createDisk = func(_ context.Context, _ string, _ int64) error { return diskErr }

	if _, err := m.Create(t.Context(), "vol", 512); !errors.Is(err, diskErr) {
		t.Errorf("error = %v, want to wrap %v", err, diskErr)
	}
}

// A volume whose definition cannot be written leaves no disk behind.
func TestCreateRemovesTheDiskOfAVolumeNotRecorded(t *testing.T) {
	m, store, _ := newTestManager(t)
	var made string
	m.createDisk = func(_ context.Context, path string, _ int64) error {
		made = filepath.Dir(path)
		return os.MkdirAll(made, 0o750)
	}
	store.failCreate = errors.New("disk full")

	if _, err := m.Create(t.Context(), "vol", 512); !errors.Is(err, store.failCreate) {
		t.Fatalf("Create() error = %v, want %v", err, store.failCreate)
	}
	if _, err := os.Stat(made); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the disk of a volume not recorded is left at %s", made)
	}
}

func TestDeleteRemovesTheDefinitionAndTheDisk(t *testing.T) {
	m, store, events := newTestManager(t)
	volume, err := m.Create(t.Context(), "to-delete", 512)
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Delete("to-delete"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := store.Volume("to-delete"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("the definition is still stored: %v", err)
	}
	if _, err := os.Stat(m.volumeDir(volume.ID)); !errors.Is(err, fs.ErrNotExist) {
		t.Error("volume directory should be removed after Delete")
	}
	events.events = events.events[1:]
	wantEvent(t, events, volume, event.ActionDeleted, "Deleted volume of 512 B and its data")
}

// wantEvent checks events holds the one event about volume that action and
// message say, with its size among the attributes.
func wantEvent(t *testing.T, events *fakeRecorder, volume Volume, action event.Action, message string) {
	t.Helper()

	if len(events.events) != 1 {
		t.Fatalf("recorded %+v, want one event", events.events)
	}
	e := events.events[0]
	if e.Kind != event.KindVolume || e.ID != volume.ID || e.Name != volume.Name || e.Action != action {
		t.Errorf("event = %+v, want volume %s %s", e, volume.Name, action)
	}
	if e.Message != message {
		t.Errorf("message = %q, want %q", e.Message, message)
	}
	if want := strconv.FormatInt(volume.SizeBytes, 10); e.Attributes["size_bytes"] != want {
		t.Errorf("attributes = %v, want size_bytes %s", e.Attributes, want)
	}
}

func TestDeleteOfAMissingVolumeIsNotFound(t *testing.T) {
	m, _, _ := newTestManager(t)

	if err := m.Delete("ghost"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("Delete() error = %v, want a not found error", err)
	}
}

// A volume the store refuses to delete, because something mounts it, keeps
// its disk.
func TestDeleteKeepsAVolumeInUse(t *testing.T) {
	m, store, _ := newTestManager(t)
	volume, err := m.Create(t.Context(), "data", 512)
	if err != nil {
		t.Fatal(err)
	}
	store.inUse["data"] = true

	if err := m.Delete("data"); !errors.Is(err, errdefs.ErrInvalidState) {
		t.Fatalf("Delete() error = %v, want an invalid state error", err)
	}
	if _, err := os.Stat(m.volumeDir(volume.ID)); err != nil {
		t.Errorf("the disk of a volume in use was removed: %v", err)
	}
}

// TestDiskBytesCountsWhatASparseDiskTakesUp covers a volume's disk taking up
// only what has been written to it, not its size.
func TestDiskBytesCountsWhatASparseDiskTakesUp(t *testing.T) {
	m, _, _ := newTestManager(t)
	const size = 64 << 20
	m.createDisk = func(_ context.Context, path string, _ int64) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		if err := f.Truncate(size); err != nil {
			return err
		}
		_, err = f.Write(make([]byte, 4096))
		return err
	}

	volume, err := m.Create(t.Context(), "data", size)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if got := m.DiskBytes(volume); got <= 0 || got >= size {
		t.Errorf("DiskBytes() = %d, want more than 0 and less than the size, %d", got, size)
	}
	if got := m.DiskBytes(Volume{ID: "missing"}); got != 0 {
		t.Errorf("DiskBytes() of a volume with no disk = %d, want 0", got)
	}
}
