// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"context"
	"fmt"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
)

// rename is Rename, for an instance its caller has looked up.
func (m *Manager) rename(ctx context.Context, instance Spec, newName string) (_ Spec, err error) {
	lock := m.lock(instance.ID)
	lock.Lock()
	defer lock.Unlock()

	current, err := m.store.Instance(instance.ID)
	if err != nil {
		return Spec{}, err
	}

	status, err := m.statusOf(current)
	if err != nil {
		return Spec{}, err
	}
	if !renameable(status.State) {
		return Spec{}, errdefs.InvalidState(
			"instance %q is %s; rename it once it has stopped", current.Name, status.State.Lowercase())
	}

	if newName == current.Name {
		return current, nil
	}

	renamed := current
	renamed.Name = newName

	if err := m.store.RenameInstance(current.ID, renamed); err != nil {
		return Spec{}, fmt.Errorf("rename instance %q: %w", current.Name, err)
	}

	m.record(renamed, event.ActionRenamed, fmt.Sprintf("Renamed instance %s to %s", current.Name, newName),
		map[string]string{"previous_name": current.Name})
	m.logger.InfoContext(ctx, "renamed instance", "instance", newName, "previous_name", current.Name)

	return renamed, nil
}

// Rename changes a Stopped or Failed instance's name and returns the renamed
// instance. Its persistent directory moves; everything keyed by ID stays.
func (m *Manager) Rename(ctx context.Context, nameOrID, newName string) (Spec, error) {
	instance, err := m.store.Instance(nameOrID)
	if err != nil {
		return Spec{}, err
	}
	return m.rename(ctx, instance, newName)
}

// renameable reports whether an instance in the state can be renamed.
func renameable(state State) bool {
	return state == StateStopped || state == StateFailed
}
