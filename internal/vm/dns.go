// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package vm

import (
	"net/netip"
	"strings"

	"github.com/konradasb/dicer/internal/types"
)

// The Manager is what a network's DNS server asks about the network: see
// package dns.

// LookupHost returns the addresses of the instances on a network whose name
// or hostname is name, compared without regard to case. Only instances that
// are running, or starting, are found: a stopped one's address answers
// nothing.
func (m *Manager) LookupHost(network, name string) []netip.Addr {
	var out []netip.Addr
	for _, inst := range m.answerable(network) {
		if !strings.EqualFold(inst.spec.Name, name) && !strings.EqualFold(inst.spec.Hostname, name) {
			continue
		}
		out = append(out, inst.addr)
	}
	return out
}

// LookupAddr returns the name of the running instance on a network that
// holds addr, and its hostname if it is another.
func (m *Manager) LookupAddr(network string, addr netip.Addr) []string {
	for _, inst := range m.answerable(network) {
		if inst.addr != addr {
			continue
		}
		names := []string{inst.spec.Name}
		if h := inst.spec.Hostname; h != "" && !strings.EqualFold(h, inst.spec.Name) {
			names = append(names, h)
		}
		return names
	}
	return nil
}

// answerableInstance is an instance a network's DNS server may give out.
type answerableInstance struct {
	spec types.InstanceSpec
	addr netip.Addr
}

// answerable returns the instances on a network that are running or
// starting, with their addresses.
func (m *Manager) answerable(network string) []answerableInstance {
	instances, err := m.definitions.ListInstances()
	if err != nil {
		return nil
	}

	var out []answerableInstance
	for _, inst := range instances {
		if inst.NetworkName != network {
			continue
		}
		rt, err := m.Runtime(inst)
		if err != nil || (!rt.State.IsActive() && rt.State != types.StateStarting) {
			continue
		}
		alloc, err := m.addresses.Get(network, inst.ID)
		if err != nil {
			continue
		}
		addr, err := netip.ParseAddr(alloc.IP)
		if err != nil {
			continue
		}
		out = append(out, answerableInstance{spec: inst, addr: addr})
	}
	return out
}
