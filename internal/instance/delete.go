// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"context"
	"fmt"
	"time"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
)

// delete is Delete, for an instance its caller has looked up.
func (m *Manager) delete(ctx context.Context, instance Spec, force bool) (err error) {
	started := time.Now()
	defer func() { m.observeOperation(operationDelete, started, err) }()

	lock := m.lock(instance.ID)
	lock.Lock()
	defer lock.Unlock()
	if err := m.rereadDefinition(&instance); err != nil {
		return err
	}
	defer m.syncWaker(ctx, instance.ID)

	status, err := m.statusOf(instance)
	if err != nil {
		return err
	}

	if status.State.IsActive() && !force {
		return errdefs.InvalidState("instance %q is %s", instance.Name, status.State.Lowercase())
	}
	m.cancelRestart(instance.ID)
	// Finish the delete even if the request is cancelled.
	ctx = context.WithoutCancel(ctx)
	m.stopVMM(ctx, instance, status, false)

	m.teardownNetwork(ctx, instance)

	if err := m.networks.Release(instance.NetworkName, instance.ID); err != nil {
		m.logger.WarnContext(ctx, "failed to release network allocation",
			"instance", instance.Name, "error", err)
	}

	if err := m.removeRuntimeDir(instance.ID); err != nil {
		m.logger.WarnContext(ctx, "failed to remove the runtime directory",
			"instance", instance.Name, "error", err)
	}

	if err := m.store.DeleteInstance(instance.Name); err != nil {
		return fmt.Errorf("delete instance %q: %w", instance.Name, err)
	}

	m.record(instance, event.ActionDeleted,
		"Deleted instance: removed its definition and disks; released its address on network "+instance.NetworkName, nil)

	// A deleted instance never runs again. One deleted as it ran was
	// stopped by it.
	if !isStopped(status.State) {
		status = Status{InstanceID: instance.ID, State: StateStopped}
	}
	m.notifyWaiters(status)
	m.logger.InfoContext(ctx, "deleted instance", "instance", instance.Name)
	return nil
}

// Delete removes an instance and everything it owns: its VM, network
// resources, address, status and directory. Volumes and snapshots are kept. An
// active instance is refused unless force is set.
func (m *Manager) Delete(ctx context.Context, nameOrID string, force bool) error {
	instance, err := m.store.Instance(nameOrID)
	if err != nil {
		return err
	}
	return m.delete(ctx, instance, force)
}
