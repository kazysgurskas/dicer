// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package hostfs

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"

	"github.com/konradasb/dicer/internal/errdefs"
)

// AllowedDirectories are the host directories that instances may mount,
// each with everything under it, as the daemon's configuration lists them.
// A request can name no other directory, so what it can reach on the host
// is what the host's administrator chose. The zero value allows none.
type AllowedDirectories []string

// Validate checks that each directory is an absolute, clean path, and not
// the root directory, which would allow the whole host.
func (a AllowedDirectories) Validate() error {
	for _, dir := range a {
		switch {
		case !filepath.IsAbs(dir):
			return fmt.Errorf("allowed directory %q is not an absolute path", dir)
		case filepath.Clean(dir) != dir:
			return fmt.Errorf("allowed directory %q is not a clean path: write it as %q", dir, filepath.Clean(dir))
		case dir == "/":
			return errors.New("the root directory cannot be allowed: it would let a request share the whole host")
		}
	}
	return nil
}

// Directory is a host directory found for a mount.
type Directory struct {
	// Path is where the host has it, with no symbolic links.
	Path string

	// Info is the directory Path named when it was found. Path may name
	// another directory by the time it is used, if a link is put in its
	// way. Whatever opens Path checks that it opened this one.
	Info fs.FileInfo
}

// Resolve finds the directory at path, which must be one of the allowed
// directories or under one. Symbolic links under the allowed directory are
// followed only as far as it, as though it were the root directory: a link
// to /etc resolves to etc in it. The error, an invalid argument, names path
// as given, and says when path is not allowed, missing or not a directory.
func (a AllowedDirectories) Resolve(path string) (Directory, error) {
	if !filepath.IsAbs(path) {
		return Directory{}, errdefs.InvalidArgument("directory %q is not an absolute path", path)
	}
	path = filepath.Clean(path)

	for _, root := range a {
		rel, ok := under(root, path)
		if !ok {
			continue
		}

		hostRoot := Path(root)
		resolved, err := securejoin.SecureJoin(hostRoot, rel)
		if err != nil {
			return Directory{}, errdefs.InvalidArgument("directory %q: %v", path, err)
		}
		resolvedRel := strings.TrimPrefix(resolved, hostRoot)

		info, err := statDirIn(hostRoot, resolvedRel)
		if err != nil {
			return Directory{}, errdefs.InvalidArgument("directory %q: %v", path, named(err, path))
		}
		return Directory{Path: filepath.Join(root, resolvedRel), Info: info}, nil
	}

	return Directory{}, errdefs.InvalidArgument(
		"directory %q is not one this daemon allows instances to mount: its host's administrator lists them in mounts.allowed_directories",
		path)
}

// statDirIn returns the directory at rel under root, without following a
// link out of root: a path that had no links a moment ago may have one now.
func statDirIn(root, rel string) (fs.FileInfo, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()

	info, err := r.Stat(cmp.Or(strings.TrimPrefix(rel, "/"), "."))
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, ErrNotDirectory
	}
	return info, nil
}

// under returns path relative to root, if path is root or under it.
func under(root, path string) (string, bool) {
	if path == root {
		return ".", true
	}
	rel, ok := strings.CutPrefix(path, root+"/")
	return rel, ok
}
