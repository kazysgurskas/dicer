// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build e2e

package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// processView is what the tests read of a process's record in dicer top
// --format json.
type processView struct {
	PID     int      `json:"pid"`
	PPID    int      `json:"ppid"`
	User    string   `json:"user"`
	State   string   `json:"state"`
	Name    string   `json:"name"`
	Command []string `json:"command"`
}

// TestInstanceTopListsTheGuestsProcesses checks that dicer top shows the processes
// in a real guest, with the PIDs dicer exec sees: the unit tests read a fake
// /proc, and only a real guest shows whose /proc the agent reads.
func TestInstanceTopListsTheGuestsProcesses(t *testing.T) {
	name := instanceName(t)
	env.createInstance(t, name, "--", "sleep", "3600")
	env.startInstance(t, name)

	out := env.dicer(t, "top", "--format", "json", name)
	processes := rows[processView](t, out, "dicer top")

	byCommand := make(map[string]processView, len(processes))
	for _, p := range processes {
		// A kernel thread has no command line.
		if len(p.Command) == 0 {
			t.Errorf("process %d, %s, has no command line, want no kernel threads", p.PID, p.Name)
		}
		byCommand[strings.Join(p.Command, " ")] = p
	}

	if init := byCommand["/init"]; init.PID != 1 {
		t.Errorf("dicer-init = %+v, want PID 1", init)
	}
	workload, ok := byCommand["sleep 3600"]
	if !ok {
		t.Fatalf("dicer top = %+v, want the workload, sleep 3600", processes)
	}
	if workload.PPID != 1 || workload.User != "root" || workload.State != "S" {
		t.Errorf("workload = %+v, want PPID 1, user root and state S", workload)
	}

	// The PID shown is the one exec sees, though the workload sees itself
	// as PID 1 of its own namespace.
	pid := strconv.Itoa(workload.PID)
	if out := env.exec(t, name, "cat", "/proc/"+pid+"/cmdline"); !strings.HasPrefix(out, "sleep") {
		t.Errorf("exec reads PID %s as %q, want the workload", pid, out)
	}
}
