// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// HostInfo is what a daemon is: its version, the hypervisors it carries, and
// how it is reached.
type HostInfo struct {
	// Version is the daemon's version.
	Version string `json:"version,omitzero"`

	// Hostname is the host's name.
	Hostname string `json:"hostname,omitzero"`

	// Hypervisors are the hypervisors the daemon can start instances with.
	Hypervisors []HypervisorInfo `json:"hypervisors,omitzero"`

	// ListenerAddresses are the addresses clients reach the daemon's TCP
	// listener at. For a listener on all of the host's addresses, they are
	// the host's own, with the default route's first. It is empty if the API
	// is not served over TCP.
	ListenerAddresses []string `json:"listener_addresses,omitzero"`

	// Fingerprint is the fingerprint of the certificate the API is served
	// with over TCP: the SHA-256 of its public key, hex-encoded. It is empty
	// if the API is not served over TCP.
	Fingerprint string `json:"fingerprint,omitzero"`

	// Token is the name of the token the call was made with. It is empty
	// over the socket.
	Token string `json:"token,omitzero"`
}

// HypervisorInfo is a hypervisor the daemon carries, and the versions of it
// an instance may name.
type HypervisorInfo struct {
	// Type is the hypervisor.
	Type HypervisorType `json:"type,omitzero"`

	// Versions are the versions available, the one an instance gets by
	// default first.
	Versions []string `json:"versions,omitzero"`

	// IsDefault reports whether this is the hypervisor an instance gets when
	// it names none.
	IsDefault bool `json:"is_default,omitzero"`

	// DeprecatedVersions are the versions that are deprecated: every one but
	// the default. Each is kept only for what still uses it, and a later
	// release of Dicer removes it.
	DeprecatedVersions []string `json:"deprecated_versions,omitzero"`
}

// Resources are how much CPU and memory instances may be given, how much is
// committed to them, and how full the data directory's disk is.
type Resources struct {
	// CPU is how the host's CPUs are shared out, counted in vCPUs.
	CPU ResourceCapacity `json:"cpu,omitzero"`

	// Memory is how the host's memory is shared out, in bytes.
	Memory ResourceCapacity `json:"memory,omitzero"`

	// Disk is the filesystem holding the data directory. Its use is
	// reported, not enforced.
	Disk DiskUsage `json:"disk,omitzero"`

	// Instances are the instances CPU and memory are committed to: those
	// starting, running or paused, in name order.
	Instances []InstanceResources `json:"instances,omitzero"`
}

// ResourceCapacity is how much of one resource instances may be given, and
// how much is committed to them. A start that would take Allocated past
// Allocatable is refused.
type ResourceCapacity struct {
	// Host is what the host has: its logical CPUs, or its memory.
	Host int64 `json:"host,omitzero"`

	// Reserved is what is kept back from instances for the host itself.
	Reserved int64 `json:"reserved,omitzero"`

	// Overcommit is how far what remains is stretched: 4 lets four vCPUs
	// share each CPU.
	Overcommit float64 `json:"overcommit,omitzero"`

	// Allocatable is what instances may be given in total:
	// (Host - Reserved) * Overcommit.
	Allocatable int64 `json:"allocatable,omitzero"`

	// Allocated is what is committed to instances.
	Allocated int64 `json:"allocated,omitzero"`

	// Available is what is left for further instances: Allocatable -
	// Allocated, or 0.
	Available int64 `json:"available,omitzero"`
}

// DiskUsage is how full the filesystem holding the data directory is, and
// how much has been promised on it. Disks are sparse, so ProvisionedBytes
// may be far more than TotalBytes without anything being wrong, until the
// guests fill them.
type DiskUsage struct {
	// Path is the data directory.
	Path string `json:"path,omitzero"`

	// TotalBytes is the filesystem's size.
	TotalBytes int64 `json:"total_bytes,omitzero"`

	// FreeBytes is what can still be written.
	FreeBytes int64 `json:"free_bytes,omitzero"`

	// ProvisionedBytes is the sizes of every instance's overlay disk and
	// every volume, added up.
	ProvisionedBytes int64 `json:"provisioned_bytes,omitzero"`
}

// InstanceResources are what is committed to one instance.
type InstanceResources struct {
	// Name is the instance's name.
	Name string `json:"name,omitzero"`

	// State is the instance's state: starting, running or paused.
	State InstanceState `json:"state,omitzero"`

	// VCPUs is how many vCPUs are committed to the instance.
	VCPUs int `json:"vcpus,omitzero"`

	// MemoryBytes is how much memory is committed to the instance.
	MemoryBytes int64 `json:"memory_bytes,omitzero"`
}

// HostInfo returns what the daemon is.
func (c *Client) HostInfo(ctx context.Context) (HostInfo, error) {
	resp, err := c.api.GetHostInfo(ctx, &dicerdv1.GetHostInfoRequest{})
	if err != nil {
		return HostInfo{}, fromStatus(err)
	}
	return hostInfoFromProto(resp), nil
}

// Resources returns how much of the host instances may be given, and how
// much is committed to them.
func (c *Client) Resources(ctx context.Context) (Resources, error) {
	resp, err := c.api.GetResources(ctx, &dicerdv1.GetResourcesRequest{})
	if err != nil {
		return Resources{}, fromStatus(err)
	}
	return resourcesFromProto(resp), nil
}

// hostInfoFromProto returns what p says the daemon is.
func hostInfoFromProto(p *dicerdv1.GetHostInfoResponse) HostInfo {
	return HostInfo{
		Version:           p.GetVersion(),
		Hostname:          p.GetHostname(),
		Hypervisors:       convertAll(p.GetHypervisors(), hypervisorInfoFromProto),
		ListenerAddresses: p.GetListenerAddresses(),
		Fingerprint:       p.GetFingerprint(),
		Token:             p.GetToken(),
	}
}

// hypervisorInfoFromProto returns the hypervisor p describes.
func hypervisorInfoFromProto(p *dicerdv1.HypervisorInfo) HypervisorInfo {
	return HypervisorInfo{
		Type:               hypervisorTypes.fromProto(p.GetType()),
		Versions:           p.GetVersions(),
		IsDefault:          p.GetIsDefault(),
		DeprecatedVersions: p.GetDeprecatedVersions(),
	}
}

// resourcesFromProto returns the resources p reports.
func resourcesFromProto(p *dicerdv1.GetResourcesResponse) Resources {
	return Resources{
		CPU:       resourceCapacityFromProto(p.GetCpu()),
		Memory:    resourceCapacityFromProto(p.GetMemory()),
		Disk:      diskUsageFromProto(p.GetDisk()),
		Instances: convertAll(p.GetInstances(), instanceResourcesFromProto),
	}
}

// diskUsageFromProto returns the disk usage p reports.
func diskUsageFromProto(p *dicerdv1.DiskUsage) DiskUsage {
	return DiskUsage{
		Path:             p.GetPath(),
		TotalBytes:       p.GetTotalBytes(),
		FreeBytes:        p.GetFreeBytes(),
		ProvisionedBytes: p.GetProvisionedBytes(),
	}
}

// resourceCapacityFromProto returns the capacity p reports.
func resourceCapacityFromProto(p *dicerdv1.ResourceCapacity) ResourceCapacity {
	return ResourceCapacity{
		Host:        p.GetHost(),
		Reserved:    p.GetReserved(),
		Overcommit:  p.GetOvercommit(),
		Allocatable: p.GetAllocatable(),
		Allocated:   p.GetAllocated(),
		Available:   p.GetAvailable(),
	}
}

// instanceResourcesFromProto returns what p says is committed to an
// instance.
func instanceResourcesFromProto(p *dicerdv1.InstanceResources) InstanceResources {
	return InstanceResources{
		Name:        p.GetName(),
		State:       instanceStates.fromProto(p.GetState()),
		VCPUs:       int(p.GetVcpus()),
		MemoryBytes: p.GetMemoryBytes(),
	}
}
