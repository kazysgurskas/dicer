// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package main

import "testing"

// Each package serves its own metrics, and the daemon registers them all on
// one registry, which refuses a name served twice at startup. The lists say
// what each package serves, so a clash is caught here first.
func TestEveryMetricIsServedOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range metricDescriptions() {
		if seen[d.Name] {
			t.Errorf("%s is served by two packages", d.Name)
		}
		seen[d.Name] = true
	}
}
