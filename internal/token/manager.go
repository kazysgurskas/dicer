// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package token

import (
	"errors"
	"time"

	"github.com/nrednav/cuid2"

	"github.com/konradasb/dicer/internal/errdefs"
)

// Store keeps the tokens.
type Store interface {
	CreateToken(t Token) error
	Token(nameOrID string) (Token, error)
	TokenBySecretSHA256(secretSHA256 string) (Token, error)
	Tokens() []Token
	UpdateToken(t Token) error
	RecordTokenUse(nameOrID string, at time.Time) error
	DeleteToken(nameOrID string) error
}

// Config configures a Manager.
type Config struct {
	// Store keeps the tokens. It is required.
	Store Store
}

// Manager creates, rotates and deletes tokens, and finds the token a secret
// belongs to. It keeps only each secret's SHA-256. It is safe for concurrent
// use.
type Manager struct {
	store Store
}

// NewManager returns a Manager.
func NewManager(cfg Config) *Manager {
	return &Manager{store: cfg.Store}
}

// Create records a token named name with scopes, and returns it with its
// secret, the only time the secret is returned. An empty secret is replaced
// by a new one. A secret another token has is refused with an
// errdefs.ErrExists error.
func (m *Manager) Create(name, secret string, scopes []Scope) (Token, string, error) {
	secret, err := m.newSecret(secret)
	if err != nil {
		return Token{}, "", err
	}

	now := time.Now()
	t := Token{
		ID:           cuid2.Generate(),
		Name:         name,
		SecretSHA256: SecretSHA256(secret),
		Scopes:       scopes,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := t.Validate(); err != nil {
		return Token{}, "", err
	}
	if err := m.store.CreateToken(t); err != nil {
		return Token{}, "", err
	}
	return t, secret, nil
}

// Rotate gives a token a new secret, and returns it with that secret. An
// empty secret is replaced by a new one. The old secret no longer works.
func (m *Manager) Rotate(nameOrID, secret string) (Token, string, error) {
	t, err := m.store.Token(nameOrID)
	if err != nil {
		return Token{}, "", err
	}
	secret, err = m.newSecret(secret)
	if err != nil {
		return Token{}, "", err
	}

	t.SecretSHA256 = SecretSHA256(secret)
	t.UpdatedAt = time.Now()
	if err := m.store.UpdateToken(t); err != nil {
		return Token{}, "", err
	}
	return t, secret, nil
}

// newSecret returns given if it is a valid secret no other token has, or a
// new secret if given is empty.
func (m *Manager) newSecret(given string) (string, error) {
	if given == "" {
		return NewSecret(), nil
	}
	if err := ValidateSecret(given); err != nil {
		return "", errdefs.InvalidArgument("%v", err)
	}

	_, err := m.store.TokenBySecretSHA256(SecretSHA256(given))
	switch {
	case err == nil:
		return "", errdefs.Exists("another token has that secret")
	case !errors.Is(err, errdefs.ErrNotFound):
		return "", err
	}
	return given, nil
}

// Token returns a token by name or ID.
func (m *Manager) Token(nameOrID string) (Token, error) {
	return m.store.Token(nameOrID)
}

// TokenBySecret returns the token whose secret is secret, or an
// errdefs.ErrNotFound error if there is none.
func (m *Manager) TokenBySecret(secret string) (Token, error) {
	return m.store.TokenBySecretSHA256(SecretSHA256(secret))
}

// Tokens returns every token, sorted by name.
func (m *Manager) Tokens() []Token {
	return m.store.Tokens()
}

// RecordUse records that a token was used at at.
func (m *Manager) RecordUse(nameOrID string, at time.Time) error {
	return m.store.RecordTokenUse(nameOrID, at)
}

// Delete removes a token, which no longer works.
func (m *Manager) Delete(nameOrID string) error {
	return m.store.DeleteToken(nameOrID)
}
