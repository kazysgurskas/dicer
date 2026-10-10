// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"context"
	"fmt"
	"time"

	"github.com/konradasb/dicer/internal/event"
)

// stop is Stop, for an instance its caller has looked up.
func (m *Manager) stop(ctx context.Context, instance Spec) (err error) {
	started := time.Now()
	defer func() { m.observeOperation(operationStop, started, err) }()

	lock := m.lock(instance.ID)
	lock.Lock()
	defer lock.Unlock()
	if err := m.rereadDefinition(&instance); err != nil {
		return err
	}
	defer m.syncWaker(ctx, instance.ID)

	m.cancelRestart(instance.ID)
	m.setStoppedByUser(ctx, instance, true)

	status, err := m.statusOf(instance)
	if err != nil {
		return err
	}
	if err := m.discardStandby(instance); err != nil {
		return err
	}
	switch status.State {
	case StateStopped:
		return nil
	case StateStandby:
		m.record(instance, event.ActionStopped, "Stopped instance: discarded what it had frozen on standby", nil)
		m.logger.InfoContext(ctx, "stopped instance", "instance", instance.Name)
		m.notifyWaiters(Status{InstanceID: instance.ID, State: StateStopped})
		m.scheduleRemoval(ctx, instance)
		return nil
	}

	if err := m.transition(instance, StateStopping); err != nil {
		return err
	}
	// Finish the stop even if the request is cancelled.
	ctx = context.WithoutCancel(ctx)

	stopping := time.Now()
	outcome := m.stopVMM(ctx, instance, status, true)
	took := time.Since(stopping)
	m.teardownNetwork(ctx, instance)

	if err := m.removeRuntimeDir(instance.ID); err != nil {
		return fmt.Errorf("remove runtime directory: %w", err)
	}

	var ranFor time.Duration
	if !status.StartedAt.IsZero() {
		ranFor = time.Since(status.StartedAt)
	}
	m.record(instance, event.ActionStopped, stopMessage(outcome, m.stopGracePeriod, took, ranFor), nil)
	m.logger.InfoContext(ctx, "stopped instance", "instance", instance.Name)

	m.notifyWaiters(Status{InstanceID: instance.ID, State: StateStopped})
	m.scheduleRemoval(ctx, instance)

	return nil
}

// Stop shuts down an instance and releases its host resources, keeping its
// definition, disk and address. An instance on standby is stopped by
// discarding what it has frozen, so that it boots afresh. It cancels any
// pending restart and sets StoppedByUser. Stopping a stopped instance is not
// an error.
func (m *Manager) Stop(ctx context.Context, nameOrID string) error {
	instance, err := m.store.Instance(nameOrID)
	if err != nil {
		return err
	}
	return m.stop(ctx, instance)
}
