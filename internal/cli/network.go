// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/konradasb/dicer"
	"github.com/konradasb/dicer/internal/cli/printer"
)

type printableNetwork struct {
	Networks []dicer.Network
}

func (p *printableNetwork) Columns() []string {
	return []string{"ID", "Name", "Subnet", "Gateway", "Bridge", "Nameservers", "MTU", "Isolated", "Internal", "Usage", "Created"}
}

func (p *printableNetwork) Rows() []map[string]any {
	rows := make([]map[string]any, 0, len(p.Networks))
	for _, n := range p.Networks {
		rows = append(rows, map[string]any{
			"ID":          n.ID,
			"Name":        n.Name,
			"Subnet":      n.Subnet,
			"Gateway":     n.Gateway,
			"Bridge":      n.Bridge,
			"Nameservers": strings.Join(n.Nameservers, ","),
			"MTU":         n.MTU,
			"Isolated":    n.Isolated,
			"Internal":    n.Internal,
			"Usage":       formatIPUsage(n.TotalIPs, n.FreeIPs),
			"Created":     age(n.CreateTime),
		})
	}
	return rows
}

// formatIPUsage renders address usage as "used/total (percent%)".
func formatIPUsage(total, free int64) string {
	if total <= 0 {
		return "0/0 (0%)"
	}
	used := total - free
	return fmt.Sprintf("%d/%d (%d%%)", used, total, used*100/total)
}

func newNetworkCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "network",
		Short:   "Manage networks",
		Aliases: []string{"networks"},
	}

	cmd.AddCommand(
		newNetworkCreateCommand(),
		newNetworkListCommand(),
		newNetworkShowCommand(),
		newNetworkDeleteCommand(),
		newNetworkAllocationCommand(),
	)

	return cmd
}

func newNetworkCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "create NAME",
		Short:   "Create a network",
		Args:    one("a name for the network"),
		Aliases: []string{"new"},
		RunE: func(cmd *cobra.Command, args []string) error {
			subnet, _ := cmd.Flags().GetString("subnet")
			gateway, _ := cmd.Flags().GetString("gateway")
			nameservers, _ := cmd.Flags().GetStringSlice("nameservers")
			mtu, _ := cmd.Flags().GetInt("mtu")
			isolated, _ := cmd.Flags().GetBool("isolated")
			internal, _ := cmd.Flags().GetBool("internal")

			client, cleanup, err := newClient(cmd)
			if err != nil {
				return err
			}
			defer cleanup()

			n, err := client.Networks.Create(cmd.Context(), dicer.NetworkSpec{
				Name:        args[0],
				Subnet:      subnet,
				Gateway:     gateway,
				Nameservers: nameservers,
				MTU:         mtu,
				Isolated:    isolated,
				Internal:    internal,
			})
			if err != nil {
				return err
			}

			succeeded(cmd, "Network %s created (%s, gateway %s, bridge %s)",
				n.Name, n.Subnet, n.Gateway, n.Bridge)
			return nil
		},
	}

	cmd.Flags().String("subnet", "", "Subnet in CIDR notation, e.g. 172.20.0.0/16")
	cmd.Flags().String("gateway", "", "Gateway address (default: the first address in the subnet)")
	cmd.Flags().StringSlice("nameservers", nil,
		"Upstream DNS servers, asked about names other than the network's instances', comma-separated (default: the daemon's)")
	cmd.Flags().Int("mtu", 0, "MTU (default: the daemon's)")
	cmd.Flags().Bool("isolated", false, "Stop instances on the network reaching each other")
	cmd.Flags().Bool("internal", false,
		"Stop instances on the network reaching anything beyond it: the outside, other networks, the host and upstream DNS")
	requireFlag(cmd, "subnet", "172.20.0.0/16")

	return cmd
}

func newNetworkListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List networks",
		Args:    noArgs,
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, cleanup, err := newClient(cmd)
			if err != nil {
				return err
			}
			defer cleanup()

			list, err := client.Networks.List(cmd.Context())
			if err != nil {
				return err
			}

			return render(cmd, &printableNetwork{Networks: list})
		},
	}

	addOutputFlags(cmd, true)

	return cmd
}

func newNetworkShowCommand() *cobra.Command {
	return newShowCommand(showSpec[dicer.Network]{
		use:   "show NAME",
		short: "Show a network",
		arg:   "a network name",
		list:  listNetworks,
		get: func(ctx context.Context, client *dicer.Client, name string) (dicer.Network, error) {
			return client.Networks.Get(ctx, name)
		},
		printable: func(v dicer.Network) printer.Printable {
			return &printableNetwork{Networks: []dicer.Network{v}}
		},
	})
}

func newNetworkDeleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete (NAME... | --all)",
		Short: "Delete one or more networks no instance uses, or all of them",
		Long: "Deletes the networks named, or with --all every network, asking first on a\n" +
			"terminal. A network an instance is defined on is refused, and so is the\n" +
			"default network, which --all leaves alone.",
		Args:              namesOrAll("network name"),
		Aliases:           []string{"rm", "remove"},
		ValidArgsFunction: complete(0, withoutDefault(listNetworks)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return eachNameOrAll(cmd, args, withoutDefault(listNetworks), "networks", func(client *dicer.Client, name string) error {
				if err := client.Networks.Delete(cmd.Context(), name); err != nil {
					return err
				}

				succeeded(cmd, "Network %s deleted", name)
				return nil
			})
		},
	}
	addDeleteAllFlags(cmd, "networks")

	return cmd
}

type printableNetworkAllocation struct {
	Allocations []dicer.NetworkAllocation
}

func (p *printableNetworkAllocation) Columns() []string {
	return []string{"Instance", "IP", "MAC", "TAP"}
}

func (p *printableNetworkAllocation) Rows() []map[string]any {
	rows := make([]map[string]any, 0, len(p.Allocations))
	for _, a := range p.Allocations {
		rows = append(rows, map[string]any{
			// The daemon resolves instance names; an allocation whose
			// instance has since been deleted shows its ID.
			"Instance": cmp.Or(a.InstanceName, a.InstanceID),
			"IP":       a.IP,
			"MAC":      a.MAC,
			"TAP":      a.TapDevice,
		})
	}
	return rows
}

func newNetworkAllocationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "allocation",
		Short:   "Inspect the addresses assigned on a network",
		Aliases: []string{"allocations", "alloc"},
	}

	cmd.AddCommand(newNetworkAllocationListCommand())

	return cmd
}

func newNetworkAllocationListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "list NETWORK",
		Short:             "List the addresses assigned on a network",
		Args:              one("a network name"),
		Aliases:           []string{"ls"},
		ValidArgsFunction: complete(1, listNetworks),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cleanup, err := newClient(cmd)
			if err != nil {
				return err
			}
			defer cleanup()

			allocations, err := client.Networks.Allocations(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			return render(cmd, &printableNetworkAllocation{Allocations: allocations})
		},
	}

	addOutputFlags(cmd, true)

	return cmd
}
