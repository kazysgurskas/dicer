// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

// Package volume manages the persistent disks instances mount: their
// definitions, and the sparse files with a filesystem that back them,
// independent of any instance.
package volume

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/nrednav/cuid2"

	"github.com/konradasb/dicer/internal/diskfile"
	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
	"github.com/konradasb/dicer/internal/humanize"
)

// Store keeps the volume definitions.
type Store interface {
	CreateVolume(v Volume) error
	Volume(nameOrID string) (Volume, error)
	Volumes() []Volume
	// DeleteVolume refuses a volume an instance or snapshot mounts.
	DeleteVolume(nameOrID string) error
}

// Config configures a Manager.
type Config struct {
	// DataDir is the directory volume disks are kept under.
	DataDir string
	// Store keeps the volume definitions. It is required.
	Store Store
	// Events records what happens to volumes. Nil records nothing.
	Events Recorder
	// Logger is where the Manager logs. Nil is slog.Default().
	Logger *slog.Logger
}

// Manager creates and deletes volumes: their definitions, kept in a Store,
// and the disk files that back them. It is safe for concurrent use.
type Manager struct {
	dataDir string
	store   Store
	events  Recorder
	logger  *slog.Logger
	metrics metrics

	// createDisk is the disk creation itself, replaced in tests.
	createDisk func(ctx context.Context, path string, sizeBytes int64) error
}

// NewManager creates a Manager.
func NewManager(cfg Config) *Manager {
	if cfg.Events == nil {
		cfg.Events = discardRecorder{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	return &Manager{
		dataDir:    cfg.DataDir,
		store:      cfg.Store,
		events:     cfg.Events,
		logger:     cfg.Logger.With("component", "volume"),
		metrics:    newMetrics(),
		createDisk: diskfile.CreateExt4,
	}
}

// Path returns the path of a volume's disk file.
func (m *Manager) Path(v Volume) string {
	return filepath.Join(m.volumeDir(v.ID), "disk.raw")
}

// volumeDir returns the directory holding a volume's disk file.
func (m *Manager) volumeDir(id string) string {
	return filepath.Join(m.dataDir, "volumes", id)
}

// Create makes a volume: a sparse, ext4-formatted disk file, and its
// definition. A volume whose name is taken is refused with an
// errdefs.ErrExists error.
func (m *Manager) Create(ctx context.Context, name string, sizeBytes int64) (Volume, error) {
	id := cuid2.Generate()
	now := time.Now()
	volume := Volume{
		ID:        id,
		Name:      name,
		SizeBytes: sizeBytes,
		CreatedAt: now,
		UpdatedAt: now,
	}
	volume.Path = m.Path(volume)
	if err := volume.Validate(); err != nil {
		return Volume{}, err
	}
	// Before the disk is made, so that a taken name costs none.
	if _, err := m.store.Volume(name); err == nil {
		return Volume{}, errdefs.Exists("volume %q already exists", name)
	}

	if err := m.createDisk(ctx, volume.Path, sizeBytes); err != nil {
		_ = os.RemoveAll(m.volumeDir(id))
		return Volume{}, fmt.Errorf("create volume disk: %w", err)
	}
	if err := m.store.CreateVolume(volume); err != nil {
		_ = os.RemoveAll(m.volumeDir(id))
		return Volume{}, err
	}

	m.logger.InfoContext(ctx, "volume created", "volume_id", id, "name", name, "size_bytes", sizeBytes)
	m.record(volume, event.ActionCreated, "Created volume of "+humanize.Bytes(sizeBytes)+", formatted ext4")
	return volume, nil
}

// Volume returns a volume by name or ID.
func (m *Manager) Volume(nameOrID string) (Volume, error) {
	return m.store.Volume(nameOrID)
}

// Volumes returns every volume, sorted by name.
func (m *Manager) Volumes() []Volume {
	return m.store.Volumes()
}

// DiskBytes returns the disk a volume's file takes up, which for a sparse
// file is less than its size, or 0 if the volume has no disk.
func (m *Manager) DiskBytes(v Volume) int64 {
	return diskfile.AllocatedBytes(m.Path(v))
}

// Delete removes a volume's definition and its disk, refusing a volume an
// instance or snapshot mounts.
func (m *Manager) Delete(nameOrID string) error {
	volume, err := m.store.Volume(nameOrID)
	if err != nil {
		return err
	}
	if err := m.store.DeleteVolume(volume.ID); err != nil {
		return err
	}
	m.record(volume, event.ActionDeleted, "Deleted volume of "+humanize.Bytes(volume.SizeBytes)+" and its data")

	if err := os.RemoveAll(m.volumeDir(volume.ID)); err != nil {
		return fmt.Errorf("remove volume disk: %w", err)
	}
	return nil
}
