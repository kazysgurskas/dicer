// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcserver

import (
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/network"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

func TestCreateNetworkNormalises(t *testing.T) {
	s, _ := newTestServer(t)

	n, err := s.CreateNetwork(t.Context(), &dicerdv1.CreateNetworkRequest{Name: "lan", Subnet: "10.9.0.77/24"})
	if err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}
	if n.GetSubnet() != "10.9.0.0/24" || n.GetGateway() != "10.9.0.1" {
		t.Errorf("subnet %s, gateway %s; want 10.9.0.0/24 and 10.9.0.1", n.GetSubnet(), n.GetGateway())
	}
}

func TestCreateNetworkRefusesWhatCannotWork(t *testing.T) {
	tests := []struct {
		name string
		req  *dicerdv1.CreateNetworkRequest
	}{
		{"IPv6", &dicerdv1.CreateNetworkRequest{Subnet: "fd00::/64"}},
		{"no room for an instance", &dicerdv1.CreateNetworkRequest{Subnet: "10.0.0.0/31"}},
		{"gateway outside", &dicerdv1.CreateNetworkRequest{Subnet: "10.0.0.0/24", Gateway: "10.0.1.1"}},
		{"gateway is the network address", &dicerdv1.CreateNetworkRequest{Subnet: "10.0.0.0/24", Gateway: "10.0.0.0"}},
		{"gateway is the broadcast address", &dicerdv1.CreateNetworkRequest{Subnet: "10.0.0.0/24", Gateway: "10.0.0.255"}},
		{"MTU too small", &dicerdv1.CreateNetworkRequest{Subnet: "10.0.0.0/24", Mtu: 100}},
		{"MTU negative", &dicerdv1.CreateNetworkRequest{Subnet: "10.0.0.0/24", Mtu: -1}},
		{"nameserver not an address", &dicerdv1.CreateNetworkRequest{Subnet: "10.0.0.0/24", Nameservers: []string{"dns"}}},
		{"internal with nameservers", &dicerdv1.CreateNetworkRequest{
			Subnet: "10.0.0.0/24", Internal: true, Nameservers: []string{"8.8.8.8"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestServer(t)
			tt.req.Name = "lan"

			_, err := s.CreateNetwork(t.Context(), tt.req)
			wantClass(t, err, errdefs.ErrInvalidArgument)
		})
	}
}

// An internal network asks no upstream nameservers, so it is given none.
func TestInternalNetworkHasNoNameservers(t *testing.T) {
	s, _ := newTestServer(t)

	n, err := s.CreateNetwork(t.Context(), &dicerdv1.CreateNetworkRequest{
		Name: "sandbox", Subnet: "10.9.0.0/24", Internal: true,
	})
	if err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}
	if !n.GetInternal() || len(n.GetNameservers()) != 0 {
		t.Errorf("internal %v, nameservers %v; want internal and none", n.GetInternal(), n.GetNameservers())
	}
}

// A subnet another network is on is refused as taken, which dicer compose up
// tells apart from a request it got wrong.
func TestCreateNetworkRefusesATakenSubnet(t *testing.T) {
	s, _ := newTestServer(t)
	if _, err := s.CreateNetwork(t.Context(), &dicerdv1.CreateNetworkRequest{Name: "lan", Subnet: "10.9.0.0/24"}); err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}

	_, err := s.CreateNetwork(t.Context(), &dicerdv1.CreateNetworkRequest{Name: "other", Subnet: "10.9.0.0/16"})
	wantClass(t, err, errdefs.ErrExists)
}

func TestDefaultNetworkCannotBeDeleted(t *testing.T) {
	s, _ := newTestServer(t)

	_, err := s.DeleteNetwork(t.Context(), &dicerdv1.DeleteNetworkRequest{Name: network.DefaultName})
	wantClass(t, err, errdefs.ErrInvalidArgument)
}
