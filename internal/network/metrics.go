// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package network

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/konradasb/dicer/internal/metric"
)

// The metrics of each network's address pool.
var (
	addressesAllocatedMetric = metric.Description{
		Name:   "dicer_network_addresses_allocated",
		Type:   metric.TypeGauge,
		Labels: []string{"network"},
		Help:   "Addresses currently assigned to instances on a network.",
		Group:  metric.GroupNetworks,
	}

	addressesAvailableMetric = metric.Description{
		Name:   "dicer_network_addresses_available",
		Type:   metric.TypeGauge,
		Labels: []string{"network"},
		Help:   "Assignable addresses still free on a network.",
		Doc:    "The network, broadcast and gateway addresses are not counted.",
		Group:  metric.GroupNetworks,
	}
)

// MetricDescriptions returns the descriptions of every metric the network
// manager serves, for the reference.
func MetricDescriptions() []metric.Description {
	return []metric.Description{addressesAllocatedMetric, addressesAvailableMetric}
}

// Store lists the networks defined on this host.
type Store interface {
	Networks() []Network
}

// metrics are the descriptors of what a Manager reads at scrape time.
type metrics struct {
	allocated *prometheus.Desc
	available *prometheus.Desc
}

func newMetrics() metrics {
	return metrics{
		allocated: metric.NewDesc(addressesAllocatedMetric),
		available: metric.NewDesc(addressesAvailableMetric),
	}
}

// Describe implements prometheus.Collector.
func (m *Manager) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.metrics.allocated
	ch <- m.metrics.available
}

// Collect implements prometheus.Collector: each defined network's address
// pool, as it is now. A network whose allocations cannot be read is left out.
func (m *Manager) Collect(ch chan<- prometheus.Metric) {
	if m.store == nil {
		return
	}

	for _, network := range m.store.Networks() {
		allocations, err := m.List(network.Name)
		if err != nil {
			m.logger.Warn("cannot read allocations for metrics", "network", network.Name, "error", err)
			continue
		}

		_, available := network.IPCounts(len(allocations))
		ch <- metric.GaugeReading(m.metrics.allocated, float64(len(allocations)), network.Name)
		ch <- metric.GaugeReading(m.metrics.available, float64(available), network.Name)
	}
}
