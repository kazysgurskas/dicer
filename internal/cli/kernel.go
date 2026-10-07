// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/konradasb/dicer"
	"github.com/konradasb/dicer/internal/cli/printer"
)

type printableKernel struct {
	Kernels []dicer.Kernel
}

func (p *printableKernel) Columns() []string {
	return []string{"ID", "Name", "Arch", "URL", "SHA256", "Created"}
}

func (p *printableKernel) Rows() []map[string]any {
	rows := make([]map[string]any, 0, len(p.Kernels))
	for _, k := range p.Kernels {
		rows = append(rows, map[string]any{
			"ID":      k.ID,
			"Name":    k.Name,
			"Arch":    string(k.Architecture),
			"URL":     k.URL,
			"SHA256":  orDash(k.SHA256),
			"Created": age(k.CreateTime),
		})
	}
	return rows
}

func newKernelCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "kernel",
		Short:   "Manage guest kernels",
		Aliases: []string{"kernels"},
	}

	cmd.AddCommand(
		newKernelImportCommand(),
		newKernelListCommand(),
		newKernelShowCommand(),
		newKernelDeleteCommand(),
	)

	return cmd
}

func newKernelImportCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import NAME",
		Short: "Record a kernel to boot instances with",
		Long: "Records a kernel by URL. It is downloaded, and verified against --sha256\n" +
			"if given, the first time an instance boots with it. An instance that names\n" +
			"no kernel boots the default kernel, which needs no import.",
		Args: one("a name for the kernel"),
		RunE: func(cmd *cobra.Command, args []string) error {
			url, _ := cmd.Flags().GetString("url")
			archFlag, _ := cmd.Flags().GetString("arch")
			sha256, _ := cmd.Flags().GetString("sha256")

			arch, err := parseChoice("--arch", archFlag, architectures)
			if err != nil {
				return usagef(cmd, "%s", err)
			}

			client, cleanup, err := newClient(cmd)
			if err != nil {
				return err
			}
			defer cleanup()

			k, err := client.Kernels.Import(cmd.Context(), dicer.KernelSpec{
				Name:         args[0],
				URL:          url,
				Architecture: arch,
				SHA256:       sha256,
			})
			if err != nil {
				return err
			}

			succeeded(cmd, "Kernel %s imported", k.Name)
			return nil
		},
	}

	cmd.Flags().String("url", "", "Where to fetch the kernel: an http(s) URL, a file:// URL or an absolute path")
	cmd.Flags().String("arch", "", "Kernel architecture, e.g. x86_64")
	cmd.Flags().String("sha256", "", "Expected SHA-256 of the kernel, hex-encoded")
	requireFlag(cmd, "url", "https://example.com/vmlinux")
	requireFlag(cmd, "arch", "x86_64")

	return cmd
}

func newKernelListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List kernels",
		Args:    noArgs,
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, cleanup, err := newClient(cmd)
			if err != nil {
				return err
			}
			defer cleanup()

			list, err := client.Kernels.List(cmd.Context())
			if err != nil {
				return err
			}

			return render(cmd, &printableKernel{Kernels: list})
		},
	}

	addOutputFlags(cmd, true)

	return cmd
}

func newKernelShowCommand() *cobra.Command {
	return newShowCommand(showSpec[dicer.Kernel]{
		use:   "show NAME",
		short: "Show a kernel",
		arg:   "a kernel name",
		list:  listKernels,
		get: func(ctx context.Context, client *dicer.Client, name string) (dicer.Kernel, error) {
			return client.Kernels.Get(ctx, name)
		},
		printable: func(v dicer.Kernel) printer.Printable { return &printableKernel{Kernels: []dicer.Kernel{v}} },
	})
}

func newKernelDeleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete (NAME... | --all)",
		Short: "Delete one or more kernels no instance uses, or all of them",
		Long: "Deletes the kernels named, or with --all every kernel, asking first on a\n" +
			"terminal. A kernel an instance is defined to boot is refused, and so is the\n" +
			"default kernel, which --all leaves alone.",
		Args:              namesOrAll("kernel name"),
		Aliases:           []string{"rm", "remove"},
		ValidArgsFunction: complete(0, withoutDefault(listKernels)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return eachNameOrAll(cmd, args, withoutDefault(listKernels), "kernels", func(client *dicer.Client, name string) error {
				if err := client.Kernels.Delete(cmd.Context(), name); err != nil {
					return err
				}

				succeeded(cmd, "Kernel %s deleted", name)
				return nil
			})
		},
	}
	addDeleteAllFlags(cmd, "kernels")

	return cmd
}
