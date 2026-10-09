// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package kernel

import (
	"os"
	"strings"
	"testing"
)

// defaultKernelBinary returns the path of the default kernel's binary.
func defaultKernelBinary(t *testing.T, m *Manager, store *fakeStore) string {
	t.Helper()

	k, err := store.Kernel(DefaultName)
	if err != nil {
		t.Fatalf("the default kernel is not defined: %v", err)
	}
	return m.binaryPath(k.ID)
}

// TestDefaultKernelIsExtractedBeforeItIsDefined checks that the default
// kernel is on the host once it is defined, and is the one this binary
// carries.
func TestDefaultKernelIsExtractedBeforeItIsDefined(t *testing.T) {
	m, store, _ := newTestManager(t)

	if err := m.EnsureDefault(); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	k, err := store.Kernel(DefaultName)
	if err != nil || k.SHA256 != Default().SHA256 {
		t.Fatalf("default kernel = %+v, %v; want the one this binary carries", k, err)
	}
	if _, err := m.Path(k); err != nil {
		t.Errorf("the default kernel is not on the host: %v", err)
	}
}

// TestDefaultKernelIsExtractedAgainIfItsCopyIsGoneOrDamaged checks that a
// default kernel whose copy has gone from the host, or been changed, is
// extracted again at start, under the same ID.
func TestDefaultKernelIsExtractedAgainIfItsCopyIsGoneOrDamaged(t *testing.T) {
	for _, tt := range []struct {
		name   string
		damage func(path string) error
	}{
		{"gone", os.Remove},
		{"damaged", func(path string) error { return os.WriteFile(path, []byte("damaged"), 0o755) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, store, _ := newTestManager(t)
			if err := m.EnsureDefault(); err != nil {
				t.Fatal(err)
			}
			path := defaultKernelBinary(t, m, store)
			if err := tt.damage(path); err != nil {
				t.Fatal(err)
			}

			if err := m.EnsureDefault(); err != nil {
				t.Fatal(err)
			}
			k, _ := store.Kernel(DefaultName)
			if again := defaultKernelBinary(t, m, store); again != path {
				t.Errorf("the default kernel moved from %s to %s", path, again)
			}
			if _, err := m.Path(k); err != nil {
				t.Errorf("the default kernel is not on the host again: %v", err)
			}
		})
	}
}

// TestDefaultKernelAnOlderVersionCarriedIsReplaced checks that the default
// kernel an older version of Dicer carried, with another SHA-256, is
// replaced in place by the one this binary carries.
func TestDefaultKernelAnOlderVersionCarriedIsReplaced(t *testing.T) {
	m, store, _ := newTestManager(t)
	if err := m.EnsureDefault(); err != nil {
		t.Fatal(err)
	}
	k, _ := store.Kernel(DefaultName)
	path := defaultKernelBinary(t, m, store)

	// What an older version would have left.
	k.SHA256 = strings.Repeat("ab", 32)
	if err := store.UpdateKernel(k); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("an older kernel"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := m.EnsureDefault(); err != nil {
		t.Fatalf("EnsureDefault after an upgrade: %v", err)
	}
	updated, _ := store.Kernel(DefaultName)
	if updated.ID != k.ID || updated.SHA256 != Default().SHA256 {
		t.Errorf("default kernel = %+v, want the one this binary carries under the same ID", updated)
	}
	if _, err := m.Path(updated); err != nil {
		t.Errorf("the new default kernel is not on the host: %v", err)
	}
}
