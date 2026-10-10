// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package boot

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/konradasb/dicer/internal/guest"
)

// The guest agent's request to shut down reaches the workload as the SIGTERM
// it stops on; other signals reach it as they are.
func TestForwardSignals(t *testing.T) {
	tests := []struct {
		name string
		sent os.Signal
		want int
	}{
		{name: "shutdown becomes SIGTERM", sent: guest.ShutdownSignal, want: 128 + int(syscall.SIGTERM)},
		{name: "SIGINT is passed on", sent: syscall.SIGINT, want: 128 + int(syscall.SIGINT)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("sleep", "30")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}

			signals := make(chan os.Signal, 1)
			defer close(signals)
			go forwardSignals(slog.New(slog.DiscardHandler), signals, cmd.Process)
			signals <- tt.sent

			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				t.Fatalf("%v was not passed on", tt.sent)
			}

			status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
			if !ok {
				t.Fatalf("Sys = %T, want a syscall.WaitStatus", cmd.ProcessState.Sys())
			}
			if got := guest.ExitStatus(status); got != tt.want {
				t.Errorf("the workload ended with %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRunWorkloadReturnsTheWorkloadsExitStatus(t *testing.T) {
	tests := map[string]int{
		"exit 0":     0,
		"exit 3":     3,
		"kill -9 $$": 137,
	}
	for script, want := range tests {
		t.Run(script, func(t *testing.T) {
			signals := make(chan os.Signal)
			defer close(signals)

			got := runWorkload(slog.New(slog.DiscardHandler), exec.Command("sh", "-c", script), signals)
			if got != want {
				t.Errorf("runWorkload = %d, want %d", got, want)
			}
		})
	}
}

// TestRunWorkloadReapsOrphans checks that a process given to dicer-init when
// its parent ends is reaped once it ends too, rather than left a zombie, and
// that the workload's exit status still arrives. A subreaper is given such
// processes as PID 1 is.
func TestRunWorkloadReapsOrphans(t *testing.T) {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 0, 0, 0, 0) })

	// The shell ends at once, leaving its background sleep to this process.
	out, err := exec.Command("sh", "-c", "sleep 0.2 & echo $!").Output()
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}

	signals := make(chan os.Signal)
	defer close(signals)
	// The workload outlives the orphan.
	workload := exec.Command("sh", "-c", "sleep 0.5; exit 3")
	if got := runWorkload(slog.New(slog.DiscardHandler), workload, signals); got != 3 {
		t.Errorf("runWorkload = %d, want the workload's 3", got)
	}

	if _, err := os.Stat(fmt.Sprintf("/proc/%d", orphan)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the orphan %d is still there, as a zombie: %v", orphan, err)
	}
}
