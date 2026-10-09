// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

// Package metrictest tests the collectors packages serve their metrics with.
package metrictest

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"

	"github.com/konradasb/dicer/internal/metric"
)

// Scrape returns what c serves, in the text format a scrape reads. It
// gathers through a pedantic registry, so a collector that serves what it
// does not describe, or describes inconsistently, fails the test.
func Scrape(t testing.TB, c prometheus.Collector) string {
	t.Helper()

	registry := prometheus.NewPedanticRegistry()
	if err := registry.Register(c); err != nil {
		t.Fatal(err)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}

	var body strings.Builder
	encoder := expfmt.NewEncoder(&body, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, family := range families {
		if err := encoder.Encode(family); err != nil {
			t.Fatal(err)
		}
	}
	return body.String()
}

// CheckDescriptions fails the test unless c describes exactly the metrics
// in descriptions, each of them valid, and serves each as the type it is
// listed as. A package calls it with the metrics it lists for the reference,
// so the two cannot drift apart. A metric read at scrape time has its type
// checked only if c serves it now.
func CheckDescriptions(t testing.TB, c prometheus.Collector, descriptions []metric.Description) {
	t.Helper()

	listed := make(map[string]string, len(descriptions))
	for _, d := range descriptions {
		if err := d.Validate(); err != nil {
			t.Error(err)
		}
		listed[prometheus.NewDesc(d.Name, d.Help, d.Labels, nil).String()] = d.Name
	}

	ch := make(chan *prometheus.Desc)
	go func() {
		c.Describe(ch)
		close(ch)
	}()
	served := map[string]bool{}
	for desc := range ch {
		served[desc.String()] = true
		if _, ok := listed[desc.String()]; !ok {
			t.Errorf("served but not listed as it is served: %s", desc)
		}
	}
	for desc, name := range listed {
		if !served[desc] {
			t.Errorf("listed but not served as it is listed: %s", name)
		}
	}

	// A descriptor does not say whether a metric read at scrape time is a
	// gauge or a counter, so what is served now is checked as well.
	types := make(map[string]metric.Type, len(descriptions))
	for _, d := range descriptions {
		types[d.Name] = d.Type
	}
	registry := prometheus.NewPedanticRegistry()
	if err := registry.Register(c); err != nil {
		t.Fatal(err)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		served := metric.Type(strings.ToLower(family.GetType().String()))
		if want, ok := types[family.GetName()]; ok && served != want {
			t.Errorf("%s is listed as a %s and served as a %s", family.GetName(), want, served)
		}
	}
}
