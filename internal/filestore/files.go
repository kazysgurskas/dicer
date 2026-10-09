// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/dicer/internal/atomicfile"
)

// configFile is the file holding a definition kept in a directory of its
// own, beside files that belong to it: an instance's overlay disk, a
// snapshot's memory.
const configFile = "config.yaml"

// stagingPrefix starts the name of a directory a snapshot is written in
// before it is moved into place. Names cannot start with a dot, so it is
// never taken for a definition.
const stagingPrefix = ".staging-"

// definitionFiles returns the names of the definitions kept in dir as
// <name>.yaml files, creating dir if needed.
func definitionFiles(dir string) ([]string, error) {
	entries, err := readDefinitionsDir(dir)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".yaml" {
			names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
		}
	}
	return names, nil
}

// definitionDirs returns the names of the definitions kept in dir in a
// directory of their own, creating dir if needed. It removes the staging
// directories a crash left half-written.
func (s *Store) definitionDirs(dir string) ([]string, error) {
	entries, err := readDefinitionsDir(dir)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, e := range entries {
		switch {
		case !e.IsDir():
		case strings.HasPrefix(e.Name(), stagingPrefix):
			path := filepath.Join(dir, e.Name())
			if err := os.RemoveAll(path); err != nil {
				s.logger.Warn("cannot remove staging directory", "path", path, "error", err)
			}
		default:
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// readDefinitionsDir creates dir if needed and returns what it holds.
func readDefinitionsDir(dir string) ([]os.DirEntry, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	return entries, nil
}

// readDefinition reads the definition in the file at path into v, and
// reports whether it could. One it cannot read is logged and skipped.
func (s *Store) readDefinition(path string, v any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		s.logger.Warn("skipping unreadable definition", "path", path, "error", err)
		return false
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		s.logger.Warn("skipping malformed definition", "path", path, "error", err)
		return false
	}
	return true
}

// isWhereNamed reports whether a definition read from path is where its name
// says, which is name. The name keys every lookup and write, so a file that
// disagrees with its location would be read under one name and written
// under another: it is logged and skipped.
func (s *Store) isWhereNamed(path, name, nameInFile string) bool {
	if nameInFile != name {
		s.logger.Warn("skipping definition whose name does not match its location",
			"path", path, "name_in_file", nameInFile)
		return false
	}
	return true
}

// writeDefinition writes v to the file at path as YAML, atomically.
func writeDefinition(path string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return atomicfile.Write(path, data, 0o600)
}

// removeDefinition removes the file at path. One already gone is not an
// error.
func removeDefinition(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
