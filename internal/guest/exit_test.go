// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package guest

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestExitStatus(t *testing.T) {
	tests := map[string]int{
		"exit 0":      0,
		"exit 3":      3,
		"kill -9 $$":  137,
		"kill -15 $$": 143,
	}
	for script, want := range tests {
		t.Run(script, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "sh", "-c", script)
			_ = cmd.Run()
			status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
			if !ok {
				t.Fatalf("Sys = %T, want a syscall.WaitStatus", cmd.ProcessState.Sys())
			}
			if got := ExitStatus(status); got != want {
				t.Errorf("ExitStatus = %d, want %d", got, want)
			}
		})
	}
}
