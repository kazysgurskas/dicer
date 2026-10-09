// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"context"
	"errors"
	"testing"
)

// TestStopFailedInstance checks that a failed start can be put to rest: Stop
// on a Failed instance cleans up and leaves it Stopped, rather than refusing.
func TestStopFailedInstance(t *testing.T) {
	manager, store, _ := newTestManager(t)
	instance := seedInstance(t, store, "web")

	manager.fail(instance.ID, errors.New("boot failed"))

	if err := manager.Stop(t.Context(), instance); err != nil {
		t.Fatalf("Stop() of a failed instance = %v, want nil", err)
	}

	status, err := manager.Status(instance)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateStopped {
		t.Errorf("state = %s, want %s", status.State, StateStopped)
	}
}

// A stop the client gives up on is still seen through: the host network is
// torn down as fully as for any other.
func TestStopOutlivesItsRequest(t *testing.T) {
	manager, store, hostNetwork := newTestManager(t)
	instance := seedInstance(t, store, "web")
	manager.fail(instance.ID, errors.New("boot failed"))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := manager.Stop(ctx, instance); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if n := hostNetwork.cancelledTeardowns.Load(); n > 0 {
		t.Errorf("%d network teardowns were asked for with a cancelled context", n)
	}
}
