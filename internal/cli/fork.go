// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/konradasb/dicer"
)

func newInstanceForkCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fork INSTANCE NAME",
		Short: "Create an instance as a copy of another",
		Long: "Creates an instance called NAME as a copy of INSTANCE. It is the same as\n" +
			"taking a snapshot of INSTANCE and forking it, except that no snapshot is kept.\n" +
			"A running or paused instance is paused while its memory is written, and its\n" +
			"copy runs. A stopped instance's copy is stopped, and boots from a copy of its\n" +
			"disk.\n\n" +
			"The copy has an address of its own, on the same network unless --network is\n" +
			"given. It publishes no ports unless -p is given: two instances cannot publish\n" +
			"the same host port.",
		Example: "  dicer instance fork web web-2\n" +
			"  dicer instance fork web web-3 -p 8081:80",
		Args:              needs([]string{"an instance name", "a name for the new instance"}),
		ValidArgsFunction: complete(1, instancesIn()),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := forkFlags(cmd, args[1])
			if err != nil {
				return err
			}

			client, cleanup, err := newClient(cmd)
			if err != nil {
				return err
			}
			defer cleanup()

			return runTask(cmd, "Forking "+args[0], func() (dicer.Instance, error) {
				return client.Instances.Fork(cmd.Context(), args[0], opts)
			}, forkedMessage("instance "+args[0]))
		},
	}
	addForkFlags(cmd)

	return cmd
}

// addForkFlags adds the flags that set a fork's network, address and
// published ports.
func addForkFlags(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.String("network", "", "Network to attach to (default: the source instance's)")
	flags.String("ip", "", "Static IP address (default: assigned from the subnet)")
	flags.StringArrayP("publish", "p", nil,
		"Publish a guest port on the host, as [hostIP:]hostPort:guestPort[/tcp|udp] (repeatable)")
	_ = cmd.RegisterFlagCompletionFunc("network", complete(0, listNetworks))
}

// forkFlags returns the network, address and published ports that the
// flags from addForkFlags give.
func forkFlags(cmd *cobra.Command, name string) (dicer.ForkOptions, error) {
	opts := dicer.ForkOptions{Name: name}
	opts.NetworkName, _ = cmd.Flags().GetString("network")
	opts.StaticIP, _ = cmd.Flags().GetString("ip")
	specs, _ := cmd.Flags().GetStringArray("publish")

	var err error
	opts.Ports, err = parseEach(specs, parsePortMapping)
	return opts, err
}

// forkedMessage returns what a fork from source reports once it is done.
func forkedMessage(source string) func(dicer.Instance, string) string {
	return func(instance dicer.Instance, took string) string {
		if instance.State != dicer.InstanceStateRunning {
			return fmt.Sprintf("Instance %s forked from %s in %s; start it to boot it", instance.Name, source, took)
		}
		return fmt.Sprintf("Instance %s forked from %s in %s (%s)", instance.Name, source, took, orDash(instance.IP))
	}
}
