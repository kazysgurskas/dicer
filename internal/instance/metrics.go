// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/konradasb/dicer/internal/metric"
)

// The operation names the operations metric is labelled with.
const (
	operationStart           = "start"
	operationStop            = "stop"
	operationPause           = "pause"
	operationResume          = "resume"
	operationStandby         = "standby"
	operationResize          = "resize"
	operationDelete          = "delete"
	operationCreateSnapshot  = "create_snapshot"
	operationRestoreSnapshot = "restore_snapshot"
	operationDeleteSnapshot  = "delete_snapshot"
	operationForkSnapshot    = "fork_snapshot"
	operationForkInstance    = "fork_instance"
)

// The metrics of the instances and their lifecycle.
var (
	instancesMetric = metric.Description{
		Name:   "dicer_instances",
		Type:   metric.TypeGauge,
		Labels: []string{"state"},
		Help:   "Instances defined on this host, by lifecycle state.",
		Doc:    "Every state is present, at 0 if none.",
		Group:  metric.GroupInstances,
	}

	instancesHealthMetric = metric.Description{
		Name:   "dicer_instances_health",
		Type:   metric.TypeGauge,
		Labels: []string{"status"},
		Help:   "Running instances whose health is checked, by what the check has found.",
		Doc:    "`status` is `starting`, `healthy` or `unhealthy`.",
		Group:  metric.GroupInstances,
	}

	instancesVCPUsMetric = metric.Description{
		Name:  "dicer_instances_vcpus",
		Type:  metric.TypeGauge,
		Help:  "vCPUs committed to instances that are starting, running or paused.",
		Group: metric.GroupInstances,
	}

	instancesMemoryMetric = metric.Description{
		Name:  "dicer_instances_memory_bytes",
		Type:  metric.TypeGauge,
		Help:  "Guest memory committed to instances that are starting, running or paused.",
		Group: metric.GroupInstances,
	}

	instancesVCPUsAllocatableMetric = metric.Description{
		Name:  "dicer_instances_vcpus_allocatable",
		Type:  metric.TypeGauge,
		Help:  "vCPUs instances may be committed in total; a start beyond it is refused.",
		Group: metric.GroupInstances,
	}

	instancesMemoryAllocatableMetric = metric.Description{
		Name:  "dicer_instances_memory_allocatable_bytes",
		Type:  metric.TypeGauge,
		Help:  "Guest memory instances may be committed in total; a start beyond it is refused.",
		Group: metric.GroupInstances,
	}

	operationsMetric = metric.Description{
		Name:   "dicer_instance_operations_total",
		Type:   metric.TypeCounter,
		Labels: []string{"operation", "outcome"},
		Help: "Instance lifecycle operations by operation " +
			"(start, stop, pause, resume, standby, resize, delete, create_snapshot, restore_snapshot, delete_snapshot, fork_snapshot, fork_instance) and outcome.",
		Doc:   "`outcome` is `success` or `error`.",
		Group: metric.GroupInstances,
	}

	// A start pulls an image and boots a guest; a stop waits on an ACPI shutdown.
	// Seconds, not milliseconds, is the right scale.
	operationDurationMetric = metric.Description{
		Name:   "dicer_instance_operation_duration_seconds",
		Type:   metric.TypeHistogram,
		Labels: []string{"operation"},
		Help:   "Time an instance lifecycle operation took.",
		Group:  metric.GroupInstances,
	}

	restartsMetric = metric.Description{
		Name:  "dicer_instance_restarts_total",
		Type:  metric.TypeCounter,
		Help:  "Instances started again by their restart policy after they ended.",
		Group: metric.GroupInstances,
	}
)

// instanceStatsLabels identify the instance a series is of. The ID stays the
// same across a rename; the name is what a person looks for.
var instanceStatsLabels = []string{"instance_id", "name"}

// The metrics of what each running or paused instance's VMM uses of the host.
// Its counters are the kernel's, read at scrape time, and begin again each
// time an instance starts, as Prometheus expects of a counter whose process
// restarts.
var (
	cpuMetric = metric.Description{
		Name:   "dicer_instance_cpu_seconds_total",
		Type:   metric.TypeCounter,
		Labels: instanceStatsLabels,
		Help:   "CPU time an instance's hypervisor process has used, user and system.",
		Doc:    "Its vCPUs and the threads emulating its devices together. `rate()` of it is the host CPUs it keeps busy.",
		Group:  metric.GroupInstanceStats,
	}

	vcpusMetric = metric.Description{
		Name:   "dicer_instance_vcpus",
		Type:   metric.TypeGauge,
		Labels: instanceStatsLabels,
		Help:   "vCPUs committed to an instance.",
		Doc:    "`rate(dicer_instance_cpu_seconds_total[1m]) / dicer_instance_vcpus` is how busy it keeps them.",
		Group:  metric.GroupInstanceStats,
	}

	residentMemoryMetric = metric.Description{
		Name:   "dicer_instance_resident_memory_bytes",
		Type:   metric.TypeGauge,
		Labels: instanceStatsLabels,
		Help:   "Host memory resident for an instance's hypervisor process.",
		Doc:    "The guest memory backed so far, and the hypervisor's own. Memory a guest frees stays resident.",
		Group:  metric.GroupInstanceStats,
	}

	memoryMetric = metric.Description{
		Name:   "dicer_instance_memory_bytes",
		Type:   metric.TypeGauge,
		Labels: instanceStatsLabels,
		Help:   "Guest memory committed to an instance.",
		Doc:    "`dicer_instance_resident_memory_bytes / dicer_instance_memory_bytes` is how much of it is resident.",
		Group:  metric.GroupInstanceStats,
	}

	diskReadMetric = metric.Description{
		Name:   "dicer_instance_disk_read_bytes_total",
		Type:   metric.TypeCounter,
		Labels: instanceStatsLabels,
		Help:   "Bytes an instance's hypervisor process read from storage.",
		Doc:    "The instance's disks, and the hypervisor's own files, such as the serial console log and a snapshot's memory. Reads served from the host's page cache are not counted.",
		Group:  metric.GroupInstanceStats,
	}

	diskWrittenMetric = metric.Description{
		Name:   "dicer_instance_disk_written_bytes_total",
		Type:   metric.TypeCounter,
		Labels: instanceStatsLabels,
		Help:   "Bytes an instance's hypervisor process wrote to storage.",
		Doc:    "The instance's disks, and the hypervisor's own files, such as the serial console log and a snapshot's memory. Counted as the process writes, before the data reaches the disk.",
		Group:  metric.GroupInstanceStats,
	}

	networkReceiveMetric = metric.Description{
		Name:   "dicer_instance_network_receive_bytes_total",
		Type:   metric.TypeCounter,
		Labels: instanceStatsLabels,
		Help:   "Bytes an instance's guest received on its network interface.",
		Group:  metric.GroupInstanceStats,
	}

	networkTransmitMetric = metric.Description{
		Name:   "dicer_instance_network_transmit_bytes_total",
		Type:   metric.TypeCounter,
		Labels: instanceStatsLabels,
		Help:   "Bytes an instance's guest transmitted on its network interface.",
		Group:  metric.GroupInstanceStats,
	}

	networkReceivePacketsMetric = metric.Description{
		Name:   "dicer_instance_network_receive_packets_total",
		Type:   metric.TypeCounter,
		Labels: instanceStatsLabels,
		Help:   "Packets an instance's guest received on its network interface.",
		Group:  metric.GroupInstanceStats,
	}

	networkTransmitPacketsMetric = metric.Description{
		Name:   "dicer_instance_network_transmit_packets_total",
		Type:   metric.TypeCounter,
		Labels: instanceStatsLabels,
		Help:   "Packets an instance's guest transmitted on its network interface.",
		Group:  metric.GroupInstanceStats,
	}

	networkReceiveDropsMetric = metric.Description{
		Name:   "dicer_instance_network_receive_drops_total",
		Type:   metric.TypeCounter,
		Labels: instanceStatsLabels,
		Help:   "Packets dropped on their way to an instance's guest.",
		Doc:    "Mostly the guest not taking packets as fast as they come.",
		Group:  metric.GroupInstanceStats,
	}

	networkTransmitDropsMetric = metric.Description{
		Name:   "dicer_instance_network_transmit_drops_total",
		Type:   metric.TypeCounter,
		Labels: instanceStatsLabels,
		Help:   "Packets an instance's guest transmitted that the host dropped.",
		Group:  metric.GroupInstanceStats,
	}

	networkReceiveErrorsMetric = metric.Description{
		Name:   "dicer_instance_network_receive_errors_total",
		Type:   metric.TypeCounter,
		Labels: instanceStatsLabels,
		Help:   "Packets to an instance's guest that failed with an error.",
		Group:  metric.GroupInstanceStats,
	}

	networkTransmitErrorsMetric = metric.Description{
		Name:   "dicer_instance_network_transmit_errors_total",
		Type:   metric.TypeCounter,
		Labels: instanceStatsLabels,
		Help:   "Packets from an instance's guest that failed with an error.",
		Group:  metric.GroupInstanceStats,
	}
)

// MetricDescriptions returns the descriptions of every metric the instance
// manager serves, for the reference.
func MetricDescriptions() []metric.Description {
	return []metric.Description{
		instancesMetric,
		instancesHealthMetric,
		instancesVCPUsMetric,
		instancesMemoryMetric,
		instancesVCPUsAllocatableMetric,
		instancesMemoryAllocatableMetric,
		operationsMetric,
		operationDurationMetric,
		restartsMetric,
		cpuMetric,
		vcpusMetric,
		residentMemoryMetric,
		memoryMetric,
		diskReadMetric,
		diskWrittenMetric,
		networkReceiveMetric,
		networkTransmitMetric,
		networkReceivePacketsMetric,
		networkTransmitPacketsMetric,
		networkReceiveDropsMetric,
		networkTransmitDropsMetric,
		networkReceiveErrorsMetric,
		networkTransmitErrorsMetric,
	}
}

// metrics are the counters a Manager keeps, and the descriptors of
// the gauges and kernel counters it reads at scrape time.
type metrics struct {
	operations *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	restarts   prometheus.Counter

	instances                  *prometheus.Desc
	instancesHealth            *prometheus.Desc
	instancesVCPUs             *prometheus.Desc
	instancesMemory            *prometheus.Desc
	instancesVCPUsAllocatable  *prometheus.Desc
	instancesMemoryAllocatable *prometheus.Desc

	cpu                    *prometheus.Desc
	vcpus                  *prometheus.Desc
	residentMemory         *prometheus.Desc
	memory                 *prometheus.Desc
	diskRead               *prometheus.Desc
	diskWritten            *prometheus.Desc
	networkReceive         *prometheus.Desc
	networkTransmit        *prometheus.Desc
	networkReceivePackets  *prometheus.Desc
	networkTransmitPackets *prometheus.Desc
	networkReceiveDrops    *prometheus.Desc
	networkTransmitDrops   *prometheus.Desc
	networkReceiveErrors   *prometheus.Desc
	networkTransmitErrors  *prometheus.Desc
}

func newMetrics() metrics {
	return metrics{
		operations: metric.NewCounterVec(operationsMetric),
		duration:   metric.NewHistogramVec(operationDurationMetric, prometheus.ExponentialBuckets(0.05, 2, 12)),
		restarts:   metric.NewCounter(restartsMetric),

		instances:                  metric.NewDesc(instancesMetric),
		instancesHealth:            metric.NewDesc(instancesHealthMetric),
		instancesVCPUs:             metric.NewDesc(instancesVCPUsMetric),
		instancesMemory:            metric.NewDesc(instancesMemoryMetric),
		instancesVCPUsAllocatable:  metric.NewDesc(instancesVCPUsAllocatableMetric),
		instancesMemoryAllocatable: metric.NewDesc(instancesMemoryAllocatableMetric),

		cpu:                    metric.NewDesc(cpuMetric),
		vcpus:                  metric.NewDesc(vcpusMetric),
		residentMemory:         metric.NewDesc(residentMemoryMetric),
		memory:                 metric.NewDesc(memoryMetric),
		diskRead:               metric.NewDesc(diskReadMetric),
		diskWritten:            metric.NewDesc(diskWrittenMetric),
		networkReceive:         metric.NewDesc(networkReceiveMetric),
		networkTransmit:        metric.NewDesc(networkTransmitMetric),
		networkReceivePackets:  metric.NewDesc(networkReceivePacketsMetric),
		networkTransmitPackets: metric.NewDesc(networkTransmitPacketsMetric),
		networkReceiveDrops:    metric.NewDesc(networkReceiveDropsMetric),
		networkTransmitDrops:   metric.NewDesc(networkTransmitDropsMetric),
		networkReceiveErrors:   metric.NewDesc(networkReceiveErrorsMetric),
		networkTransmitErrors:  metric.NewDesc(networkTransmitErrorsMetric),
	}
}

// observeOperation records a finished lifecycle operation. Call it deferred,
// over the operation's named error result.
func (m *Manager) observeOperation(operation string, started time.Time, err error) {
	m.metrics.operations.WithLabelValues(operation, metric.Outcome(err)).Inc()
	m.metrics.duration.WithLabelValues(operation).Observe(time.Since(started).Seconds())
}

// Describe implements prometheus.Collector.
func (m *Manager) Describe(ch chan<- *prometheus.Desc) {
	m.metrics.operations.Describe(ch)
	m.metrics.duration.Describe(ch)
	m.metrics.restarts.Describe(ch)

	for _, desc := range []*prometheus.Desc{
		m.metrics.instances, m.metrics.instancesHealth, m.metrics.instancesVCPUs, m.metrics.instancesMemory,
		m.metrics.instancesVCPUsAllocatable, m.metrics.instancesMemoryAllocatable,
		m.metrics.cpu, m.metrics.vcpus, m.metrics.residentMemory, m.metrics.memory,
		m.metrics.diskRead, m.metrics.diskWritten, m.metrics.networkReceive, m.metrics.networkTransmit,
		m.metrics.networkReceivePackets, m.metrics.networkTransmitPackets,
		m.metrics.networkReceiveDrops, m.metrics.networkTransmitDrops,
		m.metrics.networkReceiveErrors, m.metrics.networkTransmitErrors,
	} {
		ch <- desc
	}
}

// Collect implements prometheus.Collector: the counters the Manager keeps,
// then the instances' usage and each one's stats as they are now.
func (m *Manager) Collect(ch chan<- prometheus.Metric) {
	m.metrics.operations.Collect(ch)
	m.metrics.duration.Collect(ch)
	m.metrics.restarts.Collect(ch)

	usage := m.Usage()
	for state, n := range usage.ByState {
		ch <- metric.GaugeReading(m.metrics.instances, float64(n), strings.ToLower(string(state)))
	}
	for status, n := range usage.ByHealth {
		ch <- metric.GaugeReading(m.metrics.instancesHealth, float64(n), string(status))
	}
	allocatable := usage.Capacity.Allocatable()
	ch <- metric.GaugeReading(m.metrics.instancesVCPUs, float64(usage.Allocated.VCPUs))
	ch <- metric.GaugeReading(m.metrics.instancesMemory, float64(usage.Allocated.MemoryBytes))
	ch <- metric.GaugeReading(m.metrics.instancesVCPUsAllocatable, float64(allocatable.VCPUs))
	ch <- metric.GaugeReading(m.metrics.instancesMemoryAllocatable, float64(allocatable.MemoryBytes))

	for _, s := range m.Stats() {
		labels := []string{s.InstanceID, s.Name}

		ch <- metric.CounterReading(m.metrics.cpu, s.CPUTime.Seconds(), labels...)
		ch <- metric.GaugeReading(m.metrics.vcpus, float64(s.Committed.VCPUs), labels...)
		ch <- metric.GaugeReading(m.metrics.residentMemory, float64(s.ResidentMemoryBytes), labels...)
		ch <- metric.GaugeReading(m.metrics.memory, float64(s.Committed.MemoryBytes), labels...)
		ch <- metric.CounterReading(m.metrics.diskRead, float64(s.DiskReadBytes), labels...)
		ch <- metric.CounterReading(m.metrics.diskWritten, float64(s.DiskWrittenBytes), labels...)
		ch <- metric.CounterReading(m.metrics.networkReceive, float64(s.NetworkReceiveBytes), labels...)
		ch <- metric.CounterReading(m.metrics.networkTransmit, float64(s.NetworkTransmitBytes), labels...)
		ch <- metric.CounterReading(m.metrics.networkReceivePackets, float64(s.NetworkReceivePackets), labels...)
		ch <- metric.CounterReading(m.metrics.networkTransmitPackets, float64(s.NetworkTransmitPackets), labels...)
		ch <- metric.CounterReading(m.metrics.networkReceiveDrops, float64(s.NetworkReceiveDrops), labels...)
		ch <- metric.CounterReading(m.metrics.networkTransmitDrops, float64(s.NetworkTransmitDrops), labels...)
		ch <- metric.CounterReading(m.metrics.networkReceiveErrors, float64(s.NetworkReceiveErrors), labels...)
		ch <- metric.CounterReading(m.metrics.networkTransmitErrors, float64(s.NetworkTransmitErrors), labels...)
	}
}
