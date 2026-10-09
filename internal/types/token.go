// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package types

import (
	"slices"
	"strings"
	"time"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/naming"
)

// Scope is what a token allows: ScopeAll, or an action on a kind of
// resource, written RESOURCE:ACTION. A read scope allows the calls that only
// look at its resource. A write scope also allows those that change it.
type Scope string

// ScopeAll allows everything, now and as the API grows.
const ScopeAll Scope = "*"

// The scopes of each kind of resource. Events, the log of what happens to
// every other resource, can only be read.
const (
	ScopeInstancesRead  Scope = "instances:read"
	ScopeInstancesWrite Scope = "instances:write"
	ScopeSnapshotsRead  Scope = "snapshots:read"
	ScopeSnapshotsWrite Scope = "snapshots:write"
	ScopeNetworksRead   Scope = "networks:read"
	ScopeNetworksWrite  Scope = "networks:write"
	ScopeVolumesRead    Scope = "volumes:read"
	ScopeVolumesWrite   Scope = "volumes:write"
	ScopeImagesRead     Scope = "images:read"
	ScopeImagesWrite    Scope = "images:write"
	ScopeKernelsRead    Scope = "kernels:read"
	ScopeKernelsWrite   Scope = "kernels:write"
	ScopeTokensRead     Scope = "tokens:read"
	ScopeTokensWrite    Scope = "tokens:write"
	ScopeEventsRead     Scope = "events:read"
)

// resourceScopes are the scopes of each kind of resource, as the constants
// above name them.
var resourceScopes = []Scope{
	ScopeInstancesRead, ScopeInstancesWrite,
	ScopeSnapshotsRead, ScopeSnapshotsWrite,
	ScopeNetworksRead, ScopeNetworksWrite,
	ScopeVolumesRead, ScopeVolumesWrite,
	ScopeImagesRead, ScopeImagesWrite,
	ScopeKernelsRead, ScopeKernelsWrite,
	ScopeTokensRead, ScopeTokensWrite,
	ScopeEventsRead,
}

// Validate returns an invalid argument error unless s is ScopeAll or the
// scope of a kind of resource.
func (s Scope) Validate() error {
	if s == ScopeAll || slices.Contains(resourceScopes, s) {
		return nil
	}

	valid := make([]string, 0, len(resourceScopes))
	for _, scope := range resourceScopes {
		valid = append(valid, string(scope))
	}
	return errdefs.InvalidArgument("invalid scope %q: want %s, or one of %s",
		s, ScopeAll, strings.Join(valid, ", "))
}

// Token is a credential a client presents to the daemon's TCP listener. The
// daemon keeps the SHA-256 of its secret, never the secret itself.
type Token struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`

	// SecretSHA256 is the SHA-256 of the token's secret, hex-encoded.
	SecretSHA256 string `yaml:"secret_sha256"`

	// Scopes are what the token allows.
	Scopes []Scope `yaml:"scopes"`

	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at"`

	// LastUsedAt is when the token last made a call, to the minute. It is
	// zero for a token never used.
	LastUsedAt time.Time `yaml:"last_used_at,omitempty"`
}

// Validate returns an invalid argument error unless the token has a valid
// name, a secret's SHA-256 and at least one scope, each of them valid and
// none repeated.
func (t Token) Validate() error {
	if err := naming.Validate(t.Name); err != nil {
		return err
	}
	if !isSHA256Hex(t.SecretSHA256) {
		return errdefs.InvalidArgument("token %q has no secret's SHA-256", t.Name)
	}

	if len(t.Scopes) == 0 {
		return errdefs.InvalidArgument("a token needs at least one scope: %s for everything", ScopeAll)
	}
	for i, scope := range t.Scopes {
		if err := scope.Validate(); err != nil {
			return err
		}
		if slices.Contains(t.Scopes[:i], scope) {
			return errdefs.InvalidArgument("scope %q is given twice", scope)
		}
	}
	if len(t.Scopes) > 1 && slices.Contains(t.Scopes, ScopeAll) {
		return errdefs.InvalidArgument("scope %s allows everything already: give it alone", ScopeAll)
	}
	return nil
}

// Allows reports whether the token's scopes allow scope: it has ScopeAll,
// scope itself, or, for a read scope, the write scope of the same resource.
func (t Token) Allows(scope Scope) bool {
	if slices.Contains(t.Scopes, ScopeAll) || slices.Contains(t.Scopes, scope) {
		return true
	}
	resource, isRead := strings.CutSuffix(string(scope), ":read")
	return isRead && slices.Contains(t.Scopes, Scope(resource+":write"))
}

// AllowsAll reports whether the token's scopes allow every one of scopes,
// so that a token with them could do nothing this one cannot.
func (t Token) AllowsAll(scopes []Scope) bool {
	for _, scope := range scopes {
		if !t.Allows(scope) {
			return false
		}
	}
	return true
}
