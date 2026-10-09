// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/konradasb/dicer/internal/atomicfile"
	"github.com/konradasb/dicer/internal/health"
	"github.com/konradasb/dicer/internal/hypervisor"
	"github.com/konradasb/dicer/internal/process"
)

// An instance's status is read from and written to its runtime directory
// directly, with no cache.

// Status returns an instance's status. An instance with no status file is
// Stopped.
func (m *Manager) Status(instance Spec) (Status, error) {
	status, err := m.readStatus(instance.ID)
	if err != nil {
		return Status{}, err
	}
	// Standby leaves no runtime status, which a reboot would lose: the guest
	// frozen to disk is what says the instance is on standby.
	if status.State == StateStopped && m.onStandby(instance) {
		status.State = StateStandby
	}
	return status, nil
}

// readStatus returns the status of an instance by ID.
func (m *Manager) readStatus(instanceID string) (Status, error) {
	data, err := os.ReadFile(m.statusPath(instanceID))
	if errors.Is(err, fs.ErrNotExist) {
		return Status{InstanceID: instanceID, State: StateStopped}, nil
	}

	var status Status
	if err == nil {
		err = json.Unmarshal(data, &status)
	}
	if err != nil {
		// Report Failed so recovery cleans up.
		m.logger.Warn("corrupt instance status, treating instance as failed",
			"instance_id", instanceID, "error", err)
		return Status{
			InstanceID: instanceID,
			State:      StateFailed,
			StateError: "corrupt instance status",
		}, nil
	}

	return status, nil
}

// writeStatus records an instance's status.
func (m *Manager) writeStatus(status Status) error {
	if err := m.ensureRuntimeDir(status.InstanceID); err != nil {
		return err
	}

	status.UpdatedAt = time.Now()

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal instance status: %w", err)
	}

	return atomicfile.Write(m.statusPath(status.InstanceID), data, 0o600)
}

// runRecord is what a start or restore records of the guest it launched.
type runRecord struct {
	hypervisorVersion string
	held              Resources
	imageDigest       string
	// vsockCID is the guest's vsock context ID: its own instance's, or,
	// for a fork, that of the instance it is a copy of.
	vsockCID int64

	restarts    int
	healthCheck *health.Check
}

// recordRunning records that an instance is running on vmm and returns the
// recorded state.
func (m *Manager) recordRunning(instance Spec, vmm *process.Process, run runRecord) (Status, error) {
	pid := vmm.PID()

	status := Status{
		InstanceID:           instance.ID,
		State:                StateRunning,
		VMMPID:               &pid,
		HypervisorSocketPath: m.hypervisorSocketPath(instance.ID),
		HypervisorVersion:    run.hypervisorVersion,
		VsockCID:             run.vsockCID,
		VsockPath:            m.vsockPath(instance.ID),
		VCPUs:                run.held.VCPUs,
		MemoryBytes:          run.held.MemoryBytes,
		ImageDigest:          run.imageDigest,
		HealthCheck:          run.healthCheck,
		StartedAt:            time.Now(),
		RestartCount:         run.restarts,
	}
	if err := m.writeStatus(status); err != nil {
		return Status{}, fmt.Errorf("record instance status: %w", err)
	}

	return status, nil
}

// removeRuntimeDir removes an instance's runtime directory and everything
// in it, its status included.
func (m *Manager) removeRuntimeDir(instanceID string) error {
	dir := m.runtimeDir(instanceID)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove runtime directory %s: %w", dir, err)
	}

	return nil
}

// ensureRuntimeDir creates an instance's runtime directory.
func (m *Manager) ensureRuntimeDir(instanceID string) error {
	dir := m.runtimeDir(instanceID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create runtime directory %s: %w", dir, err)
	}

	return nil
}

// prepareRuntimeDir readies an instance's runtime directory for a new VMM.
// It removes sockets a previous VMM left behind, and links the overlay disk
// and the console and hypervisor logs in from the instance directory.
func (m *Manager) prepareRuntimeDir(instance Spec) error {
	if err := m.ensureRuntimeDir(instance.ID); err != nil {
		return err
	}

	for _, path := range []string{m.hypervisorSocketPath(instance.ID), m.vsockPath(instance.ID)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove stale socket %s: %w", path, err)
		}
	}

	hypervisorLogLink := hypervisor.LogPath(m.hypervisorSocketPath(instance.ID))
	if err := os.MkdirAll(filepath.Dir(hypervisorLogLink), 0o750); err != nil {
		return fmt.Errorf("create hypervisor log directory: %w", err)
	}

	// Linked afresh each time: a rename moves the instance directory.
	dir := m.runtimeDir(instance.ID)
	links := map[string]string{
		filepath.Join(dir, overlayDiskFile): m.overlayDiskPath(instance),
		filepath.Join(dir, serialLogFile):   m.serialLogPath(instance),
		hypervisorLogLink:                   m.hypervisorLogPath(instance),
	}
	for link, target := range links {
		if err := os.Remove(link); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove stale link %s: %w", link, err)
		}
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("link %s into the runtime directory: %w", target, err)
		}
	}

	return nil
}
