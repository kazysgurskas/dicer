// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package hostnet

import (
	"net/netip"
	"slices"
	"testing"
)

func TestHostAddressesPutTheUplinkFirst(t *testing.T) {
	addrs := func(ss ...string) []netip.Addr {
		out := make([]netip.Addr, 0, len(ss))
		for _, s := range ss {
			out = append(out, netip.MustParseAddr(s))
		}
		return out
	}

	interfaces := []interfaceAddresses{
		{name: "lo", up: true, loopback: true, addresses: addrs("127.0.0.1", "::1")},
		{name: "eth1", up: true, addresses: addrs("fe80::1", "192.168.1.5")},
		{name: "eth0", up: true, addresses: addrs("2001:db8::10", "10.10.0.101", "fe80::2")},
		{name: "eth2", addresses: addrs("192.168.2.5")},
		{name: "dicer-default", up: true, addresses: addrs("172.20.0.1")},
	}

	got := hostAddresses(interfaces, "eth0", map[string]bool{"dicer-default": true})
	want := addrs("10.10.0.101", "2001:db8::10", "192.168.1.5")
	if !slices.Equal(got, want) {
		t.Errorf("hostAddresses = %v, want %v: the uplink's first, IPv4 first, "+
			"and none that is loopback, link-local, down or a bridge's", got, want)
	}

	// Without an uplink, the interfaces' own order.
	got = hostAddresses(interfaces, "", nil)
	want = addrs("192.168.1.5", "10.10.0.101", "2001:db8::10", "172.20.0.1")
	if !slices.Equal(got, want) {
		t.Errorf("hostAddresses with no uplink = %v, want %v", got, want)
	}
}
