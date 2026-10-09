// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package network

import "github.com/konradasb/dicer/internal/event"

// Recorder records what happens to networks. It is declared here, and
// satisfied by internal/event, so that this package reports what it does
// without knowing who listens.
type Recorder interface {
	Record(e event.Event)
}

// discardRecorder is the Recorder used when none is configured.
type discardRecorder struct{}

func (discardRecorder) Record(event.Event) {}

// record records that action happened to n, with its subnet and gateway
// among the attributes.
func (m *Manager) record(n Network, action event.Action, message string) {
	m.events.Record(event.Event{
		Kind:       event.KindNetwork,
		ID:         n.ID,
		Name:       n.Name,
		Action:     action,
		Message:    message,
		Attributes: map[string]string{"subnet": n.Subnet, "gateway": n.Gateway},
	})
}
