// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package guest

import "syscall"

// ExitStatus returns a finished process's exit status as a shell reports it:
// its exit code, or 128 plus the signal that killed it.
func ExitStatus(ws syscall.WaitStatus) int {
	if ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ws.ExitStatus()
}

// ShutdownSignal asks the guest's PID 1 to shut down: SIGRTMIN+4, systemd's
// power-off signal, which dicer-init also honours.
const ShutdownSignal = syscall.Signal(38)
