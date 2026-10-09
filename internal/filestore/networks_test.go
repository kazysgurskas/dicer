// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import (
	"errors"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/network"
)

func TestCreateRejectsPathTraversal(t *testing.T) {
	s := newTestStore(t)

	err := s.CreateNetwork(network.Network{ID: "n1", Name: "../escape"})
	if !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Fatalf("CreateNetwork(../escape) = %v, want ErrInvalidArgument", err)
	}
}

// A network an instance or a snapshot is attached to cannot be deleted. One
// nothing is attached to can.
func TestNetworkInUseCannotBeDeleted(t *testing.T) {
	checkInUseCannotBeDeleted(t, func(s *Store) error { return s.DeleteNetwork("default") })
}
