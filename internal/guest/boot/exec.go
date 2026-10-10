// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package boot

import (
	"cmp"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/konradasb/dicer/internal/guest"
)

// bootExec starts the guest agent, then runs the entrypoint in the overlay
// root as PID 1 of its own PID namespace, with a mount namespace and /proc of
// its own, as cfg's user. When it exits, its exit code is written to the
// status disk and the machine ends. It does not return.
func bootExec(log *slog.Logger, cfg *guest.Config) {
	if err := syscall.Chroot(overlayRoot); err != nil {
		fatal(log, "chroot", err)
	}
	if err := os.Chdir("/"); err != nil {
		fatal(log, "chdir /", err)
	}

	_ = os.Setenv("PATH", guestPath)
	_ = os.Setenv("HOME", guestHome)

	env := guestEnv(cfg.Env)

	log.Debug("starting guest agent")
	var agentArgs []string
	if cfg.User != "" {
		agentArgs = []string{"--user", cfg.User}
	}
	agentCmd := exec.Command(guestAgentPath, agentArgs...)
	agentCmd.Stdout = os.Stdout
	agentCmd.Stderr = os.Stderr
	agentCmd.Env = env
	if err := agentCmd.Start(); err != nil {
		log.Error("failed to start guest agent", "error", err)
	}

	// Listened for before the start, so that a stop asked for as the
	// entrypoint starts is not lost.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, forwardedSignals...)

	exitCode := exitCannotExecute
	if cmd, err := entrypointCmd(cfg); err != nil {
		log.Error("entrypoint start failed", "error", err)
	} else {
		log.Info("starting entrypoint", "argv", cfg.Argv(), "user", cfg.User, "workdir", cmd.Dir)
		exitCode = runWorkload(log, cmd, signals)
	}

	// The workload is the reason the machine exists: once it has exited,
	// the machine ends, and the host decides what happens next. The guest
	// agent goes with it.
	log.Info("entrypoint exited", "code", exitCode)
	reportExit(log, cfg, exitCode)
	halt(log, cfg.Halt)
}

// entrypointCmd returns the command that starts cfg's entrypoint, as cfg's
// user with the user's home directory as HOME, or as root if it names no
// user. It fails if the guest has no such user.
//
// dicer-init starts again as the namespaces' first process, to mount their
// /proc before it becomes the workload: Go cannot run code between creating
// a process and running its program. It switches to the user only then,
// since the user could not mount /proc. /proc/self/exe is this binary, which
// the chroot has otherwise left behind.
func entrypointCmd(cfg *guest.Config) (*exec.Cmd, error) {
	args := []string{entrypointCommand}
	env := cfg.Env
	if cfg.User != "" {
		user, err := guest.UserNamed(cfg.User)
		if err != nil {
			return nil, err
		}
		args = append(args, credentialArgs(user.Credential)...)
		env = user.EnvWithHome(env)
	}
	args = append(append(args, "--"), cfg.Argv()...)

	cmd := exec.Command("/proc/self/exe", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = guestEnv(env)
	cmd.Dir = cmp.Or(cfg.Workdir, "/")
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWPID | syscall.CLONE_NEWNS}
	return cmd, nil
}

// runWorkload starts cmd, passes signals on to it and waits for it to exit,
// returning its exit code. A workload that cannot be started ends as one
// that exited would, with the status a shell gives: the instance then says
// why, rather than running with nothing in it.
func runWorkload(log *slog.Logger, cmd *exec.Cmd, signals <-chan os.Signal) int {
	if err := cmd.Start(); err != nil {
		log.Error("entrypoint start failed", "error", err)
		return exitCannotExecute
	}
	go forwardSignals(log, signals, cmd.Process)

	return reapUntilExit(log, cmd.Process.Pid)
}

// reapUntilExit reaps each of dicer-init's children as it ends, and returns
// the workload's exit code once the workload has ended. As PID 1, dicer-init
// is given every process whose parent ends first, such as one that a command
// run by dicer exec left in the background. Reaping them keeps them from
// staying zombies. All waiting is done here, because any other wait could
// take the workload's exit status.
func reapUntilExit(log *slog.Logger, workloadPID int) int {
	for {
		var status syscall.WaitStatus
		reaped, err := syscall.Wait4(-1, &status, 0, nil)
		switch {
		case errors.Is(err, syscall.EINTR):
			// A signal arrived during the wait, which goes on.
		case err != nil:
			log.Error("entrypoint wait failed", "error", err)
			return 0
		case reaped == workloadPID:
			return guest.ExitStatus(status)
		}
	}
}

// forwardedSignals are the signals to dicer-init that ask the workload to
// stop.
var forwardedSignals = []os.Signal{
	syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT, guest.ShutdownSignal,
}

// forwardSignals passes the signals dicer-init receives to the workload,
// translating the shutdown signal into SIGTERM.
func forwardSignals(log *slog.Logger, signals <-chan os.Signal, process *os.Process) {
	for sig := range signals {
		if sig == guest.ShutdownSignal {
			sig = syscall.SIGTERM
		}
		log.Info("passing a signal on to the workload", "signal", sig)
		if err := process.Signal(sig); err != nil {
			return
		}
	}
}
