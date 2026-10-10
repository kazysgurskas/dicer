// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

// Package agent runs inside the guest and serves the host's exec, copy, probe,
// process listing, shutdown, clock, identity and info requests over vsock.
package agent

// server implements diceragentv1.AgentServiceServer.
type server struct {
	// user is who a command runs as when its request names no one: the
	// workload's user, as user, uid, user:group or uid:gid. Empty is root.
	user string
}
