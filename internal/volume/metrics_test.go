// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package volume

import (
	"strings"
	"testing"

	"github.com/konradasb/dicer/internal/metric/metrictest"
)

func TestMetricsMatchTheirDescriptions(t *testing.T) {
	m, _, _ := newTestManager(t)

	metrictest.CheckDescriptions(t, m, MetricDescriptions())
}

func TestVolumesAreServedWithTheirSize(t *testing.T) {
	m, _, _ := newTestManager(t)
	if _, err := m.Create(t.Context(), "data", 2048); err != nil {
		t.Fatal(err)
	}

	body := metrictest.Scrape(t, m)
	for _, want := range []string{"dicer_volumes 1", "dicer_volume_size_bytes 2048", "dicer_volume_disk_bytes 0"} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape is missing %q:\n%s", want, body)
		}
	}
}
