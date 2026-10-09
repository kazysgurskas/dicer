// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"fmt"
	"net/netip"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/events"
	"github.com/konradasb/dicer/internal/filestore"
	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/network"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// networkHandler handles network-related RPCs.
type networkHandler struct {
	definitions *filestore.Manager
	networks    *network.Manager
	hostSubnets func() ([]netip.Prefix, error)
	events      recorder
}

// CreateNetwork records a network, refusing a subnet another network or
// the host is on. Unset gateway, MTU and nameservers take their defaults.
func (h *networkHandler) CreateNetwork(
	_ context.Context, req *dicerdv1.CreateNetworkRequest,
) (*dicerdv1.Network, error) {
	n, err := network.New(network.Spec{
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
	if _, err := h.definitions.Network(n.Name); err == nil {
		return nil, errdefs.Exists("network %q already exists", n.Name)
	}

	var hostSubnets []netip.Prefix
	if h.hostSubnets != nil {
		if hostSubnets, err = h.hostSubnets(); err != nil {
			return nil, fmt.Errorf("list the host's subnets: %w", err)
		}
	}
	if err := network.CheckSubnetOverlap(n, h.definitions.Networks(), hostSubnets); err != nil {
		return nil, err
	}

	if err := h.definitions.CreateNetwork(n); err != nil {
		return nil, err
	}
	message := fmt.Sprintf("Created network with subnet %s, gateway %s", n.Subnet, n.Gateway)
	if n.Isolated {
		message += "; isolated: its instances cannot reach each other"
	}
	if n.Internal {
		message += "; internal: its instances cannot reach the host or beyond it"
	}
	h.record(n, events.ActionCreated, message)

	return networkToProto(n, 0), nil
}

// ListNetworks lists the networks with their address usage, sorted by name.
func (h *networkHandler) ListNetworks(
	_ context.Context, _ *dicerdv1.ListNetworksRequest,
) (*dicerdv1.ListNetworksResponse, error) {
	networks := h.definitions.Networks()

	resp := &dicerdv1.ListNetworksResponse{
		Networks: make([]*dicerdv1.Network, 0, len(networks)),
	}
	for _, n := range networks {
		allocations, err := h.networks.List(n.Name)
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
	n, err := h.definitions.Network(req.GetName())
	if err != nil {
		return nil, err
	}

	allocations, err := h.networks.List(n.Name)
	if err != nil {
		return nil, err
	}

	return networkToProto(n, len(allocations)), nil
}

// DeleteNetwork removes a network and its allocations, refusing the default
// network and one an instance is on.
func (h *networkHandler) DeleteNetwork(
	_ context.Context, req *dicerdv1.DeleteNetworkRequest,
) (*emptypb.Empty, error) {
	n, err := h.definitions.Network(req.GetName())
	if err != nil {
		return nil, err
	}
	if n.Name == network.DefaultName {
		return nil, errdefs.InvalidArgument("the default network cannot be deleted")
	}

	inUse := func(instance instance.Spec) bool { return instance.NetworkName == n.Name }
	if err := refuseInUse(h.definitions, fmt.Sprintf("network %q is in use", n.Name), inUse); err != nil {
		return nil, err
	}

	if err := h.definitions.DeleteNetwork(n.Name); err != nil {
		return nil, err
	}
	h.record(n, events.ActionDeleted, "Deleted network with subnet "+n.Subnet)

	if err := h.networks.Forget(n.Name); err != nil {
		return nil, fmt.Errorf("discard allocations: %w", err)
	}

	return &emptypb.Empty{}, nil
}

// record records that action happened to n, with its subnet and gateway
// among the attributes.
func (h *networkHandler) record(n network.Network, action events.Action, message string) {
	h.events.Record(events.Event{
		Kind:       events.KindNetwork,
		ID:         n.ID,
		Name:       n.Name,
		Action:     action,
		Message:    message,
		Attributes: map[string]string{"subnet": n.Subnet, "gateway": n.Gateway},
	})
}

// ListNetworkAllocations lists the addresses a network has allocated.
func (h *networkHandler) ListNetworkAllocations(
	_ context.Context, req *dicerdv1.ListNetworkAllocationsRequest,
) (*dicerdv1.ListNetworkAllocationsResponse, error) {
	n, err := h.definitions.Network(req.GetName())
	if err != nil {
		return nil, err
	}

	allocations, err := h.networks.List(n.Name)
	if err != nil {
		return nil, err
	}

	instances := h.definitions.Instances()
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
