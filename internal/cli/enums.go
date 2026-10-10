// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"strings"

	"github.com/konradasb/dicer"
)

// The values of the client's enumerations that the CLI takes on its
// command line, in the order it offers them.
var (
	instanceStates = []dicer.InstanceState{
		dicer.InstanceStateStopped, dicer.InstanceStateStarting, dicer.InstanceStateRunning,
		dicer.InstanceStatePaused, dicer.InstanceStateStopping, dicer.InstanceStateRestarting,
		dicer.InstanceStateFailed, dicer.InstanceStateStandby,
	}
	hypervisorTypes = []dicer.HypervisorType{dicer.HypervisorTypeCloudHypervisor, dicer.HypervisorTypeFirecracker}
	initModes       = []dicer.InitMode{dicer.InitModeAuto, dicer.InitModeExec, dicer.InitModeSystemd}
	pullPolicies    = []dicer.PullPolicy{dicer.PullPolicyMissing, dicer.PullPolicyAlways, dicer.PullPolicyNever}
	restartModes    = []dicer.RestartMode{
		dicer.RestartModeNo, dicer.RestartModeOnFailure, dicer.RestartModeUnlessStopped, dicer.RestartModeAlways,
	}
	mountTypes     = []dicer.MountType{dicer.MountTypeVolume, dicer.MountTypeFile, dicer.MountTypeDirectory, dicer.MountTypeTmpfs}
	protocols      = []dicer.Protocol{dicer.ProtocolTCP, dicer.ProtocolUDP}
	architectures  = []dicer.Architecture{dicer.ArchitectureX86_64, dicer.ArchitectureAArch64}
	logSources     = []dicer.LogSource{dicer.LogSourceGuest, dicer.LogSourceHypervisor}
	waitConditions = []dicer.WaitCondition{dicer.WaitConditionStopped, dicer.WaitConditionHealthy}
	eventKinds     = []dicer.EventKind{
		dicer.EventKindInstance, dicer.EventKindImage, dicer.EventKindNetwork,
		dicer.EventKindVolume, dicer.EventKindKernel, dicer.EventKindSnapshot,
	}
)

// parseChoice reads one of values, as the client writes it, in any case and
// with underscores and hyphens alike: "on-failure", "X86_64".
func parseChoice[T ~string](what, s string, values []T) (T, error) {
	for _, v := range values {
		if normalChoice(string(v)) == normalChoice(s) {
			return v, nil
		}
	}

	return "", fmt.Errorf("invalid %s %q: want %s", what, s, orList(choiceNames(values)))
}

// normalChoice returns s in lower case, with hyphens for underscores.
func normalChoice(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "_", "-")
}

// choiceNames returns values as strings.
func choiceNames[T ~string](values []T) []string {
	names := make([]string, len(values))
	for i, v := range values {
		names[i] = string(v)
	}
	return names
}

// stateName is an instance state as the CLI shows it on its own: "Running".
func stateName(s dicer.InstanceState) string {
	return capitalize(string(s))
}

// isActive reports whether an instance in state s has a live guest.
func isActive(s dicer.InstanceState) bool {
	return s == dicer.InstanceStateRunning || s == dicer.InstanceStatePaused
}

// protocolName is a port mapping's protocol: "tcp" when unset, as the
// daemon takes it.
func protocolName(p dicer.Protocol) string {
	if p == "" {
		return string(dicer.ProtocolTCP)
	}
	return string(p)
}

// restartPolicyName is a restart policy as --restart takes it:
// "on-failure:5", or "no" for none.
func restartPolicyName(p dicer.RestartPolicy) string {
	mode := p.Mode
	if mode == "" {
		mode = dicer.RestartModeNo
	}
	if p.MaxRetries > 0 {
		return fmt.Sprintf("%s:%d", mode, p.MaxRetries)
	}
	return string(mode)
}
