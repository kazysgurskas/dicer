// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"errors"
	"io"
	"time"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// Images are the calls about images, reached as Client.Images. An image is
// named by its reference, as it is pulled: nginx:1.27 names
// docker.io/library/nginx:1.27.
type Images struct {
	api dicerdv1.DaemonServiceClient
}

// Image is a pulled image, converted to a root filesystem a guest can boot.
type Image struct {
	// Name is the image's reference, in full, such as
	// docker.io/library/nginx:1.27.
	Name string `json:"name,omitzero"`

	// Digest is the digest of the image's manifest.
	Digest string `json:"digest,omitzero"`

	// SizeBytes is the size of the image's disk.
	SizeBytes int64 `json:"size_bytes,omitzero"`

	// CreateTime is when the image was pulled.
	CreateTime time.Time `json:"create_time,omitzero"`

	// UpdateTime is when the image was last changed.
	UpdateTime time.Time `json:"update_time,omitzero"`

	// LastUsedTime is when the image was last pulled or in use: by an
	// instance defined to boot from it, a running guest, a guest on standby
	// or a snapshot. Garbage collection judges it by this.
	LastUsedTime time.Time `json:"last_used_time,omitzero"`

	// HealthCheck is the HEALTHCHECK the image declares, which instances of
	// it run unless they set their own. It is nil if it declares none.
	HealthCheck *HealthCheck `json:"health_check,omitzero"`
}

// PullPolicy is when creating an instance pulls its image, as docker run
// --pull says.
type PullPolicy string

// The pull policies.
const (
	// PullPolicyMissing pulls the image only if the host does not hold it.
	PullPolicyMissing PullPolicy = "missing"

	// PullPolicyAlways pulls the image even if the host holds it, so that a
	// tag that has moved is followed. Nothing is downloaded if the host
	// already has what the tag points at.
	PullPolicyAlways PullPolicy = "always"

	// PullPolicyNever uses the image the host holds, and fails with
	// ErrNotFound if it holds none.
	PullPolicyNever PullPolicy = "never"
)

var pullPolicies = enum[PullPolicy, dicerdv1.PullPolicy]{"pull policy", map[PullPolicy]dicerdv1.PullPolicy{
	PullPolicyMissing: dicerdv1.PullPolicy_PULL_POLICY_MISSING,
	PullPolicyAlways:  dicerdv1.PullPolicy_PULL_POLICY_ALWAYS,
	PullPolicyNever:   dicerdv1.PullPolicy_PULL_POLICY_NEVER,
}}

// PullProgress is how far a pull has got.
type PullProgress struct {
	// Stage is the step the pull is at.
	Stage PullStage `json:"stage,omitzero"`

	// DownloadedBytes is how many compressed layer bytes have been fetched.
	// It is zero outside the downloading stage.
	DownloadedBytes int64 `json:"downloaded_bytes,omitzero"`

	// TotalBytes is how many compressed layer bytes are to be fetched. It
	// counts only what is actually fetched: a layer already held is not
	// downloaded again. It is zero outside the downloading stage.
	TotalBytes int64 `json:"total_bytes,omitzero"`
}

// PullStage is the part of a pull that is working.
type PullStage string

// The pull stages.
const (
	// PullStageResolving is asking the registry what the reference points
	// at.
	PullStageResolving PullStage = "resolving"

	// PullStageDownloading is fetching the layers, the only stage with a
	// byte count.
	PullStageDownloading PullStage = "downloading"

	// PullStageUnpacking is writing those layers out as a root filesystem.
	PullStageUnpacking PullStage = "unpacking"

	// PullStageConverting is packing that filesystem into the disk a guest
	// boots from.
	PullStageConverting PullStage = "converting"
)

var pullStages = enum[PullStage, dicerdv1.PullStage]{"pull stage", map[PullStage]dicerdv1.PullStage{
	PullStageResolving:   dicerdv1.PullStage_PULL_STAGE_RESOLVING,
	PullStageDownloading: dicerdv1.PullStage_PULL_STAGE_DOWNLOADING,
	PullStageUnpacking:   dicerdv1.PullStage_PULL_STAGE_UNPACKING,
	PullStageConverting:  dicerdv1.PullStage_PULL_STAGE_CONVERTING,
}}

// PruneResult is what Images.Prune removed.
type PruneResult struct {
	// Images are the images deleted.
	Images []Image `json:"images,omitzero"`

	// ReclaimedBytes is the disk space their disks and cached layers gave
	// back.
	ReclaimedBytes int64 `json:"reclaimed_bytes,omitzero"`
}

// Pull fetches an image and converts it to a root filesystem a guest can
// boot, and returns it once it is ready. onProgress, if not nil, is called
// with each report of how far the pull has got.
func (s *Images) Pull(ctx context.Context, ref string, onProgress func(PullProgress)) (Image, error) {
	stream, err := s.api.PullImage(ctx, &dicerdv1.PullImageRequest{Ref: ref})
	if err != nil {
		return Image{}, fromStatus(err)
	}

	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return Image{}, errors.New("the pull ended without an image")
		}
		if err != nil {
			return Image{}, fromStatus(err)
		}

		if onProgress != nil && msg.GetStage() != dicerdv1.PullStage_PULL_STAGE_UNSPECIFIED {
			onProgress(pullProgressFromProto(msg))
		}
		if image := msg.GetImage(); image != nil {
			return imageFromProto(image), nil
		}
	}
}

// List returns every pulled image.
func (s *Images) List(ctx context.Context) ([]Image, error) {
	resp, err := s.api.ListImages(ctx, &dicerdv1.ListImagesRequest{})
	if err != nil {
		return nil, fromStatus(err)
	}
	return convertAll(resp.GetImages(), imageFromProto), nil
}

// Get returns one pulled image, or ErrNotFound.
func (s *Images) Get(ctx context.Context, ref string) (Image, error) {
	resp, err := s.api.GetImage(ctx, &dicerdv1.GetImageRequest{Ref: ref})
	if err != nil {
		return Image{}, fromStatus(err)
	}
	return imageFromProto(resp), nil
}

// Delete removes a pulled image. It refuses an image an instance is defined
// to boot from with ErrFailedPrecondition, unless opts.Force is set: that
// instance then pulls it again at its next start.
func (s *Images) Delete(ctx context.Context, ref string, opts DeleteOptions) error {
	_, err := s.api.DeleteImage(ctx, &dicerdv1.DeleteImageRequest{Ref: ref, Force: opts.Force})
	return fromStatus(err)
}

// Prune removes every image no instance is defined to boot from.
func (s *Images) Prune(ctx context.Context) (PruneResult, error) {
	resp, err := s.api.PruneImages(ctx, &dicerdv1.PruneImagesRequest{})
	if err != nil {
		return PruneResult{}, fromStatus(err)
	}
	return pruneResultFromProto(resp), nil
}

// pullProgressFromProto returns the progress p reports. The image the last
// report carries is Pull's to return.
func pullProgressFromProto(p *dicerdv1.PullImageProgress) PullProgress {
	return PullProgress{
		Stage:           pullStages.fromProto(p.GetStage()),
		DownloadedBytes: p.GetDownloadedBytes(),
		TotalBytes:      p.GetTotalBytes(),
	}
}

// pruneResultFromProto returns what p says a prune removed.
func pruneResultFromProto(p *dicerdv1.PruneImagesResponse) PruneResult {
	return PruneResult{
		Images:         convertAll(p.GetImages(), imageFromProto),
		ReclaimedBytes: p.GetReclaimedBytes(),
	}
}

// imageFromProto returns the image p describes.
func imageFromProto(p *dicerdv1.Image) Image {
	return Image{
		Name:         p.GetName(),
		Digest:       p.GetDigest(),
		SizeBytes:    p.GetSizeBytes(),
		CreateTime:   timeFromProto(p.GetCreateTime()),
		UpdateTime:   timeFromProto(p.GetUpdateTime()),
		LastUsedTime: timeFromProto(p.GetLastUsedTime()),
		HealthCheck:  healthCheckFromProto(p.GetHealthCheck()),
	}
}
