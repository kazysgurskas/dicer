// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package token

import (
	"errors"
	"testing"
	"time"

	"github.com/konradasb/dicer/internal/errdefs"
)

// fakeStore is a Store of the tokens it holds, by name.
type fakeStore struct{ tokens map[string]Token }

func (s *fakeStore) CreateToken(t Token) error {
	if _, ok := s.tokens[t.Name]; ok {
		return errdefs.Exists("token %q already exists", t.Name)
	}
	s.tokens[t.Name] = t
	return nil
}

func (s *fakeStore) Token(nameOrID string) (Token, error) {
	for _, t := range s.tokens {
		if t.Name == nameOrID || t.ID == nameOrID {
			return t, nil
		}
	}
	return Token{}, errdefs.NotFound("no token %q", nameOrID)
}

func (s *fakeStore) TokenBySecretSHA256(secretSHA256 string) (Token, error) {
	for _, t := range s.tokens {
		if t.SecretSHA256 == secretSHA256 {
			return t, nil
		}
	}
	return Token{}, errdefs.NotFound("no token with that secret")
}

func (s *fakeStore) Tokens() []Token { return nil }

func (s *fakeStore) UpdateToken(t Token) error {
	s.tokens[t.Name] = t
	return nil
}

func (s *fakeStore) RecordTokenUse(string, time.Time) error { return nil }

func (s *fakeStore) DeleteToken(nameOrID string) error {
	t, err := s.Token(nameOrID)
	if err != nil {
		return err
	}
	delete(s.tokens, t.Name)
	return nil
}

func newTestManager() *Manager {
	return NewManager(Config{Store: &fakeStore{tokens: map[string]Token{}}})
}

// A token is found by its secret, which the manager returns once and keeps
// only the SHA-256 of.
func TestCreateReturnsTheSecretOnce(t *testing.T) {
	m := newTestManager()

	created, secret, err := m.Create("ci", "", []Scope{ScopeAll})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := ValidateSecret(secret); err != nil || created.SecretSHA256 != SecretSHA256(secret) {
		t.Errorf("Create returned secret %q for %+v, want a new secret it keeps the SHA-256 of", secret, created)
	}
	if found, err := m.TokenBySecret(secret); err != nil || found.ID != created.ID {
		t.Errorf("TokenBySecret = %+v, %v; want %+v", found, err, created)
	}
}

func TestCreateRefusesWhatCannotBeAToken(t *testing.T) {
	m := newTestManager()
	_, taken, err := m.Create("ci", "", []Scope{ScopeAll})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, token, secret string
		scopes              []Scope
		want                error
	}{
		{"no scopes", "deploy", "", nil, errdefs.ErrInvalidArgument},
		{"an invalid secret", "deploy", "not a secret!", []Scope{ScopeAll}, errdefs.ErrInvalidArgument},
		{"another token's secret", "deploy", taken, []Scope{ScopeAll}, errdefs.ErrExists},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := m.Create(tt.token, tt.secret, tt.scopes); !errors.Is(err, tt.want) {
				t.Errorf("Create = %v, want %v", err, tt.want)
			}
		})
	}
}

// A rotated token answers to its new secret, and not to its old one.
func TestRotateReplacesTheSecret(t *testing.T) {
	m := newTestManager()
	created, old, err := m.Create("ci", "", []Scope{ScopeAll})
	if err != nil {
		t.Fatal(err)
	}

	rotated, secret, err := m.Rotate("ci", "")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if rotated.ID != created.ID || secret == old {
		t.Errorf("Rotate = %+v with secret %q, want the same token with a new secret", rotated, secret)
	}
	if _, err := m.TokenBySecret(old); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("the old secret still works: %v", err)
	}
	if _, err := m.TokenBySecret(secret); err != nil {
		t.Errorf("the new secret does not work: %v", err)
	}
}
