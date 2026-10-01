// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package compose

import (
	"errors"
	"net/netip"
)

// SubnetPool is where a project network's subnet is picked from when the
// file gives none: one /24 of it per network.
var SubnetPool = netip.MustParsePrefix("10.213.0.0/16")

// subnetBits is the size of a picked subnet: 254 addresses.
const subnetBits = 24

// FreeSubnet returns the first subnet in SubnetPool that overlaps none of
// taken, the subnets of the networks there are already.
func FreeSubnet(taken []string) (string, error) {
	var used []netip.Prefix
	for _, s := range taken {
		if p, err := netip.ParsePrefix(s); err == nil {
			used = append(used, p.Masked())
		}
	}

	step := 1 << (32 - subnetBits)
	first := SubnetPool.Addr().As4()
	base := uint32(first[0])<<24 | uint32(first[1])<<16 | uint32(first[2])<<8 | uint32(first[3])
	count := 1 << (subnetBits - SubnetPool.Bits())

	for i := range count {
		n := base + uint32(i*step)
		candidate := netip.PrefixFrom(netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}), subnetBits)
		free := true
		for _, u := range used {
			if u.Overlaps(candidate) {
				free = false
				break
			}
		}
		if free {
			return candidate.String(), nil
		}
	}
	return "", errors.New("no free subnet left in " + SubnetPool.String() + ": give the network a subnet")
}
