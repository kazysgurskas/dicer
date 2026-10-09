// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package health

import (
	"sync"
	"time"
)

// Monitor holds what an instance's health check has found. It is safe for
// concurrent use.
type Monitor struct {
	check     Check
	startedAt time.Time

	mu     sync.Mutex
	health Health
}

// NewMonitor returns a monitor of check, for an instance started at
// startedAt. It has reached no verdict.
func NewMonitor(check Check, startedAt time.Time) *Monitor {
	return &Monitor{check: check, startedAt: startedAt, health: New()}
}

// Check returns the check whose results the monitor holds.
func (m *Monitor) Check() Check { return m.check }

// Health returns what the check has found so far.
func (m *Monitor) Health() Health {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.health
}

// Observe adds a probe's result and returns the health before and after.
func (m *Monitor) Observe(r Result) (before, after Health) {
	m.mu.Lock()
	defer m.mu.Unlock()

	before = m.health
	m.health = m.health.After(m.check, r, r.At.Sub(m.startedAt))

	return before, m.health
}
