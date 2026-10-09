// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package guest

// InitMode is how the guest starts an instance's command.
type InitMode string

const (
	// InitModeAuto has dicer-init decide, once the root filesystem is mounted:
	// systemd if the command is systemd, else exec.
	InitModeAuto InitMode = "auto"

	// InitModeExec runs the command as PID 1 of its own PID namespace.
	InitModeExec InitMode = "exec"

	// InitModeSystemd hands the machine's PID 1 to systemd itself.
	InitModeSystemd InitMode = "systemd"
)
