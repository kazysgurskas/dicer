// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package agent

import (
	"slices"
	"testing"

	diceragentv1 "github.com/konradasb/dicer/proto/diceragent/v1"
)

// TestExecUserNamedAsDockerExecTakesIt checks the forms docker exec -u
// takes, and that a uid or gid with no entry is taken as it is.
func TestExecUserNamedAsDockerExecTakesIt(t *testing.T) {
	tests := []struct {
		spec     string
		uid, gid uint32
		home     string
	}{
		{"root", 0, 0, "/root"},
		{"0", 0, 0, "/root"},
		{"root:4242", 0, 4242, "/root"},
		{"4242", 4242, 0, "/"},
		{"4242:4343", 4242, 4343, "/"},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			u, err := execUserNamed(tt.spec)
			if err != nil {
				t.Fatalf("execUserNamed: %v", err)
			}
			if u.credential.Uid != tt.uid || u.credential.Gid != tt.gid || u.home != tt.home {
				t.Errorf("execUserNamed(%q) = %d:%d with HOME %s, want %d:%d with HOME %s",
					tt.spec, u.credential.Uid, u.credential.Gid, u.home, tt.uid, tt.gid, tt.home)
			}
		})
	}

	// A uid with no entry has no groups but its own, rather than root's.
	if u, _ := execUserNamed("4242"); len(u.credential.Groups) != 0 {
		t.Errorf("groups = %v, want none", u.credential.Groups)
	}
	if u, _ := execUserNamed("root"); !slices.Contains(u.credential.Groups, 0) {
		t.Errorf("root's groups = %v, want them from /etc/group", u.credential.Groups)
	}
}

func TestExecUserNamedRefusesWhatTheGuestDoesNotKnow(t *testing.T) {
	for _, spec := range []string{"no-such-user", "root:no-such-group", ""} {
		t.Run(spec, func(t *testing.T) {
			if _, err := execUserNamed(spec); err == nil {
				t.Errorf("execUserNamed(%q) succeeded", spec)
			}
		})
	}
}

// TestExecCommandGivesAUserItsHome checks that a command run as a user gets
// its credential and its HOME, unless the start sets HOME itself.
func TestExecCommandGivesAUserItsHome(t *testing.T) {
	u, err := execUserNamed("4242")
	if err != nil {
		t.Fatal(err)
	}

	cmd := execCommand(t.Context(), &diceragentv1.ExecStart{}, []string{"true"}, &u)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Credential.Uid != 4242 || !slices.Contains(cmd.Env, "HOME=/") {
		t.Errorf("command = %+v with %v, want uid 4242 and HOME=/", cmd.SysProcAttr, cmd.Env)
	}

	start := &diceragentv1.ExecStart{Env: map[string]string{"HOME": "/srv"}}
	if cmd := execCommand(t.Context(), start, []string{"true"}, &u); !slices.Contains(cmd.Env, "HOME=/srv") {
		t.Errorf("env = %v, want the start's HOME", cmd.Env)
	}

	if cmd := execCommand(t.Context(), &diceragentv1.ExecStart{}, []string{"true"}, nil); cmd.SysProcAttr != nil {
		t.Errorf("a command without a user has %+v, want it run as the agent", cmd.SysProcAttr)
	}
}
