// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package network

import (
	"fmt"
	"net/netip"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
)

// Store keeps the network definitions.
type Store interface {
	CreateNetwork(n Network) error
	Network(nameOrID string) (Network, error)
	Networks() []Network
	// DeleteNetwork refuses a network an instance or snapshot is on.
	DeleteNetwork(nameOrID string) error
}

// Create defines the network spec asks for, refusing a name that is taken and
// a subnet another network or the host is on. Unset gateway, MTU and
// nameservers take their defaults.
func (m *Manager) Create(spec Spec) (Network, error) {
	n, err := New(spec)
	if err != nil {
		return Network{}, err
	}
	if _, err := m.store.Network(n.Name); err == nil {
		return Network{}, errdefs.Exists("network %q already exists", n.Name)
	}
	if err := m.checkSubnetIsFree(n); err != nil {
		return Network{}, err
	}
	if err := m.store.CreateNetwork(n); err != nil {
		return Network{}, err
	}

	message := fmt.Sprintf("Created network with subnet %s, gateway %s", n.Subnet, n.Gateway)
	if n.Isolated {
		message += "; isolated: its instances cannot reach each other"
	}
	if n.Internal {
		message += "; internal: its instances cannot reach the host or beyond it"
	}
	m.record(n, event.ActionCreated, message)
	return n, nil
}

// checkSubnetIsFree returns an errdefs.ErrExists error if n's subnet overlaps
// another network's or one the host is on.
func (m *Manager) checkSubnetIsFree(n Network) error {
	var hostSubnets []netip.Prefix
	if m.hostSubnets != nil {
		var err error
		if hostSubnets, err = m.hostSubnets(); err != nil {
			return fmt.Errorf("list the host's subnets: %w", err)
		}
	}
	return CheckSubnetOverlap(n, m.store.Networks(), hostSubnets)
}

// Network returns a network by name or ID.
func (m *Manager) Network(nameOrID string) (Network, error) {
	return m.store.Network(nameOrID)
}

// Networks returns every network, sorted by name.
func (m *Manager) Networks() []Network {
	return m.store.Networks()
}

// Delete removes a network's definition and its allocations, refusing the
// default network and one an instance or snapshot is on.
func (m *Manager) Delete(nameOrID string) error {
	n, err := m.store.Network(nameOrID)
	if err != nil {
		return err
	}
	if n.Name == DefaultName {
		return errdefs.InvalidArgument("the default network cannot be deleted")
	}
	if err := m.store.DeleteNetwork(n.ID); err != nil {
		return err
	}
	m.record(n, event.ActionDeleted, "Deleted network with subnet "+n.Subnet)

	if err := m.Forget(n.Name); err != nil {
		return fmt.Errorf("discard allocations: %w", err)
	}
	return nil
}
