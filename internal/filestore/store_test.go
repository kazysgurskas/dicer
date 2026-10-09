// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package filestore

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/network"
	"github.com/konradasb/dicer/internal/token"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()

	dir := t.TempDir()
	s, err := New(Config{DataDir: filepath.Join(dir, "data")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return s
}

func testNetwork(name string) network.Network {
	return network.Network{
		ID:      "net-" + name,
		Name:    name,
		Subnet:  "10.0.0.0/24",
		Gateway: "10.0.0.1",
		Bridge:  "br-" + name,
	}
}

func testInstance(name string) instance.Spec {
	return instance.Spec{
		ID:          "id-" + name,
		Name:        name,
		ImageRef:    "docker.io/library/alpine:latest",
		KernelName:  "default",
		NetworkName: "default",
		VCPUs:       1,
	}
}

func TestInstancesAreFoundByNameOrIDAndListedByName(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateInstance(testInstance("web")); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	for _, key := range []string{"web", "id-web"} {
		got, err := s.Instance(key)
		if err != nil {
			t.Fatalf("Instance(%q): %v", key, err)
		}
		if got.Name != "web" {
			t.Errorf("Instance(%q) = %q, want %q", key, got.Name, "web")
		}
	}

	if err := s.CreateInstance(testInstance("db")); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	var got []string
	for _, instance := range s.Instances() {
		got = append(got, instance.Name)
	}
	if want := []string{"db", "web"}; !slices.Equal(got, want) {
		t.Errorf("Instances = %q, want %q", got, want)
	}
}

func TestCreateDuplicateFailsWithExists(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateInstance(testInstance("web")); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	err := s.CreateInstance(testInstance("web"))
	if !errors.Is(err, errdefs.ErrExists) {
		t.Errorf("duplicate create error = %v, want ErrExists", err)
	}
}

func TestMissingInstanceIsNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Instance("nope")
	if !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestDefinitionsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DataDir: filepath.Join(dir, "data")}

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.CreateInstance(testInstance("web")); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := s.CreateNetwork(testNetwork("default")); err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}

	reopened, err := New(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	got, err := reopened.Instance("web")
	if err != nil {
		t.Fatalf("Instance after reopen: %v", err)
	}
	if got.ImageRef != "docker.io/library/alpine:latest" {
		t.Errorf("ImageRef = %q, want the value written before reopen", got.ImageRef)
	}
	if _, err := reopened.Network("default"); err != nil {
		t.Errorf("Network after reopen: %v", err)
	}
}

func TestDeleteRemovesInstanceDirectory(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateInstance(testInstance("web")); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// A sibling file in the instance directory (an overlay disk, say) must go
	// away with the instance.
	disk := filepath.Join(s.InstanceDir("web"), "overlay.img")
	if err := os.WriteFile(disk, []byte("disk"), 0o600); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	if err := s.DeleteInstance("web"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}
	if _, err := os.Stat(s.InstanceDir("web")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("instance directory still present after delete")
	}
}

func TestMalformedDefinitionIsSkippedNotFatal(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DataDir: filepath.Join(dir, "data")}

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.CreateInstance(testInstance("good")); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// A hand-edit gone wrong must not stop the daemon from starting.
	bad := filepath.Join(cfg.DataDir, "instances", "bad")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bad, configFile), []byte("{{{not yaml"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	reopened, err := New(cfg)
	if err != nil {
		t.Fatalf("New with a malformed definition present: %v", err)
	}

	if _, err := reopened.Instance("good"); err != nil {
		t.Errorf("good instance should still load: %v", err)
	}
	if _, err := reopened.Instance("bad"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("malformed instance error = %v, want ErrNotFound", err)
	}
}

// A definition copied to another place by hand still names its old one. It
// is skipped rather than loaded under a name that its own contents contradict.
func TestMisplacedDefinitionIsSkipped(t *testing.T) {
	cfg := Config{DataDir: filepath.Join(t.TempDir(), "data")}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.CreateInstance(testInstance("web")); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	copied := filepath.Join(cfg.DataDir, "instances", "copy")
	if err := os.MkdirAll(copied, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.InstanceDir("web"), configFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copied, configFile), data, 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := reopened.Instance("copy"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("Instance(copy) = %v, want the misplaced definition skipped", err)
	}
	if got, err := reopened.Instance("web"); err != nil || got.Name != "web" {
		t.Errorf("Instance(web) = %+v, %v; want the original", got, err)
	}
}

func TestCreateRejectsPathTraversal(t *testing.T) {
	s := newTestStore(t)

	err := s.CreateNetwork(network.Network{ID: "n1", Name: "../escape"})
	if !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Fatalf("CreateNetwork(../escape) = %v, want ErrInvalidArgument", err)
	}
}

func TestMatchingInstancesAreThoseMatchAccepts(t *testing.T) {
	s := newTestStore(t)
	for _, name := range []string{"web", "db", "cache"} {
		if err := s.CreateInstance(testInstance(name)); err != nil {
			t.Fatalf("CreateInstance %s: %v", name, err)
		}
	}

	tests := []struct {
		name  string
		match func(instance.Spec) bool
		want  []string
	}{
		{"none", func(instance.Spec) bool { return false }, nil},
		{"some", func(i instance.Spec) bool { return i.Name != "db" }, []string{"cache", "web"}},
		{"all", func(instance.Spec) bool { return true }, []string{"cache", "db", "web"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, instance := range s.MatchingInstances(tt.match) {
				got = append(got, instance.Name)
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("MatchingInstances = %q, want %q", got, tt.want)
			}
		})
	}
}

// Finding one instance among many costs a scan, not a copy and sort of
// them all, as Instances does.
func BenchmarkMatchingInstances(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			s, err := New(Config{
				DataDir: filepath.Join(b.TempDir(), "data"),
				Logger:  slog.New(slog.DiscardHandler),
			})
			if err != nil {
				b.Fatal(err)
			}
			for i := range size {
				if err := s.CreateInstance(testInstance("instance-" + strconv.Itoa(i))); err != nil {
					b.Fatal(err)
				}
			}

			b.ReportAllocs()
			for b.Loop() {
				s.MatchingInstances(func(i instance.Spec) bool { return i.Name == "instance-5" })
			}
		})
	}
}

func TestStagedSnapshotIsMovedIntoPlace(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DataDir: filepath.Join(dir, "data")}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	staged, err := s.StageSnapshot()
	if err != nil {
		t.Fatalf("StageSnapshot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staged, "overlay.img"), []byte("disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := instance.Snapshot{ID: "id-snap", Name: "snap", Kind: instance.SnapshotKindDisk, Instance: testInstance("web")}
	if err := s.CreateSnapshot(snapshot, staged); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	if _, err := os.Stat(filepath.Join(s.SnapshotDir("snap"), "overlay.img")); err != nil {
		t.Errorf("the staged file is not in the snapshot's directory: %v", err)
	}
	if _, err := os.Stat(staged); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the staging directory is still there")
	}

	reopened, err := New(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := reopened.Snapshot("id-snap")
	if err != nil {
		t.Fatalf("Snapshot after reopen: %v", err)
	}
	if got.Instance.Name != "web" {
		t.Errorf("Instance.Name = %q, want the instance it was taken from", got.Instance.Name)
	}
}

func TestCreateSnapshotOfATakenNameFails(t *testing.T) {
	s := newTestStore(t)

	for i, want := range []error{nil, errdefs.ErrExists} {
		staged, err := s.StageSnapshot()
		if err != nil {
			t.Fatalf("StageSnapshot: %v", err)
		}
		err = s.CreateSnapshot(instance.Snapshot{ID: "id-" + strconv.Itoa(i), Name: "snap"}, staged)
		if !errors.Is(err, want) {
			t.Errorf("CreateSnapshot %d = %v, want %v", i, err, want)
		}
	}
}

// TestStagingLeftByACrashIsRemoved checks that a snapshot a crash left
// half-written takes no space, and no name, once the store loads again.
func TestStagingLeftByACrashIsRemoved(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DataDir: filepath.Join(dir, "data")}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	staged, err := s.StageSnapshot()
	if err != nil {
		t.Fatalf("StageSnapshot: %v", err)
	}

	if _, err := New(cfg); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := os.Stat(staged); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the staging directory survived a reload")
	}
}

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
