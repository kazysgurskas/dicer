// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"testing"

	"github.com/konradasb/dicer/internal/image"
)

func TestImagesInUseKeepsWhatGuestsAndSnapshotsNeed(t *testing.T) {
	h := newHarness(t)
	h.running(t)
	if _, err := h.manager.createSnapshot(t.Context(), h.instance, "kept"); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	inUse, err := h.manager.ImagesInUse()
	if err != nil {
		t.Fatalf("ImagesInUse: %v", err)
	}
	if user := inUse["sha256:aaaa"]; user != `instance "web"` {
		t.Errorf("in use = %v, want the running guest's image, used by instance web", inUse)
	}

	// Stopped and deleted, the instance no longer needs its image, but its
	// snapshot does.
	if err := h.manager.removeRuntimeDir(h.instance.ID); err != nil {
		t.Fatal(err)
	}
	delete(h.store.instances, h.instance.Name)
	if inUse, err = h.manager.ImagesInUse(); err != nil {
		t.Fatal(err)
	}
	if user := inUse["sha256:aaaa"]; user != `snapshot "kept"` {
		t.Errorf("in use = %v, want the snapshot's image kept, used by snapshot kept", inUse)
	}
}

// TestImagesInUseKeepsThePinnedImage checks that a stopped instance keeps
// the image it was created with, which it boots from next, and not the one
// its reference names now.
func TestImagesInUseKeepsThePinnedImage(t *testing.T) {
	h := newHarness(t)
	images, ok := h.manager.images.(*fakeImages)
	if !ok {
		t.Fatalf("images is %T", h.manager.images)
	}
	images.held = &image.Image{Name: h.instance.ImageRef, Digest: "sha256:bbbb"}

	inUse, err := h.manager.ImagesInUse()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := inUse["sha256:aaaa"]; !ok {
		t.Errorf("in use = %v, want sha256:aaaa, the image the instance was created with", inUse)
	}
	if _, ok := inUse["sha256:bbbb"]; ok {
		t.Errorf("in use = %v, want not sha256:bbbb, which the instance never boots", inUse)
	}
}

// TestImagesInUseKeepsWhatStandbyNeeds checks that an instance on standby
// keeps the image its frozen guest booted from, even once its reference
// names a newer image: resuming it needs the old one.
func TestImagesInUseKeepsWhatStandbyNeeds(t *testing.T) {
	h := newHarness(t)
	h.start(t)
	if err := h.manager.Standby(t.Context(), h.instance.Name); err != nil {
		t.Fatalf("Standby: %v", err)
	}

	images, ok := h.manager.images.(*fakeImages)
	if !ok {
		t.Fatalf("images is %T", h.manager.images)
	}
	images.held = &image.Image{Name: h.instance.ImageRef, Digest: "sha256:bbbb"}

	inUse, err := h.manager.ImagesInUse()
	if err != nil {
		t.Fatalf("ImagesInUse: %v", err)
	}
	if _, ok := inUse["sha256:aaaa"]; !ok {
		t.Errorf("in use = %v, want sha256:aaaa", inUse)
	}
}
