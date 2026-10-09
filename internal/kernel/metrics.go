// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package kernel

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/konradasb/dicer/internal/metric"
)

// The metrics of the kernels defined.
var (
	kernelsMetric = metric.Description{
		Name:  "dicer_kernels",
		Type:  metric.TypeGauge,
		Help:  "Kernels defined on this host.",
		Group: metric.GroupKernels,
	}

	kernelDiskMetric = metric.Description{
		Name:  "dicer_kernel_disk_bytes",
		Type:  metric.TypeGauge,
		Help:  "Total size of those kernels on this host's disk.",
		Group: metric.GroupKernels,
	}
)

// MetricDescriptions returns the descriptions of every metric the kernel
// manager serves, for the reference.
func MetricDescriptions() []metric.Description {
	return []metric.Description{kernelsMetric, kernelDiskMetric}
}

// metrics are the descriptors of what a Manager reads at scrape time.
type metrics struct {
	kernels *prometheus.Desc
	disk    *prometheus.Desc
}

func newMetrics() metrics {
	return metrics{
		kernels: metric.NewDesc(kernelsMetric),
		disk:    metric.NewDesc(kernelDiskMetric),
	}
}

// Describe implements prometheus.Collector.
func (m *Manager) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.metrics.kernels
	ch <- m.metrics.disk
}

// Collect implements prometheus.Collector: the kernels defined, and what
// they take on disk, as they are now.
func (m *Manager) Collect(ch chan<- prometheus.Metric) {
	kernels := m.store.Kernels()
	var diskBytes int64
	for _, k := range kernels {
		diskBytes += m.DiskBytes(k)
	}
	ch <- metric.GaugeReading(m.metrics.kernels, float64(len(kernels)))
	ch <- metric.GaugeReading(m.metrics.disk, float64(diskBytes))
}
