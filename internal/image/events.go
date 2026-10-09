// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package image

import "github.com/konradasb/dicer/internal/event"

// Recorder records what happens to images. It is declared here, and satisfied
// by internal/event, so that this package reports what it does without
// knowing who listens.
type Recorder interface {
	Record(e event.Event)
}

// discardRecorder is the Recorder used when none is configured.
type discardRecorder struct{}

func (discardRecorder) Record(event.Event) {}

// record records that action happened to image: known by its reference, with
// its digest among the attributes.
func (m *Manager) record(image *Image, action event.Action, message string, attrs map[string]string) {
	if attrs == nil {
		attrs = map[string]string{}
	}
	attrs["digest"] = image.Digest

	m.events.Record(event.Event{
		Kind:       event.KindImage,
		Name:       image.Name,
		Action:     action,
		Message:    message,
		Attributes: attrs,
	})
}
