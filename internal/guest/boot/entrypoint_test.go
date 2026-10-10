// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package boot

import (
	"reflect"
	"slices"
	"syscall"
	"testing"

	"github.com/konradasb/dicer/internal/guest"
)

// TestCredentialFlagsTakeWhatCredentialArgsPass checks that the entrypoint
// command gets back the credential bootExec passes it, and none if it is
// passed none.
func TestCredentialFlagsTakeWhatCredentialArgsPass(t *testing.T) {
	for _, want := range []*syscall.Credential{
		{Uid: 1000, Gid: 1000, Groups: []uint32{27, 100}},
		{Uid: 65532},
	} {
		cmd := newEntrypointCommand()
		if err := cmd.ParseFlags(credentialArgs(want)); err != nil {
			t.Fatalf("parse %q: %v", credentialArgs(want), err)
		}
		if got := credentialFromFlags(cmd); !reflect.DeepEqual(got, want) {
			t.Errorf("credential = %+v, want %+v", got, want)
		}
	}

	cmd := newEntrypointCommand()
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatal(err)
	}
	if got := credentialFromFlags(cmd); got != nil {
		t.Errorf("credential = %+v with no flags, want nil, for root", got)
	}
}

// TestEntrypointCmdRunsAsTheUser checks that the entrypoint is passed its
// user's credential and HOME, and refused a user the guest does not know.
func TestEntrypointCmdRunsAsTheUser(t *testing.T) {
	tests := []struct {
		name     string
		cfg      guest.Config
		wantArgs []string
		wantHome string
	}{
		{
			name:     "root",
			cfg:      guest.Config{Cmd: []string{"true"}},
			wantArgs: []string{entrypointCommand, "--", "true"},
			wantHome: "HOME=" + guestHome,
		},
		{
			name:     "a uid with no entry",
			cfg:      guest.Config{Cmd: []string{"true"}, User: "4242"},
			wantArgs: []string{entrypointCommand, "--uid", "4242", "--gid", "0", "--", "true"},
			wantHome: "HOME=/",
		},
		{
			name:     "a HOME of the instance's",
			cfg:      guest.Config{Cmd: []string{"true"}, User: "4242", Env: map[string]string{"HOME": "/srv"}},
			wantArgs: []string{entrypointCommand, "--uid", "4242", "--gid", "0", "--", "true"},
			wantHome: "HOME=/srv",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := entrypointCmd(&tt.cfg)
			if err != nil {
				t.Fatalf("entrypointCmd: %v", err)
			}
			if got := cmd.Args[1:]; !slices.Equal(got, tt.wantArgs) {
				t.Errorf("args = %q, want %q", got, tt.wantArgs)
			}
			if !slices.Contains(cmd.Env, tt.wantHome) {
				t.Errorf("env = %q, want %s", cmd.Env, tt.wantHome)
			}
		})
	}

	if _, err := entrypointCmd(&guest.Config{Cmd: []string{"true"}, User: "no-such-user"}); err == nil {
		t.Error("entrypointCmd succeeded for a user the guest does not know")
	}
}
