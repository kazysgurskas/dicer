// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"time"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// Volumes are the calls about volumes, reached as Client.Volumes.
type Volumes struct {
	api dicerdv1.DaemonServiceClient
}

// Volume is a persistent block device that outlives the instances using it.
type Volume struct {
	// ID is the volume's ID.
	ID string `json:"id,omitzero"`

	// Name is the volume's name, which a mount gives as its Source.
	Name string `json:"name,omitzero"`

	// SizeBytes is the volume's size.
	SizeBytes int64 `json:"size_bytes,omitzero"`

	// CreateTime is when the volume was created.
	CreateTime time.Time `json:"create_time,omitzero"`

	// UpdateTime is when the volume was last changed.
	UpdateTime time.Time `json:"update_time,omitzero"`
}

// Create provisions a volume of sizeBytes.
func (s *Volumes) Create(ctx context.Context, name string, sizeBytes int64) (Volume, error) {
	resp, err := s.api.CreateVolume(ctx, &dicerdv1.CreateVolumeRequest{Name: name, SizeBytes: sizeBytes})
	if err != nil {
		return Volume{}, fromStatus(err)
	}
	return volumeFromProto(resp), nil
}

// List returns every volume.
func (s *Volumes) List(ctx context.Context) ([]Volume, error) {
	resp, err := s.api.ListVolumes(ctx, &dicerdv1.ListVolumesRequest{})
	if err != nil {
		return nil, fromStatus(err)
	}
	return convertAll(resp.GetVolumes(), volumeFromProto), nil
}

// Get returns one volume, or ErrNotFound.
func (s *Volumes) Get(ctx context.Context, name string) (Volume, error) {
	resp, err := s.api.GetVolume(ctx, &dicerdv1.GetVolumeRequest{Name: name})
	if err != nil {
		return Volume{}, fromStatus(err)
	}
	return volumeFromProto(resp), nil
}

// Delete removes a volume that no instance references.
func (s *Volumes) Delete(ctx context.Context, name string) error {
	_, err := s.api.DeleteVolume(ctx, &dicerdv1.DeleteVolumeRequest{Name: name})
	return fromStatus(err)
}

// volumeFromProto returns the volume p describes.
func volumeFromProto(p *dicerdv1.Volume) Volume {
	return Volume{
		ID:         p.GetId(),
		Name:       p.GetName(),
		SizeBytes:  p.GetSizeBytes(),
		CreateTime: timeFromProto(p.GetCreateTime()),
		UpdateTime: timeFromProto(p.GetUpdateTime()),
	}
}
