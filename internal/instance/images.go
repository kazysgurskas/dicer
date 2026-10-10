// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/konradasb/dicer/internal/image"
)

// ImagesInUse returns the images that must be kept, each with what uses it:
// those instances are defined to boot from, those active guests booted from,
// those guests on standby booted from, and those memory snapshots' guests
// booted from.
func (m *Manager) ImagesInUse() (image.InUse, error) {
	inUse := image.InUse{}
	use := func(digest, user string) {
		if _, ok := inUse[digest]; !ok && digest != "" {
			inUse[digest] = user
		}
	}

	for _, instance := range m.store.Instances() {
		user := fmt.Sprintf("instance %q", instance.Name)
		use(instance.ImageDigest, user)

		status, err := m.statusOf(instance)
		if err != nil {
			return nil, err
		}
		switch {
		case status.State.HoldsResources() || status.State == StateStopping:
			use(status.ImageDigest, user)
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
			use(standby.ImageDigest, user)
		}
	}
	for _, snapshot := range m.store.Snapshots() {
		use(snapshot.ImageDigest, fmt.Sprintf("snapshot %q", snapshot.Name))
	}

	return inUse, nil
}
