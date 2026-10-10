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

			instance := Spec{ID: "new-id", Name: "new", ImageRef: "alpine", KernelName: "k", NetworkName: "default", VCPUs: 1, MemoryBytes: 1 << 30, DiskBytes: 1 << 30}
			err := h.manager.Create(t.Context(), instance, tt.policy)

			if images.pulls != tt.wantPulls {
				t.Errorf("pulls = %d, want %d", images.pulls, tt.wantPulls)
			}

			_, getErr := h.store.Instance(instance.Name)
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

// TestCreatePinsTheImageDigest checks that a new instance records the digest
// its image reference resolves to, the image it boots from then on.
func TestCreatePinsTheImageDigest(t *testing.T) {
	h := newHarness(t)
	images, ok := h.manager.images.(*fakeImages)
	if !ok {
		t.Fatalf("images is a %T, want the fake", h.manager.images)
	}
	images.held = &image.Image{Name: "docker.io/library/alpine:latest", Digest: "sha256:bbbb", DiskPath: images.diskPath}

	instance := Spec{ID: "new-id", Name: "new", ImageRef: "alpine", KernelName: "k", NetworkName: "default", VCPUs: 1, MemoryBytes: 1 << 30, DiskBytes: 1 << 30}
	if err := h.manager.Create(t.Context(), instance, image.PullPolicyMissing); err != nil {
		t.Fatalf("Create: %v", err)
	}

	created, err := h.store.Instance(instance.Name)
	if err != nil {
		t.Fatal(err)
	}
	if created.ImageDigest != "sha256:bbbb" {
		t.Errorf("ImageDigest = %q, want sha256:bbbb, the digest alpine resolved to", created.ImageDigest)
	}
}

// A definition that could never start is refused when it is created, before
// its image is pulled.
func TestCreateRefusesWhatCouldNeverStart(t *testing.T) {
	missingVolume := Spec{ID: "new-id", Name: "new", ImageRef: "alpine", KernelName: "k", NetworkName: "default",
		VCPUs: 1, MemoryBytes: 1 << 30, DiskBytes: 1 << 30,
		Mounts: []Mount{{Type: MountTypeVolume, Source: "gone", Target: "/data"}}}
	foreignIP := missingVolume
	foreignIP.Mounts, foreignIP.StaticIP = nil, "192.168.9.9"
	missingKernel := foreignIP
	missingKernel.StaticIP, missingKernel.KernelName = "", "gone"
	// The harness's manager allows no directory.
	directoryNotAllowed := foreignIP
	directoryNotAllowed.StaticIP = ""
	directoryNotAllowed.Mounts = []Mount{{Type: MountTypeDirectory, Source: t.TempDir(), Target: "/app"}}

	for name, spec := range map[string]Spec{
		"a missing volume":                 missingVolume,
		"a static IP the network lacks":    foreignIP,
		"a missing kernel":                 missingKernel,
		"a directory not allowed":          directoryNotAllowed,
		"an invalid definition (no image)": {ID: "new-id", Name: "new", VCPUs: 1},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			images, ok := h.manager.images.(*fakeImages)
			if !ok {
				t.Fatalf("images is a %T, want the fake", h.manager.images)
			}

			if err := h.manager.Create(t.Context(), spec, image.PullPolicyAlways); !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Create = %v, want an invalid argument error", err)
			}
			if images.pulls != 0 {
				t.Errorf("an image was pulled for a definition that could never start")
			}
		})
	}
}
