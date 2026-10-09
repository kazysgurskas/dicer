// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package kernel

import (
	"strings"
	"testing"

	"github.com/konradasb/dicer/internal/metric/metrictest"
)

func TestMetricsMatchTheirDescriptions(t *testing.T) {
	m, _, _ := newTestManager(t)

	metrictest.CheckDescriptions(t, m, MetricDescriptions())
}

func TestKernelsAreServedWithTheirDisk(t *testing.T) {
	m, _, _ := newTestManager(t)
	importKernel(t, m, "test", "vmlinux")

	body := metrictest.Scrape(t, m)
	for _, want := range []string{"dicer_kernels 1", "dicer_kernel_disk_bytes 7"} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape is missing %q:\n%s", want, body)
		}
	}
}
