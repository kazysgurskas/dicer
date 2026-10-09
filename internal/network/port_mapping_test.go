// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package network

import (
	"errors"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
)

func TestPortMappingOverlaps(t *testing.T) {
	tests := []struct {
		name string
		a, b PortMapping
		want bool
	}{
		{"same port", PortMapping{HostPort: 80}, PortMapping{HostPort: 80}, true},
		{"tcp by default", PortMapping{HostPort: 80}, PortMapping{HostPort: 80, Protocol: "tcp"}, true},
		{"other protocol", PortMapping{HostPort: 80}, PortMapping{HostPort: 80, Protocol: "udp"}, false},
		{"other port", PortMapping{HostPort: 80}, PortMapping{HostPort: 81}, false},
		{"every address and one", PortMapping{HostPort: 80}, PortMapping{HostIP: "10.0.0.1", HostPort: 80}, true},
		{
			"same address",
			PortMapping{HostIP: "10.0.0.1", HostPort: 80},
			PortMapping{HostIP: "10.0.0.1", HostPort: 80},
			true,
		},
		{
			"other address",
			PortMapping{HostIP: "10.0.0.1", HostPort: 80},
			PortMapping{HostIP: "10.0.0.2", HostPort: 80},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Overlaps(tt.b); got != tt.want {
				t.Errorf("%s overlaps %s = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			if got := tt.b.Overlaps(tt.a); got != tt.want {
				t.Errorf("%s overlaps %s = %v, want %v", tt.b, tt.a, got, tt.want)
			}
		})
	}
}

func TestPortMappingValidate(t *testing.T) {
	tests := []struct {
		name    string
		mapping PortMapping
		valid   bool
	}{
		{"tcp by default", PortMapping{HostPort: 8080, GuestPort: 80}, true},
		{"udp", PortMapping{HostPort: 8080, GuestPort: 80, Protocol: "udp"}, true},
		{"host IP", PortMapping{HostIP: "10.0.0.1", HostPort: 443, GuestPort: 443}, true},
		{"no host port", PortMapping{GuestPort: 80}, false},
		{"no guest port", PortMapping{HostPort: 80}, false},
		{"protocol", PortMapping{HostPort: 80, GuestPort: 80, Protocol: "sctp"}, false},
		{"invalid host IP", PortMapping{HostIP: "not-an-ip", HostPort: 80, GuestPort: 80}, false},
		{"IPv6 host IP", PortMapping{HostIP: "::1", HostPort: 80, GuestPort: 80}, false},
		{"loopback", PortMapping{HostIP: "127.0.0.1", HostPort: 80, GuestPort: 80}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mapping.Validate()
			switch {
			case tt.valid && err != nil:
				t.Errorf("%s: Validate() = %v, want nil", tt.mapping, err)
			case !tt.valid && !errors.Is(err, errdefs.ErrInvalidArgument):
				t.Errorf("%s: Validate() = %v, want an invalid argument error", tt.mapping, err)
			}
		})
	}
}
