// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package boot

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

// imageDirectory makes a directory as an image might have at a volume's
// target, and returns it opened as a root.
func imageDirectory(t *testing.T) *os.Root {
	t.Helper()
	dir := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dir, "plugins"), 0o750))
	must(t, os.WriteFile(filepath.Join(dir, "plugins", "app.conf"), []byte("listen 8080\n"), 0o640))
	must(t, os.Link(filepath.Join(dir, "plugins", "app.conf"), filepath.Join(dir, "app.conf")))
	must(t, os.Symlink("plugins/app.conf", filepath.Join(dir, "current")))
	must(t, syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o600))
	must(t, os.Chmod(dir, 0o1777))

	// Owners other than root's, where the test can give them.
	if os.Geteuid() == 0 {
		for _, name := range []string{".", "plugins", "plugins/app.conf"} {
			must(t, os.Lchown(filepath.Join(dir, name), 472, 0))
		}
		must(t, os.Lchown(filepath.Join(dir, "current"), 472, 0))
	}

	modified := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	must(t, os.Chtimes(filepath.Join(dir, "plugins", "app.conf"), modified, modified))

	root, err := os.OpenRoot(dir)
	must(t, err)
	t.Cleanup(func() { _ = root.Close() })
	return root
}

// emptyVolume makes a directory as a new ext4 volume is: holding nothing but
// lost+found.
func emptyVolume(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dir, "lost+found"), 0o700))
	return dir
}

// ownerOf returns the owner of the file info describes, as uid:gid.
func ownerOf(t *testing.T, info fs.FileInfo) string {
	t.Helper()
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("%s has no owner", info.Name())
	}
	return fmt.Sprintf("%d:%d", stat.Uid, stat.Gid)
}

// must fails the test at once if err is not nil.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestPopulateVolumeCopiesTheImagesDirectory checks that an empty volume gets
// the image's files with their owners, permissions and times, and its hard
// links as links.
func TestPopulateVolumeCopiesTheImagesDirectory(t *testing.T) {
	image := imageDirectory(t)
	volume := emptyVolume(t)

	if err := populateVolume(image, volume); err != nil {
		t.Fatalf("populateVolume: %v", err)
	}

	for _, name := range []string{".", "plugins", "plugins/app.conf", "app.conf", "current"} {
		want, err := image.Lstat(name)
		must(t, err)
		got, err := os.Lstat(filepath.Join(volume, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got.Mode() != want.Mode() {
			t.Errorf("%s: mode %v, want %v", name, got.Mode(), want.Mode())
		}
		if gotOwner, wantOwner := ownerOf(t, got), ownerOf(t, want); gotOwner != wantOwner {
			t.Errorf("%s: owner %s, want %s", name, gotOwner, wantOwner)
		}
		if got.Mode().IsRegular() && !got.ModTime().Equal(want.ModTime()) {
			t.Errorf("%s: modified %v, want %v", name, got.ModTime(), want.ModTime())
		}
	}

	if content, err := os.ReadFile(filepath.Join(volume, "current")); err != nil || string(content) != "listen 8080\n" {
		t.Errorf("current = %q, %v; want the link to the copied file", content, err)
	}
	linked, _ := os.Stat(filepath.Join(volume, "app.conf"))
	original, _ := os.Stat(filepath.Join(volume, "plugins", "app.conf"))
	if !os.SameFile(linked, original) {
		t.Error("app.conf is a copy of plugins/app.conf, want a hard link to it")
	}
	if _, err := os.Lstat(filepath.Join(volume, "fifo")); err == nil {
		t.Error("the FIFO was copied, want it skipped")
	}
}

// TestPopulateVolumeLeavesAVolumeThatIsNotEmpty checks that a volume that
// already holds anything, however little, keeps it untouched.
func TestPopulateVolumeLeavesAVolumeThatIsNotEmpty(t *testing.T) {
	image := imageDirectory(t)
	volume := emptyVolume(t)
	must(t, os.WriteFile(filepath.Join(volume, "data"), nil, 0o600))

	if err := populateVolume(image, volume); err != nil {
		t.Fatalf("populateVolume: %v", err)
	}

	entries, err := os.ReadDir(volume)
	must(t, err)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if want := []string{"data", "lost+found"}; !slices.Equal(names, want) {
		t.Errorf("volume holds %q, want %q", names, want)
	}
}

func TestClearVolumeKeepsLostAndFound(t *testing.T) {
	volume := emptyVolume(t)
	must(t, os.MkdirAll(filepath.Join(volume, "plugins", "deep"), 0o755))
	must(t, os.WriteFile(filepath.Join(volume, "app.conf"), nil, 0o600))

	root, err := os.OpenRoot(volume)
	must(t, err)
	defer func() { _ = root.Close() }()
	if err := clearVolume(root); err != nil {
		t.Fatalf("clearVolume: %v", err)
	}

	if empty, err := isEmptyVolume(root); err != nil || !empty {
		t.Errorf("isEmptyVolume = %v, %v after clearing it; want true", empty, err)
	}
}

// TestPopulateVolumeReadsTheDirectoryTheVolumeCovers checks what
// mountVolume relies on: a root opened on a directory goes on reading that
// directory once something is mounted over it.
func TestPopulateVolumeReadsTheDirectoryTheVolumeCovers(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("mounting needs root")
	}
	target := t.TempDir()
	must(t, os.WriteFile(filepath.Join(target, "app.conf"), []byte("listen 8080\n"), 0o640))

	image, err := os.OpenRoot(target)
	must(t, err)
	defer func() { _ = image.Close() }()

	must(t, syscall.Mount("tmpfs", target, "tmpfs", 0, ""))
	defer func() { _ = syscall.Unmount(target, 0) }()

	if err := populateVolume(image, target); err != nil {
		t.Fatalf("populateVolume: %v", err)
	}
	info, err := os.Stat(filepath.Join(target, "app.conf"))
	if err != nil || info.Mode() != fs.FileMode(0o640) {
		t.Errorf("app.conf in the volume = %v, %v; want the image's, mode 0640", info, err)
	}
}
