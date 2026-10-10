// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcserver

import (
	"context"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/instance"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// snapshotHandler handles snapshot-related RPCs.
type snapshotHandler struct {
	instanceManager *instance.Manager
}

// CreateSnapshot snapshots an instance.
func (h *snapshotHandler) CreateSnapshot(
	ctx context.Context, req *dicerdv1.CreateSnapshotRequest,
) (*dicerdv1.Snapshot, error) {
	if req.GetInstance() == "" {
		return nil, errdefs.InvalidArgument("instance is required")
	}
	snapshot, err := h.instanceManager.CreateSnapshot(ctx, req.GetInstance(), req.GetName())
	if err != nil {
		return nil, err
	}

	return snapshotToProto(snapshot, h.instanceName(snapshot)), nil
}

// ListSnapshots lists the snapshots, or those of one instance.
func (h *snapshotHandler) ListSnapshots(
	_ context.Context, req *dicerdv1.ListSnapshotsRequest,
) (*dicerdv1.ListSnapshotsResponse, error) {
	var instanceID string
	if req.GetInstance() != "" {
		instance, err := h.instanceManager.Instance(req.GetInstance())
		if err != nil {
			return nil, err
		}
		instanceID = instance.ID
	}

	resp := &dicerdv1.ListSnapshotsResponse{}
	for _, snapshot := range h.instanceManager.Snapshots() {
		if instanceID == "" || snapshot.Instance.ID == instanceID {
			resp.Snapshots = append(resp.Snapshots, snapshotToProto(snapshot, h.instanceName(snapshot)))
		}
	}

	return resp, nil
}

// GetSnapshot returns a snapshot.
func (h *snapshotHandler) GetSnapshot(
	_ context.Context, req *dicerdv1.GetSnapshotRequest,
) (*dicerdv1.Snapshot, error) {
	snapshot, err := h.snapshot(req.GetName())
	if err != nil {
		return nil, err
	}

	return snapshotToProto(snapshot, h.instanceName(snapshot)), nil
}

// DeleteSnapshot removes a snapshot.
func (h *snapshotHandler) DeleteSnapshot(
	ctx context.Context, req *dicerdv1.DeleteSnapshotRequest,
) (*emptypb.Empty, error) {
	snapshot, err := h.snapshot(req.GetName())
	if err != nil {
		return nil, err
	}

	if err := h.instanceManager.DeleteSnapshot(ctx, snapshot.ID); err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

// RestoreSnapshot puts the instance a snapshot was taken of back as it was.
func (h *snapshotHandler) RestoreSnapshot(
	ctx context.Context, req *dicerdv1.RestoreSnapshotRequest,
) (*dicerdv1.Instance, error) {
	snapshot, err := h.snapshot(req.GetName())
	if err != nil {
		return nil, err
	}

	instance, err := h.instanceManager.RestoreSnapshot(ctx, snapshot.ID)
	if err != nil {
		return nil, err
	}

	return viewInstance(h.instanceManager, instance)
}

// ForkSnapshot creates an instance as a copy of the one a snapshot was taken
// of.
func (h *snapshotHandler) ForkSnapshot(
	ctx context.Context, req *dicerdv1.ForkSnapshotRequest,
) (*dicerdv1.Instance, error) {
	snapshot, err := h.snapshot(req.GetName())
	if err != nil {
		return nil, err
	}
	name := req.GetForkName()
	if name == "" {
		name = generateInstanceName(h.instanceManager, snapshot.Name)
	}
	fork, err := forkDefinition(snapshot.Instance, name, req)
	if err != nil {
		return nil, err
	}

	if err := h.instanceManager.ForkSnapshot(ctx, snapshot.ID, fork); err != nil {
		return nil, err
	}

	forked, err := h.instanceManager.Instance(fork.ID)
	if err != nil {
		return nil, err
	}
	return viewInstance(h.instanceManager, forked)
}

// snapshot resolves the snapshot a request names.
func (h *snapshotHandler) snapshot(nameOrID string) (instance.Snapshot, error) {
	if nameOrID == "" {
		return instance.Snapshot{}, errdefs.InvalidArgument("snapshot name is required")
	}

	return h.instanceManager.Snapshot(nameOrID)
}

// instanceName returns the name a snapshot's instance has now, or, if it
// has been deleted, the name it had.
func (h *snapshotHandler) instanceName(snapshot instance.Snapshot) string {
	if instance, err := h.instanceManager.Instance(snapshot.Instance.ID); err == nil {
		return instance.Name
	}
	return snapshot.Instance.Name
}
