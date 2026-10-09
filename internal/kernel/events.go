// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package kernel

import "github.com/konradasb/dicer/internal/event"

// Recorder records what happens to kernels. It is declared here, and
// satisfied by internal/event, so that this package reports what it does
// without knowing who listens.
type Recorder interface {
	Record(e event.Event)
}

// discardRecorder is the Recorder used when none is configured.
type discardRecorder struct{}

func (discardRecorder) Record(event.Event) {}

// record records that action happened to k, with its architecture among the
// attributes.
func (m *Manager) record(k Kernel, action event.Action, message string) {
	m.events.Record(event.Event{
		Kind:       event.KindKernel,
		ID:         k.ID,
		Name:       k.Name,
		Action:     action,
		Message:    message,
		Attributes: map[string]string{"arch": k.Architecture},
	})
}
