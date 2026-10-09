// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package hostnet

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/konradasb/dicer/internal/network"
)

// interfaceAddresses are the addresses of one of the host's interfaces.
type interfaceAddresses struct {
	name      string
	up        bool
	loopback  bool
	addresses []netip.Addr
}

// Addresses returns the addresses other machines may reach this host at:
// those of its interfaces that are up, other than loopback and link-local
// ones and those of the networks' bridges. The uplink's come first, then
// the rest in the order of their interfaces, IPv4 before IPv6 on each.
func (h *Host) Addresses(networks []network.Network) ([]netip.Addr, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list interfaces: %w", err)
	}

	all := make([]interfaceAddresses, 0, len(interfaces))
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("list the addresses of %s: %w", iface.Name, err)
		}

		ia := interfaceAddresses{
			name:     iface.Name,
			up:       iface.Flags&net.FlagUp != 0,
			loopback: iface.Flags&net.FlagLoopback != 0,
		}
		for _, a := range addrs {
			if prefix, err := netip.ParsePrefix(a.String()); err == nil {
				ia.addresses = append(ia.addresses, prefix.Addr().Unmap())
			}
		}
		all = append(all, ia)
	}

	// Without a default route there is no uplink to put first.
	uplink, _ := h.uplink()

	bridges := make(map[string]bool, len(networks))
	for _, nw := range networks {
		bridges[nw.Bridge] = true
	}

	return hostAddresses(all, uplink, bridges), nil
}

// hostAddresses returns the addresses Addresses does, of the given
// interfaces.
func hostAddresses(interfaces []interfaceAddresses, uplink string, bridges map[string]bool) []netip.Addr {
	var first, rest []netip.Addr
	for _, iface := range interfaces {
		if !iface.up || iface.loopback || bridges[iface.name] {
			continue
		}

		var v4, v6 []netip.Addr
		for _, a := range iface.addresses {
			switch {
			case !a.IsGlobalUnicast():
				continue
			case a.Is4():
				v4 = append(v4, a)
			default:
				v6 = append(v6, a)
			}
		}

		if iface.name == uplink {
			first = append(append(first, v4...), v6...)
		} else {
			rest = append(append(rest, v4...), v6...)
		}
	}
	return append(first, rest...)
}
