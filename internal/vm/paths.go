// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package vm

import (
	"path/filepath"

	"github.com/konradasb/dicer/internal/types"
)

// An instance's files live in two places. The persistent instance directory,
// keyed by name, holds the overlay disk, the console and hypervisor logs and,
// on standby, its frozen guest. The runtime directory under RunDir, keyed by
// ID, holds the status, the sockets, and the config and status disks.
//
// The VMM runs in the runtime directory, where the overlay disk and console
// log are linked in, and is given each of these files by its name alone. A
// snapshot of the guest thus names no directory of the instance's, and
// restores into any instance's runtime directory: the same instance's after
// a rename, or another's. The VMM's own output goes to the hypervisor log
// through a link there too, so that the log outlives the VMM.
const (
	standbyDirName       = "standby"
	overlayDiskFile      = "overlay.img"
	serialLogFile        = "serial.log"
	hypervisorLogFile    = "hypervisor.log"
	statusFile           = "state.json"
	configDiskFile       = "config.img"
	statusDiskFile       = "status.img"
	hypervisorSocketFile = "hypervisor.sock"
	vsockSocketFile      = "vsock.sock"
)

// instanceDir returns an instance's persistent directory.
func (m *Manager) instanceDir(instance types.InstanceSpec) string {
	return m.definitions.InstanceDir(instance.Name)
}

// overlayDiskPath returns the writable disk holding an instance's root
// filesystem.
func (m *Manager) overlayDiskPath(instance types.InstanceSpec) string {
	return filepath.Join(m.instanceDir(instance), overlayDiskFile)
}

// keptOverlayDiskPath returns where an instance's overlay disk is kept while
// a restore replaces it, until the restore succeeds.
func (m *Manager) keptOverlayDiskPath(instance types.InstanceSpec) string {
	return m.overlayDiskPath(instance) + ".kept"
}

// standbyDir returns the directory an instance on standby is frozen in.
func (m *Manager) standbyDir(instance types.InstanceSpec) string {
	return filepath.Join(m.instanceDir(instance), standbyDirName)
}

// serialLogPath returns the file an instance's serial console is written to.
func (m *Manager) serialLogPath(instance types.InstanceSpec) string {
	return filepath.Join(m.instanceDir(instance), serialLogFile)
}

// snapshotDir returns the directory holding a snapshot's files.
func (m *Manager) snapshotDir(snapshot types.Snapshot) string {
	return m.definitions.SnapshotDir(snapshot.Name)
}

// snapshotOverlayDiskPath returns a snapshot's copy of the overlay disk.
func (m *Manager) snapshotOverlayDiskPath(snapshot types.Snapshot) string {
	return filepath.Join(m.snapshotDir(snapshot), overlayDiskFile)
}

// runtimeDir returns an instance's ephemeral directory.
func (m *Manager) runtimeDir(instanceID string) string {
	return filepath.Join(m.runDir, "instances", instanceID)
}

// statusPath returns the file an instance's status is kept in.
func (m *Manager) statusPath(instanceID string) string {
	return filepath.Join(m.runtimeDir(instanceID), statusFile)
}

// configDiskPath returns the disk dicer-init reads its configuration from.
func (m *Manager) configDiskPath(instanceID string) string {
	return filepath.Join(m.runtimeDir(instanceID), configDiskFile)
}

// statusDiskPath returns the disk the guest reports its end on.
func (m *Manager) statusDiskPath(instanceID string) string {
	return filepath.Join(m.runtimeDir(instanceID), statusDiskFile)
}

// hypervisorSocketPath returns the socket an instance's VMM serves its API on.
func (m *Manager) hypervisorSocketPath(instanceID string) string {
	return filepath.Join(m.runtimeDir(instanceID), hypervisorSocketFile)
}

// hypervisorLogPath returns the file an instance's VMM writes its own log
// to, through a link in the runtime directory.
func (m *Manager) hypervisorLogPath(instance types.InstanceSpec) string {
	return filepath.Join(m.instanceDir(instance), hypervisorLogFile)
}

// vsockPath returns the host end of an instance's vsock device.
func (m *Manager) vsockPath(instanceID string) string {
	return filepath.Join(m.runtimeDir(instanceID), vsockSocketFile)
}
