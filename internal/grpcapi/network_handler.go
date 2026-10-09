// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/network"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// networkHandler handles network-related RPCs.
type networkHandler struct {
	networkManager  *network.Manager
	instanceManager *instance.Manager
}

// CreateNetwork records a network, refusing a subnet another network or
// the host is on. Unset gateway, MTU and nameservers take their defaults.
func (h *networkHandler) CreateNetwork(
	_ context.Context, req *dicerdv1.CreateNetworkRequest,
) (*dicerdv1.Network, error) {
	n, err := h.networkManager.Create(network.Spec{
		Name:        req.GetName(),
		Subnet:      req.GetSubnet(),
		Gateway:     req.GetGateway(),
		MTU:         int(req.GetMtu()),
		Nameservers: req.GetNameservers(),
		Isolated:    req.GetIsolated(),
		Internal:    req.GetInternal(),
	})
	if err != nil {
		return nil, err
	}
	return networkToProto(n, 0), nil
}

// ListNetworks lists the networks, sorted by name, with their address usage.
func (h *networkHandler) ListNetworks(
	_ context.Context, _ *dicerdv1.ListNetworksRequest,
) (*dicerdv1.ListNetworksResponse, error) {
	networks := h.networkManager.Networks()

	resp := &dicerdv1.ListNetworksResponse{
		Networks: make([]*dicerdv1.Network, 0, len(networks)),
	}
	for _, n := range networks {
		allocations, err := h.networkManager.List(n.Name)
		if err != nil {
			return nil, err
		}
		resp.Networks = append(resp.Networks, networkToProto(n, len(allocations)))
	}

	return resp, nil
}

// GetNetwork returns a network with its address usage.
func (h *networkHandler) GetNetwork(
	_ context.Context, req *dicerdv1.GetNetworkRequest,
) (*dicerdv1.Network, error) {
	n, err := h.networkManager.Network(req.GetName())
	if err != nil {
		return nil, err
	}
	allocations, err := h.networkManager.List(n.Name)
	if err != nil {
		return nil, err
	}
	return networkToProto(n, len(allocations)), nil
}

// DeleteNetwork removes a network and its allocations, refusing the default
// network and one an instance or snapshot is on.
func (h *networkHandler) DeleteNetwork(
	_ context.Context, req *dicerdv1.DeleteNetworkRequest,
) (*emptypb.Empty, error) {
	if err := h.networkManager.Delete(req.GetName()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// ListNetworkAllocations lists the addresses a network has allocated.
func (h *networkHandler) ListNetworkAllocations(
	_ context.Context, req *dicerdv1.ListNetworkAllocationsRequest,
) (*dicerdv1.ListNetworkAllocationsResponse, error) {
	allocations, err := h.networkManager.List(req.GetName())
	if err != nil {
		return nil, err
	}

	instances := h.instanceManager.Instances()
	nameByID := make(map[string]string, len(instances))
	for _, instance := range instances {
		nameByID[instance.ID] = instance.Name
	}

	resp := &dicerdv1.ListNetworkAllocationsResponse{
		Allocations: make([]*dicerdv1.NetworkAllocation, 0, len(allocations)),
	}
	for _, a := range allocations {
		resp.Allocations = append(resp.Allocations, allocationToProto(a, nameByID[a.InstanceID]))
	}

	return resp, nil
}
