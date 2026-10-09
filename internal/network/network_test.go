// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package network

import (
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
)

func TestNetworkValidate(t *testing.T) {
	valid := map[string]Network{
		"plain":                        {Name: "lan", MTU: 1500},
		"nameservers":                  {Name: "lan", MTU: 1500, Nameservers: []string{"8.8.8.8"}},
		"internal without nameservers": {Name: "lan", MTU: 1500, Internal: true},
	}
	for name, n := range valid {
		t.Run(name, func(t *testing.T) {
			if err := n.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}

	invalid := map[string]Network{
		"bad name":                  {Name: "Not Valid", MTU: 1500},
		"MTU too small":             {Name: "lan", MTU: 100},
		"MTU too large":             {Name: "lan", MTU: 10000},
		"bad nameserver":            {Name: "lan", MTU: 1500, Nameservers: []string{"dns.example"}},
		"internal with nameservers": {Name: "lan", MTU: 1500, Internal: true, Nameservers: []string{"8.8.8.8"}},
	}
	for name, n := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := n.Validate(); !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Validate() = %v, want an invalid argument error", err)
			}
		})
	}
}

func TestNewFillsInDefaults(t *testing.T) {
	tests := []struct {
		name            string
		spec            Spec
		wantGateway     string
		wantNameservers []string
	}{
		{"plain", Spec{Name: "lan", Subnet: "10.9.0.77/24"}, "10.9.0.1", []string{DefaultNameserver}},
		{"internal", Spec{Name: "lan", Subnet: "10.9.0.0/24", Internal: true}, "10.9.0.1", nil},
		{"own gateway", Spec{Name: "lan", Subnet: "10.9.0.0/24", Gateway: "10.9.0.254"}, "10.9.0.254", []string{DefaultNameserver}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := New(tt.spec)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if n.Subnet != "10.9.0.0/24" || n.Gateway != tt.wantGateway || n.MTU != DefaultMTU ||
				!slices.Equal(n.Nameservers, tt.wantNameservers) || n.Bridge != BridgeName("lan") || n.ID == "" {
				t.Errorf("New = %+v, want subnet 10.9.0.0/24, gateway %s, MTU %d, nameservers %v",
					n, tt.wantGateway, DefaultMTU, tt.wantNameservers)
			}
		})
	}
}

func TestNewRefusesWhatCannotWork(t *testing.T) {
	tests := map[string]Spec{
		"bad name":             {Name: "Not Valid", Subnet: "10.9.0.0/24"},
		"no subnet":            {Name: "lan"},
		"gateway outside":      {Name: "lan", Subnet: "10.9.0.0/24", Gateway: "10.8.0.1"},
		"MTU too small":        {Name: "lan", Subnet: "10.9.0.0/24", MTU: 100},
		"bad nameserver":       {Name: "lan", Subnet: "10.9.0.0/24", Nameservers: []string{"dns.example"}},
		"internal nameservers": {Name: "lan", Subnet: "10.9.0.0/24", Internal: true, Nameservers: []string{"1.1.1.1"}},
	}
	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := New(spec); !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("New = %v, want an invalid argument error", err)
			}
		})
	}
}

func TestOverlappingSubnetsAreRefused(t *testing.T) {
	networks := []Network{{Name: "lan", Subnet: "10.9.0.0/24"}}
	hostSubnets := []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}

	tests := []struct {
		subnet  string
		overlap bool
	}{
		{"10.9.0.0/16", true},
		{"10.9.0.128/25", true},
		{"192.168.0.0/16", true},
		{"10.10.0.0/24", false},
	}
	for _, tt := range tests {
		t.Run(tt.subnet, func(t *testing.T) {
			err := CheckSubnetOverlap(Network{Subnet: tt.subnet}, networks, hostSubnets)
			if got := errors.Is(err, errdefs.ErrExists); got != tt.overlap {
				t.Errorf("CheckSubnetOverlap = %v, want an overlap %v", err, tt.overlap)
			}
		})
	}
}
