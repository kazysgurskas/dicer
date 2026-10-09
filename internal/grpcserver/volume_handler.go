// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcserver

import (
	"context"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/dicer/internal/volume"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// volumeHandler handles volume-related RPCs.
type volumeHandler struct {
	volumeManager *volume.Manager
}

// CreateVolume creates and formats a volume.
func (h *volumeHandler) CreateVolume(
	ctx context.Context, req *dicerdv1.CreateVolumeRequest,
) (*dicerdv1.Volume, error) {
	v, err := h.volumeManager.Create(ctx, req.GetName(), req.GetSizeBytes())
	if err != nil {
		return nil, err
	}
	return volumeToProto(v), nil
}

// ListVolumes lists the volumes, sorted by name.
func (h *volumeHandler) ListVolumes(
	_ context.Context, _ *dicerdv1.ListVolumesRequest,
) (*dicerdv1.ListVolumesResponse, error) {
	volumes := h.volumeManager.Volumes()

	resp := &dicerdv1.ListVolumesResponse{
		Volumes: make([]*dicerdv1.Volume, 0, len(volumes)),
	}
	for _, v := range volumes {
		resp.Volumes = append(resp.Volumes, volumeToProto(v))
	}

	return resp, nil
}

// GetVolume returns a volume.
func (h *volumeHandler) GetVolume(
	_ context.Context, req *dicerdv1.GetVolumeRequest,
) (*dicerdv1.Volume, error) {
	v, err := h.volumeManager.Volume(req.GetName())
	if err != nil {
		return nil, err
	}
	return volumeToProto(v), nil
}

// DeleteVolume removes a volume and its data, refusing one an instance or
// snapshot mounts.
func (h *volumeHandler) DeleteVolume(
	_ context.Context, req *dicerdv1.DeleteVolumeRequest,
) (*emptypb.Empty, error) {
	if err := h.volumeManager.Delete(req.GetName()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}
