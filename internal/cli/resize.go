// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/spf13/cobra"

	"github.com/konradasb/dicer"
	"github.com/konradasb/dicer/internal/humanize"
)

func newInstanceResizeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resize NAME",
		Short: "Change a running instance's vCPUs and memory",
		Long: "Changes a running instance's vCPUs or memory without restarting it, and\n" +
			"its definition with them, so it keeps them when it next starts.\n\n" +
			"An instance grows only as far as the --max-vcpus and --max-memory it was\n" +
			"started with, and its memory shrinks no further than it was started with.\n" +
			"vCPUs change on Cloud Hypervisor only. The guest's kernel must support\n" +
			"memory and CPU hotplug: virtio-mem, and CONFIG_HOTPLUG_CPU.\n\n" +
			"Memory is resized in steps of 2MiB. On Firecracker, resize waits for the\n" +
			"guest to take or give up the memory; Cloud Hypervisor cannot tell.",
		Example: "  dicer resize web --memory 4GiB\n" +
			"  dicer resize web --vcpus 4 --memory 2GiB",
		Args:              one("an instance name"),
		ValidArgsFunction: complete(0, instancesIn(dicer.InstanceStateRunning)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var opts dicer.ResizeOptions
			flags := cmd.Flags()
			if flags.Changed("vcpus") {
				opts.VCPUs, _ = flags.GetInt("vcpus")
			}
			if flags.Changed("memory") {
				v, _ := flags.GetString("memory")
				bytes, err := parseMemoryBytes(v)
				if err != nil {
					return usagef(cmd, "%s", err)
				}
				opts.MemoryBytes = bytes
			}
			if opts == (dicer.ResizeOptions{}) {
				return usagef(cmd, "%s needs --vcpus, --memory or both", cmd.CommandPath())
			}

			client, cleanup, err := newClient(cmd)
			if err != nil {
				return err
			}
			defer cleanup()

			instance, err := client.Instances.Resize(cmd.Context(), args[0], opts)
			if err != nil {
				return suggest(cmd.Context(), client, instancesIn(), args[0], err)
			}

			succeeded(cmd, "Instance %s resized to %s, %s memory", instance.Name,
				humanize.Count(instance.VCPUs, "vCPU"), humanize.Bytes(instance.MemoryBytes))

			return nil
		},
	}

	cmd.Flags().SortFlags = false
	cmd.Flags().Int("vcpus", 0, "Number of virtual CPUs")
	cmd.Flags().StringP("memory", "m", "", "Memory, e.g. 512MiB or 2GiB")

	return cmd
}
