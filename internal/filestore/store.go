// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

// Package filestore stores the instance, snapshot, network, volume, kernel
// and token definitions as YAML files, cached in memory and written through:
//
//	/var/lib/dicer/instances/<name>/config.yaml
//	/var/lib/dicer/snapshots/<name>/config.yaml
//	/var/lib/dicer/networks/<name>.yaml
//	/var/lib/dicer/volumes/<name>.yaml
//	/var/lib/dicer/kernels/<name>.yaml
//	/var/lib/dicer/tokens/<name>.yaml
package filestore

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/konradasb/dicer/internal/defaults"
	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/kernel"
	"github.com/konradasb/dicer/internal/network"
	"github.com/konradasb/dicer/internal/token"
	"github.com/konradasb/dicer/internal/volume"
)

// Config configures a Store.
type Config struct {
	// DataDir holds the definitions; empty means defaults.DataDir.
	DataDir string
	// Logger defaults to slog.Default.
	Logger *slog.Logger
}

// Store holds the definitions, backed by YAML files. It implements
// instance.Store and is safe for concurrent use.
//
// Every lookup takes a name or an ID and returns an errdefs.ErrNotFound error
// if there is no such definition.
type Store struct {
	instances *collection[instance.Spec]
	snapshots *collection[instance.Snapshot]
	networks  *collection[network.Network]
	volumes   *collection[volume.Volume]
	kernels   *collection[kernel.Kernel]
	tokens    *collection[token.Token]
}

// New loads all definitions into memory, creating their directories
// if needed. Malformed files are logged and skipped.
func New(cfg Config) (*Store, error) {
	if cfg.DataDir == "" {
		cfg.DataDir = defaults.DataDir
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	logger := cfg.Logger.With("component", "filestore")

	s := &Store{
		instances: newCollection(
			"instance", filepath.Join(cfg.DataDir, "instances"), nested, logger,
			func(v instance.Spec) (string, string) { return v.ID, v.Name },
		),
		snapshots: newCollection(
			"snapshot", filepath.Join(cfg.DataDir, "snapshots"), nested, logger,
			func(v instance.Snapshot) (string, string) { return v.ID, v.Name },
		),
		networks: newCollection(
			"network", filepath.Join(cfg.DataDir, "networks"), flat, logger,
			func(v network.Network) (string, string) { return v.ID, v.Name },
		),
		volumes: newCollection(
			"volume", filepath.Join(cfg.DataDir, "volumes"), flat, logger,
			func(v volume.Volume) (string, string) { return v.ID, v.Name },
		),
		kernels: newCollection(
			"kernel", filepath.Join(cfg.DataDir, "kernels"), flat, logger,
			func(v kernel.Kernel) (string, string) { return v.ID, v.Name },
		),
		tokens: newCollection(
			"token", filepath.Join(cfg.DataDir, "tokens"), flat, logger,
			func(v token.Token) (string, string) { return v.ID, v.Name },
		),
	}
	for _, load := range []func() error{
		s.instances.load, s.snapshots.load, s.networks.load, s.volumes.load, s.kernels.load, s.tokens.load,
	} {
		if err := load(); err != nil {
			return nil, err
		}
	}

	logger.Info("definitions loaded",
		"data_dir", cfg.DataDir,
		"instances", s.instances.len(),
		"snapshots", s.snapshots.len(),
		"networks", s.networks.len(),
		"volumes", s.volumes.len(),
		"kernels", s.kernels.len(),
		"tokens", s.tokens.len(),
	)

	return s, nil
}

// CreateInstance records a new instance definition.
func (s *Store) CreateInstance(v instance.Spec) error {
	return s.instances.create(v)
}

// Instance returns an instance by name or ID.
func (s *Store) Instance(nameOrID string) (instance.Spec, error) {
	return s.instances.definition(nameOrID)
}

// UpdateInstance overwrites an existing instance definition.
func (s *Store) UpdateInstance(v instance.Spec) error {
	return s.instances.update(v)
}

// RenameInstance moves an instance to a new name, taking its directory, with
// its overlay disk, console log and snapshots, with it.
func (s *Store) RenameInstance(nameOrID string, renamed instance.Spec) error {
	return s.instances.rename(nameOrID, renamed)
}

// DeleteInstance removes an instance and its directory, including its
// overlay disk.
func (s *Store) DeleteInstance(nameOrID string) error {
	return s.instances.delete(nameOrID)
}

// Instances returns every instance, sorted by name.
func (s *Store) Instances() []instance.Spec {
	return s.instances.definitions()
}

// MatchingInstances returns the instances match reports true for, in no
// particular order. match must not call back into the Store.
func (s *Store) MatchingInstances(match func(instance.Spec) bool) []instance.Spec {
	return s.instances.matchingDefinitions(match)
}

// InstanceDir returns an instance's persistent directory, removed when the
// instance is deleted.
func (s *Store) InstanceDir(name string) string {
	return s.instances.nestedDir(name)
}

// StageSnapshot returns a new, empty directory beside the snapshots for a
// snapshot's files to be written in before CreateSnapshot moves it into
// place. One a crash leaves behind is removed when the store next loads.
func (s *Store) StageSnapshot() (string, error) {
	dir, err := os.MkdirTemp(s.snapshots.dir, stagingPrefix)
	if err != nil {
		return "", fmt.Errorf("create snapshot staging directory: %w", err)
	}
	return dir, nil
}

// CreateSnapshot records a new snapshot whose files are in staged, a
// directory from StageSnapshot, moving it into place. A snapshot is thus
// either whole or absent.
func (s *Store) CreateSnapshot(v instance.Snapshot, staged string) error {
	return s.snapshots.createFrom(v, staged)
}

// Snapshot returns a snapshot by name or ID.
func (s *Store) Snapshot(nameOrID string) (instance.Snapshot, error) {
	return s.snapshots.definition(nameOrID)
}

// Snapshots returns every snapshot, sorted by name.
func (s *Store) Snapshots() []instance.Snapshot {
	return s.snapshots.definitions()
}

// DeleteSnapshot removes a snapshot and its files.
func (s *Store) DeleteSnapshot(nameOrID string) error {
	return s.snapshots.delete(nameOrID)
}

// SnapshotDir returns the directory holding a snapshot's files.
func (s *Store) SnapshotDir(name string) string {
	return s.snapshots.nestedDir(name)
}

// CreateNetwork records a new network definition.
func (s *Store) CreateNetwork(v network.Network) error {
	return s.networks.create(v)
}

// Network returns a network by name or ID.
func (s *Store) Network(nameOrID string) (network.Network, error) {
	return s.networks.definition(nameOrID)
}

// DeleteNetwork removes a network definition.
func (s *Store) DeleteNetwork(nameOrID string) error {
	return s.networks.delete(nameOrID)
}

// Networks returns every network, sorted by name.
func (s *Store) Networks() []network.Network {
	return s.networks.definitions()
}

// CreateVolume records a new volume definition.
func (s *Store) CreateVolume(v volume.Volume) error {
	return s.volumes.create(v)
}

// Volume returns a volume by name or ID.
func (s *Store) Volume(nameOrID string) (volume.Volume, error) {
	return s.volumes.definition(nameOrID)
}

// DeleteVolume removes a volume definition. The backing disk is the volume
// manager's to delete.
func (s *Store) DeleteVolume(nameOrID string) error {
	return s.volumes.delete(nameOrID)
}

// Volumes returns every volume, sorted by name.
func (s *Store) Volumes() []volume.Volume {
	return s.volumes.definitions()
}

// CreateKernel records a new kernel definition.
func (s *Store) CreateKernel(v kernel.Kernel) error {
	return s.kernels.create(v)
}

// Kernel returns a kernel by name or ID.
func (s *Store) Kernel(nameOrID string) (kernel.Kernel, error) {
	return s.kernels.definition(nameOrID)
}

// UpdateKernel replaces a kernel definition.
func (s *Store) UpdateKernel(v kernel.Kernel) error {
	return s.kernels.update(v)
}

// DeleteKernel removes a kernel definition. The binary is the kernel
// manager's to delete.
func (s *Store) DeleteKernel(nameOrID string) error {
	return s.kernels.delete(nameOrID)
}

// Kernels returns every kernel, sorted by name.
func (s *Store) Kernels() []kernel.Kernel {
	return s.kernels.definitions()
}

// CreateToken records a new token.
func (s *Store) CreateToken(v token.Token) error {
	return s.tokens.create(v)
}

// Token returns a token by name or ID.
func (s *Store) Token(nameOrID string) (token.Token, error) {
	return s.tokens.definition(nameOrID)
}

// TokenBySecretSHA256 returns the token whose secret has the SHA-256
// secretSHA256, or an errdefs.ErrNotFound error if there is none. It
// compares them in constant time.
func (s *Store) TokenBySecretSHA256(secretSHA256 string) (token.Token, error) {
	matches := s.tokens.matchingDefinitions(func(v token.Token) bool {
		return subtle.ConstantTimeCompare([]byte(v.SecretSHA256), []byte(secretSHA256)) == 1
	})
	if len(matches) == 0 {
		return token.Token{}, errdefs.NotFound("no token with that secret")
	}
	return matches[0], nil
}

// UpdateToken replaces a token.
func (s *Store) UpdateToken(v token.Token) error {
	return s.tokens.update(v)
}

// RecordTokenUse records that a token, by name or ID, made a call at a time.
// It changes nothing else, so a token rotated meanwhile stays rotated.
func (s *Store) RecordTokenUse(nameOrID string, at time.Time) error {
	return s.tokens.change(nameOrID, func(v token.Token) token.Token {
		v.LastUsedAt = at
		return v
	})
}

// DeleteToken removes a token.
func (s *Store) DeleteToken(nameOrID string) error {
	return s.tokens.delete(nameOrID)
}

// Tokens returns every token, sorted by name.
func (s *Store) Tokens() []token.Token {
	return s.tokens.definitions()
}
