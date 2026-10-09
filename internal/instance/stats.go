// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"fmt"
	"time"

	"github.com/prometheus/procfs"

	"github.com/konradasb/dicer/internal/network"
)

// Stats is what an instance's VMM process uses of the host, read from the host
// at one moment. Its vCPUs and the threads emulating its devices are counted
// together; totals are since the VMM started.
type Stats struct {
	InstanceID string `json:"instance_id"`
	Name       string `json:"name"`

	// StartedAt is when the VMM was started. Totals read with another
	// StartedAt are of another VMM, and cannot be compared.
	StartedAt time.Time `json:"started_at"`

	// ReadAt is when the stats were read.
	ReadAt time.Time `json:"read_at"`

	// Committed is the vCPUs and guest memory committed to the instance.
	Committed Resources `json:"committed"`

	// CPUTime is the CPU time the VMM has used, user and system.
	CPUTime time.Duration `json:"cpu_time"`

	// ResidentMemoryBytes is the VMM's resident host memory: the guest
	// memory backed so far and its own. Memory a guest frees stays resident.
	ResidentMemoryBytes int64 `json:"resident_memory_bytes"`

	// DiskReadBytes and DiskWrittenBytes are what the VMM process read from
	// and wrote to storage: the guest's disks, and the VMM's own files, such
	// as the serial console log and a snapshot's memory. Reads served from
	// the page cache are not counted; writes are counted as the VMM makes
	// them, before they reach the disk.
	DiskReadBytes    int64 `json:"disk_read_bytes"`
	DiskWrittenBytes int64 `json:"disk_written_bytes"`

	// NetworkReceiveBytes and NetworkTransmitBytes are what the guest
	// received and transmitted on its network interface.
	NetworkReceiveBytes  int64 `json:"network_receive_bytes"`
	NetworkTransmitBytes int64 `json:"network_transmit_bytes"`

	// NetworkReceivePackets and NetworkTransmitPackets are the packets the
	// guest received and transmitted.
	NetworkReceivePackets  int64 `json:"network_receive_packets"`
	NetworkTransmitPackets int64 `json:"network_transmit_packets"`

	// NetworkReceiveDrops and NetworkTransmitDrops are the packets dropped on
	// their way to and from the guest. Receive drops mostly mean the guest
	// does not take packets as fast as they come.
	NetworkReceiveDrops  int64 `json:"network_receive_drops"`
	NetworkTransmitDrops int64 `json:"network_transmit_drops"`

	// NetworkReceiveErrors and NetworkTransmitErrors are the packets to and
	// from the guest that failed with an error.
	NetworkReceiveErrors  int64 `json:"network_receive_errors"`
	NetworkTransmitErrors int64 `json:"network_transmit_errors"`
}

// CPUPercent returns the CPU s used since prev, as a percentage of one host
// CPU: 200 is two CPUs kept busy. It is false if prev was read from another
// VMM, or not before s.
func (s Stats) CPUPercent(prev Stats) (float64, bool) {
	elapsed := s.ReadAt.Sub(prev.ReadAt)
	if !s.StartedAt.Equal(prev.StartedAt) || elapsed <= 0 {
		return 0, false
	}

	return float64(s.CPUTime-prev.CPUTime) / float64(elapsed) * 100, true
}

// Stats are read from the host alone, so they need nothing of the guest and
// are the same for every hypervisor: CPU, memory and disk from the VMM's
// entries in /proc, network from its TAP device's counters.

// Stats returns what the VMM of each running or paused instance uses of the
// host now, in name order. An instance whose VMM cannot be read is left out.
func (m *Manager) Stats() []Stats {
	instances := m.store.Instances()

	proc, err := procfs.NewFS(m.procDir)
	if err != nil {
		m.logger.Warn("cannot read stats", "error", err)
		return nil
	}
	devices, err := proc.NetDev()
	if err != nil {
		m.logger.Warn("cannot read network device stats", "error", err)
		return nil
	}

	var stats []Stats
	for _, instance := range instances {
		vmm := m.vmm(instance.ID)
		if vmm == nil {
			continue
		}

		instanceStats, err := m.readStats(proc, devices, instance, vmm.PID())

		// Once the VMM has exited its PID may be another process's, so
		// what was read cannot be trusted, nor is it missed.
		select {
		case <-vmm.Done():
			continue
		default:
		}
		if err != nil {
			m.logger.Warn("cannot read instance stats", "instance_id", instance.ID, "error", err)
			continue
		}

		stats = append(stats, instanceStats)
	}

	return stats
}

// readStats reads what the VMM at pid uses of the host for instance.
func (m *Manager) readStats(
	proc procfs.FS, devices procfs.NetDev, instance Spec, pid int,
) (Stats, error) {
	status, err := m.statusOf(instance)
	if err != nil {
		return Stats{}, err
	}

	process, err := proc.Proc(pid)
	if err != nil {
		return Stats{}, fmt.Errorf("read hypervisor process %d: %w", pid, err)
	}
	stat, err := process.Stat()
	if err != nil {
		return Stats{}, fmt.Errorf("read hypervisor process %d: %w", pid, err)
	}
	processIO, err := process.IO()
	if err != nil {
		return Stats{}, fmt.Errorf("read hypervisor process %d: %w", pid, err)
	}

	// The TAP device is the host's end of the guest's interface: what it
	// transmits, the guest receives, and what it fails to transmit, the
	// guest never receives.
	tap := devices[network.TAPName(instance.ID)]

	return Stats{
		InstanceID:             instance.ID,
		Name:                   instance.Name,
		StartedAt:              status.StartedAt,
		ReadAt:                 time.Now(),
		Committed:              status.HeldResources(),
		CPUTime:                time.Duration(stat.CPUTime() * float64(time.Second)),
		ResidentMemoryBytes:    int64(stat.ResidentMemory()),
		DiskReadBytes:          int64(processIO.ReadBytes),
		DiskWrittenBytes:       int64(processIO.WriteBytes),
		NetworkReceiveBytes:    int64(tap.TxBytes),
		NetworkTransmitBytes:   int64(tap.RxBytes),
		NetworkReceivePackets:  int64(tap.TxPackets),
		NetworkTransmitPackets: int64(tap.RxPackets),
		NetworkReceiveDrops:    int64(tap.TxDropped),
		NetworkTransmitDrops:   int64(tap.RxDropped),
		NetworkReceiveErrors:   int64(tap.TxErrors),
		NetworkTransmitErrors:  int64(tap.RxErrors),
	}, nil
}
