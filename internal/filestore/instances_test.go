// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/instance"
)

func TestInstancesAreFoundByNameOrIDAndListedByName(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateInstance(testInstance("web")); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	for _, key := range []string{"web", "id-web"} {
		got, err := s.Instance(key)
		if err != nil {
			t.Fatalf("Instance(%q): %v", key, err)
		}
		if got.Name != "web" {
			t.Errorf("Instance(%q) = %q, want %q", key, got.Name, "web")
		}
	}

	if err := s.CreateInstance(testInstance("db")); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	var got []string
	for _, instance := range s.Instances() {
		got = append(got, instance.Name)
	}
	if want := []string{"db", "web"}; !slices.Equal(got, want) {
		t.Errorf("Instances = %q, want %q", got, want)
	}
}

func TestCreateDuplicateFailsWithExists(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateInstance(testInstance("web")); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	err := s.CreateInstance(testInstance("web"))
	if !errors.Is(err, errdefs.ErrExists) {
		t.Errorf("duplicate create error = %v, want ErrExists", err)
	}
}

func TestMissingInstanceIsNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Instance("nope")
	if !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestDeleteRemovesInstanceDirectory(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateInstance(testInstance("web")); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// A sibling file in the instance directory (an overlay disk, say) must go
	// away with the instance.
	disk := filepath.Join(s.InstanceDir("web"), "overlay.img")
	if err := os.WriteFile(disk, []byte("disk"), 0o600); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	if err := s.DeleteInstance("web"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}
	if _, err := os.Stat(s.InstanceDir("web")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("instance directory still present after delete")
	}
}

func TestMatchingInstancesAreThoseMatchAccepts(t *testing.T) {
	s := newTestStore(t)
	for _, name := range []string{"web", "db", "cache"} {
		if err := s.CreateInstance(testInstance(name)); err != nil {
			t.Fatalf("CreateInstance %s: %v", name, err)
		}
	}

	tests := []struct {
		name  string
		match func(instance.Spec) bool
		want  []string
	}{
		{"none", func(instance.Spec) bool { return false }, nil},
		{"some", func(i instance.Spec) bool { return i.Name != "db" }, []string{"cache", "web"}},
		{"all", func(instance.Spec) bool { return true }, []string{"cache", "db", "web"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, instance := range s.MatchingInstances(tt.match) {
				got = append(got, instance.Name)
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("MatchingInstances = %q, want %q", got, tt.want)
			}
		})
	}
}

// Finding one instance among many costs a scan, not a copy and sort of
// them all, as Instances does.
func BenchmarkMatchingInstances(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			s, err := New(Config{
				DataDir: filepath.Join(b.TempDir(), "data"),
				Logger:  slog.New(slog.DiscardHandler),
			})
			if err != nil {
				b.Fatal(err)
			}
			addDefaults(b, s)
			for i := range size {
				if err := s.CreateInstance(testInstance("instance-" + strconv.Itoa(i))); err != nil {
					b.Fatal(err)
				}
			}

			b.ReportAllocs()
			for b.Loop() {
				s.MatchingInstances(func(i instance.Spec) bool { return i.Name == "instance-5" })
			}
		})
	}
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
