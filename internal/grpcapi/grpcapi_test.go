// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/konradasb/dicer/internal/filestore"
	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/kernel"
	"github.com/konradasb/dicer/internal/network"
	"github.com/konradasb/dicer/internal/volume"
)

// wantClass checks that an error reached the client in the class the handler
// put it in, which is the whole of what a client can match on.
func wantClass(t *testing.T, err, class error) {
	t.Helper()

	if !errors.Is(err, class) {
		t.Errorf("error %v is not in class %v", err, class)
	}
}

// testCapacity is a 4-CPU, 8GiB host with the daemon's default admission:
// 16 vCPUs and 7GiB.
var testCapacity = instance.Capacity{
	Host:                instance.Resources{VCPUs: 4, MemoryBytes: 8 << 30},
	ReservedMemoryBytes: 1 << 30,
	CPUOvercommit:       4,
	MemoryOvercommit:    1,
}

// newTestServer returns a Server over a real store and an instance manager
// with testCapacity, and the store.
func newTestServer(t *testing.T) (*Server, *filestore.Store) {
	t.Helper()

	logger := slog.New(slog.DiscardHandler)
	dataDir := filepath.Join(t.TempDir(), "data")

	store, err := filestore.New(filestore.Config{DataDir: dataDir, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	// What an instance a test seeds names, as the API fills it in.
	if err := store.CreateKernel(kernel.Kernel{ID: "kernel-default", Name: kernel.DefaultName}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateNetwork(network.Network{
		ID: "network-default", Name: network.DefaultName, Subnet: "10.0.0.0/24", Gateway: "10.0.0.1", Bridge: "dicer0",
	}); err != nil {
		t.Fatal(err)
	}

	networkManager, err := network.NewManager(network.Config{
		Dir: filepath.Join(dataDir, "allocations"), Store: store, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	kernelManager, err := kernel.NewManager(kernel.Config{DataDir: dataDir, Store: store, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}

	instanceManager := instance.NewManager(instance.Config{
		Store:    store,
		RunDir:   filepath.Join(t.TempDir(), "run"),
		Capacity: testCapacity,
		Logger:   logger,
	})

	return NewServer(Config{
		NetworkManager:  networkManager,
		InstanceManager: instanceManager,
		VolumeManager:   volume.NewManager(volume.Config{DataDir: dataDir, Store: store, Logger: logger}),
		KernelManager:   kernelManager,
		DataDir:         dataDir,
	}), store
}
