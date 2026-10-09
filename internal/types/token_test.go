// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package types

import (
	"errors"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
)

func TestTokenValidate(t *testing.T) {
	const hash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

	tests := []struct {
		name  string
		token Token
		valid bool
	}{
		{"everything", Token{Name: "ci", SecretSHA256: hash, Scopes: []Scope{ScopeAll}}, true},
		{"some scopes", Token{Name: "ci", SecretSHA256: hash, Scopes: []Scope{"instances:write", "kernels:read"}}, true},
		{"no name", Token{SecretSHA256: hash, Scopes: []Scope{ScopeAll}}, false},
		{"an invalid name", Token{Name: "CI!", SecretSHA256: hash, Scopes: []Scope{ScopeAll}}, false},
		{"no hash", Token{Name: "ci", Scopes: []Scope{ScopeAll}}, false},
		{"no scopes", Token{Name: "ci", SecretSHA256: hash}, false},
		{"a scope twice", Token{Name: "ci", SecretSHA256: hash, Scopes: []Scope{"events:read", "events:read"}}, false},
		{"everything and more", Token{Name: "ci", SecretSHA256: hash, Scopes: []Scope{ScopeAll, "events:read"}}, false},
		{"an invalid scope", Token{Name: "ci", SecretSHA256: hash, Scopes: []Scope{"instances"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.token.Validate()
			if tt.valid && err != nil {
				t.Errorf("Validate = %v", err)
			}
			if !tt.valid && !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Validate = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestScopeValidate(t *testing.T) {
	tests := []struct {
		scope Scope
		valid bool
	}{
		{ScopeAll, true},
		{"instances:read", true},
		{"events:read", true},
		{"events:write", false},
		{"host:read", false},
		{"tokens:write", true},
		{"", false},
		{"instances", false},
		{"instance:read", false},
		{"instances:delete", false},
		{"instances:read:write", false},
		{"Instances:read", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.scope), func(t *testing.T) {
			if err := tt.scope.Validate(); (err == nil) != tt.valid {
				t.Errorf("Scope(%q).Validate() = %v, want valid %v", tt.scope, err, tt.valid)
			}
		})
	}
}

func TestTokenAllows(t *testing.T) {
	tests := []struct {
		name   string
		scopes []Scope
		scope  Scope
		want   bool
	}{
		{"everything", []Scope{ScopeAll}, "tokens:write", true},
		{"the scope itself", []Scope{"kernels:read"}, "kernels:read", true},
		{"reading what it may write", []Scope{"instances:write"}, "instances:read", true},
		{"writing what it may read", []Scope{"instances:read"}, "instances:write", false},
		{"another resource", []Scope{"instances:write"}, "volumes:read", false},
		{"everything, from less", []Scope{"instances:write"}, ScopeAll, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Token{Scopes: tt.scopes}).Allows(tt.scope); got != tt.want {
				t.Errorf("Token{Scopes: %v}.Allows(%q) = %v, want %v", tt.scopes, tt.scope, got, tt.want)
			}
		})
	}
}

// TestTokenAllowsAllOnlyWhatItCovers checks the rule that keeps a token from
// making one that could do more than it can.
func TestTokenAllowsAllOnlyWhatItCovers(t *testing.T) {
	ci := Token{Scopes: []Scope{"instances:write", "tokens:write"}}

	if !ci.AllowsAll([]Scope{"instances:read", "tokens:write"}) {
		t.Error("a token does not allow scopes it covers")
	}
	if ci.AllowsAll([]Scope{"instances:write", "volumes:write"}) {
		t.Error("a token allows a scope it lacks")
	}
	if ci.AllowsAll([]Scope{ScopeAll}) {
		t.Error("a token allows everything without having it")
	}
}
