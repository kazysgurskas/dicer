// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

// Package remote is the daemons the CLI knows, kept with the current one in
// ~/.config/dicer/remotes.yaml, and how each is reached.
package remote

import (
	"net"
	"path/filepath"
	"strings"

	"github.com/konradasb/dicer"
	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/token"
)

const (
	// Local names the built-in remote: the daemon on this machine, on its
	// default socket. It is always there and cannot be deleted.
	Local = "local"

	// socketScheme is what the address of a daemon's socket starts with.
	socketScheme = "unix://"
)

// Remote is a daemon the CLI talks to: on its socket, or on its TCP
// listener with a token.
type Remote struct {
	// Address is a gRPC target: unix:///path/to/socket, or HOST:PORT for a
	// daemon's TCP listener.
	Address string `yaml:"address"`

	// Token is what every call to a TCP address is made with, which also
	// says how the daemon is checked. A socket takes none.
	Token string `yaml:"token,omitempty"`
}

// IsAddress reports whether s is an address rather than a remote's name,
// which holds neither ':' nor '/'.
func IsAddress(s string) bool {
	return strings.ContainsAny(s, ":/")
}

// Parse returns the remote an address names, with no token.
func Parse(address string) (Remote, error) {
	r := Remote{Address: address}
	if err := r.Validate(); err != nil {
		return Remote{}, err
	}

	return r, nil
}

// IsSocket reports whether the remote is a daemon's socket.
func (r Remote) IsSocket() bool {
	return strings.HasPrefix(r.Address, socketScheme)
}

// Validate returns an error if the remote cannot be connected to. A TCP
// address without a token is valid: the token can come from elsewhere.
func (r Remote) Validate() error {
	if path, ok := strings.CutPrefix(r.Address, socketScheme); ok {
		switch {
		case !filepath.IsAbs(path):
			return errdefs.InvalidArgument("invalid address %q: the socket path must be absolute", r.Address)
		case r.Token != "":
			return errdefs.InvalidArgument(
				"a unix:// remote takes no token: a socket is controlled by its file permissions")
		}
		return nil
	}

	if _, _, err := net.SplitHostPort(strings.TrimPrefix(r.Address, "dns:///")); err != nil {
		return errdefs.InvalidArgument("invalid address %q: want %s/PATH or HOST:PORT", r.Address, socketScheme)
	}
	if r.Token != "" {
		if _, _, err := token.Parse(r.Token); err != nil {
			return errdefs.InvalidArgument("%v", err)
		}
	}
	return nil
}

// ClientOptions returns what dicer.NewClient needs to reach the remote.
func (r Remote) ClientOptions() []dicer.Option {
	opts := []dicer.Option{dicer.WithAddress(r.Address)}
	if r.Token != "" {
		opts = append(opts, dicer.WithToken(r.Token))
	}

	return opts
}
