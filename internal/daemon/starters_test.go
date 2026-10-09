// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package daemon

import (
	"slices"
	"testing"

	"github.com/konradasb/dicer/internal/hypervisor"
)

// TestDriversCoverEverySupportedType guards the wiring: a hypervisor the API
// accepts must be one the daemon can actually start.
func TestDriversCoverEverySupportedType(t *testing.T) {
	d := drivers()

	for _, hypervisorType := range hypervisor.Types() {
		if _, ok := d[hypervisorType]; !ok {
			t.Errorf("no driver for supported hypervisor %q", hypervisorType)
		}
	}
	if len(d) != len(hypervisor.Types()) {
		t.Errorf("drivers() has %d entries for %d supported types", len(d), len(hypervisor.Types()))
	}
}

func TestVersionsPutTheDefaultFirst(t *testing.T) {
	type version string

	got := versions([]version{"v1", "v2", "v3"}, "v2")
	want := []string{"v2", "v1", "v3"}

	if !slices.Equal(got, want) {
		t.Errorf("versions() = %v, want %v", got, want)
	}
}
