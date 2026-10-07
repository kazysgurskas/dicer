// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package diskfile

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// skipUnlessInstalled skips a test on a host without every one of programs.
func skipUnlessInstalled(t *testing.T, programs ...string) {
	t.Helper()

	for _, program := range programs {
		if _, err := exec.LookPath(program); err != nil {
			t.Skipf("%s is not installed", program)
		}
	}
}

// TestCreateExt4MakesASparseDisk checks the disk file is the size asked for,
// takes up far less than that, and is left alone in its directory.
func TestCreateExt4MakesASparseDisk(t *testing.T) {
	skipUnlessInstalled(t, "mke2fs")

	const size = 64 << 20
	path := filepath.Join(t.TempDir(), "disks", "overlay.raw")
	if err := CreateExt4(t.Context(), path, size); err != nil {
		t.Fatalf("CreateExt4: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != size {
		t.Errorf("size = %d, want %d", info.Size(), size)
	}
	if got := AllocatedBytes(path); got <= 0 || got >= size {
		t.Errorf("AllocatedBytes = %d, want more than 0 and less than the size, %d", got, size)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the directory holds %d files, want only the disk", len(entries))
	}
}

// TestCreateExt4FromCopiesTheDirectory checks the filesystem holds the files
// it was made from.
func TestCreateExt4FromCopiesTheDirectory(t *testing.T) {
	skipUnlessInstalled(t, "mke2fs", "debugfs")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.raw")
	if err := CreateExt4From(t.Context(), path, 4<<20, dir); err != nil {
		t.Fatalf("CreateExt4From: %v", err)
	}

	out, err := exec.CommandContext(t.Context(), "debugfs", "-R", "cat /config.json", path).Output()
	if err != nil {
		t.Fatalf("read the disk: %v", err)
	}
	if !strings.Contains(string(out), `{"a":1}`) {
		t.Errorf("config.json on the disk = %q, want what was written", out)
	}
}

// TestFailedCreateLeavesNothing checks a disk that cannot be formatted leaves
// no file behind, under its name or another.
func TestFailedCreateLeavesNothing(t *testing.T) {
	skipUnlessInstalled(t, "mke2fs")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.raw")
	if err := CreateExt4From(t.Context(), path, 4<<20, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("CreateExt4From of a directory that is not there succeeded")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a failed create left %v", entries)
	}
}

// TestGrowExt4GrowsTheFilesystem checks both the file and the filesystem
// in it are the new size, and the files in it are kept.
func TestGrowExt4GrowsTheFilesystem(t *testing.T) {
	skipUnlessInstalled(t, "mke2fs", "e2fsck", "resize2fs", "dumpe2fs", "debugfs")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data"), []byte("guest data"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "overlay.raw")
	if err := CreateExt4From(t.Context(), path, 32<<20, dir); err != nil {
		t.Fatalf("CreateExt4From: %v", err)
	}

	const size = 64 << 20
	if err := GrowExt4(t.Context(), path, size); err != nil {
		t.Fatalf("GrowExt4: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != size {
		t.Errorf("disk file is %d bytes, want %d", info.Size(), size)
	}
	if got := filesystemBytes(t, path); got != size {
		t.Errorf("filesystem is %d bytes, want %d", got, size)
	}
	out, err := exec.CommandContext(t.Context(), "debugfs", "-R", "cat /data", path).Output()
	if err != nil {
		t.Fatalf("debugfs: %v", err)
	}
	if string(out) != "guest data" {
		t.Errorf("data = %q after growing, want %q", out, "guest data")
	}
}

// filesystemBytes returns the size of the ext4 filesystem at path, by its
// superblock.
func filesystemBytes(t *testing.T, path string) int64 {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "dumpe2fs", "-h", path).Output()
	if err != nil {
		t.Fatalf("dumpe2fs: %v", err)
	}
	var blocks, blockSize int64
	for line := range strings.Lines(string(out)) {
		name, value, _ := strings.Cut(line, ":")
		switch name {
		case "Block count":
			blocks, _ = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		case "Block size":
			blockSize, _ = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		}
	}
	return blocks * blockSize
}

// TestGrowExt4LeavesALargerDiskAlone checks a disk at least the size asked
// for is not touched, so it needs no filesystem in it.
func TestGrowExt4LeavesALargerDiskAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overlay.raw")
	if err := os.WriteFile(path, make([]byte, 4<<20), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := GrowExt4(t.Context(), path, 1<<20); err != nil {
		t.Fatalf("GrowExt4: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 4<<20 {
		t.Errorf("disk file is %d bytes, want it left at %d", info.Size(), 4<<20)
	}
}

// TestFailedGrowExt4KeepsTheSize checks a disk whose filesystem cannot be
// grown, here because it has none, keeps its size.
func TestFailedGrowExt4KeepsTheSize(t *testing.T) {
	skipUnlessInstalled(t, "e2fsck", "resize2fs")

	path := filepath.Join(t.TempDir(), "overlay.raw")
	if err := os.WriteFile(path, make([]byte, 4<<20), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := GrowExt4(t.Context(), path, 8<<20); err == nil {
		t.Fatal("GrowExt4 of a disk with no filesystem succeeded")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 4<<20 {
		t.Errorf("disk file is %d bytes, want it kept at %d", info.Size(), 4<<20)
	}
}

func TestAllocatedBytesOfAMissingFileIsZero(t *testing.T) {
	if got := AllocatedBytes(filepath.Join(t.TempDir(), "missing")); got != 0 {
		t.Errorf("AllocatedBytes = %d, want 0", got)
	}
}

// TestAllocatedBytesUnderSumsTheTree checks every file in the tree is counted,
// those in subdirectories too.
func TestAllocatedBytesUnderSumsTheTree(t *testing.T) {
	dir := t.TempDir()
	files := []string{filepath.Join(dir, "vmstate"), filepath.Join(dir, "disks", "overlay.raw")}
	var want int64
	for _, path := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, bytes.Repeat([]byte{1}, 64<<10), 0o600); err != nil {
			t.Fatal(err)
		}
		want += AllocatedBytes(path)
	}

	got, err := AllocatedBytesUnder(dir)
	if err != nil {
		t.Fatalf("AllocatedBytesUnder: %v", err)
	}
	if want == 0 || got != want {
		t.Errorf("AllocatedBytesUnder = %d, want the files' %d", got, want)
	}
}

func TestAllocatedBytesUnderAMissingDirectoryFails(t *testing.T) {
	if _, err := AllocatedBytesUnder(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("AllocatedBytesUnder of a missing directory succeeded")
	}
}
