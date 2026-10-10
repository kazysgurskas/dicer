// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package boot

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"syscall"
)

// populateVolume copies what image holds into the volume mounted at target,
// if the volume is empty, as Docker populates a new volume from the image.
// The copies keep the owners, permissions and times the image gives them, so
// a workload that does not run as root can write to the volume as it could
// to the image's directory. A volume that is not empty is left alone. If the
// copy fails, the volume is emptied again, so that the next boot tries again.
func populateVolume(image *os.Root, target string) error {
	volume, err := os.OpenRoot(target)
	if err != nil {
		return err
	}
	defer func() { _ = volume.Close() }()

	empty, err := isEmptyVolume(volume)
	if err != nil || !empty {
		return err
	}
	if err := copyEntry(image, volume, ".", map[inode]string{}); err != nil {
		return errors.Join(err, clearVolume(volume))
	}
	return nil
}

// isEmptyVolume reports whether volume holds nothing but the lost+found
// directory every ext4 filesystem has.
func isEmptyVolume(volume *os.Root) (bool, error) {
	names, err := entryNames(volume, ".")
	if err != nil {
		return false, err
	}
	return len(names) == 0 || len(names) == 1 && names[0] == "lost+found", nil
}

// clearVolume removes everything in volume but its lost+found directory.
func clearVolume(volume *os.Root) error {
	names, err := entryNames(volume, ".")
	if err != nil {
		return err
	}
	for _, name := range names {
		if name == "lost+found" {
			continue
		}
		if err := volume.RemoveAll(name); err != nil {
			return err
		}
	}
	return nil
}

// inode identifies a file, so that the hard links to it are copied as links.
type inode struct {
	dev, ino uint64
}

// copyEntry copies what src holds at rel to the same place in dst, with its
// owner, permissions and modification time. A directory is copied with
// everything in it. A file with several hard links is copied once, and
// links records where, so that its other links are linked to that copy.
// Devices, sockets and FIFOs are skipped.
func copyEntry(src, dst *os.Root, rel string, links map[inode]string) error {
	info, err := src.Lstat(rel)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: no owner", rel)
	}

	switch mode := info.Mode(); {
	case mode.IsDir():
		if rel != "." {
			if err := dst.Mkdir(rel, 0o700); err != nil {
				return err
			}
		}
		names, err := entryNames(src, rel)
		if err != nil {
			return err
		}
		for _, name := range names {
			if err := copyEntry(src, dst, path.Join(rel, name), links); err != nil {
				return err
			}
		}

	case mode.IsRegular():
		file := inode{dev: stat.Dev, ino: stat.Ino}
		if first, ok := links[file]; ok {
			return dst.Link(first, rel)
		}
		if stat.Nlink > 1 {
			links[file] = rel
		}
		if err := copyFile(src, dst, rel); err != nil {
			return err
		}

	case mode&fs.ModeSymlink != 0:
		target, err := src.Readlink(rel)
		if err != nil {
			return err
		}
		if err := dst.Symlink(target, rel); err != nil {
			return err
		}
		return dst.Lchown(rel, int(stat.Uid), int(stat.Gid))

	default:
		return nil
	}

	// Last: a chown clears the setuid and setgid bits, and writing a
	// directory's entries changes its modification time.
	if err := dst.Lchown(rel, int(stat.Uid), int(stat.Gid)); err != nil {
		return err
	}
	if err := dst.Chmod(rel, info.Mode()&(fs.ModePerm|fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky)); err != nil {
		return err
	}
	return dst.Chtimes(rel, info.ModTime(), info.ModTime())
}

// copyFile copies the contents of the regular file at rel in src to a new
// file at rel in dst.
func copyFile(src, dst *os.Root, rel string) error {
	in, err := src.Open(rel)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := dst.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// entryNames returns the names of the entries in the directory at rel in
// root.
func entryNames(root *os.Root, rel string) ([]string, error) {
	dir, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()

	return dir.Readdirnames(-1)
}
