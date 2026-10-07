// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/konradasb/dicer"
	"github.com/konradasb/dicer/internal/humanize"
)

// completionTimeout bounds how long a completion waits on the daemon. A
// shell that hangs on <Tab> is worse than one that offers nothing.
const completionTimeout = 2 * time.Second

// completer lists the names a completion offers, each with a description
// after a tab.
type completer func(ctx context.Context, client *dicer.Client, args []string) ([]string, error)

// complete turns a completer into a cobra completion function for the first
// maxArgs arguments (all if 0), skipping names already given.
func complete(maxArgs int, list completer) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if maxArgs > 0 && len(args) >= maxArgs {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		client, cleanup, err := newClient(cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		defer cleanup()

		ctx, cancel := context.WithTimeout(contextOf(cmd), completionTimeout)
		defer cancel()

		names, err := list(ctx, client, args)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return slices.DeleteFunc(names, func(n string) bool {
			return slices.Contains(args, completionValue(n))
		}), cobra.ShellCompDirectiveNoFileComp
	}
}

// contextOf returns the command's context, which is unset for a command
// run outside Execute -- as a completion function can be in a test.
func contextOf(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

// completionValue returns the value of a completion, without the
// description that follows it.
func completionValue(s string) string {
	value, _, _ := strings.Cut(s, "\t")
	return value
}

// instancesIn completes instance names, limited to the given states if any.
func instancesIn(states ...dicer.InstanceState) completer {
	return func(ctx context.Context, client *dicer.Client, _ []string) ([]string, error) {
		instances, err := client.Instances.List(ctx)
		if err != nil {
			return nil, err
		}

		var names []string
		for _, instance := range instances {
			if len(states) > 0 && !slices.Contains(states, instance.State) {
				continue
			}
			names = append(names, instance.Name+"\t"+stateName(instance.State)+", "+instance.ImageRef)
		}

		return names, nil
	}
}

// listImages completes image names, each described by its size.
func listImages(ctx context.Context, client *dicer.Client, _ []string) ([]string, error) {
	images, err := client.Images.List(ctx)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(images))
	for _, image := range images {
		names = append(names, image.Name+"\t"+humanize.Bytes(image.SizeBytes))
	}

	return names, nil
}

// listNetworks completes network names, each described by its subnet.
func listNetworks(ctx context.Context, client *dicer.Client, _ []string) ([]string, error) {
	networks, err := client.Networks.List(ctx)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(networks))
	for _, n := range networks {
		names = append(names, n.Name+"\t"+n.Subnet)
	}

	return names, nil
}

// listVolumes completes volume names, each described by its size.
func listVolumes(ctx context.Context, client *dicer.Client, _ []string) ([]string, error) {
	volumes, err := client.Volumes.List(ctx)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(volumes))
	for _, v := range volumes {
		names = append(names, v.Name+"\t"+humanize.Bytes(v.SizeBytes))
	}

	return names, nil
}

// listKernels completes kernel names, each described by its architecture.
func listKernels(ctx context.Context, client *dicer.Client, _ []string) ([]string, error) {
	kernels, err := client.Kernels.List(ctx)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(kernels))
	for _, k := range kernels {
		names = append(names, k.Name+"\t"+string(k.Architecture))
	}

	return names, nil
}

// defaultName is the name of the default network and of the default kernel,
// which the daemon provides and which cannot be deleted.
const defaultName = "default"

// withoutDefault returns list without the default network or kernel, so that
// delete --all and completion skip what cannot be deleted.
func withoutDefault(list completer) completer {
	return func(ctx context.Context, client *dicer.Client, args []string) ([]string, error) {
		names, err := list(ctx, client, args)
		return slices.DeleteFunc(names, func(n string) bool { return completionValue(n) == defaultName }), err
	}
}

// listSnapshots completes snapshot names, each described by its instance
// and age.
func listSnapshots(ctx context.Context, client *dicer.Client, _ []string) ([]string, error) {
	snapshots, err := client.Snapshots.List(ctx, "")
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(snapshots))
	for _, s := range snapshots {
		names = append(names, s.Name+"\t"+s.InstanceName+", "+age(s.CreateTime))
	}

	return names, nil
}

// completeRemotes completes the name of a configured remote. They live on
// this machine, so no daemon is asked.
func completeRemotes(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	cfg, err := loadRemoteConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var names []string
	for _, name := range cfg.Names() {
		r, _ := cfg.Remote(name)
		names = append(names, name+"\t"+r.Address)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// fixedCompletions completes a flag from a fixed set of values.
func fixedCompletions(values ...string) cobra.CompletionFunc {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}
