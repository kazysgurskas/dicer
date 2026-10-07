// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"time"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// Snapshots are the calls about snapshots, reached as Client.Snapshots. A
// call that takes a snapshot's name takes its ID as well.
type Snapshots struct {
	api dicerdv1.DaemonServiceClient
}

// Snapshot is an instance frozen to disk. It outlives the instance it was
// taken of, and never holds the instance's volumes.
type Snapshot struct {
	ID   string       `json:"id,omitzero"`
	Name string       `json:"name,omitzero"`
	Kind SnapshotKind `json:"kind,omitzero"`

	// InstanceID and InstanceName are the instance it was taken of: its
	// name now, or, if it has been deleted, its name then.
	InstanceID   string `json:"instance_id,omitzero"`
	InstanceName string `json:"instance_name,omitzero"`

	// HypervisorType and HypervisorVersion are what took a memory snapshot.
	// Only the same version can restore it. They are empty for a disk
	// snapshot.
	HypervisorType    HypervisorType `json:"hypervisor_type,omitzero"`
	HypervisorVersion string         `json:"hypervisor_version,omitzero"`

	// VCPUs and MemoryBytes are what a memory snapshot's guest had. They
	// are zero for a disk snapshot.
	VCPUs       int   `json:"vcpus,omitzero"`
	MemoryBytes int64 `json:"memory_bytes,omitzero"`

	// SizeBytes is what the snapshot occupies on disk, which on a
	// copy-on-write filesystem may be far less than the guest's memory and
	// disk together.
	SizeBytes int64 `json:"size_bytes,omitzero"`

	CreateTime time.Time `json:"create_time,omitzero"`
}

// SnapshotKind is what a snapshot holds.
type SnapshotKind string

// The snapshot kinds.
const (
	// SnapshotKindMemory is a running or paused guest's memory and device
	// state, with its overlay disk as it was at the same moment. Restoring
	// it resumes the guest.
	SnapshotKindMemory SnapshotKind = "memory"

	// SnapshotKindDisk is a stopped instance's overlay disk alone. Restoring
	// it rolls the disk back, and the guest boots from it afresh.
	SnapshotKindDisk SnapshotKind = "disk"
)

var snapshotKinds = enum[SnapshotKind, dicerdv1.SnapshotKind]{"snapshot kind", map[SnapshotKind]dicerdv1.SnapshotKind{
	SnapshotKindMemory: dicerdv1.SnapshotKind_SNAPSHOT_KIND_MEMORY,
	SnapshotKindDisk:   dicerdv1.SnapshotKind_SNAPSHOT_KIND_DISK,
}}

// Create freezes an instance to disk. Of a running or paused instance it
// takes a memory snapshot, pausing a running one for as long as it takes. Of
// a stopped one it takes a disk snapshot. An empty name is the instance's
// and the time's, as in web-20260102t150405z.
func (s *Snapshots) Create(ctx context.Context, instance, name string) (Snapshot, error) {
	resp, err := s.api.CreateSnapshot(ctx, &dicerdv1.CreateSnapshotRequest{Instance: instance, Name: name})
	if err != nil {
		return Snapshot{}, fromStatus(err)
	}
	return snapshotFromProto(resp), nil
}

// List returns the snapshots taken of an instance, oldest first, or every
// snapshot if instance is empty.
func (s *Snapshots) List(ctx context.Context, instance string) ([]Snapshot, error) {
	resp, err := s.api.ListSnapshots(ctx, &dicerdv1.ListSnapshotsRequest{Instance: instance})
	if err != nil {
		return nil, fromStatus(err)
	}
	return convertAll(resp.GetSnapshots(), snapshotFromProto), nil
}

// Get returns one snapshot, or ErrNotFound.
func (s *Snapshots) Get(ctx context.Context, name string) (Snapshot, error) {
	resp, err := s.api.GetSnapshot(ctx, &dicerdv1.GetSnapshotRequest{Name: name})
	if err != nil {
		return Snapshot{}, fromStatus(err)
	}
	return snapshotFromProto(resp), nil
}

// Delete removes a snapshot.
func (s *Snapshots) Delete(ctx context.Context, name string) error {
	_, err := s.api.DeleteSnapshot(ctx, &dicerdv1.DeleteSnapshotRequest{Name: name})
	return fromStatus(err)
}

// Restore puts the instance a snapshot was taken of, which must be stopped,
// back as it was then, discarding what it has written to its disk since. A
// memory snapshot resumes the guest where it was; a disk snapshot leaves
// the instance stopped.
func (s *Snapshots) Restore(ctx context.Context, name string) (Instance, error) {
	return instanceOf(s.api.RestoreSnapshot(ctx, &dicerdv1.RestoreSnapshotRequest{Name: name}))
}

// Fork creates an instance as a copy of the one a snapshot was taken of,
// with its definition but the identity opts gives it. A memory snapshot's
// fork runs, resumed where the snapshot's guest was; a disk snapshot's fork
// is stopped. A fork that fails leaves no instance behind.
func (s *Snapshots) Fork(ctx context.Context, name string, opts ForkOptions) (Instance, error) {
	req, err := forkSnapshotRequest(name, opts)
	if err != nil {
		return Instance{}, err
	}
	return instanceOf(s.api.ForkSnapshot(ctx, req))
}

// forkSnapshotRequest returns the request that forks the snapshot name.
func forkSnapshotRequest(name string, opts ForkOptions) (*dicerdv1.ForkSnapshotRequest, error) {
	ports, err := portMappingsToProto(opts.Ports)
	if err != nil {
		return nil, err
	}

	return &dicerdv1.ForkSnapshotRequest{
		Name:        name,
		ForkName:    opts.Name,
		NetworkName: opts.NetworkName,
		StaticIp:    opts.StaticIP,
		Ports:       ports,
	}, nil
}

// snapshotFromProto returns the snapshot p describes.
func snapshotFromProto(p *dicerdv1.Snapshot) Snapshot {
	return Snapshot{
		ID:                p.GetId(),
		Name:              p.GetName(),
		Kind:              snapshotKinds.fromProto(p.GetKind()),
		InstanceID:        p.GetInstanceId(),
		InstanceName:      p.GetInstanceName(),
		HypervisorType:    hypervisorTypes.fromProto(p.GetHypervisorType()),
		HypervisorVersion: p.GetHypervisorVersion(),
		VCPUs:             int(p.GetVcpus()),
		MemoryBytes:       p.GetMemoryBytes(),
		SizeBytes:         p.GetSizeBytes(),
		CreateTime:        timeFromProto(p.GetCreateTime()),
	}
}
