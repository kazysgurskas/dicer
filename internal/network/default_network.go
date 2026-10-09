// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package network

import (
	"fmt"

	"github.com/konradasb/dicer/internal/event"
)

// EnsureDefault creates the default network on subnet, unless it already
// exists. An existing one is left as it is.
func (m *Manager) EnsureDefault(subnet string) error {
	if _, err := m.store.Network(DefaultName); err == nil {
		return nil
	}

	n, err := New(Spec{Name: DefaultName, Subnet: subnet})
	if err != nil {
		return fmt.Errorf("create the default network: %w", err)
	}
	if err := m.checkSubnetIsFree(n); err != nil {
		return fmt.Errorf("create the default network: %w", err)
	}
	if err := m.store.CreateNetwork(n); err != nil {
		return fmt.Errorf("create the default network: %w", err)
	}

	m.record(n, event.ActionCreated, fmt.Sprintf("Created the default network with subnet %s, gateway %s", n.Subnet, n.Gateway))
	return nil
}
