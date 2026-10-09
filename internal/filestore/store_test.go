// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/kernel"
	"github.com/konradasb/dicer/internal/network"
	"github.com/konradasb/dicer/internal/volume"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()

	dir := t.TempDir()
	s, err := New(Config{DataDir: filepath.Join(dir, "data")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	addDefaults(t, s)

	return s
}

// addDefaults defines the kernel and network testInstance names.
func addDefaults(t testing.TB, s *Store) {
	t.Helper()

	if err := s.CreateKernel(kernel.Kernel{ID: "kernel-default", Name: "default"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateNetwork(testNetwork("default")); err != nil {
		t.Fatal(err)
	}
}

func testNetwork(name string) network.Network {
	return network.Network{
		ID:      "net-" + name,
		Name:    name,
		Subnet:  "10.0.0.0/24",
		Gateway: "10.0.0.1",
		Bridge:  "br-" + name,
	}
}

func testInstance(name string) instance.Spec {
	return instance.Spec{
		ID:          "id-" + name,
		Name:        name,
		ImageRef:    "docker.io/library/alpine:latest",
		KernelName:  "default",
		NetworkName: "default",
		VCPUs:       1,
	}
}

// withVolume returns spec mounting the volume named name.
func withVolume(spec instance.Spec, name string) instance.Spec {
	spec.Mounts = []instance.Mount{{Type: instance.MountTypeVolume, Source: name, Target: "/data"}}
	return spec
}

// createVolume defines a volume named name.
func createVolume(t *testing.T, s *Store, name string) {
	t.Helper()

	if err := s.CreateVolume(volume.Volume{ID: "volume-" + name, Name: name, SizeBytes: 1 << 20}); err != nil {
		t.Fatal(err)
	}
}

// checkInUseCannotBeDeleted checks that deleteUsed, which deletes a
// definition withVolume(testInstance(...), "data") names, fails while an
// instance or a snapshot names it, and succeeds once nothing does.
func checkInUseCannotBeDeleted(t *testing.T, deleteUsed func(*Store) error) {
	t.Helper()

	t.Run("used by an instance", func(t *testing.T) {
		s := newTestStore(t)
		createVolume(t, s, "data")
		if err := s.CreateInstance(withVolume(testInstance("web"), "data")); err != nil {
			t.Fatal(err)
		}

		err := deleteUsed(s)
		if !errors.Is(err, errdefs.ErrInvalidState) || !strings.Contains(err.Error(), `instance "web"`) {
			t.Errorf("delete = %v, want an invalid state error naming the instance", err)
		}
	})

	t.Run("used by a snapshot", func(t *testing.T) {
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

		err = deleteUsed(s)
		if !errors.Is(err, errdefs.ErrInvalidState) || !strings.Contains(err.Error(), `snapshot "before"`) {
			t.Errorf("delete = %v, want an invalid state error naming the snapshot", err)
		}
	})

	t.Run("used by nothing", func(t *testing.T) {
		s := newTestStore(t)
		createVolume(t, s, "data")

		if err := deleteUsed(s); err != nil {
			t.Errorf("delete = %v, want nil", err)
		}
	})
}

func TestDefinitionsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DataDir: filepath.Join(dir, "data")}

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	addDefaults(t, s)
	if err := s.CreateInstance(testInstance("web")); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	reopened, err := New(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	got, err := reopened.Instance("web")
	if err != nil {
		t.Fatalf("Instance after reopen: %v", err)
	}
	if got.ImageRef != "docker.io/library/alpine:latest" {
		t.Errorf("ImageRef = %q, want the value written before reopen", got.ImageRef)
	}
	if _, err := reopened.Network("default"); err != nil {
		t.Errorf("Network after reopen: %v", err)
	}
}

// Writes are serialised, so of many creates of one name at once exactly one
// succeeds, and the others find it taken.
func TestConcurrentCreatesOfOneNameKeepOne(t *testing.T) {
	s := newTestStore(t)

	const attempts = 16
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Go(func() {
			v := testInstance("web")
			v.ID = "id-" + strconv.Itoa(i)
			errs <- s.CreateInstance(v)
		})
	}
	wg.Wait()
	close(errs)

	created := 0
	for err := range errs {
		switch {
		case err == nil:
			created++
		case !errors.Is(err, errdefs.ErrExists):
			t.Errorf("CreateInstance = %v, want nil or an exists error", err)
		}
	}
	if created != 1 {
		t.Errorf("%d creates succeeded, want 1", created)
	}
}
