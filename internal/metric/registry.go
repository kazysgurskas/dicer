// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

// Package metric serves the daemon's Prometheus metrics, and is how the rest
// of the daemon defines its own. A package builds its metrics from
// Descriptions, lists them for the reference, and serves them as a
// prometheus.Collector the daemon registers on a Registry.
package metric

import (
	"log/slog"
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

var buildInfoMetric = Description{
	Name:   "dicer_build_info",
	Type:   TypeGauge,
	Labels: []string{"version", "commit", "go_version"},
	Help:   "Build information for the running daemon. Always 1.",
	Doc:    "The build is in its labels.",
	Group:  GroupDaemon,
}

// Descriptions returns the descriptions of the metrics a Registry serves of
// its own: build information.
func Descriptions() []Description {
	return []Description{buildInfoMetric}
}

// Config configures a Registry.
type Config struct {
	// Version and Commit identify the build, reported as labels on
	// dicer_build_info.
	Version string
	Commit  string

	// Logger receives errors encountered while serving a scrape. Defaults
	// to slog.Default.
	Logger *slog.Logger
}

// Registry holds the metrics the daemon serves: build information, the Go
// runtime's and the process's, and those of every collector registered on
// it. It is safe for concurrent use.
type Registry struct {
	registry *prometheus.Registry
	logger   *slog.Logger
}

// New returns a Registry with this package's own metrics registered.
func New(cfg Config) *Registry {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	r := &Registry{registry: prometheus.NewRegistry(), logger: cfg.Logger}
	r.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	// The conventional info metric: always 1, carrying the build in its
	// labels so a dashboard can join on it or alert on a version change.
	buildInfo := NewGaugeVec(buildInfoMetric)
	buildInfo.WithLabelValues(cfg.Version, cfg.Commit, runtime.Version()).Set(1)
	r.registry.MustRegister(buildInfo)

	return r
}

// Register adds c's metrics to what the registry serves. Every caller is the
// daemon registering a collector it built, so a clash is a programming error
// and Register panics.
func (r *Registry) Register(c prometheus.Collector) {
	r.registry.MustRegister(c)
}
