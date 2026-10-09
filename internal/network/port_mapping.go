// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package network

import (
	"fmt"
	"net"

	"github.com/konradasb/dicer/internal/errdefs"
)

// The protocols a port mapping may name.
const (
	ProtocolTCP = "tcp"
	ProtocolUDP = "udp"
)

// PortMapping publishes a guest port on the host.
type PortMapping struct {
	// HostIP is the host address to publish on. Empty means every
	// non-loopback address.
	HostIP    string `yaml:"host_ip,omitempty" json:"host_ip,omitempty"`
	HostPort  uint16 `yaml:"host_port" json:"host_port"`
	GuestPort uint16 `yaml:"guest_port" json:"guest_port"`

	// Protocol is tcp or udp. Empty means tcp.
	Protocol string `yaml:"protocol,omitempty" json:"protocol,omitempty"`
}

// EffectiveProtocol returns the mapping's protocol, filling in the default.
func (p PortMapping) EffectiveProtocol() string {
	if p.Protocol == "" {
		return ProtocolTCP
	}

	return p.Protocol
}

// String is the mapping as a person writes it:
// "[hostIP:]hostPort:guestPort/protocol".
func (p PortMapping) String() string {
	s := fmt.Sprintf("%d:%d/%s", p.HostPort, p.GuestPort, p.EffectiveProtocol())
	if p.HostIP != "" {
		s = p.HostIP + ":" + s
	}

	return s
}

// Overlaps reports whether two mappings claim the same host port. A mapping
// on every address overlaps one on any single address.
func (p PortMapping) Overlaps(other PortMapping) bool {
	if p.EffectiveProtocol() != other.EffectiveProtocol() || p.HostPort != other.HostPort {
		return false
	}

	return p.HostIP == "" || other.HostIP == "" || p.HostIP == other.HostIP
}

// Validate returns an invalid argument error if the mapping cannot be
// published.
func (p PortMapping) Validate() error {
	switch {
	case p.HostPort == 0:
		return errdefs.InvalidArgument("invalid port mapping %s: no host port", p)
	case p.GuestPort == 0:
		return errdefs.InvalidArgument("invalid port mapping %s: no guest port", p)
	case p.EffectiveProtocol() != ProtocolTCP && p.EffectiveProtocol() != ProtocolUDP:
		return errdefs.InvalidArgument("invalid port mapping %s: the protocol must be tcp or udp", p)
	}

	if p.HostIP == "" {
		return nil
	}
	ip := net.ParseIP(p.HostIP)
	switch {
	case ip == nil || ip.To4() == nil:
		return errdefs.InvalidArgument("invalid port mapping %s: %q is not an IPv4 address", p, p.HostIP)
	case ip.IsLoopback():
		return errdefs.InvalidArgument("cannot publish port %s on loopback: "+
			"publish it on one of the host's own addresses instead", p)
	}

	return nil
}
