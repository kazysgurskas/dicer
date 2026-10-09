// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package kernel

import (
	"strings"
	"testing"

	"github.com/konradasb/dicer/internal/metric/metrictest"
)

// fakeStore is a Store of the kernels it holds.
type fakeStore []Kernel

func (s fakeStore) Kernels() []Kernel { return s }

func TestMetricsMatchTheirDescriptions(t *testing.T) {
	m := newTestManager(t)
	m.store = fakeStore{}

	metrictest.CheckDescriptions(t, m, MetricDescriptions())
}

func TestKernelsAreServedWithTheirDisk(t *testing.T) {
	m := newTestManager(t)
	k := importKernel(t, m, "k1", "test", "vmlinux")
	m.store = fakeStore{k}

	body := metrictest.Scrape(t, m)
	for _, want := range []string{"dicer_kernels 1", "dicer_kernel_disk_bytes 7"} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape is missing %q:\n%s", want, body)
		}
	}
}
