// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/konradasb/dicer"
)

func newInstanceExecCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec [flags] NAME [COMMAND [ARG...]]",
		Short: "Run a command inside a running instance",
		Long: "Runs a command inside a running instance, /bin/sh if none is given.\n\n" +
			"A pseudo-TTY is allocated when this terminal is on both ends -- stdin and\n" +
			"stdout -- so a shell is interactive, and output piped elsewhere is not\n" +
			"mangled by one. -t and -T force it on or off. Flags go before the name:\n" +
			"everything after it is the command's.\n\n" +
			"The command runs as the workload does: as the instance's user, its image's\n" +
			"USER, or root. -u names another of the guest's users, as user, uid,\n" +
			"user:group or uid:gid. The command gets the user's groups, and the user's\n" +
			"home directory as HOME.\n\n" +
			"If the instance is still booting, exec waits up to 30 seconds for its guest\n" +
			"agent to answer.",
		Example: "  dicer exec web\n" +
			"  dicer exec web ls -la /srv\n" +
			"  dicer exec -e DEBUG=1 -w /srv web ./check.sh\n" +
			"  dicer exec -u postgres db psql\n" +
			"  dicer exec -T web cat /var/log/app.log > app.log",
		RunE: runInstanceExecCommand,
		Args: oneThenCommand("an instance name"),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveDefault
			}
			return complete(1, instancesIn(dicer.InstanceStateRunning))(cmd, args, toComplete)
		},
	}

	// Everything after the name is the guest command's, flags and all:
	// 'dicer exec web ls -la' runs ls -la.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().SortFlags = false
	cmd.Flags().BoolP("tty", "t", false, "Allocate a pseudo-TTY (default: when stdin and stdout are a terminal)")
	cmd.Flags().BoolP("no-tty", "T", false, "Do not allocate a pseudo-TTY")
	cmd.Flags().BoolP("interactive", "i", true, "Accepted for Docker compatibility; stdin is always forwarded")
	_ = cmd.Flags().MarkHidden("interactive")
	cmd.Flags().StringArrayP("env", "e", nil,
		"Environment variable as KEY=VALUE, or KEY to pass this shell's value (repeatable)")
	cmd.Flags().StringP("workdir", "w", "", "Working directory inside the instance")
	cmd.Flags().StringP("user", "u", "", "User to run as: user, uid, user:group or uid:gid (default: the workload's user)")
	cmd.Flags().Duration("timeout", 0, "Kill the command after this long, e.g. 30s (0: no limit)")
	cmd.MarkFlagsMutuallyExclusive("tty", "no-tty")

	return cmd
}

// wantTTY decides whether exec allocates a pseudo-TTY: when asked, or when
// stdin and stdout are both terminals.
func wantTTY(force, never, stdinTTY, stdoutTTY bool) bool {
	switch {
	case never:
		return false
	case force:
		return true
	default:
		return stdinTTY && stdoutTTY
	}
}

func runInstanceExecCommand(cmd *cobra.Command, args []string) error {
	opts, err := parseExecOptions(cmd)
	if err != nil {
		return err
	}

	client, cleanup, err := newClient(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	command := client.Instances.Command(args[0], execArgs(args)...)
	command.TTY, command.Dir, command.Timeout, command.Env = opts.tty, opts.dir, opts.timeout, opts.env
	command.User = opts.user
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if command.TTY {
		if w, h, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
			command.Rows, command.Columns = h, w
		}
	}

	// Cancel on SIGINT/SIGTERM. In TTY mode Ctrl+C is a byte forwarded to
	// the guest instead.
	ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if command.TTY {
		stdin := int(os.Stdin.Fd())
		if oldState, err := term.MakeRaw(stdin); err == nil {
			defer func() { _ = term.Restore(stdin, oldState) }()
		}
	}
	if err := command.Start(ctx); err != nil {
		return suggest(cmd.Context(), client, instancesIn(), command.Instance, err)
	}
	if command.TTY {
		go forwardResizes(command, int(os.Stdin.Fd()))
	}

	var exitErr *dicer.ExitError
	switch err := command.Wait(); {
	case errors.As(err, &exitErr):
		// The guest command's own output already explains the failure; pass
		// its status through without adding a message of ours.
		return &exitError{code: exitErr.Code}
	case err != nil && ctx.Err() != nil:
		// Interrupted by a signal, which is a clean exit.
		return nil
	case err != nil:
		return suggest(cmd.Context(), client, instancesIn(), command.Instance, err)
	default:
		return nil
	}
}

// execOptions are how exec runs its command.
type execOptions struct {
	tty     bool
	dir     string
	user    string
	timeout time.Duration
	env     map[string]string
}

// parseExecOptions reads exec's flags, and this terminal.
func parseExecOptions(cmd *cobra.Command) (execOptions, error) {
	ttyFlag, _ := cmd.Flags().GetBool("tty")
	noTTY, _ := cmd.Flags().GetBool("no-tty")
	envSpecs, _ := cmd.Flags().GetStringArray("env")

	var opts execOptions
	opts.dir, _ = cmd.Flags().GetString("workdir")
	opts.user, _ = cmd.Flags().GetString("user")
	opts.timeout, _ = cmd.Flags().GetDuration("timeout")
	if opts.timeout < 0 {
		return opts, usagef(cmd, "invalid --timeout %s: it cannot be negative", opts.timeout)
	}

	var err error
	if opts.env, err = parseEnv(envSpecs, nil); err != nil {
		return opts, usagef(cmd, "%s", err)
	}
	opts.tty = wantTTY(ttyFlag, noTTY, term.IsTerminal(int(os.Stdin.Fd())), term.IsTerminal(int(os.Stdout.Fd())))

	return opts, nil
}

// execArgs returns the command exec's arguments give after the instance's
// name, or /bin/sh if they give none.
func execArgs(args []string) []string {
	if command := trimDash(args[1:]); len(command) > 0 {
		return command
	}
	return []string{"/bin/sh"}
}

// forwardResizes tells the guest the terminal's new size each time it
// changes.
func forwardResizes(command *dicer.Cmd, tty int) {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)

	for range winch {
		w, h, err := term.GetSize(tty)
		if err != nil {
			continue
		}
		_ = command.Resize(h, w)
	}
}
