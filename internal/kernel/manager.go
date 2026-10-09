// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

// Package kernel manages guest kernels: their definitions, and their
// binaries on the host. A kernel is the default one this binary carries or
// one a client imports, checked against its SHA-256.
package kernel

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/nrednav/cuid2"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
	"github.com/konradasb/dicer/internal/humanize"
)

// Store keeps the kernel definitions.
type Store interface {
	CreateKernel(k Kernel) error
	Kernel(nameOrID string) (Kernel, error)
	Kernels() []Kernel
	UpdateKernel(k Kernel) error
	// DeleteKernel refuses a kernel an instance or snapshot boots.
	DeleteKernel(nameOrID string) error
}

// Config configures a Manager.
type Config struct {
	// DataDir holds the kernels, under kernels/<id>; it is required.
	DataDir string
	// Store keeps the kernel definitions. It is required.
	Store Store
	// Events records what happens to kernels. Nil records nothing.
	Events Recorder
	// Logger defaults to slog.Default.
	Logger *slog.Logger
}

// Manager imports and deletes kernels: their definitions, kept in a Store,
// and their binaries on the host. It is safe for concurrent use.
type Manager struct {
	dataDir string
	store   Store
	events  Recorder
	logger  *slog.Logger
	metrics metrics
}

// NewManager returns a Manager for the kernels under cfg.DataDir.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("data directory is required")
	}
	if cfg.Events == nil {
		cfg.Events = discardRecorder{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	return &Manager{
		dataDir: cfg.DataDir,
		store:   cfg.Store,
		events:  cfg.Events,
		logger:  cfg.Logger.With("component", "kernel"),
		metrics: newMetrics(),
	}, nil
}

// Import keeps the kernel a client sends, read from r, and defines it. It
// refuses an invalid kernel, the default kernel's name and a name already
// taken, and checks the kernel against sha256 if that is set. The SHA-256 is
// kept whether or not it is set, so that the kernel is checked each time an
// instance boots it. Nothing is kept if it fails.
func (m *Manager) Import(name, architecture, sha256 string, r io.Reader) (Kernel, error) {
	now := time.Now()
	k := Kernel{
		ID:           cuid2.Generate(),
		Name:         name,
		Architecture: architecture,
		SHA256:       strings.ToLower(sha256),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := k.Validate(); err != nil {
		return Kernel{}, err
	}
	if k.Name == DefaultName {
		return Kernel{}, errdefs.InvalidArgument("%q is the default kernel's name: import the kernel under another", k.Name)
	}
	if _, err := m.store.Kernel(k.Name); err == nil {
		return Kernel{}, errdefs.Exists("kernel %q already exists", k.Name)
	}

	digest, err := m.writeBinary(k.ID, r, k.SHA256)
	if err != nil {
		_ = m.removeBinary(k.ID)
		return Kernel{}, err
	}
	k.SHA256 = digest
	if err := m.store.CreateKernel(k); err != nil {
		_ = m.removeBinary(k.ID)
		return Kernel{}, err
	}

	verified := "checksum verified"
	if sha256 == "" {
		verified = "no checksum given to verify it by"
	}
	m.record(k, event.ActionImported, fmt.Sprintf("Imported kernel for %s: %s, %s",
		k.Architecture, humanize.Bytes(m.DiskBytes(k)), verified))
	return k, nil
}

// Kernel returns a kernel by name or ID.
func (m *Manager) Kernel(nameOrID string) (Kernel, error) {
	return m.store.Kernel(nameOrID)
}

// Kernels returns every kernel, sorted by name.
func (m *Manager) Kernels() []Kernel {
	return m.store.Kernels()
}

// Delete removes a kernel's definition and its binary, refusing the default
// kernel and one an instance or snapshot boots.
func (m *Manager) Delete(nameOrID string) error {
	k, err := m.store.Kernel(nameOrID)
	if err != nil {
		return err
	}
	if k.Name == DefaultName {
		return errdefs.InvalidArgument("the default kernel cannot be deleted")
	}
	if err := m.store.DeleteKernel(k.ID); err != nil {
		return err
	}
	m.record(k, event.ActionDeleted, "Deleted kernel and its copy on the host")

	if err := m.removeBinary(k.ID); err != nil {
		return fmt.Errorf("remove kernel binary: %w", err)
	}
	return nil
}
