// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package volume

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/konradasb/dicer/internal/metric"
)

// The metrics of the volumes defined.
var (
	volumesMetric = metric.Description{
		Name:  "dicer_volumes",
		Type:  metric.TypeGauge,
		Help:  "Volumes defined on this host.",
		Group: metric.GroupVolumes,
	}

	volumeSizeMetric = metric.Description{
		Name:  "dicer_volume_size_bytes",
		Type:  metric.TypeGauge,
		Help:  "Total size of those volumes, as their guests see it.",
		Group: metric.GroupVolumes,
	}

	volumeDiskMetric = metric.Description{
		Name: "dicer_volume_disk_bytes",
		Type: metric.TypeGauge,
		Help: "Disk those volumes take up on this host.",
		Doc: "Volumes are sparse files that take up disk as guests write to them, so it is less " +
			"than `dicer_volume_size_bytes` until they fill. A block a guest frees may stay taken.",
		Group: metric.GroupVolumes,
	}
)

// MetricDescriptions returns the descriptions of every metric the volume
// manager serves, for the reference.
func MetricDescriptions() []metric.Description {
	return []metric.Description{volumesMetric, volumeSizeMetric, volumeDiskMetric}
}

// metrics are the descriptors of what a Manager reads at scrape time.
type metrics struct {
	volumes *prometheus.Desc
	size    *prometheus.Desc
	disk    *prometheus.Desc
}

func newMetrics() metrics {
	return metrics{
		volumes: metric.NewDesc(volumesMetric),
		size:    metric.NewDesc(volumeSizeMetric),
		disk:    metric.NewDesc(volumeDiskMetric),
	}
}

// Describe implements prometheus.Collector.
func (m *Manager) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.metrics.volumes
	ch <- m.metrics.size
	ch <- m.metrics.disk
}

// Collect implements prometheus.Collector: the volumes defined, their size
// and the disk they take up, as they are now.
func (m *Manager) Collect(ch chan<- prometheus.Metric) {
	volumes := m.store.Volumes()
	var sizeBytes, diskBytes int64
	for _, v := range volumes {
		sizeBytes += v.SizeBytes
		diskBytes += m.DiskBytes(v)
	}
	ch <- metric.GaugeReading(m.metrics.volumes, float64(len(volumes)))
	ch <- metric.GaugeReading(m.metrics.size, float64(sizeBytes))
	ch <- metric.GaugeReading(m.metrics.disk, float64(diskBytes))
}
