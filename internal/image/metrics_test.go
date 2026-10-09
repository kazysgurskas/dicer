// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package image

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/konradasb/dicer/internal/metric/metrictest"
)

func TestMetricsMatchTheirDescriptions(t *testing.T) {
	m, fakeRegistry := newManagerWithFakes(t)
	pullTestImage(t, m, fakeRegistry, "docker.io/library/nginx:1.27", "sha256:aaa")

	metrictest.CheckDescriptions(t, m, MetricDescriptions())
}

// A pull is counted when it goes to a registry; asking again for an image
// already held is a cache hit, not a pull.
func TestPullsAndCacheLookupsAreCounted(t *testing.T) {
	m, fakeRegistry := newManagerWithFakes(t)
	pullTestImage(t, m, fakeRegistry, "docker.io/library/nginx:1.27", "sha256:aaa")
	pullTestImage(t, m, fakeRegistry, "docker.io/library/nginx:1.27", "sha256:aaa")

	for _, tt := range []struct {
		name string
		got  float64
		want float64
	}{
		{"successful pulls", testutil.ToFloat64(m.metrics.pulls.WithLabelValues("success")), 1},
		{"cache misses", testutil.ToFloat64(m.metrics.cacheLookups.WithLabelValues("miss")), 1},
		{"cache hits", testutil.ToFloat64(m.metrics.cacheLookups.WithLabelValues("hit")), 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

// The images held are read when a scrape arrives, so the gauge follows
// pulls rather than what was held at startup.
func TestImagesAreReadPerScrape(t *testing.T) {
	m, fakeRegistry := newManagerWithFakes(t)

	if body := metrictest.Scrape(t, m); !strings.Contains(body, "dicer_images 0") {
		t.Errorf("scrape of an empty store is missing dicer_images 0:\n%s", body)
	}

	pullTestImage(t, m, fakeRegistry, "docker.io/library/nginx:1.27", "sha256:aaa")
	if body := metrictest.Scrape(t, m); !strings.Contains(body, "dicer_images 1") {
		t.Errorf("scrape after a pull is missing dicer_images 1:\n%s", body)
	}
}
