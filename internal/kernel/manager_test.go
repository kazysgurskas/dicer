// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package kernel

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
)

// newTestManager returns a Manager over a fake store, recording into a fake
// recorder.
func newTestManager(t *testing.T) (*Manager, *fakeStore, *fakeRecorder) {
	t.Helper()

	store, events := newFakeStore(), &fakeRecorder{}
	m, err := NewManager(Config{
		DataDir: t.TempDir(),
		Store:   store,
		Events:  events,
		Logger:  slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m, store, events
}

func sha256Of(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// importKernel imports contents as the kernel named name, and returns it.
func importKernel(t *testing.T, m *Manager, name, contents string) Kernel {
	t.Helper()

	k, err := m.Import(name, ArchitectureX86_64, "", strings.NewReader(contents))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	return k
}

// writeKernel writes contents as the binary of a kernel named name, as
// importing it would, but without defining it or checking its name.
func writeKernel(t *testing.T, m *Manager, id, name, contents string) Kernel {
	t.Helper()

	digest, err := m.writeBinary(id, strings.NewReader(contents), "")
	if err != nil {
		t.Fatalf("writeBinary: %v", err)
	}
	return Kernel{ID: id, Name: name, Architecture: ArchitectureX86_64, SHA256: digest}
}

// TestImportKeepsAKernelWithItsChecksum checks that an imported kernel is
// kept as the binary instances boot, with the SHA-256 of what was sent, and
// that one that fails a checksum it was given, or is empty, is not kept.
func TestImportKeepsAKernelWithItsChecksum(t *testing.T) {
	m, store, events := newTestManager(t)

	k := importKernel(t, m, "test", "vmlinux")
	if k.SHA256 != sha256Of("vmlinux") {
		t.Errorf("Import returned %s, want the SHA-256 of what was sent", k.SHA256)
	}
	path, err := m.Path(k)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "vmlinux" {
		t.Errorf("the kernel kept = %q, %v; want vmlinux", data, err)
	}
	if got, err := store.Kernel("test"); err != nil || got != k {
		t.Errorf("stored kernel = %+v, %v; want %+v", got, err, k)
	}
	wantEvent(t, events, k, event.ActionImported, "Imported kernel for x86_64: 7 B, no checksum given to verify it by")

	for _, tt := range []struct {
		name, contents, sha256 string
	}{
		{"a checksum it fails", "vmlinux", sha256Of("other")},
		{"empty", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := m.Import("bad", ArchitectureX86_64, tt.sha256, strings.NewReader(tt.contents)); !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Import = %v, want errdefs.ErrInvalidArgument", err)
			}
			if _, err := store.Kernel("bad"); err == nil {
				t.Error("a refused kernel was defined")
			}
			if entries, _ := os.ReadDir(filepath.Join(m.dataDir, "kernels")); len(entries) != 1 {
				t.Errorf("a refused kernel was kept: %d kernels on the host, want the one imported", len(entries))
			}
		})
	}
}

// TestPathRefusesAMissingOrDamagedKernel checks that a kernel whose binary
// has gone from the host, no longer matches its checksum, or has none, is
// refused, saying so and what to do.
func TestPathRefusesAMissingOrDamagedKernel(t *testing.T) {
	m, _, _ := newTestManager(t)

	tests := []struct {
		name   string
		kernel func() Kernel
		want   string
	}{
		{
			"missing",
			func() Kernel {
				k := writeKernel(t, m, "k1", "gone", "vmlinux")
				_ = os.Remove(m.binaryPath(k.ID))
				return k
			},
			`kernel "gone" is missing from the host: delete it and import it again`,
		},
		{
			"damaged",
			func() Kernel {
				k := writeKernel(t, m, "k2", "bad", "vmlinux")
				_ = os.WriteFile(m.binaryPath(k.ID), []byte("damaged"), 0o755)
				return k
			},
			`kernel "bad" on the host does not match its checksum: delete it and import it again`,
		},
		{
			"with no checksum",
			func() Kernel {
				k := writeKernel(t, m, "k3", "old", "vmlinux")
				k.SHA256 = ""
				return k
			},
			`kernel "old" has no checksum to check it by: delete it and import it again`,
		},
		{
			"the default kernel, missing",
			func() Kernel {
				k := writeKernel(t, m, "k4", DefaultName, "vmlinux")
				_ = os.Remove(m.binaryPath(k.ID))
				return k
			},
			`kernel "default" is missing from the host: restart the daemon, which puts it back`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.Path(tt.kernel())
			if !errors.Is(err, errdefs.ErrInvalidState) || err.Error() != tt.want {
				t.Errorf("Path = %v, want errdefs.ErrInvalidState: %s", err, tt.want)
			}
		})
	}
}

// TestExtractDefaultReplacesWhatIsThere checks that the default kernel is
// extracted as a kernel's binary, in place of whatever was there.
func TestExtractDefaultReplacesWhatIsThere(t *testing.T) {
	m, _, _ := newTestManager(t)
	writeKernel(t, m, "k1", DefaultName, "an older default kernel")

	if err := m.extractDefault("k1"); err != nil {
		t.Fatalf("extractDefault: %v", err)
	}

	k := Default()
	k.ID = "k1"
	if _, err := m.Path(k); err != nil {
		t.Errorf("Path of the extracted default kernel: %v", err)
	}
}

func TestDiskBytesIsSizeOfTheKernel(t *testing.T) {
	m, _, _ := newTestManager(t)

	if got := m.DiskBytes(Kernel{ID: "k1"}); got != 0 {
		t.Errorf("DiskBytes before import = %d, want 0", got)
	}
	k := importKernel(t, m, "test", "vmlinux")
	if got := m.DiskBytes(k); got != 7 {
		t.Errorf("DiskBytes after import = %d, want 7", got)
	}
}

func TestDeleteRemovesTheDefinitionAndTheBinary(t *testing.T) {
	m, store, events := newTestManager(t)
	k := importKernel(t, m, "test", "vmlinux")

	if err := m.Delete("test"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Kernel("test"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("the definition is still stored: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(m.binaryPath(k.ID))); !errors.Is(err, fs.ErrNotExist) {
		t.Error("kernel directory still present after Delete")
	}
	events.events = events.events[1:]
	wantEvent(t, events, k, event.ActionDeleted, "Deleted kernel and its copy on the host")

	if err := m.Delete("test"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("second Delete = %v, want a not found error", err)
	}
}

// The default kernel's name is the daemon's: no kernel is imported under it,
// and the default kernel is never deleted.
func TestDefaultKernelIsReserved(t *testing.T) {
	m, _, _ := newTestManager(t)

	_, err := m.Import(DefaultName, ArchitectureX86_64, "", strings.NewReader("vmlinux"))
	if !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Import of the default kernel's name = %v, want an invalid argument error", err)
	}

	if err := m.EnsureDefault(); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(DefaultName); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Delete of the default kernel = %v, want an invalid argument error", err)
	}
}

func TestImportRefusesATakenName(t *testing.T) {
	m, _, _ := newTestManager(t)
	importKernel(t, m, "test", "vmlinux")

	_, err := m.Import("test", ArchitectureX86_64, "", strings.NewReader("vmlinux"))
	if !errors.Is(err, errdefs.ErrExists) {
		t.Errorf("Import of a taken name = %v, want an exists error", err)
	}
}

// A kernel the store refuses to delete, because something boots it, keeps
// its binary.
func TestDeleteKeepsAKernelInUse(t *testing.T) {
	m, store, _ := newTestManager(t)
	k := importKernel(t, m, "test", "vmlinux")
	store.inUse["test"] = true

	if err := m.Delete("test"); !errors.Is(err, errdefs.ErrInvalidState) {
		t.Fatalf("Delete = %v, want an invalid state error", err)
	}
	if _, err := m.Path(k); err != nil {
		t.Errorf("the binary of a kernel in use was removed: %v", err)
	}
}

// wantEvent checks events holds the one event about k that action and
// message say, with its architecture among the attributes.
func wantEvent(t *testing.T, events *fakeRecorder, k Kernel, action event.Action, message string) {
	t.Helper()

	if len(events.events) != 1 {
		t.Fatalf("recorded %+v, want one event", events.events)
	}
	e := events.events[0]
	if e.Kind != event.KindKernel || e.ID != k.ID || e.Name != k.Name || e.Action != action {
		t.Errorf("event = %+v, want kernel %s %s", e, k.Name, action)
	}
	if e.Message != message {
		t.Errorf("message = %q, want %q", e.Message, message)
	}
	if e.Attributes["arch"] != k.Architecture {
		t.Errorf("attributes = %v, want arch %s", e.Attributes, k.Architecture)
	}
}
