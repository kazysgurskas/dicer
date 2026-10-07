// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/konradasb/dicer"
)

// eachName runs do for each name, reporting failures as they happen and
// failing at the end. A name not found gets a suggestion from list.
func eachName(
	cmd *cobra.Command, names []string, list completer,
	do func(client *dicer.Client, name string) error,
) error {
	client, cleanup, err := newClient(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	run := func(name string) error {
		return suggest(cmd.Context(), client, list, name, do(client, name))
	}

	if len(names) == 1 {
		return run(names[0])
	}

	failed := false
	for _, name := range names {
		if err := run(name); err != nil {
			failed = true
			cmd.PrintErrf("Error: %s\n", err.Error())
		}
	}
	if failed {
		return &exitError{code: 1}
	}
	return nil
}

func newInstanceStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "start NAME...",
		Short:             "Start one or more defined instances, or resume them from standby",
		Args:              oneOrMore("instance name"),
		ValidArgsFunction: complete(0, instancesIn(dicer.InstanceStateStopped, dicer.InstanceStateFailed, dicer.InstanceStateRestarting, dicer.InstanceStateStandby)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return eachName(cmd, args, instancesIn(), func(client *dicer.Client, name string) error {
				return runTask(cmd, "Starting "+name, func() (dicer.Instance, error) {
					return client.Instances.Start(cmd.Context(), name)
				}, func(instance dicer.Instance, took string) string {
					return fmt.Sprintf("Instance %s started in %s (%s)", instance.Name, took, orDash(instance.IP))
				})
			})
		},
	}
}

func newInstanceStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop NAME...",
		Short: "Stop one or more running instances, keeping their definitions and disks",
		Long: "Stops each instance, keeping its definition, disk and address. An instance on\n" +
			"standby is stopped by discarding what it froze, so that it boots afresh.",
		Args:              oneOrMore("instance name"),
		ValidArgsFunction: complete(0, instancesIn(dicer.InstanceStateRunning, dicer.InstanceStatePaused, dicer.InstanceStateStarting, dicer.InstanceStateRestarting, dicer.InstanceStateStandby)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return eachName(cmd, args, instancesIn(), func(client *dicer.Client, name string) error {
				return runTask(cmd, "Stopping "+name, func() (dicer.Instance, error) {
					return client.Instances.Stop(cmd.Context(), name)
				}, func(instance dicer.Instance, took string) string {
					return fmt.Sprintf("Instance %s stopped in %s", instance.Name, took)
				})
			})
		},
	}
}

func newInstanceRestartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "restart NAME...",
		Short: "Stop one or more instances if they are running, then start them",
		Long: "Stops each instance if it is running, paused or on standby, then starts it\n" +
			"afresh. A stopped instance is just started. Restarting is how a changed file\n" +
			"mount or an updated image takes effect.",
		Args:              oneOrMore("instance name"),
		ValidArgsFunction: complete(0, instancesIn()),
		RunE: func(cmd *cobra.Command, args []string) error {
			return eachName(cmd, args, instancesIn(), func(client *dicer.Client, name string) error {
				return runTask(cmd, "Restarting "+name, func() (dicer.Instance, error) {
					instance, err := client.Instances.Get(cmd.Context(), name)
					if err != nil {
						return dicer.Instance{}, err
					}

					switch instance.State {
					case dicer.InstanceStateRunning, dicer.InstanceStatePaused, dicer.InstanceStateStarting, dicer.InstanceStateStandby:
						if _, err := client.Instances.Stop(cmd.Context(), name); err != nil {
							return dicer.Instance{}, err
						}
					}

					return client.Instances.Start(cmd.Context(), name)
				}, func(instance dicer.Instance, took string) string {
					return fmt.Sprintf("Instance %s restarted in %s (%s)", instance.Name, took, orDash(instance.IP))
				})
			})
		},
	}
}

func newInstancePauseCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "pause NAME...",
		Short:             "Pause one or more running instances",
		Args:              oneOrMore("instance name"),
		ValidArgsFunction: complete(0, instancesIn(dicer.InstanceStateRunning)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return eachName(cmd, args, instancesIn(), func(client *dicer.Client, name string) error {
				instance, err := client.Instances.Pause(cmd.Context(), name)
				if err != nil {
					return err
				}

				succeeded(cmd, "Instance %s paused", instance.Name)

				return nil
			})
		},
	}
}

func newInstanceStandbyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "standby NAME...",
		Short: "Freeze one or more running instances to disk, freeing their CPU and memory",
		Long: "Freezes each running or paused instance to disk and ends its hypervisor, so\n" +
			"that no CPU or memory is committed to it. It keeps its disk, address,\n" +
			"published ports and writable volumes. Starting it resumes it where it was;\n" +
			"stopping it discards what it froze.",
		Args:              oneOrMore("instance name"),
		ValidArgsFunction: complete(0, instancesIn(dicer.InstanceStateRunning, dicer.InstanceStatePaused)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return eachName(cmd, args, instancesIn(), func(client *dicer.Client, name string) error {
				return runTask(cmd, "Putting "+name+" on standby", func() (dicer.Instance, error) {
					return client.Instances.Standby(cmd.Context(), name)
				}, func(instance dicer.Instance, took string) string {
					return fmt.Sprintf("Instance %s put on standby in %s", instance.Name, took)
				})
			})
		},
	}
}

func newInstanceResumeCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "resume NAME...",
		Short:             "Resume one or more paused instances",
		Aliases:           []string{"unpause"},
		Args:              oneOrMore("instance name"),
		ValidArgsFunction: complete(0, instancesIn(dicer.InstanceStatePaused)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return eachName(cmd, args, instancesIn(), func(client *dicer.Client, name string) error {
				instance, err := client.Instances.Resume(cmd.Context(), name)
				if err != nil {
					return err
				}

				succeeded(cmd, "Instance %s resumed", instance.Name)

				return nil
			})
		},
	}
}

func newInstanceDeleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete (NAME... | --all)",
		Short: "Delete one or more instances, or all of them",
		Long: "Deletes the instances named, or with --all every instance, asking first on a\n" +
			"terminal. A running instance is refused unless -f stops it first.",
		Example: "  dicer rm web\n" +
			"  dicer rm -f web worker\n" +
			"  dicer rm --all -f",
		Args:              namesOrAll("instance name"),
		Aliases:           []string{"rm", "remove"},
		ValidArgsFunction: complete(0, instancesIn()),
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")

			return eachNameOrAll(cmd, args, instancesIn(), "instances", func(client *dicer.Client, name string) error {
				err := client.Instances.Delete(cmd.Context(), name, dicer.DeleteOptions{Force: force})
				if err != nil {
					return withHint(err, dicer.ErrFailedPrecondition, "stop it first or use -f")
				}

				succeeded(cmd, "Instance %s deleted", name)
				return nil
			})
		},
	}

	cmd.Flags().BoolP("force", "f", false, "Stop an instance first if it is running")
	addDeleteAllFlags(cmd, "instances")

	return cmd
}
