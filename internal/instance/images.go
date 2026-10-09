// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"errors"
	"io/fs"
)

// ImagesInUse returns the digests of images that must be kept: those
// instances are defined to boot from, those active guests booted from, those
// guests on standby booted from, and those memory snapshots' guests booted
// from.
func (m *Manager) ImagesInUse() (map[string]struct{}, error) {
	inUse := make(map[string]struct{})
	for _, instance := range m.store.Instances() {
		if image, err := m.images.Image(instance.ImageRef); err == nil {
			inUse[image.Digest] = struct{}{}
		}

		status, err := m.Status(instance)
		if err != nil {
			return nil, err
		}
		switch {
		case status.State.HoldsResources() || status.State == StateStopping:
			if status.ImageDigest != "" {
				inUse[status.ImageDigest] = struct{}{}
			}
		case status.State == StateStandby:
			// Standby leaves no runtime status, so the image is read from
			// what was frozen. Resuming needs that image, whatever the
			// definition now names.
			standby, err := m.readStandby(instance)
			if errors.Is(err, fs.ErrNotExist) {
				continue // resumed or stopped since its status was read
			}
			if err != nil {
				return nil, err
			}
			if standby.ImageDigest != "" {
				inUse[standby.ImageDigest] = struct{}{}
			}
		}
	}
	for _, snapshot := range m.store.Snapshots() {
		if snapshot.ImageDigest != "" {
			inUse[snapshot.ImageDigest] = struct{}{}
		}
	}

	return inUse, nil
}
