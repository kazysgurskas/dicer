// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"testing"

	"google.golang.org/grpc"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// TestPullReportsProgressThenReturnsTheImage checks that each report of a
// pull reaches the callback, and the image the last carries is returned.
func TestPullReportsProgressThenReturnsTheImage(t *testing.T) {
	c := connect(t, &fakeDaemon{
		pullImage: func(_ *dicerdv1.PullImageRequest, stream grpc.ServerStreamingServer[dicerdv1.PullImageProgress]) error {
			for _, p := range []*dicerdv1.PullImageProgress{
				{Stage: dicerdv1.PullStage_PULL_STAGE_RESOLVING},
				{Stage: dicerdv1.PullStage_PULL_STAGE_DOWNLOADING, DownloadedBytes: 5, TotalBytes: 10},
				{Image: &dicerdv1.Image{Name: "docker.io/library/nginx:1.27", Digest: "sha256:abc"}},
			} {
				if err := stream.Send(p); err != nil {
					return err
				}
			}
			return nil
		},
	})

	var reports []PullProgress
	image, err := c.Images.Pull(t.Context(), "nginx:1.27", func(p PullProgress) { reports = append(reports, p) })
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if image.Digest != "sha256:abc" {
		t.Errorf("pulled %+v", image)
	}
	if len(reports) != 2 || reports[1] != (PullProgress{Stage: PullStageDownloading, DownloadedBytes: 5, TotalBytes: 10}) {
		t.Errorf("reported %+v", reports)
	}
}

// TestPullWithoutAnImageFails checks that a pull whose stream ends before
// an image is reported as failed.
func TestPullWithoutAnImageFails(t *testing.T) {
	c := connect(t, &fakeDaemon{
		pullImage: func(*dicerdv1.PullImageRequest, grpc.ServerStreamingServer[dicerdv1.PullImageProgress]) error {
			return nil
		},
	})

	if _, err := c.Images.Pull(t.Context(), "nginx:1.27", nil); err == nil {
		t.Error("Pull succeeded")
	}
}
