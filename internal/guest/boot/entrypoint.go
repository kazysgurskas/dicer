// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package boot

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

// entrypointCommand is the hidden subcommand bootExec runs dicer-init again as,
// in the entrypoint's own PID and mount namespaces, to start it there.
const entrypointCommand = "entrypoint"

// Exit statuses for an entrypoint that could not be started, as a shell
// reports them.
const (
	exitCannotExecute = 126
	exitNotFound      = 127
)

func newEntrypointCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    entrypointCommand + " [--uid UID --gid GID [--groups GID,...]] -- COMMAND [ARG...]",
		Short:  "Start the entrypoint in the namespaces dicer-init made for it",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, argv []string) {
			os.Exit(runEntrypoint(argv, credentialFromFlags(cmd)))
		},
	}

	cmd.Flags().Uint32("uid", 0, "The user to run the entrypoint as (default root)")
	cmd.Flags().Uint32("gid", 0, "The entrypoint's group")
	cmd.Flags().UintSlice("groups", nil, "The entrypoint's supplementary groups")
	cmd.MarkFlagsRequiredTogether("uid", "gid")

	return cmd
}

// credentialArgs returns the flags that pass c to the entrypoint command.
func credentialArgs(c *syscall.Credential) []string {
	args := []string{"--uid", strconv.FormatUint(uint64(c.Uid), 10), "--gid", strconv.FormatUint(uint64(c.Gid), 10)}
	if len(c.Groups) > 0 {
		groups := make([]string, len(c.Groups))
		for i, g := range c.Groups {
			groups[i] = strconv.FormatUint(uint64(g), 10)
		}
		args = append(args, "--groups", strings.Join(groups, ","))
	}
	return args
}

// credentialFromFlags returns the credential credentialArgs passed the
// entrypoint command, or nil if it was passed none.
func credentialFromFlags(cmd *cobra.Command) *syscall.Credential {
	if !cmd.Flags().Changed("uid") {
		return nil
	}
	c := &syscall.Credential{}
	c.Uid, _ = cmd.Flags().GetUint32("uid")
	c.Gid, _ = cmd.Flags().GetUint32("gid")
	groups, _ := cmd.Flags().GetUintSlice("groups")
	for _, g := range groups {
		c.Groups = append(c.Groups, uint32(g))
	}
	return c
}

// runEntrypoint mounts a /proc of the entrypoint's own PID namespace, then
// switches to credential, unless it is nil, and replaces itself with the
// entrypoint, which so becomes the namespace's PID 1.
// Without its own /proc the entrypoint would see the machine's processes under
// IDs other than its own, and anything reading /proc/<pid> -- ps, or
// containerd looking up its parent -- would find the wrong process or none.
//
// It returns only if the entrypoint could not be started, with the status a
// shell would give.
func runEntrypoint(argv []string, credential *syscall.Credential) int {
	// bootExec starts it as the first process of a new PID namespace. Run any
	// other way, it would mount a /proc over the machine's own.
	if os.Getpid() != 1 {
		return entrypointFailed("start "+argv[0],
			errors.New("not the first process of a PID namespace: dicer-init starts this itself"), exitCannotExecute)
	}

	// The new /proc must stay in this mount namespace, not propagate back to
	// dicer-init's.
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return entrypointFailed("make mounts private", err, exitCannotExecute)
	}
	const flags = syscall.MS_NOSUID | syscall.MS_NODEV | syscall.MS_NOEXEC
	if err := syscall.Mount("proc", "/proc", "proc", flags, ""); err != nil {
		return entrypointFailed("mount /proc", err, exitCannotExecute)
	}
	if credential != nil {
		if err := chownStdio(credential.Uid); err != nil {
			return entrypointFailed("give the user its stdio", err, exitCannotExecute)
		}
		if err := switchUser(credential); err != nil {
			return entrypointFailed("switch user", err, exitCannotExecute)
		}
	}

	path, err := exec.LookPath(argv[0])
	if err != nil {
		status := exitCannotExecute
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			status = exitNotFound
		}
		return entrypointFailed("start "+argv[0], err, status)
	}

	err = syscall.Exec(path, argv, os.Environ())

	status := exitCannotExecute
	if errors.Is(err, syscall.ENOENT) {
		status = exitNotFound
	}
	return entrypointFailed("start "+argv[0], err, status)
}

// chownStdio makes uid the owner of the stdin, stdout and stderr the
// entrypoint inherits, unless they are /dev/null, as runc does for a
// container's user. The workload can then open them again by path: an image
// may link its log files to /dev/stderr, which is the guest's console.
func chownStdio(uid uint32) error {
	var null unix.Stat_t
	if err := unix.Stat("/dev/null", &null); err != nil {
		return fmt.Errorf("stat /dev/null: %w", err)
	}
	for fd := range 3 {
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			return fmt.Errorf("stat fd %d: %w", fd, err)
		}
		if st.Uid == uid || st.Rdev == null.Rdev {
			continue
		}
		if err := unix.Fchown(fd, int(uid), -1); err != nil {
			return fmt.Errorf("chown fd %d: %w", fd, err)
		}
	}
	return nil
}

// switchUser makes every thread of the process run as c, with c's groups in
// place of root's.
func switchUser(c *syscall.Credential) error {
	groups := make([]int, len(c.Groups))
	for i, g := range c.Groups {
		groups[i] = int(g)
	}
	// The uid goes last: once it is no longer root's, the groups and the
	// gid cannot be changed.
	if err := syscall.Setgroups(groups); err != nil {
		return fmt.Errorf("set groups: %w", err)
	}
	if err := syscall.Setgid(int(c.Gid)); err != nil {
		return fmt.Errorf("set gid: %w", err)
	}
	if err := syscall.Setuid(int(c.Uid)); err != nil {
		return fmt.Errorf("set uid: %w", err)
	}
	return nil
}

// entrypointFailed says on the console why the entrypoint did not start, and
// returns the status to exit with.
func entrypointFailed(what string, err error, status int) int {
	fmt.Fprintf(os.Stderr, "dicer-init: %s: %v\n", what, err)
	return status
}
