// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"errors"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/image"
)

// TestCreateFollowsThePullPolicy checks that Create pulls the image as its
// policy says, and records no instance whose image it cannot have.
func TestCreateFollowsThePullPolicy(t *testing.T) {
	tests := []struct {
		name      string
		policy    image.PullPolicy
		held      bool
		wantPulls int
		wantErr   error
	}{
		{name: "missing pulls an image the host lacks", policy: image.PullPolicyMissing, wantPulls: 1},
		{name: "missing uses the image held", policy: image.PullPolicyMissing, held: true},
		{name: "always pulls an image held", policy: image.PullPolicyAlways, held: true, wantPulls: 1},
		{name: "never uses the image held", policy: image.PullPolicyNever, held: true},
		{name: "never refuses an image the host lacks", policy: image.PullPolicyNever, wantErr: errdefs.ErrNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			images, ok := h.manager.images.(*fakeImages)
			if !ok {
				t.Fatalf("images is a %T, want the fake", h.manager.images)
			}
			if tt.held {
				images.held = &image.Image{Name: "alpine", Digest: "sha256:bbbb", DiskPath: images.diskPath}
			}

			instance := Spec{ID: "new-id", Name: "new", ImageRef: "alpine"}
			err := h.manager.Create(t.Context(), instance, tt.policy)

			if images.pulls != tt.wantPulls {
				t.Errorf("pulls = %d, want %d", images.pulls, tt.wantPulls)
			}

			_, getErr := h.definitions.Instance(instance.Name)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Create = %v, want %v", err, tt.wantErr)
				}
				if getErr == nil {
					t.Error("the instance was recorded though its image could not be had")
				}
				return
			}
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if getErr != nil {
				t.Errorf("the instance was not recorded: %v", getErr)
			}
		})
	}
}
