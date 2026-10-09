// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/filestore"
	"github.com/konradasb/dicer/internal/token"
	"github.com/konradasb/dicer/internal/types"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// testFingerprint stands for the fingerprint of the daemon's certificate.
var testFingerprint = strings.Repeat("ab", 32)

// newTokenHandler returns a token handler for a daemon served over TCP with
// a certificate of its own, and the definitions it keeps tokens in.
func newTokenHandler(t *testing.T) (*tokenHandler, *filestore.Manager) {
	t.Helper()

	definitions, err := filestore.NewManager(filestore.Config{DataDir: filepath.Join(t.TempDir(), "data")})
	if err != nil {
		t.Fatal(err)
	}
	return &tokenHandler{definitions: definitions, servesTCP: true, fingerprint: testFingerprint}, definitions
}

// TestCreateTokenKeepsOnlyTheSecretsSHA256 checks that the value returned is
// the one thing that holds the secret: the daemon keeps its hash, and the
// token answers to it.
func TestCreateTokenKeepsOnlyTheSecretsSHA256(t *testing.T) {
	h, definitions := newTokenHandler(t)

	issued, err := h.CreateToken(t.Context(), &dicerdv1.CreateTokenRequest{Name: "ci"})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	secret, fingerprint, err := token.Parse(issued.GetValue())
	if err != nil {
		t.Fatalf("the value %q is not a token: %v", issued.GetValue(), err)
	}
	if fingerprint != testFingerprint {
		t.Errorf("fingerprint = %q, want the daemon's", fingerprint)
	}

	stored, err := definitions.TokenBySecretSHA256(token.SecretSHA256(secret))
	if err != nil || stored.Name != "ci" {
		t.Fatalf("TokenBySecretSHA256 = %+v, %v; want the token ci", stored, err)
	}
	if strings.Contains(stored.SecretSHA256, secret) {
		t.Error("the secret is stored")
	}
	if got := issued.GetToken().GetScopes(); !slices.Equal(got, []string{string(types.ScopeAll)}) {
		t.Errorf("scopes = %v, want everything by default", got)
	}
}

// TestCreateTokenWithoutAFingerprint checks that a daemon served with a
// certificate clients verify for themselves issues tokens without one.
func TestCreateTokenWithoutAFingerprint(t *testing.T) {
	h, _ := newTokenHandler(t)
	h.fingerprint = ""

	issued, err := h.CreateToken(t.Context(), &dicerdv1.CreateTokenRequest{Name: "ci"})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if _, fingerprint, err := token.Parse(issued.GetValue()); err != nil || fingerprint != "" {
		t.Errorf("Parse(%q) = fingerprint %q, %v; want none", issued.GetValue(), fingerprint, err)
	}
}

func TestCreateTokenIsRefusedWithoutATCPListener(t *testing.T) {
	h, _ := newTokenHandler(t)
	h.servesTCP = false

	_, err := h.CreateToken(t.Context(), &dicerdv1.CreateTokenRequest{Name: "ci"})
	wantClass(t, err, errdefs.ErrInvalidState)
	if err != nil && !strings.Contains(err.Error(), "server.listen") {
		t.Errorf("error = %v, want it to say to set server.listen", err)
	}
}

func TestCreateTokenRefusesWhatIsInvalid(t *testing.T) {
	h, _ := newTokenHandler(t)
	taken := strings.Repeat("a", token.MinSecretLength)
	if _, err := h.CreateToken(t.Context(), &dicerdv1.CreateTokenRequest{Name: "ci", Secret: taken}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		req   *dicerdv1.CreateTokenRequest
		class error
	}{
		{"no name", &dicerdv1.CreateTokenRequest{}, errdefs.ErrInvalidArgument},
		{"a name taken", &dicerdv1.CreateTokenRequest{Name: "ci"}, errdefs.ErrExists},
		{"an unknown scope", &dicerdv1.CreateTokenRequest{Name: "deploy", Scopes: []string{"cats:read"}}, errdefs.ErrInvalidArgument},
		{"a short secret", &dicerdv1.CreateTokenRequest{Name: "deploy", Secret: "abc"}, errdefs.ErrInvalidArgument},
		{"a secret taken", &dicerdv1.CreateTokenRequest{Name: "deploy", Secret: taken}, errdefs.ErrExists},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.CreateToken(t.Context(), tt.req)
			wantClass(t, err, tt.class)
		})
	}
}

// TestCreateTokenWithAGivenSecret checks that a tool can choose the secret,
// so that it knows the token before asking for it.
func TestCreateTokenWithAGivenSecret(t *testing.T) {
	h, _ := newTokenHandler(t)
	secret := strings.Repeat("Ab1", 12)

	issued, err := h.CreateToken(t.Context(), &dicerdv1.CreateTokenRequest{
		Name: "ci", Secret: secret, Scopes: []string{"instances:write", "kernels:read"},
	})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if want := token.Format(secret, testFingerprint); issued.GetValue() != want {
		t.Errorf("value = %q, want %q", issued.GetValue(), want)
	}
	if got := issued.GetToken().GetScopes(); !slices.Equal(got, []string{"instances:write", "kernels:read"}) {
		t.Errorf("scopes = %v", got)
	}
}

func TestRotateTokenReplacesTheSecret(t *testing.T) {
	h, definitions := newTokenHandler(t)

	created, err := h.CreateToken(t.Context(), &dicerdv1.CreateTokenRequest{Name: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := h.RotateToken(t.Context(), &dicerdv1.RotateTokenRequest{Name: "ci"})
	if err != nil {
		t.Fatalf("RotateToken: %v", err)
	}

	oldSecret, _, _ := token.Parse(created.GetValue())
	newSecret, _, _ := token.Parse(rotated.GetValue())
	if _, err := definitions.TokenBySecretSHA256(token.SecretSHA256(oldSecret)); err == nil {
		t.Error("the old secret still names the token")
	}
	if got, err := definitions.TokenBySecretSHA256(token.SecretSHA256(newSecret)); err != nil || got.Name != "ci" {
		t.Errorf("the new secret names %+v, %v; want ci", got, err)
	}
	if rotated.GetToken().GetId() != created.GetToken().GetId() ||
		!rotated.GetToken().GetCreateTime().AsTime().Equal(created.GetToken().GetCreateTime().AsTime()) {
		t.Error("rotating made another token, not a new secret for the same one")
	}

	_, err = h.RotateToken(t.Context(), &dicerdv1.RotateTokenRequest{Name: "gone"})
	wantClass(t, err, errdefs.ErrNotFound)
}

func TestTokensAreListedFoundAndDeleted(t *testing.T) {
	h, _ := newTokenHandler(t)
	for _, name := range []string{"deploy", "ci"} {
		if _, err := h.CreateToken(t.Context(), &dicerdv1.CreateTokenRequest{Name: name}); err != nil {
			t.Fatal(err)
		}
	}

	list, err := h.ListTokens(t.Context(), &dicerdv1.ListTokensRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tok := range list.GetTokens() {
		names = append(names, tok.GetName())
	}
	if !slices.Equal(names, []string{"ci", "deploy"}) {
		t.Errorf("ListTokens = %v, want ci and deploy by name", names)
	}

	if got, err := h.GetToken(t.Context(), &dicerdv1.GetTokenRequest{Name: "ci"}); err != nil || got.GetName() != "ci" {
		t.Errorf("GetToken = %v, %v", got, err)
	}

	if _, err := h.DeleteToken(t.Context(), &dicerdv1.DeleteTokenRequest{Name: "ci"}); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	_, err = h.GetToken(t.Context(), &dicerdv1.GetTokenRequest{Name: "ci"})
	wantClass(t, err, errdefs.ErrNotFound)
}

// TestATokenCannotManageOneThatCanDoMore checks that tokens:write is no way
// to more than a token already has: it makes, rotates and deletes only
// tokens its own scopes cover.
func TestATokenCannotManageOneThatCanDoMore(t *testing.T) {
	h, _ := newTokenHandler(t)
	if _, err := h.CreateToken(t.Context(), &dicerdv1.CreateTokenRequest{Name: "admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.CreateToken(t.Context(), &dicerdv1.CreateTokenRequest{Name: "viewer", Scopes: []string{"instances:read"}}); err != nil {
		t.Fatal(err)
	}

	ci := types.Token{Name: "ci", Scopes: []types.Scope{types.ScopeInstancesWrite, types.ScopeTokensWrite}}
	ctx := context.WithValue(t.Context(), tokenKey{}, ci)

	_, err := h.CreateToken(ctx, &dicerdv1.CreateTokenRequest{Name: "root"})
	wantClass(t, err, errdefs.ErrPermissionDenied)
	_, err = h.CreateToken(ctx, &dicerdv1.CreateTokenRequest{Name: "more", Scopes: []string{"volumes:write"}})
	wantClass(t, err, errdefs.ErrPermissionDenied)
	_, err = h.RotateToken(ctx, &dicerdv1.RotateTokenRequest{Name: "admin"})
	wantClass(t, err, errdefs.ErrPermissionDenied)
	_, err = h.DeleteToken(ctx, &dicerdv1.DeleteTokenRequest{Name: "admin"})
	wantClass(t, err, errdefs.ErrPermissionDenied)

	// What its scopes cover, it may.
	if _, err := h.CreateToken(ctx, &dicerdv1.CreateTokenRequest{Name: "less", Scopes: []string{"instances:read"}}); err != nil {
		t.Errorf("CreateToken of a token it covers: %v", err)
	}
	if _, err := h.RotateToken(ctx, &dicerdv1.RotateTokenRequest{Name: "viewer"}); err != nil {
		t.Errorf("RotateToken of a token it covers: %v", err)
	}
	if _, err := h.DeleteToken(ctx, &dicerdv1.DeleteTokenRequest{Name: "viewer"}); err != nil {
		t.Errorf("DeleteToken of a token it covers: %v", err)
	}
}
