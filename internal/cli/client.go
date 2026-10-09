// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/konradasb/dicer"
	"github.com/konradasb/dicer/internal/cli/remote"
)

const (
	// remoteEnv chooses the remote when --remote does not.
	remoteEnv = "DICER_REMOTE"

	// tokenEnv is the token a command reaches a daemon's TCP listener with,
	// in place of the remote's own.
	tokenEnv = "DICER_TOKEN"
)

// target is the daemon a command talks to, and what it is called.
type target struct {
	name   string
	remote remote.Remote
}

// String describes the target for a person: "prod (192.0.2.1:7443)".
func (t target) String() string {
	if t.name == t.remote.Address {
		return t.name
	}

	return t.name + " (" + t.remote.Address + ")"
}

// resolveTarget decides which daemon a command talks to: --remote, then
// $DICER_REMOTE, then the current remote, then the local daemon. Each may be a
// remote's name or an address. $DICER_TOKEN, if set, is the token for a TCP
// address, in place of the remote's own.
func resolveTarget(cmd *cobra.Command) (target, error) {
	t, err := resolveRemote(cmd)
	if err != nil {
		return target{}, err
	}

	if value := os.Getenv(tokenEnv); value != "" && !t.remote.IsSocket() {
		t.remote.Token = value
		if err := t.remote.Validate(); err != nil {
			return target{}, fmt.Errorf("$%s: %w", tokenEnv, err)
		}
	}
	return t, nil
}

// resolveRemote returns the remote a command talks to, as resolveTarget
// decides it, with the remote's own token.
func resolveRemote(cmd *cobra.Command) (target, error) {
	name, _ := cmd.Flags().GetString("remote")
	if name == "" {
		name = os.Getenv(remoteEnv)
	}

	if remote.IsAddress(name) {
		r, err := remote.Parse(name)
		if err != nil {
			return target{}, err
		}
		return target{name: name, remote: r}, nil
	}

	cfg, err := loadRemoteConfig()
	if err != nil {
		return target{}, err
	}

	if name == "" {
		name = cfg.CurrentName()
	}
	r, err := cfg.Remote(name)
	if err != nil {
		return target{}, err
	}

	return target{name: name, remote: r}, nil
}

// newClient connects to the daemon the command is aimed at. The returned
// function closes the connection.
func newClient(cmd *cobra.Command) (*dicer.Client, func(), error) {
	t, err := resolveTarget(cmd)
	if err != nil {
		return nil, nil, err
	}

	if debugging(cmd) {
		newTracer(cmd).printf("remote %s", t)
	}

	if !t.remote.IsSocket() && t.remote.Token == "" {
		return nil, nil, fmt.Errorf("remote %s: a daemon's TCP listener needs a token: "+
			"add it with 'dicer remote create NAME ADDRESS', or set $%s", t, tokenEnv)
	}

	opts := t.remote.ClientOptions()
	c, err := dicer.NewClient(append(opts, dicer.WithDialOptions(dialOptions(cmd)...))...)
	if err != nil {
		// The client's errors already name the address.
		if !remote.IsAddress(t.name) {
			return nil, nil, fmt.Errorf("remote %s: %w", t, err)
		}
		return nil, nil, err
	}

	return c, func() { _ = c.Close() }, nil
}
