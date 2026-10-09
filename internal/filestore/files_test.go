// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
)

func TestMalformedDefinitionIsSkippedNotFatal(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DataDir: filepath.Join(dir, "data")}

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	addDefaults(t, s)
	if err := s.CreateInstance(testInstance("good")); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// A hand-edit gone wrong must not stop the daemon from starting.
	bad := filepath.Join(cfg.DataDir, "instances", "bad")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bad, configFile), []byte("{{{not yaml"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	reopened, err := New(cfg)
	if err != nil {
		t.Fatalf("New with a malformed definition present: %v", err)
	}

	if _, err := reopened.Instance("good"); err != nil {
		t.Errorf("good instance should still load: %v", err)
	}
	if _, err := reopened.Instance("bad"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("malformed instance error = %v, want ErrNotFound", err)
	}
}

// A definition copied to another place by hand still names its old one. It
// is skipped rather than loaded under a name that its own contents contradict.
func TestMisplacedDefinitionIsSkipped(t *testing.T) {
	cfg := Config{DataDir: filepath.Join(t.TempDir(), "data")}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	addDefaults(t, s)
	if err := s.CreateInstance(testInstance("web")); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	copied := filepath.Join(cfg.DataDir, "instances", "copy")
	if err := os.MkdirAll(copied, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.InstanceDir("web"), configFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copied, configFile), data, 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := reopened.Instance("copy"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("Instance(copy) = %v, want the misplaced definition skipped", err)
	}
	if got, err := reopened.Instance("web"); err != nil || got.Name != "web" {
		t.Errorf("Instance(web) = %+v, %v; want the original", got, err)
	}
}

// TestStagingLeftByACrashIsRemoved checks that a snapshot a crash left
// half-written takes no space, and no name, once the store loads again.
func TestStagingLeftByACrashIsRemoved(t *testing.T) {
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

	if _, err := New(cfg); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := os.Stat(staged); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the staging directory survived a reload")
	}
}
