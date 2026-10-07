// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/konradasb/dicer"
)

func newInstanceWaitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait NAME...",
		Short: "Wait until one or more instances stop, and print their exit codes",
		Long: "Waits until each instance stops and prints the status its guest ended with,\n" +
			"a line for each: the workload's exit code, 0 for a guest that powered\n" +
			"itself off, or 125 for one that ended without saying how.\n\n" +
			"An instance that has already stopped is not waited for; its last status is\n" +
			"reported at once, even if --rm has deleted it since. An instance its\n" +
			"restart policy starts again has not stopped, so the wait goes on.\n\n" +
			"As with docker wait, the statuses are printed, not exited with: the command\n" +
			"fails only for an instance it cannot wait for.",
		Example: "  dicer wait web\n" +
			"  dicer wait job1 job2\n" +
			"  dicer wait --timeout 30s web\n" +
			"  status=$(dicer wait job)",
		Args:              oneOrMore("instance name"),
		ValidArgsFunction: complete(0, instancesIn()),
		RunE:              runInstanceWaitCommand,
	}

	cmd.Flags().Duration("timeout", 0, "Give up after this long (0: wait indefinitely)")

	return cmd
}

func runInstanceWaitCommand(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	timeout, _ := cmd.Flags().GetDuration("timeout")
	if timeout < 0 {
		return usagef(cmd, "invalid --timeout %s: it cannot be negative", timeout)
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	return eachName(cmd, args, instancesIn(), func(client *dicer.Client, name string) error {
		code, err := client.Instances.Wait(ctx, name, dicer.WaitOptions{})
		if err != nil {
			return err
		}

		_, err = fmt.Fprintln(cmd.OutOrStdout(), code)
		return err
	})
}
