// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/dicer/internal/errdefs"
	imagepkg "github.com/konradasb/dicer/internal/image"
	"github.com/konradasb/dicer/internal/instance"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// imageHandler handles image-related RPCs.
type imageHandler struct {
	instanceManager *instance.Manager
	imageManager    *imagepkg.Manager
}

// PullImage pulls an image, streaming progress and finally the image.
func (h *imageHandler) PullImage(
	req *dicerdv1.PullImageRequest,
	stream grpc.ServerStreamingServer[dicerdv1.PullImageProgress],
) error {
	if req.GetRef() == "" {
		return errdefs.InvalidArgument("ref is required")
	}

	// Progress is reported on this goroutine.
	onProgress := func(p imagepkg.PullProgress) {
		_ = stream.Send(&dicerdv1.PullImageProgress{
			Stage:           pullStages.toProto(p.Stage),
			DownloadedBytes: p.DownloadedBytes,
			TotalBytes:      p.TotalBytes,
		})
	}

	image, err := h.imageManager.Pull(stream.Context(), req.GetRef(), onProgress)
	if err != nil {
		return imageError(err)
	}

	return stream.Send(&dicerdv1.PullImageProgress{
		Stage: dicerdv1.PullStage_PULL_STAGE_UNSPECIFIED,
		Image: imageToProto(image),
	})
}

// ListImages lists the images this host holds.
func (h *imageHandler) ListImages(
	_ context.Context, _ *dicerdv1.ListImagesRequest,
) (*dicerdv1.ListImagesResponse, error) {
	images := h.imageManager.List()

	resp := &dicerdv1.ListImagesResponse{
		Images: make([]*dicerdv1.Image, 0, len(images)),
	}
	for _, image := range images {
		resp.Images = append(resp.Images, imageToProto(image))
	}

	return resp, nil
}

// GetImage returns an image this host holds without contacting a registry.
func (h *imageHandler) GetImage(
	_ context.Context, req *dicerdv1.GetImageRequest,
) (*dicerdv1.Image, error) {
	image, err := h.imageManager.Image(req.GetRef())
	if err != nil {
		return nil, imageError(err)
	}

	return imageToProto(image), nil
}

// DeleteImage removes an image, refusing one that is in use unless the
// request forces it.
func (h *imageHandler) DeleteImage(
	_ context.Context, req *dicerdv1.DeleteImageRequest,
) (*emptypb.Empty, error) {
	var inUse imagepkg.InUse
	if !req.GetForce() {
		var err error
		if inUse, err = h.instanceManager.ImagesInUse(); err != nil {
			return nil, err
		}
	}

	if err := h.imageManager.Delete(req.GetRef(), inUse); err != nil {
		return nil, imageError(err)
	}
	return &emptypb.Empty{}, nil
}

// PruneImages removes the images nothing is defined to boot from.
func (h *imageHandler) PruneImages(
	_ context.Context, _ *dicerdv1.PruneImagesRequest,
) (*dicerdv1.PruneImagesResponse, error) {
	keep, err := h.instanceManager.ImagesInUse()
	if err != nil {
		return nil, err
	}

	result, err := h.imageManager.Prune(keep)
	if err != nil {
		return nil, err
	}

	resp := &dicerdv1.PruneImagesResponse{
		Images:         make([]*dicerdv1.Image, 0, len(result.Images)),
		ReclaimedBytes: result.ReclaimedBytes,
	}
	for _, image := range result.Images {
		resp.Images = append(resp.Images, imageToProto(&image))
	}

	return resp, nil
}

// imageError returns err, an image reference that cannot be parsed made an
// invalid argument.
func imageError(err error) error {
	if errors.Is(err, imagepkg.ErrInvalidReference) {
		return errdefs.InvalidArgument("%v", err)
	}
	return err
}
