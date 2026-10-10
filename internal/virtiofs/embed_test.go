// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package virtiofs

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExtract(t *testing.T) {
	dstPath := filepath.Join(t.TempDir(), Version, "virtiofsd")

	got, err := Extract(dstPath)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got != dstPath {
		t.Errorf("Extract() = %q, want %q", got, dstPath)
	}

	info, err := os.Stat(dstPath)
	if err != nil {
		t.Fatalf("stat extracted binary: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("mode = %v, want an executable", info.Mode())
	}

	// The embedded virtiofsd has every option Start passes it. It runs
	// only where it was built for.
	if runtime.GOOS != "linux" {
		return
	}
	help, err := exec.CommandContext(t.Context(), dstPath, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("virtiofsd --help: %v", err)
	}
	for _, opt := range []string{"--socket-path", "--shared-dir", "--cache", "--sandbox", "--announce-submounts", "--readonly"} {
		if !strings.Contains(string(help), opt) {
			t.Errorf("the embedded virtiofsd has no %s", opt)
		}
	}
}

// TestExtractKeepsExistingBinary covers the common case: the binary was
// extracted by an earlier run, and must not be written again.
func TestExtractKeepsExistingBinary(t *testing.T) {
	dstPath := filepath.Join(t.TempDir(), "virtiofsd")
	if err := os.WriteFile(dstPath, []byte("already here"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Extract(dstPath); err != nil {
		t.Fatalf("Extract over an existing binary: %v", err)
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "already here" {
		t.Error("an existing binary was overwritten")
	}
}
