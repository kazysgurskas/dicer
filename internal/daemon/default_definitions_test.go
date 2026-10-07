// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package daemon

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/events"
	"github.com/konradasb/dicer/internal/filestore"
	"github.com/konradasb/dicer/internal/kernel"
	"github.com/konradasb/dicer/internal/types"
)

// newDefinitionsDaemon returns a daemon with only what creating the default
// network and kernel needs. Its default network's subnet is one a test host
// is unlikely to be on.
func newDefinitionsDaemon(t *testing.T) *daemon {
	t.Helper()

	dataDir := t.TempDir()
	logger := slog.New(slog.DiscardHandler)

	definitions, err := filestore.NewManager(filestore.Config{DataDir: dataDir, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	kernels, err := kernel.NewManager(kernel.Config{DataDir: dataDir, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	log, err := events.Open(events.Config{File: filepath.Join(dataDir, eventsFile), Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })

	cfg := defaultConfig()
	cfg.DataDir = dataDir
	cfg.Network.DefaultSubnet = "10.250.0.0/16"

	return &daemon{cfg: &cfg, logger: logger, definitions: definitions, kernels: kernels, events: log}
}

func TestDefaultNetworkIsCreatedOnce(t *testing.T) {
	d := newDefinitionsDaemon(t)

	if err := d.ensureDefaultNetwork(); err != nil {
		t.Fatalf("ensureDefaultNetwork: %v", err)
	}
	n, err := d.definitions.Network(types.DefaultNetworkName)
	if err != nil || n.Subnet != "10.250.0.0/16" || n.Gateway != "10.250.0.1" {
		t.Fatalf("default network = %+v, %v; want it on 10.250.0.0/16", n, err)
	}

	// A later start leaves it as it is, even when the subnet configured has
	// changed.
	d.cfg.Network.DefaultSubnet = "10.251.0.0/16"
	if err := d.ensureDefaultNetwork(); err != nil {
		t.Fatalf("ensureDefaultNetwork again: %v", err)
	}
	if again, _ := d.definitions.Network(types.DefaultNetworkName); again.ID != n.ID || again.Subnet != n.Subnet {
		t.Errorf("default network = %+v after a second start, want %+v", again, n)
	}
}

func TestDefaultNetworkRefusesATakenSubnet(t *testing.T) {
	d := newDefinitionsDaemon(t)
	if err := d.definitions.CreateNetwork(types.Network{ID: "n-1", Name: "lan", Subnet: "10.250.1.0/24"}); err != nil {
		t.Fatal(err)
	}

	if err := d.ensureDefaultNetwork(); !errors.Is(err, errdefs.ErrExists) {
		t.Errorf("ensureDefaultNetwork = %v, want the overlap refused", err)
	}
}

// TestDefaultKernelFollowsThePinnedRelease checks that the default kernel is
// defined as this version pins it, and that one an older version defined is
// updated in place, its fetched binary removed.
func TestDefaultKernelFollowsThePinnedRelease(t *testing.T) {
	d := newDefinitionsDaemon(t)
	want, err := kernel.Default()
	if err != nil {
		t.Fatal(err)
	}

	if err := d.ensureDefaultKernel(); err != nil {
		t.Fatalf("ensureDefaultKernel: %v", err)
	}
	k, err := d.definitions.Kernel(types.DefaultKernelName)
	if err != nil || k.URL != want.URL || k.SHA256 != want.SHA256 {
		t.Fatalf("default kernel = %+v, %v; want %s", k, err, want.URL)
	}

	k.URL, k.SHA256 = "https://example.invalid/old-vmlinux", ""
	if err := d.definitions.UpdateKernel(k); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(d.cfg.DataDir, "kernels", k.ID, "vmlinux")
	if err := os.MkdirAll(filepath.Dir(binary), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := d.ensureDefaultKernel(); err != nil {
		t.Fatalf("ensureDefaultKernel after an upgrade: %v", err)
	}
	updated, _ := d.definitions.Kernel(types.DefaultKernelName)
	if updated.ID != k.ID || updated.URL != want.URL || updated.SHA256 != want.SHA256 {
		t.Errorf("default kernel = %+v, want %s under the same ID", updated, want.URL)
	}
	if _, err := os.Stat(binary); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the old binary is still there: %v", err)
	}
}
