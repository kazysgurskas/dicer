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
		Short: "Wait until one or more instances stop, or are healthy",
		Long: "Waits until each instance stops and prints the status its guest ended with,\n" +
			"a line for each: the workload's exit code, 0 for a guest that powered\n" +
			"itself off, or 125 for one that ended without saying how.\n\n" +
			"An instance that has already stopped is not waited for. Its last status is\n" +
			"reported at once. An instance its restart policy starts again has not\n" +
			"stopped, so the wait goes on.\n\n" +
			"An instance that --rm has deleted cannot be waited for. To read the status\n" +
			"of a job run with -d, leave out --rm, and delete the job after the wait.\n\n" +
			"As with docker wait, the statuses are printed, not exited with: the command\n" +
			"fails only for an instance it cannot wait for.\n\n" +
			"With --condition healthy, it waits instead until each instance's health\n" +
			"check passes, and prints nothing. An unhealthy instance is waited for,\n" +
			"since a later probe or a restart can make it healthy. The command fails for\n" +
			"an instance with no health check, one that is not running, and one that\n" +
			"stops first.",
		Example: "  dicer wait web\n" +
			"  dicer wait job1 job2\n" +
			"  dicer wait --timeout 30s web\n" +
			"  dicer wait --condition healthy --timeout 2m web\n" +
			"  status=$(dicer wait job)",
		Args:              oneOrMore("instance name"),
		ValidArgsFunction: complete(0, instancesIn()),
		RunE:              runInstanceWaitCommand,
	}

	cmd.Flags().String("condition", string(dicer.WaitConditionStopped), "What to wait for: stopped or healthy")
	_ = cmd.RegisterFlagCompletionFunc("condition", fixedCompletions(choiceNames(waitConditions)...))
	cmd.Flags().Duration("timeout", 0, "Give up after this long (0: wait indefinitely)")

	return cmd
}

func runInstanceWaitCommand(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	condition, _ := cmd.Flags().GetString("condition")
	var opts dicer.WaitOptions
	var err error
	if opts.Condition, err = parseChoice("--condition", condition, waitConditions); err != nil {
		return usagef(cmd, "%s", err)
	}
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
		code, err := client.Instances.Wait(ctx, name, opts)
		if err != nil {
			return err
		}
		if opts.Condition == dicer.WaitConditionHealthy {
			return nil
		}

		_, err = fmt.Fprintln(cmd.OutOrStdout(), code)
		return err
	})
}
