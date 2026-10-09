// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package metric

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// Metric groups: the sections of the reference.
const (
	GroupDaemon    = "daemon"
	GroupInstances = "instances"
	// GroupInstanceStats holds what each running or paused instance uses of
	// the host.
	GroupInstanceStats = "instance_stats"
	GroupImages        = "images"
	GroupKernels       = "kernels"
	GroupVolumes       = "volumes"
	GroupNetworks      = "networks"
	GroupDNS           = "dns"
	GroupAPI           = "api"
)

// Type is the kind of a metric, as Prometheus names it.
type Type string

// The metric types.
const (
	TypeCounter   Type = "counter"
	TypeGauge     Type = "gauge"
	TypeHistogram Type = "histogram"
)

// Validate returns an error unless t is one of the metric types.
func (t Type) Validate() error {
	switch t {
	case TypeCounter, TypeGauge, TypeHistogram:
		return nil
	}
	return fmt.Errorf("unknown metric type %q", t)
}

// Description describes one of Dicer's metrics. Every metric is built from
// one, and the package that serves it lists it for the reference.
type Description struct {
	// Name is the metric's full name.
	Name string

	Type Type

	// Labels are the metric's label names.
	Labels []string

	// Help is the metric's HELP: one plain sentence.
	Help string

	// Doc is what the reference adds to Help, in Markdown.
	Doc string

	// Group is the section of the reference the metric is listed in.
	Group string
}

// validName is a metric name: dicer_ and lower-case words joined by
// underscores.
var validName = regexp.MustCompile(`^dicer(_[a-z0-9]+)+$`)

// Validate returns an error if d breaks the naming rules: a name in
// snake_case after the dicer_ prefix, _total on counters and nothing else, no
// histogram's reserved suffix, and no label Prometheus sets itself.
func (d Description) Validate() error {
	if !validName.MatchString(d.Name) {
		return fmt.Errorf("metric %q: a name is dicer_ and lower-case words joined by underscores", d.Name)
	}
	if err := d.Type.Validate(); err != nil {
		return fmt.Errorf("metric %s: %w", d.Name, err)
	}
	if d.Help == "" || d.Group == "" {
		return fmt.Errorf("metric %s: no help or group", d.Name)
	}

	isTotal := strings.HasSuffix(d.Name, "_total")
	switch {
	case d.Type == TypeCounter && !isTotal:
		return fmt.Errorf("metric %s: a counter's name ends in _total", d.Name)
	case d.Type != TypeCounter && isTotal:
		return fmt.Errorf("metric %s: only a counter's name ends in _total", d.Name)
	}
	for _, suffix := range []string{"_count", "_sum", "_bucket"} {
		if strings.HasSuffix(d.Name, suffix) {
			return fmt.Errorf("metric %s: %s is reserved for a histogram's series", d.Name, suffix)
		}
	}
	for _, label := range d.Labels {
		if label == "instance" || label == "job" {
			return fmt.Errorf("metric %s: Prometheus sets the label %s itself", d.Name, label)
		}
	}

	return nil
}

// NewCounter returns the counter d describes, which has no labels.
//
// Like every constructor here, it panics if d is invalid or describes another
// type: metrics are built at startup from descriptions in the code, so that is
// a programming error.
func NewCounter(d Description) prometheus.Counter {
	mustDescribe(d, TypeCounter)
	return prometheus.NewCounter(prometheus.CounterOpts{Name: d.Name, Help: d.Help})
}

// NewCounterVec returns the counter vector d describes.
func NewCounterVec(d Description) *prometheus.CounterVec {
	mustDescribe(d, TypeCounter)
	return prometheus.NewCounterVec(prometheus.CounterOpts{Name: d.Name, Help: d.Help}, d.Labels)
}

// NewGaugeVec returns the gauge vector d describes.
func NewGaugeVec(d Description) *prometheus.GaugeVec {
	mustDescribe(d, TypeGauge)
	return prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: d.Name, Help: d.Help}, d.Labels)
}

// NewHistogram returns the histogram d describes, which has no labels.
func NewHistogram(d Description, buckets []float64) prometheus.Histogram {
	mustDescribe(d, TypeHistogram)
	return prometheus.NewHistogram(prometheus.HistogramOpts{Name: d.Name, Help: d.Help, Buckets: buckets})
}

// NewHistogramVec returns the histogram vector d describes.
func NewHistogramVec(d Description, buckets []float64) *prometheus.HistogramVec {
	mustDescribe(d, TypeHistogram)
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: d.Name, Help: d.Help, Buckets: buckets}, d.Labels)
}

// NewDesc returns the descriptor of a gauge or counter that a collector reads
// at scrape time, from state or from counters something else keeps.
func NewDesc(d Description) *prometheus.Desc {
	mustDescribe(d, TypeGauge, TypeCounter)
	return prometheus.NewDesc(d.Name, d.Help, d.Labels, nil)
}

// mustDescribe panics unless d is valid and of one of types.
func mustDescribe(d Description, types ...Type) {
	if err := d.Validate(); err != nil {
		panic(err)
	}
	if !slices.Contains(types, d.Type) {
		panic(fmt.Sprintf("metric %s is a %s, not one of %v", d.Name, d.Type, types))
	}
}

// GaugeReading is one reading of a gauge read at scrape time. The labels come
// from the collector reading them, so a mismatch is a programming error and
// it panics.
func GaugeReading(d *prometheus.Desc, v float64, labels ...string) prometheus.Metric {
	return prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
}

// CounterReading is one reading of a counter something else keeps, such as
// the kernel, read at scrape time. It panics as GaugeReading does.
func CounterReading(d *prometheus.Desc, v float64, labels ...string) prometheus.Metric {
	return prometheus.MustNewConstMetric(d, prometheus.CounterValue, v, labels...)
}

// Outcome is the outcome label of an operation that returned err: success or
// error. It is derived from the error rather than passed by the caller, so a
// failure cannot be reported as a success by mistake.
func Outcome(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}
