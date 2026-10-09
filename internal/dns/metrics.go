// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dns

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/konradasb/dicer/internal/metric"
)

// The metrics of what the networks' servers answer.
var (
	queriesMetric = metric.Description{
		Name:   "dicer_dns_queries_total",
		Type:   metric.TypeCounter,
		Labels: []string{"network", "result"},
		Help:   "DNS queries guests sent a network's server, by how they were answered.",
		Doc: "`result` is `local`, from the network's own names, found or not; `forwarded`, " +
			"answered by an upstream nameserver; `failed`, by none, with SERVFAIL; `invalid`, " +
			"not one query; or `dropped`, for the server being too busy. A TCP connection closed " +
			"for that counts as one query dropped.",
		Group: metric.GroupDNS,
	}

	forwardDurationMetric = metric.Description{
		Name:   "dicer_dns_forward_duration_seconds",
		Type:   metric.TypeHistogram,
		Labels: []string{"network"},
		Help:   "Time asking a network's upstream nameservers for an answer took, answered or not.",
		Doc:    "Each is asked in turn until one answers, for up to 3 seconds each.",
		Group:  metric.GroupDNS,
	}
)

// MetricDescriptions returns the descriptions of every metric the DNS servers
// serve, for the reference.
func MetricDescriptions() []metric.Description {
	return []metric.Description{queriesMetric, forwardDurationMetric}
}

// metrics are the counters the servers keep. Each network's server holds a
// copy, and the copies share their vectors. It is safe for concurrent use.
type metrics struct {
	queries         *prometheus.CounterVec
	forwardDuration *prometheus.HistogramVec
}

func newMetrics() metrics {
	return metrics{
		queries:         metric.NewCounterVec(queriesMetric),
		forwardDuration: metric.NewHistogramVec(forwardDurationMetric, prometheus.ExponentialBuckets(0.001, 2, 14)),
	}
}

// recordQuery records a query to a network's server, and how it was
// answered: QueryLocal, QueryForwarded, QueryFailed, QueryInvalid or
// QueryDropped.
func (m metrics) recordQuery(network, result string) {
	m.queries.WithLabelValues(network, result).Inc()
}

// recordForward records how long asking a network's upstream nameservers
// took, answered or not.
func (m metrics) recordForward(network string, d time.Duration) {
	m.forwardDuration.WithLabelValues(network).Observe(d.Seconds())
}

// Describe implements prometheus.Collector.
func (s *Servers) Describe(ch chan<- *prometheus.Desc) {
	s.metrics.queries.Describe(ch)
	s.metrics.forwardDuration.Describe(ch)
}

// Collect implements prometheus.Collector.
func (s *Servers) Collect(ch chan<- prometheus.Metric) {
	s.metrics.queries.Collect(ch)
	s.metrics.forwardDuration.Collect(ch)
}
