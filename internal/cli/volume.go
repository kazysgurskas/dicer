// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"fmt"

	"github.com/docker/go-units"
	"github.com/spf13/cobra"

	"github.com/konradasb/dicer"
	"github.com/konradasb/dicer/internal/cli/printer"
	"github.com/konradasb/dicer/internal/humanize"
)

type printableVolume struct {
	Volumes []dicer.Volume
}

func (p *printableVolume) Columns() []string {
	return []string{"ID", "Name", "Size", "Created"}
}

func (p *printableVolume) Rows() []map[string]any {
	rows := make([]map[string]any, 0, len(p.Volumes))
	for _, v := range p.Volumes {
		rows = append(rows, map[string]any{
			"ID":      v.ID,
			"Name":    v.Name,
			"Size":    humanize.Bytes(v.SizeBytes),
			"Created": age(v.CreateTime),
		})
	}
	return rows
}

func newVolumeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "volume",
		Short:   "Manage volumes",
		Aliases: []string{"volumes"},
	}

	cmd.AddCommand(
		newVolumeCreateCommand(),
		newVolumeListCommand(),
		newVolumeShowCommand(),
		newVolumeDeleteCommand(),
	)

	return cmd
}

func newVolumeCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "create NAME",
		Short:   "Create a volume",
		Args:    one("a name for the volume"),
		Aliases: []string{"new"},
		RunE: func(cmd *cobra.Command, args []string) error {
			sizeFlag, _ := cmd.Flags().GetString("size")
			sizeBytes, err := units.RAMInBytes(sizeFlag)
			if err != nil {
				return fmt.Errorf("invalid volume size %q: want a size like 10GiB", sizeFlag)
			}

			client, cleanup, err := newClient(cmd)
			if err != nil {
				return err
			}
			defer cleanup()

			v, err := client.Volumes.Create(cmd.Context(), args[0], sizeBytes)
			if err != nil {
				return err
			}

			succeeded(cmd, "Volume %s created (%s)", v.Name, humanize.Bytes(v.SizeBytes))
			return nil
		},
	}

	cmd.Flags().String("size", "", "Volume size, e.g. 10GiB")
	requireFlag(cmd, "size", "10GiB")

	return cmd
}

func newVolumeListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List volumes",
		Args:    noArgs,
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, cleanup, err := newClient(cmd)
			if err != nil {
				return err
			}
			defer cleanup()

			list, err := client.Volumes.List(cmd.Context())
			if err != nil {
				return err
			}

			return render(cmd, &printableVolume{Volumes: list})
		},
	}

	addOutputFlags(cmd, true)

	return cmd
}

func newVolumeShowCommand() *cobra.Command {
	return newShowCommand(showSpec[dicer.Volume]{
		use:   "show NAME",
		short: "Show a volume",
		arg:   "a volume name",
		list:  listVolumes,
		get: func(ctx context.Context, client *dicer.Client, name string) (dicer.Volume, error) {
			return client.Volumes.Get(ctx, name)
		},
		printable: func(v dicer.Volume) printer.Printable { return &printableVolume{Volumes: []dicer.Volume{v}} },
	})
}

func newVolumeDeleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete (NAME... | --all)",
		Short: "Delete one or more volumes no instance uses, or all of them",
		Long: "Deletes the volumes named, or with --all every volume, asking first on a\n" +
			"terminal. A volume an instance is defined to mount is refused.",
		Args:              namesOrAll("volume name"),
		Aliases:           []string{"rm", "remove"},
		ValidArgsFunction: complete(0, listVolumes),
		RunE: func(cmd *cobra.Command, args []string) error {
			return eachNameOrAll(cmd, args, listVolumes, "volumes", func(client *dicer.Client, name string) error {
				if err := client.Volumes.Delete(cmd.Context(), name); err != nil {
					return err
				}

				succeeded(cmd, "Volume %s deleted", name)
				return nil
			})
		},
	}
	addDeleteAllFlags(cmd, "volumes")

	return cmd
}
