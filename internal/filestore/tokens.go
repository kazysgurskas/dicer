// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import (
	"crypto/subtle"
	"maps"
	"path/filepath"
	"slices"
	"time"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/naming"
	"github.com/konradasb/dicer/internal/token"
)

// tokensDir returns the directory every token is kept in.
func (s *Store) tokensDir() string {
	return filepath.Join(s.dataDir, "tokens")
}

// tokenPath returns the file holding the named token.
func (s *Store) tokenPath(name string) string {
	return filepath.Join(s.tokensDir(), name+".yaml")
}

// loadTokens reads every token into memory.
func (s *Store) loadTokens() error {
	names, err := definitionFiles(s.tokensDir())
	if err != nil {
		return err
	}
	for _, name := range names {
		var v token.Token
		path := s.tokenPath(name)
		if s.readDefinition(path, &v) && s.isWhereNamed(path, name, v.Name) {
			s.putToken(v)
		}
	}
	return nil
}

// CreateToken records a new token, refusing one whose name is taken.
func (s *Store) CreateToken(v token.Token) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := naming.Validate(v.Name); err != nil {
		return err
	}
	if _, err := s.Token(v.Name); err == nil {
		return errdefs.Exists("token %q already exists", v.Name)
	}

	if err := writeDefinition(s.tokenPath(v.Name), v); err != nil {
		return err
	}
	s.putToken(v)
	return nil
}

// Token returns a token by name or ID.
func (s *Store) Token(nameOrID string) (token.Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if v, ok := s.tokens[nameOrID]; ok {
		return v, nil
	}
	if name, ok := s.tokenNamesByID[nameOrID]; ok {
		return s.tokens[name], nil
	}
	return token.Token{}, errdefs.NotFound("no token %q", nameOrID)
}

// TokenBySecretSHA256 returns the token whose secret has the SHA-256
// secretSHA256, or an errdefs.ErrNotFound error if there is none. It
// compares them in constant time.
func (s *Store) TokenBySecretSHA256(secretSHA256 string) (token.Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, v := range s.tokens {
		if subtle.ConstantTimeCompare([]byte(v.SecretSHA256), []byte(secretSHA256)) == 1 {
			return v, nil
		}
	}
	return token.Token{}, errdefs.NotFound("no token with that secret")
}

// UpdateToken replaces a token.
func (s *Store) UpdateToken(v token.Token) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if _, err := s.Token(v.Name); err != nil {
		return err
	}

	if err := writeDefinition(s.tokenPath(v.Name), v); err != nil {
		return err
	}
	s.putToken(v)
	return nil
}

// RecordTokenUse records that a token, by name or ID, made a call at a time.
// It changes nothing else, so a token rotated meanwhile stays rotated.
func (s *Store) RecordTokenUse(nameOrID string, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	v, err := s.Token(nameOrID)
	if err != nil {
		return err
	}
	v.LastUsedAt = at

	if err := writeDefinition(s.tokenPath(v.Name), v); err != nil {
		return err
	}
	s.putToken(v)
	return nil
}

// DeleteToken removes a token.
func (s *Store) DeleteToken(nameOrID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	v, err := s.Token(nameOrID)
	if err != nil {
		return err
	}
	if err := removeDefinition(s.tokenPath(v.Name)); err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.tokens, v.Name)
	delete(s.tokenNamesByID, v.ID)
	s.mu.Unlock()
	return nil
}

// Tokens returns every token, sorted by name.
func (s *Store) Tokens() []token.Token {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tokens := make([]token.Token, 0, len(s.tokens))
	for _, name := range slices.Sorted(maps.Keys(s.tokens)) {
		tokens = append(tokens, s.tokens[name])
	}
	return tokens
}

// putToken keeps v in memory, under its name and its ID.
func (s *Store) putToken(v token.Token) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tokens[v.Name] = v
	if v.ID != "" {
		s.tokenNamesByID[v.ID] = v.Name
	}
}
