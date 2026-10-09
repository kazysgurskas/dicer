// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/konradasb/dicer/internal/dns"
	"github.com/konradasb/dicer/internal/grpcapi"
	"github.com/konradasb/dicer/internal/image"
	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/kernel"
	"github.com/konradasb/dicer/internal/metric"
	"github.com/konradasb/dicer/internal/network"
	"github.com/konradasb/dicer/internal/volume"
)

// metricSection is a section of the metrics reference: a group's table, and
// what follows it.
type metricSection struct {
	group, heading, after string
}

// metricSections are the reference's sections, in order. A metric in a group
// with no section fails the generation.
var metricSections = []metricSection{
	{
		group:   metric.GroupDaemon,
		heading: "Daemon",
		after:   "The daemon's uptime is `time() - process_start_time_seconds`.",
	},
	{
		group:   metric.GroupInstances,
		heading: "Instances",
		after: "The allocatable amounts are the host's CPUs and memory, less the reserve, multiplied by " +
			"the overcommit, as the configuration's [`resources`]({{< relref \"/docs/reference/configuration#resources\" >}}) " +
			"sets them. See [Capacity]({{< relref \"/docs/guides/capacity\" >}}).",
	},
	{
		group:   metric.GroupInstanceStats,
		heading: "Instance stats",
		after: "What each running or paused instance uses of the host, read from its hypervisor process " +
			"and TAP device, with nothing asked of the guest. A series begins again each time the instance " +
			"starts, and is gone while it is stopped. `dicer stats` shows the same live.",
	},
	{
		group:   metric.GroupImages,
		heading: "Images",
	},
	{
		group:   metric.GroupKernels,
		heading: "Kernels",
	},
	{
		group:   metric.GroupVolumes,
		heading: "Volumes",
	},
	{
		group:   metric.GroupNetworks,
		heading: "Networks",
	},
	{
		group:   metric.GroupDNS,
		heading: "DNS",
		after: "What each network's DNS server, on its gateway address, has answered. There is none while the " +
			"configuration's [`network.dns`]({{< relref \"/docs/reference/configuration#network-dns\" >}}) is off.",
	},
	{
		group:   metric.GroupAPI,
		heading: "API",
	},
}

// metricsIntro opens the page.
const metricsIntro = "The Prometheus metrics the daemon serves at `/metrics`, once " +
	"[enabled]({{< relref \"/docs/reference/configuration#metrics\" >}}) with `metrics.enable`. " +
	"Every metric of Dicer's own is named `dicer_`. See " +
	"[Monitoring]({{< relref \"/docs/guides/monitoring\" >}}) for scraping and alerts.\n\n" +
	"The endpoint also serves the Go runtime's `go_*` metrics and the daemon process's `process_*` " +
	"metrics, and speaks OpenMetrics to a scraper that asks for it.\n"

// metricDescriptions returns every metric the daemon serves, as each package
// lists it.
func metricDescriptions() []metric.Description {
	return slices.Concat(
		metric.Descriptions(),
		instance.MetricDescriptions(),
		image.MetricDescriptions(),
		kernel.MetricDescriptions(),
		volume.MetricDescriptions(),
		network.MetricDescriptions(),
		dns.MetricDescriptions(),
		grpcapi.MetricDescriptions(),
	)
}

// writeMetrics writes the metrics reference from the metrics each package
// lists.
func writeMetrics(dir string) error {
	byGroup := map[string][]metric.Description{}
	for _, d := range metricDescriptions() {
		byGroup[d.Group] = append(byGroup[d.Group], d)
	}

	var body bytes.Buffer
	body.WriteString(metricsIntro)

	for _, section := range metricSections {
		fmt.Fprintf(&body, "\n## %s\n\n", section.heading)
		body.WriteString("| Metric | Type | Labels | Description |\n|---|---|---|---|\n")
		for _, d := range byGroup[section.group] {
			labels := make([]string, len(d.Labels))
			for i, label := range d.Labels {
				labels[i] = "`" + label + "`"
			}

			text := d.Help
			if d.Doc != "" {
				text += " " + d.Doc
			}

			fmt.Fprintf(&body, "| `%s` | %s | %s | %s |\n", d.Name, d.Type, strings.Join(labels, ", "), cell(text))
		}

		if section.after != "" {
			body.WriteString("\n" + section.after + "\n")
		}

		delete(byGroup, section.group)
	}

	for group, descriptions := range byGroup {
		return fmt.Errorf("%s is in group %q, which the reference has no section for", descriptions[0].Name, group)
	}

	return writePage(filepath.Join(dir, "metrics.md"), frontMatter{
		title: "Metrics", weight: 5, icon: "chart-bar",
		description: "Every Prometheus metric the daemon serves, with its labels.",
	}, body.Bytes())
}
