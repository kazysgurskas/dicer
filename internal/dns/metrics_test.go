// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dns

import (
	"testing"

	"github.com/konradasb/dicer/internal/metric/metrictest"
)

func TestMetricsMatchTheirDescriptions(t *testing.T) {
	servers := NewServers(Config{})
	servers.metrics.recordQuery("shop", QueryLocal)
	servers.metrics.recordForward("shop", 0)

	metrictest.CheckDescriptions(t, servers, MetricDescriptions())
}
