// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/token"
)

func testToken(name, secretHash string) token.Token {
	return token.Token{ID: "id-" + name, Name: name, SecretSHA256: secretHash, Scopes: []token.Scope{token.ScopeAll}}
}

func TestTokenIsFoundBySecretSHA256(t *testing.T) {
	s := newTestStore(t)

	const ciHash, deployHash = "aa", "bb"
	for _, tok := range []token.Token{testToken("ci", ciHash), testToken("deploy", deployHash)} {
		if err := s.CreateToken(tok); err != nil {
			t.Fatalf("CreateToken: %v", err)
		}
	}

	got, err := s.TokenBySecretSHA256(deployHash)
	if err != nil || got.Name != "deploy" {
		t.Errorf("TokenBySecretSHA256 = %q, %v; want deploy", got.Name, err)
	}
	if _, err := s.TokenBySecretSHA256("cc"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("TokenBySecretSHA256 of an unknown hash = %v, want ErrNotFound", err)
	}
}

// TestRecordTokenUseKeepsARotation checks that recording a token's use, which
// a call does with what it read before, never puts back a secret the token
// was rotated away from meanwhile.
func TestRecordTokenUseKeepsARotation(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateToken(testToken("ci", "old")); err != nil {
		t.Fatal(err)
	}
	rotated := testToken("ci", "new")
	if err := s.UpdateToken(rotated); err != nil {
		t.Fatal(err)
	}

	used := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	if err := s.RecordTokenUse("ci", used); err != nil {
		t.Fatalf("RecordTokenUse: %v", err)
	}

	got, _ := s.Token("ci")
	if got.SecretSHA256 != "new" || !got.LastUsedAt.Equal(used) {
		t.Errorf("token = %+v, want the new secret, last used at %v", got, used)
	}
	if err := s.RecordTokenUse("gone", used); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("RecordTokenUse of a missing token = %v, want ErrNotFound", err)
	}
}

// A deleted token is found neither by its name, its ID nor its secret, and
// stays deleted when the store loads again.
func TestDeletedTokenIsNotFound(t *testing.T) {
	cfg := Config{DataDir: filepath.Join(t.TempDir(), "data")}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.CreateToken(testToken("ci", "aa")); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteToken("id-ci"); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	for _, key := range []string{"ci", "id-ci"} {
		if _, err := s.Token(key); !errors.Is(err, errdefs.ErrNotFound) {
			t.Errorf("Token(%q) after delete = %v, want ErrNotFound", key, err)
		}
	}
	if _, err := s.TokenBySecretSHA256("aa"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("TokenBySecretSHA256 after delete = %v, want ErrNotFound", err)
	}
	if err := s.DeleteToken("ci"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("second DeleteToken = %v, want ErrNotFound", err)
	}

	reopened, err := New(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := reopened.Token("ci"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("Token after reopen = %v, want ErrNotFound", err)
	}
}
