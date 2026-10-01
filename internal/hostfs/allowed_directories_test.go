// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package hostfs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
)

func TestAllowedDirectoriesValidate(t *testing.T) {
	tests := []struct {
		name    string
		dirs    AllowedDirectories
		wantErr bool
	}{
		{"none", nil, false},
		{"absolute", AllowedDirectories{"/srv/shared", "/home/dev/projects"}, false},
		{"relative", AllowedDirectories{"srv/shared"}, true},
		{"not clean", AllowedDirectories{"/srv/shared/"}, true},
		{"dot dot", AllowedDirectories{"/srv/../etc"}, true},
		{"root", AllowedDirectories{"/"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.dirs.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Validate() = %v, want an error: %v", err, tt.wantErr)
			}
		})
	}
}

// allowedTree makes an allowed directory with a subdirectory, a file, and
// symbolic links inside and outside it, beside a directory that is not
// allowed.
func allowedTree(t *testing.T) (allowed, outside string) {
	t.Helper()
	base := t.TempDir()
	// The temporary directory may itself be behind a link, as on macOS.
	base, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	allowed = filepath.Join(base, "allowed")
	outside = filepath.Join(base, "outside")

	for _, dir := range []string{filepath.Join(allowed, "app"), filepath.Join(allowed, "etc"), outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(allowed, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"to-app":     "app",
		"to-outside": outside,
		"to-etc":     "/etc",
		"up":         "../outside",
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(allowed, name)); err != nil {
			t.Fatal(err)
		}
	}
	return allowed, outside
}

func TestAllowedDirectoriesResolve(t *testing.T) {
	allowed, outside := allowedTree(t)
	dirs := AllowedDirectories{allowed}

	tests := []struct {
		name string
		path string
		want string
	}{
		{"the allowed directory", allowed, allowed},
		{"under it", filepath.Join(allowed, "app"), filepath.Join(allowed, "app")},
		{"unclean under it", allowed + "/./app/", filepath.Join(allowed, "app")},
		{"a link inside", filepath.Join(allowed, "to-app"), filepath.Join(allowed, "app")},
		// A link out of the allowed directory resolves inside it, as though
		// it were the root.
		{"an absolute link out", filepath.Join(allowed, "to-etc"), filepath.Join(allowed, "etc")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := dirs.Resolve(tt.path)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tt.path, err)
			}
			if got != tt.want {
				t.Errorf("Resolve(%q) = %q, want %q", tt.path, got, tt.want)
			}
			if !strings.HasPrefix(got+"/", allowed+"/") {
				t.Errorf("Resolve(%q) = %q, outside %s", tt.path, got, allowed)
			}
		})
	}

	refused := map[string]string{
		"outside":                 outside,
		"a sibling with a prefix": allowed + "-other",
		"dot dot out":             filepath.Join(allowed, "..", "outside"),
		"a link to outside":       filepath.Join(allowed, "to-outside"),
		"a relative link up":      filepath.Join(allowed, "up"),
		"missing":                 filepath.Join(allowed, "missing"),
		"a file":                  filepath.Join(allowed, "file"),
		"relative":                "allowed/app",
	}
	for name, path := range refused {
		t.Run("refuses "+name, func(t *testing.T) {
			got, err := dirs.Resolve(path)
			if err == nil {
				t.Fatalf("Resolve(%q) = %q, want it refused", path, got)
			}
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Resolve(%q) = %v, want an invalid argument", path, err)
			}
		})
	}
}

func TestNoAllowedDirectoriesAllowsNone(t *testing.T) {
	var dirs AllowedDirectories
	if _, err := dirs.Resolve(t.TempDir()); err == nil || !strings.Contains(err.Error(), "mounts.allowed_directories") {
		t.Errorf("Resolve with none allowed = %v, want a refusal naming the setting", err)
	}
}
