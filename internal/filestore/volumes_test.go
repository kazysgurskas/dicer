// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import "testing"

// A volume an instance or a snapshot mounts cannot be deleted. One nothing
// mounts can.
func TestVolumeInUseCannotBeDeleted(t *testing.T) {
	checkInUseCannotBeDeleted(t, func(s *Store) error { return s.DeleteVolume("data") })
}
