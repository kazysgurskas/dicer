// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"context"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/konradasb/dicer/internal/metric/metrictest"
	"github.com/konradasb/dicer/internal/network"
)

func TestMetricsMatchTheirDescriptions(t *testing.T) {
	manager, _, _ := newTestManager(t)

	metrictest.CheckDescriptions(t, manager, MetricDescriptions())
}

func TestOperationsAreRecordedWithTheirOutcome(t *testing.T) {
	manager, store, _ := newTestManager(t)

	instance := Spec{ID: "i-1", Name: "web", VCPUs: 1, MemoryBytes: 1 << 30}
	store.instances[instance.Name] = instance

	// Stopping an already-stopped instance succeeds, and is still an
	// operation that happened.
	if err := manager.Stop(context.Background(), instance); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Pausing one that is not running fails before it touches the host,
	// which is exactly the kind of failure the counter should catch.
	if err := manager.Pause(context.Background(), instance); err == nil {
		t.Fatal("Pause on a stopped instance should fail")
	}

	for _, tt := range []struct {
		operation, outcome string
		want               float64
	}{
		{operationStop, "success", 1},
		{operationPause, "error", 1},
		{operationPause, "success", 0},
	} {
		t.Run(tt.operation+" "+tt.outcome, func(t *testing.T) {
			got := testutil.ToFloat64(manager.metrics.operations.WithLabelValues(tt.operation, tt.outcome))
			if got != tt.want {
				t.Errorf("operations = %v, want %v", got, tt.want)
			}
		})
	}
	if got := testutil.CollectAndCount(manager.metrics.duration); got != 2 {
		t.Errorf("duration series = %d, want 2, one per operation", got)
	}
}

// The usage gauges are read when a scrape arrives, so they follow the
// instances rather than what they were at startup.
func TestUsageIsReadPerScrape(t *testing.T) {
	h := newHarness(t)
	h.start(t)

	body := metrictest.Scrape(t, h.manager)
	for _, want := range []string{
		`dicer_instances{state="running"} 1`,
		`dicer_instances{state="stopped"} 0`,
		"dicer_instances_vcpus 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape is missing %q:\n%s", want, body)
		}
	}

	if err := h.manager.Stop(t.Context(), h.instance); err != nil {
		t.Fatal(err)
	}
	if body := metrictest.Scrape(t, h.manager); !strings.Contains(body, `dicer_instances{state="stopped"} 1`) {
		t.Errorf("second scrape did not see the instance stop:\n%s", body)
	}
}

// Each running instance's stats are served as what its VMM has used, the
// kernel's counters as counters.
func TestStatsAreServedPerInstance(t *testing.T) {
	h := newHarness(t)
	h.start(t)

	proc := newFakeProc(t, h.manager)
	proc.process(t, h.starter.vmm().PID(), 250, 50, 1000, 4096, 8192)
	proc.device(t, network.TAPName(h.instance.ID),
		deviceCounters{bytes: 300}, deviceCounters{bytes: 700})

	labels := `{instance_id="` + h.instance.ID + `",name="` + h.instance.Name + `"}`
	body := metrictest.Scrape(t, h.manager)
	for _, want := range []string{
		"# TYPE dicer_instance_cpu_seconds_total counter",
		"dicer_instance_cpu_seconds_total" + labels + " 3",
		"# TYPE dicer_instance_resident_memory_bytes gauge",
		"dicer_instance_disk_read_bytes_total" + labels + " 4096",
		"dicer_instance_disk_written_bytes_total" + labels + " 8192",
		"dicer_instance_network_receive_bytes_total" + labels + " 700",
		"dicer_instance_network_transmit_bytes_total" + labels + " 300",
		"dicer_instance_vcpus" + labels + " 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape is missing %q:\n%s", want, body)
		}
	}

	// A stopped instance has no series rather than a stale one.
	if err := h.manager.Stop(t.Context(), h.instance); err != nil {
		t.Fatal(err)
	}
	if body := metrictest.Scrape(t, h.manager); strings.Contains(body, "dicer_instance_cpu_seconds_total{") {
		t.Errorf("a stopped instance still has stats:\n%s", body)
	}
}
