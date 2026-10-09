// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import "github.com/konradasb/dicer/internal/health"

// Usage returns what the instances on this host hold now. An instance whose
// status cannot be read counts as Failed.
func (m *Manager) Usage() Usage {
	usage := Usage{
		ByState:  make(map[State]int, len(States())),
		ByHealth: make(map[health.Status]int, len(health.Statuses())),
		Capacity: m.capacity,
	}
	for _, state := range States() {
		usage.ByState[state] = 0
	}
	for _, healthStatus := range health.Statuses() {
		usage.ByHealth[healthStatus] = 0
	}

	instances := m.definitions.Instances()

	for _, instance := range instances {
		status, err := m.Status(instance)
		if err != nil {
			status = Status{State: StateFailed}
		}

		usage.ByState[status.State]++
		if _, health, ok := m.Health(instance); ok {
			usage.ByHealth[health.Status]++
		}

		if !status.State.HoldsResources() {
			continue
		}
		held := status.HeldResources()
		usage.Allocated = usage.Allocated.Add(held)
		usage.Instances = append(usage.Instances, HeldResources{Name: instance.Name, State: status.State, Resources: held})
	}

	return usage
}
