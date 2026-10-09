// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package volume

import (
	"strconv"

	"github.com/konradasb/dicer/internal/event"
)

// Recorder records what happens to volumes. It is declared here, and
// satisfied by internal/event, so that this package reports what it does
// without knowing who listens.
type Recorder interface {
	Record(e event.Event)
}

// discardRecorder is the Recorder used when none is configured.
type discardRecorder struct{}

func (discardRecorder) Record(event.Event) {}

// record records that action happened to volume, with its size among the
// attributes.
func (m *Manager) record(volume Volume, action event.Action, message string) {
	m.events.Record(event.Event{
		Kind:       event.KindVolume,
		ID:         volume.ID,
		Name:       volume.Name,
		Action:     action,
		Message:    message,
		Attributes: map[string]string{"size_bytes": strconv.FormatInt(volume.SizeBytes, 10)},
	})
}
