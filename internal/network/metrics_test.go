// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package network

import (
	"strings"
	"testing"

	"github.com/konradasb/dicer/internal/metric/metrictest"
)

// fakeStore is a Store of the networks it holds.
type fakeStore []Network

func (s *fakeStore) Networks() []Network { return *s }

func TestMetricsMatchTheirDescriptions(t *testing.T) {
	m := newTestManager(t)
	m.store = &fakeStore{testNetwork()}

	metrictest.CheckDescriptions(t, m, MetricDescriptions())
}

// Each network's pool is read when a scrape arrives, so the gauges follow a
// filling subnet, which is what they are there to catch.
func TestAddressPoolsAreReadPerScrape(t *testing.T) {
	m := newTestManager(t)
	// A /24 has 256 addresses, of which the network, broadcast and gateway
	// addresses are not assignable: 253 can be handed out.
	nw := Network{ID: "n-1", Name: "default", Subnet: "172.20.0.0/24", Gateway: "172.20.0.1", Bridge: "dicer0"}
	m.store = &fakeStore{nw}

	for _, id := range []string{"i-1", "i-2"} {
		if _, err := m.Allocate(nw, id, ""); err != nil {
			t.Fatalf("allocate for %s: %v", id, err)
		}
	}

	body := metrictest.Scrape(t, m)
	for _, want := range []string{
		`dicer_network_addresses_allocated{network="default"} 2`,
		`dicer_network_addresses_available{network="default"} 251`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape is missing %q:\n%s", want, body)
		}
	}

	if _, err := m.Allocate(nw, "i-3", ""); err != nil {
		t.Fatal(err)
	}
	if body := metrictest.Scrape(t, m); !strings.Contains(body, `dicer_network_addresses_available{network="default"} 250`) {
		t.Errorf("second scrape did not see the address handed out:\n%s", body)
	}
}

// A host can define no networks at all, which must scrape cleanly rather
// than fail or invent a series.
func TestNoNetworksServeNoAddressPools(t *testing.T) {
	m := newTestManager(t)
	m.store = &fakeStore{}

	if body := metrictest.Scrape(t, m); strings.Contains(body, "dicer_network_addresses") {
		t.Errorf("scrape invented a series with no networks defined:\n%s", body)
	}
}
