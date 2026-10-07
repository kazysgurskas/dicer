// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"time"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// Networks are the calls about networks, reached as Client.Networks.
type Networks struct {
	api dicerdv1.DaemonServiceClient
}

// Network is a host-local bridge with NAT to the uplink.
type Network struct {
	ID string `json:"id,omitzero"`

	NetworkSpec

	Bridge   string `json:"bridge,omitzero"`
	TotalIPs int64  `json:"total_ips,omitzero"`
	FreeIPs  int64  `json:"free_ips,omitzero"`

	CreateTime time.Time `json:"create_time,omitzero"`
	UpdateTime time.Time `json:"update_time,omitzero"`
}

// NetworkSpec is the definition of a network. Empty fields take the
// daemon's defaults.
type NetworkSpec struct {
	Name    string `json:"name,omitzero"`
	Subnet  string `json:"subnet,omitzero"`
	Gateway string `json:"gateway,omitzero"`
	MTU     int    `json:"mtu,omitzero"`

	// Nameservers are the upstream nameservers the network's instances are
	// given. An internal network takes none.
	Nameservers []string `json:"nameservers,omitzero"`

	// Isolated stops instances on the network reaching each other. Each can
	// still reach the gateway and, through NAT, the outside.
	Isolated bool `json:"isolated,omitzero"`

	// Internal stops instances on the network reaching anything beyond it:
	// the outside, other networks, the host's services and upstream
	// nameservers. They can still be reached through published ports.
	Internal bool `json:"internal,omitzero"`
}

// NetworkAllocation is an address assigned to an instance.
type NetworkAllocation struct {
	InstanceID   string `json:"instance_id,omitzero"`
	InstanceName string `json:"instance_name,omitzero"`
	IP           string `json:"ip,omitzero"`
	MAC          string `json:"mac,omitzero"`
	TapDevice    string `json:"tap_device,omitzero"`
}

// Create defines a network. Its bridge is brought up when the first instance
// on it starts.
func (s *Networks) Create(ctx context.Context, spec NetworkSpec) (Network, error) {
	resp, err := s.api.CreateNetwork(ctx, createNetworkRequest(spec))
	if err != nil {
		return Network{}, fromStatus(err)
	}
	return networkFromProto(resp), nil
}

// List returns every network.
func (s *Networks) List(ctx context.Context) ([]Network, error) {
	resp, err := s.api.ListNetworks(ctx, &dicerdv1.ListNetworksRequest{})
	if err != nil {
		return nil, fromStatus(err)
	}
	return convertAll(resp.GetNetworks(), networkFromProto), nil
}

// Get returns one network, or ErrNotFound.
func (s *Networks) Get(ctx context.Context, name string) (Network, error) {
	resp, err := s.api.GetNetwork(ctx, &dicerdv1.GetNetworkRequest{Name: name})
	if err != nil {
		return Network{}, fromStatus(err)
	}
	return networkFromProto(resp), nil
}

// Delete removes a network that no instance references. The default network
// cannot be deleted.
func (s *Networks) Delete(ctx context.Context, name string) error {
	_, err := s.api.DeleteNetwork(ctx, &dicerdv1.DeleteNetworkRequest{Name: name})
	return fromStatus(err)
}

// Allocations returns the addresses assigned on a network.
func (s *Networks) Allocations(ctx context.Context, name string) ([]NetworkAllocation, error) {
	resp, err := s.api.ListNetworkAllocations(ctx, &dicerdv1.ListNetworkAllocationsRequest{Name: name})
	if err != nil {
		return nil, fromStatus(err)
	}
	return convertAll(resp.GetAllocations(), networkAllocationFromProto), nil
}

// createNetworkRequest returns the request that creates the network spec
// defines.
func createNetworkRequest(spec NetworkSpec) *dicerdv1.CreateNetworkRequest {
	return &dicerdv1.CreateNetworkRequest{
		Name:        spec.Name,
		Subnet:      spec.Subnet,
		Gateway:     spec.Gateway,
		Mtu:         int32(spec.MTU),
		Nameservers: spec.Nameservers,
		Isolated:    spec.Isolated,
		Internal:    spec.Internal,
	}
}

// networkFromProto returns the network p describes.
func networkFromProto(p *dicerdv1.Network) Network {
	return Network{
		ID: p.GetId(),
		NetworkSpec: NetworkSpec{
			Name:        p.GetName(),
			Subnet:      p.GetSubnet(),
			Gateway:     p.GetGateway(),
			MTU:         int(p.GetMtu()),
			Nameservers: p.GetNameservers(),
			Isolated:    p.GetIsolated(),
			Internal:    p.GetInternal(),
		},
		Bridge:     p.GetBridge(),
		TotalIPs:   p.GetTotalIps(),
		FreeIPs:    p.GetFreeIps(),
		CreateTime: timeFromProto(p.GetCreateTime()),
		UpdateTime: timeFromProto(p.GetUpdateTime()),
	}
}

// networkAllocationFromProto returns the allocation p describes.
func networkAllocationFromProto(p *dicerdv1.NetworkAllocation) NetworkAllocation {
	return NetworkAllocation{
		InstanceID:   p.GetInstanceId(),
		InstanceName: p.GetInstanceName(),
		IP:           p.GetIp(),
		MAC:          p.GetMac(),
		TapDevice:    p.GetTapDevice(),
	}
}
