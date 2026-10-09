// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"time"

	"google.golang.org/grpc"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// InstanceStats is what an instance's hypervisor process uses of the host,
// read from the process rather than asked of the guest. Its vCPUs and the
// threads emulating its devices are counted together, and totals are since
// the process started: each start of the instance begins them again.
type InstanceStats struct {
	// Name is the instance's name.
	Name string `json:"name,omitzero"`

	// ID is the instance's ID.
	ID string `json:"id,omitzero"`

	// CPUPercent is the CPU used over the last second, as a percentage of
	// one host CPU: 200 is two CPUs kept busy.
	CPUPercent float64 `json:"cpu_percent,omitzero"`

	// CPUTime is the CPU time used in total.
	CPUTime time.Duration `json:"cpu_time,omitzero"`

	// VCPUs is how many vCPUs are committed to the instance.
	VCPUs int `json:"vcpus,omitzero"`

	// MemoryBytes is how much memory is committed to the instance.
	MemoryBytes int64 `json:"memory_bytes,omitzero"`

	// ResidentMemoryBytes is the resident host memory of the instance's
	// hypervisor process: the guest memory backed so far, and the
	// hypervisor's own. Memory a guest frees stays resident.
	ResidentMemoryBytes int64 `json:"resident_memory_bytes,omitzero"`

	// DiskReadBytes is how many bytes the hypervisor process read from
	// storage. Reads served from the host's page cache are not counted.
	DiskReadBytes int64 `json:"disk_read_bytes,omitzero"`

	// DiskWrittenBytes is how many bytes the hypervisor process wrote to
	// storage.
	DiskWrittenBytes int64 `json:"disk_written_bytes,omitzero"`

	// NetworkReceiveBytes is how many bytes the guest received on its network
	// interface.
	NetworkReceiveBytes int64 `json:"network_receive_bytes,omitzero"`

	// NetworkTransmitBytes is how many bytes the guest transmitted on its
	// network interface.
	NetworkTransmitBytes int64 `json:"network_transmit_bytes,omitzero"`

	// NetworkReceivePackets is how many packets the guest received on its
	// network interface.
	NetworkReceivePackets int64 `json:"network_receive_packets,omitzero"`

	// NetworkTransmitPackets is how many packets the guest transmitted on its
	// network interface.
	NetworkTransmitPackets int64 `json:"network_transmit_packets,omitzero"`

	// NetworkReceiveDrops is how many packets were dropped on their way to
	// the guest. They mostly mean the guest does not take packets as fast as
	// they come.
	NetworkReceiveDrops int64 `json:"network_receive_drops,omitzero"`

	// NetworkTransmitDrops is how many packets were dropped on their way from
	// the guest.
	NetworkTransmitDrops int64 `json:"network_transmit_drops,omitzero"`

	// NetworkReceiveErrors is how many packets to the guest failed with an
	// error.
	NetworkReceiveErrors int64 `json:"network_receive_errors,omitzero"`

	// NetworkTransmitErrors is how many packets from the guest failed with an
	// error.
	NetworkTransmitErrors int64 `json:"network_transmit_errors,omitzero"`
}

// InstanceStatsOptions pick the instances Instances.Stats reports on.
type InstanceStatsOptions struct {
	// Names are the instances to report on, by name or ID. Empty means
	// every instance running or paused, as they come and go. A named
	// instance is left out of a batch while it is not running or paused.
	Names []string

	// Follow keeps the stream open, sending a batch a second. Otherwise a
	// single batch is sent.
	Follow bool
}

// InstanceStatsBatch is what the instances used over one second.
type InstanceStatsBatch struct {
	// ReadTime is when the batch was read.
	ReadTime time.Time `json:"read_time,omitzero"`

	// Instances are the instances, in name order.
	Instances []InstanceStats `json:"instances,omitzero"`
}

// InstanceStatsStream is a stream of instance stats, read with Next.
type InstanceStatsStream struct {
	stream grpc.ServerStreamingClient[dicerdv1.GetInstanceStatsResponse]
	cancel context.CancelFunc
}

// Stats streams what running and paused instances use of the host. Each
// batch is read over a second, so the first comes a second after the call.
// The stream must be closed when done.
func (s *Instances) Stats(ctx context.Context, opts InstanceStatsOptions) (*InstanceStatsStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	stream, err := s.api.GetInstanceStats(ctx, &dicerdv1.GetInstanceStatsRequest{
		Names:  opts.Names,
		Follow: opts.Follow,
	})
	if err != nil {
		cancel()
		return nil, fromStatus(err)
	}

	return &InstanceStatsStream{stream: stream, cancel: cancel}, nil
}

// Next returns the next batch. It returns io.EOF after the last, and the
// error the stream failed with otherwise, such as context.Canceled once it
// is closed.
func (s *InstanceStatsStream) Next() (InstanceStatsBatch, error) {
	resp, err := s.stream.Recv()
	if err != nil {
		return InstanceStatsBatch{}, fromStatus(err)
	}
	return instanceStatsBatchFromProto(resp), nil
}

// Close ends the stream.
func (s *InstanceStatsStream) Close() error {
	s.cancel()
	return nil
}

// instanceStatsBatchFromProto returns the batch p carries.
func instanceStatsBatchFromProto(p *dicerdv1.GetInstanceStatsResponse) InstanceStatsBatch {
	return InstanceStatsBatch{
		ReadTime:  timeFromProto(p.GetReadTime()),
		Instances: convertAll(p.GetInstances(), instanceStatsFromProto),
	}
}

// instanceStatsFromProto returns the stats p reports.
func instanceStatsFromProto(p *dicerdv1.InstanceStats) InstanceStats {
	return InstanceStats{
		Name:                   p.GetName(),
		ID:                     p.GetId(),
		CPUPercent:             p.GetCpuPercent(),
		CPUTime:                durationFromProto(p.GetCpuTime()),
		VCPUs:                  int(p.GetVcpus()),
		MemoryBytes:            p.GetMemoryBytes(),
		ResidentMemoryBytes:    p.GetResidentMemoryBytes(),
		DiskReadBytes:          p.GetDiskReadBytes(),
		DiskWrittenBytes:       p.GetDiskWrittenBytes(),
		NetworkReceiveBytes:    p.GetNetworkReceiveBytes(),
		NetworkTransmitBytes:   p.GetNetworkTransmitBytes(),
		NetworkReceivePackets:  p.GetNetworkReceivePackets(),
		NetworkTransmitPackets: p.GetNetworkTransmitPackets(),
		NetworkReceiveDrops:    p.GetNetworkReceiveDrops(),
		NetworkTransmitDrops:   p.GetNetworkTransmitDrops(),
		NetworkReceiveErrors:   p.GetNetworkReceiveErrors(),
		NetworkTransmitErrors:  p.GetNetworkTransmitErrors(),
	}
}
