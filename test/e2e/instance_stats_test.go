// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build e2e

package e2e

import (
	"testing"
)

// statsView is what the tests read of an instance's record in dicer stats
// --format json.
type statsView struct {
	Name                string `json:"name"`
	MemoryBytes         int64  `json:"memory_bytes"`
	ResidentMemoryBytes int64  `json:"resident_memory_bytes"`
	DiskWrittenBytes    int64  `json:"disk_written_bytes"`
}

// TestInstanceStatsFollowWhatTheGuestDoes makes a guest use CPU, disk and
// network, and checks that what the host reads of its VMM
// and TAP device moves with it.
//
// The unit tests read a fake /proc. Only a real VM shows that the VMM's
// process is the one read, that its CPU time includes the vCPUs, that the
// guest's disk writes reach the host as the VMM's, and which end of the TAP
// device is the guest's.
func TestInstanceStatsFollowWhatTheGuestDoes(t *testing.T) {
	name := instanceName(t)

	env.createInstance(t, name)
	env.startInstance(t, name)
	env.waitForAgent(t, name)

	id := env.instance(t, name).ID
	if id == "" {
		t.Fatalf("instance %s has no ID", name)
	}
	labels := `{instance_id="` + id + `",name="` + name + `"}`
	var (
		cpu      = "dicer_instance_cpu_seconds_total" + labels
		resident = "dicer_instance_resident_memory_bytes" + labels
		written  = "dicer_instance_disk_written_bytes_total" + labels
		transmit = "dicer_instance_network_transmit_bytes_total" + labels
	)

	cpuBefore := env.seriesValue(t, cpu)
	writtenBefore := env.seriesValue(t, written)
	transmitBefore := env.seriesValue(t, transmit)

	// Two seconds of one vCPU kept busy, ending as timeout kills it.
	_, _ = env.tryExec(t, name, "timeout", "2", "sh", "-c", "while :; do :; done")
	if got := env.seriesValue(t, cpu) - cpuBefore; got < 1.5 {
		t.Errorf("CPU time grew by %vs over 2s of a busy vCPU, want at least 1.5s", got)
	}

	// Synced, so the writes leave the guest's page cache for its disk.
	env.exec(t, name, "dd", "if=/dev/zero", "of=/stats", "bs=1M", "count=32", "conv=fsync")
	if got := env.seriesValue(t, written) - writtenBefore; got < 32<<20 {
		t.Errorf("disk writes grew by %v bytes after the guest wrote 32MiB, want at least that", got)
	}

	// Whether or not anything answers, the packets leave the guest.
	_, _ = env.tryExec(t, name, "ping", "-c", "3", "-W", "1", "192.0.2.1")
	if got := env.seriesValue(t, transmit) - transmitBefore; got <= 0 {
		t.Errorf("transmitted bytes grew by %v after the guest sent pings, want more", got)
	}

	if got := env.seriesValue(t, resident); got < 16<<20 {
		t.Errorf("resident memory = %v bytes for a booted guest, want at least 16MiB", got)
	}

	out := env.dicer(t, "stats", "--no-stream", "--format", "json", name)
	stats := rows[statsView](t, out, "dicer stats")
	if len(stats) != 1 || stats[0].Name != name {
		t.Fatalf("dicer stats %s = %+v, want the one instance", name, stats)
	}
	if stats[0].MemoryBytes == 0 || stats[0].ResidentMemoryBytes == 0 {
		t.Errorf("stats = %+v, want the guest's committed and resident memory", stats[0])
	}
	if stats[0].DiskWrittenBytes == 0 {
		t.Error("disk written = 0 bytes, want the 32MiB the guest wrote counted")
	}

	env.dicer(t, "instance", "stop", name)
	env.waitForState(t, name, "Stopped")

	// A stopped instance has no series, rather than a stale one.
	if got := env.seriesValue(t, resident); got != 0 {
		t.Errorf("resident memory = %v after the instance stopped, want no series", got)
	}
}
