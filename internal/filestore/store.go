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
	"log/slog"
	"sync"

	"github.com/konradasb/dicer/internal/defaults"
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
	dataDir string
	logger  *slog.Logger

	// writeMu serialises the writes. Each checks what it must against the
	// definitions in memory, writes its file, and only then puts the
	// definition in memory, so that what it checked still holds when it is
	// done.
	writeMu sync.Mutex

	// mu guards the definitions in memory, each kind by name and each name
	// by ID. It is held only to read or change them, never across a file's
	// read or write.
	mu                sync.RWMutex
	instances         map[string]instance.Spec
	instanceNamesByID map[string]string
	snapshots         map[string]instance.Snapshot
	snapshotNamesByID map[string]string
	networks          map[string]network.Network
	networkNamesByID  map[string]string
	volumes           map[string]volume.Volume
	volumeNamesByID   map[string]string
	kernels           map[string]kernel.Kernel
	kernelNamesByID   map[string]string
	tokens            map[string]token.Token
	tokenNamesByID    map[string]string
}

// New loads all definitions into memory, creating their directories if
// needed. Unreadable, malformed and misplaced definitions are logged and
// skipped.
func New(cfg Config) (*Store, error) {
	if cfg.DataDir == "" {
		cfg.DataDir = defaults.DataDir
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	s := &Store{
		dataDir:           cfg.DataDir,
		logger:            cfg.Logger.With("component", "filestore"),
		instances:         make(map[string]instance.Spec),
		instanceNamesByID: make(map[string]string),
		snapshots:         make(map[string]instance.Snapshot),
		snapshotNamesByID: make(map[string]string),
		networks:          make(map[string]network.Network),
		networkNamesByID:  make(map[string]string),
		volumes:           make(map[string]volume.Volume),
		volumeNamesByID:   make(map[string]string),
		kernels:           make(map[string]kernel.Kernel),
		kernelNamesByID:   make(map[string]string),
		tokens:            make(map[string]token.Token),
		tokenNamesByID:    make(map[string]string),
	}
	for _, load := range []func() error{
		s.loadInstances, s.loadSnapshots, s.loadNetworks, s.loadVolumes, s.loadKernels, s.loadTokens,
	} {
		if err := load(); err != nil {
			return nil, err
		}
	}

	s.logger.Info("definitions loaded",
		"data_dir", cfg.DataDir,
		"instances", len(s.instances),
		"snapshots", len(s.snapshots),
		"networks", len(s.networks),
		"volumes", len(s.volumes),
		"kernels", len(s.kernels),
		"tokens", len(s.tokens),
	)

	return s, nil
}
