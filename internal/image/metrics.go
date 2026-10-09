// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package image

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/konradasb/dicer/internal/metric"
)

// The metrics of the images held, pulled and collected.
var (
	imagesMetric = metric.Description{
		Name:  "dicer_images",
		Type:  metric.TypeGauge,
		Help:  "Images held on this host.",
		Group: metric.GroupImages,
	}

	imageDiskMetric = metric.Description{
		Name:  "dicer_image_disk_bytes",
		Type:  metric.TypeGauge,
		Help:  "Total size of the bootable disks those images were converted to.",
		Group: metric.GroupImages,
	}

	pullsMetric = metric.Description{
		Name:   "dicer_image_pulls_total",
		Type:   metric.TypeCounter,
		Labels: []string{"outcome"},
		Help:   "Image pulls that reached a registry, by outcome. Cache hits are not pulls.",
		Doc:    "`outcome` is `success` or `error`.",
		Group:  metric.GroupImages,
	}

	pullDurationMetric = metric.Description{
		Name:  "dicer_image_pull_duration_seconds",
		Type:  metric.TypeHistogram,
		Help:  "Time a pull took, from resolving the reference to a bootable disk.",
		Group: metric.GroupImages,
	}

	pulledBytesMetric = metric.Description{
		Name:  "dicer_image_pulled_bytes_total",
		Type:  metric.TypeCounter,
		Help:  "Compressed layer bytes pulls downloaded from registries.",
		Group: metric.GroupImages,
	}

	conversionDurationMetric = metric.Description{
		Name:  "dicer_image_conversion_duration_seconds",
		Type:  metric.TypeHistogram,
		Help:  "Time spent packing an unpacked image into its EROFS disk.",
		Group: metric.GroupImages,
	}

	cacheLookupsMetric = metric.Description{
		Name:   "dicer_image_cache_lookups_total",
		Type:   metric.TypeCounter,
		Labels: []string{"result"},
		Help:   "Image lookups by whether this host already held the image.",
		Doc:    "`result` is `hit` or `miss`.",
		Group:  metric.GroupImages,
	}

	gcCollectedMetric = metric.Description{
		Name:   "dicer_image_gc_collected_total",
		Type:   metric.TypeCounter,
		Labels: []string{"reason"},
		Help:   "Images garbage collection removed, by reason (unused, size).",
		Group:  metric.GroupImages,
	}

	gcReclaimedMetric = metric.Description{
		Name:  "dicer_image_gc_reclaimed_bytes_total",
		Type:  metric.TypeCounter,
		Help:  "Disk image garbage collection gave back: bootable disks and cached layers.",
		Group: metric.GroupImages,
	}
)

// MetricDescriptions returns the descriptions of every metric the image manager
// serves, for the reference.
func MetricDescriptions() []metric.Description {
	return []metric.Description{
		imagesMetric,
		imageDiskMetric,
		pullsMetric,
		pullDurationMetric,
		pulledBytesMetric,
		conversionDurationMetric,
		cacheLookupsMetric,
		gcCollectedMetric,
		gcReclaimedMetric,
	}
}

// metrics are the counters a Manager keeps, and the descriptors of
// the gauges it reads at scrape time.
type metrics struct {
	pulls              *prometheus.CounterVec
	pullDuration       prometheus.Histogram
	pulledBytes        prometheus.Counter
	conversionDuration prometheus.Histogram
	cacheLookups       *prometheus.CounterVec
	gcCollected        *prometheus.CounterVec
	gcReclaimed        prometheus.Counter

	images *prometheus.Desc
	disk   *prometheus.Desc
}

func newMetrics() metrics {
	return metrics{
		pulls:              metric.NewCounterVec(pullsMetric),
		pullDuration:       metric.NewHistogram(pullDurationMetric, prometheus.ExponentialBuckets(0.5, 2, 12)),
		pulledBytes:        metric.NewCounter(pulledBytesMetric),
		conversionDuration: metric.NewHistogram(conversionDurationMetric, prometheus.ExponentialBuckets(0.25, 2, 12)),
		cacheLookups:       metric.NewCounterVec(cacheLookupsMetric),
		gcCollected:        metric.NewCounterVec(gcCollectedMetric),
		gcReclaimed:        metric.NewCounter(gcReclaimedMetric),

		images: metric.NewDesc(imagesMetric),
		disk:   metric.NewDesc(imageDiskMetric),
	}
}

// observePull records a pull that went to a registry: its outcome, how long
// it took and the compressed bytes it downloaded. Call it deferred, over the
// pull's named error result.
func (m *Manager) observePull(err error, started time.Time, downloadedBytes int64) {
	m.metrics.pulls.WithLabelValues(metric.Outcome(err)).Inc()
	m.metrics.pullDuration.Observe(time.Since(started).Seconds())
	m.metrics.pulledBytes.Add(float64(downloadedBytes))
}

// cacheLookupResult is the result label of a lookup that found the image
// held on this host, or did not.
func cacheLookupResult(hit bool) string {
	if hit {
		return "hit"
	}
	return "miss"
}

// Describe implements prometheus.Collector.
func (m *Manager) Describe(ch chan<- *prometheus.Desc) {
	m.metrics.pulls.Describe(ch)
	m.metrics.pullDuration.Describe(ch)
	m.metrics.pulledBytes.Describe(ch)
	m.metrics.conversionDuration.Describe(ch)
	m.metrics.cacheLookups.Describe(ch)
	m.metrics.gcCollected.Describe(ch)
	m.metrics.gcReclaimed.Describe(ch)
	ch <- m.metrics.images
	ch <- m.metrics.disk
}

// Collect implements prometheus.Collector: the counters the Manager keeps,
// then the images it holds as they are now.
func (m *Manager) Collect(ch chan<- prometheus.Metric) {
	m.metrics.pulls.Collect(ch)
	m.metrics.pullDuration.Collect(ch)
	m.metrics.pulledBytes.Collect(ch)
	m.metrics.conversionDuration.Collect(ch)
	m.metrics.cacheLookups.Collect(ch)
	m.metrics.gcCollected.Collect(ch)
	m.metrics.gcReclaimed.Collect(ch)

	images := m.List()
	var diskBytes int64
	for _, image := range images {
		diskBytes += image.SizeBytes
	}
	ch <- metric.GaugeReading(m.metrics.images, float64(len(images)))
	ch <- metric.GaugeReading(m.metrics.disk, float64(diskBytes))
}
