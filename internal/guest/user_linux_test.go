// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package guest

import (
	"maps"
	"slices"
	"testing"
)

// TestUserNamedAsDockerTakesIt checks the forms docker run -u takes, and
// that a uid or gid with no entry is taken as it is.
func TestUserNamedAsDockerTakesIt(t *testing.T) {
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
			u, err := UserNamed(tt.spec)
			if err != nil {
				t.Fatalf("UserNamed: %v", err)
			}
			if u.Credential.Uid != tt.uid || u.Credential.Gid != tt.gid || u.Home != tt.home {
				t.Errorf("UserNamed(%q) = %d:%d with HOME %s, want %d:%d with HOME %s",
					tt.spec, u.Credential.Uid, u.Credential.Gid, u.Home, tt.uid, tt.gid, tt.home)
			}
		})
	}

	// A uid with no entry has no groups but its own, rather than root's.
	if u, _ := UserNamed("4242"); len(u.Credential.Groups) != 0 {
		t.Errorf("groups = %v, want none", u.Credential.Groups)
	}
	if u, _ := UserNamed("root"); !slices.Contains(u.Credential.Groups, 0) {
		t.Errorf("root's groups = %v, want them from /etc/group", u.Credential.Groups)
	}
}

func TestUserNamedRefusesWhatTheGuestDoesNotKnow(t *testing.T) {
	for _, spec := range []string{"no-such-user", "root:no-such-group", ""} {
		t.Run(spec, func(t *testing.T) {
			if _, err := UserNamed(spec); err == nil {
				t.Errorf("UserNamed(%q) succeeded", spec)
			}
		})
	}
}

// TestEnvWithHomeKeepsAHomeTheEnvironmentSets checks that a user's home
// directory is HOME only where the environment does not set one.
func TestEnvWithHomeKeepsAHomeTheEnvironmentSets(t *testing.T) {
	u := User{Home: "/home/app"}

	if got := u.EnvWithHome(nil); got["HOME"] != "/home/app" {
		t.Errorf("EnvWithHome(nil) = %v, want HOME=/home/app", got)
	}

	env := map[string]string{"HOME": "/srv", "DEBUG": "1"}
	if got := u.EnvWithHome(env); !maps.Equal(got, env) {
		t.Errorf("EnvWithHome(%v) = %v, want it unchanged", env, got)
	}

	env = map[string]string{"DEBUG": "1"}
	if u.EnvWithHome(env); len(env) != 1 {
		t.Errorf("EnvWithHome changed its argument to %v", env)
	}
}
