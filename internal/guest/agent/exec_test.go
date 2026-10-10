// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package agent

import (
	"context"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/konradasb/dicer/internal/guest"
	diceragentv1 "github.com/konradasb/dicer/proto/diceragent/v1"
)

// runScript runs a shell script as Exec does, under ctx, and returns the
// exit code it would report.
func runScript(ctx context.Context, script string) int32 {
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	return exitCodeOf(ctx, cmd, cmd.Run())
}

func TestExitCodeIsAsAShellReportsIt(t *testing.T) {
	tests := []struct {
		script string
		want   int32
	}{
		{"exit 0", 0},
		{"exit 3", 3},
		// Killed, but not by the timeout: the OOM killer, say. A shell
		// reports 128 plus the signal, not a timeout.
		{"kill -9 $$", 137},
		{"kill -15 $$", 143},
	}
	for _, tt := range tests {
		t.Run(tt.script, func(t *testing.T) {
			if got := runScript(t.Context(), tt.script); got != tt.want {
				t.Errorf("%q = %d, want %d", tt.script, got, tt.want)
			}
		})
	}
}

func TestExitCodeOfCommandKilledByItsTimeoutIsTimedOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	if got := runScript(ctx, "sleep 10"); got != exitTimedOut {
		t.Errorf("a command killed by its timeout = %d, want %d", got, exitTimedOut)
	}
}

// A command that exits on its own is reported as it exited, even if the
// deadline has passed by the time it is looked at.
func TestExitCodeLookedAtAfterTheDeadlineIsAsTheCommandExited(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
	cmd := exec.CommandContext(ctx, "sh", "-c", "exit 5")
	err := cmd.Run()
	cancel()

	expired, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stop()
	if got := exitCodeOf(expired, cmd, err); got != 5 {
		t.Errorf("exit 5 looked at after the deadline = %d, want 5", got)
	}
}

// TestExecCommandGivesAUserItsHome checks that a command run as a user gets
// its credential and its HOME, unless the start sets HOME itself.
func TestExecCommandGivesAUserItsHome(t *testing.T) {
	u, err := guest.UserNamed("4242")
	if err != nil {
		t.Fatal(err)
	}

	cmd := execCommand(t.Context(), &diceragentv1.ExecStart{}, []string{"true"}, u)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Credential.Uid != 4242 || !slices.Contains(cmd.Env, "HOME=/") {
		t.Errorf("command = %+v with %v, want uid 4242 and HOME=/", cmd.SysProcAttr, cmd.Env)
	}

	start := &diceragentv1.ExecStart{Env: map[string]string{"HOME": "/srv"}}
	if cmd := execCommand(t.Context(), start, []string{"true"}, u); !slices.Contains(cmd.Env, "HOME=/srv") {
		t.Errorf("env = %v, want the start's HOME", cmd.Env)
	}

	if cmd := execCommand(t.Context(), &diceragentv1.ExecStart{}, []string{"true"}, guest.User{}); cmd.SysProcAttr != nil {
		t.Errorf("a command without a user has %+v, want it run as the agent", cmd.SysProcAttr)
	}
}

// TestCommandUserIsTheWorkloadsUnlessNamed checks that a command that names
// no user runs as the workload's user, and one that does as its own.
func TestCommandUserIsTheWorkloadsUnlessNamed(t *testing.T) {
	tests := []struct {
		name     string
		workload string
		spec     string
		want     *uint32
	}{
		{"root without a workload user", "", "", nil},
		{"the workload's user", "4242", "", new(uint32(4242))},
		{"a named user over the workload's", "4242", "0", new(uint32(0))},
		{"a named user without a workload user", "", "4343", new(uint32(4343))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := (&server{user: tt.workload}).commandUser(tt.spec)
			if err != nil {
				t.Fatalf("commandUser: %v", err)
			}
			switch {
			case tt.want == nil && u.Credential != nil:
				t.Errorf("commandUser(%q) = uid %d, want root as the agent runs", tt.spec, u.Credential.Uid)
			case tt.want != nil && (u.Credential == nil || u.Credential.Uid != *tt.want):
				t.Errorf("commandUser(%q) = %+v, want uid %d", tt.spec, u, *tt.want)
			}
		})
	}

	if _, err := (&server{user: "no-such-user"}).commandUser(""); err == nil {
		t.Error("commandUser succeeded for a workload user the guest does not know")
	}
}
