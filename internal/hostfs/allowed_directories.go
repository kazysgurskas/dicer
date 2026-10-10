// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package hostfs

import (
	"errors"
	"fmt"
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

// Resolve returns the host path of the directory at path, which must be one
// of the allowed directories or under one. Symbolic links under the allowed
// directory are followed only as far as it, as though it were the root
// directory: a link to /etc resolves to etc in it. The error, an invalid
// argument, names path as given, and says when path is not allowed, missing
// or not a directory.
func (a AllowedDirectories) Resolve(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errdefs.InvalidArgument("directory %q is not an absolute path", path)
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
			return "", errdefs.InvalidArgument("directory %q: %v", path, err)
		}
		hostPath := filepath.Join(root, strings.TrimPrefix(resolved, hostRoot))

		if err := CheckDir(hostPath); err != nil {
			return "", errdefs.InvalidArgument("directory %q: %v", path, err)
		}
		return hostPath, nil
	}

	return "", errdefs.InvalidArgument(
		"directory %q is not one this daemon allows instances to mount: its host's administrator lists them in mounts.allowed_directories",
		path)
}

// under returns path relative to root, if path is root or under it.
func under(root, path string) (string, bool) {
	if path == root {
		return ".", true
	}
	rel, ok := strings.CutPrefix(path, root+"/")
	return rel, ok
}
