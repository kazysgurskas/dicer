// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package virtiofs

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeEnv makes the test binary act as virtiofsd: one that listens on
// --socket-path until it is killed, or exits at once if the variable says
// "fail".
const fakeEnv = "DICER_FAKE_VIRTIOFSD"

func TestMain(m *testing.M) {
	if mode, ok := os.LookupEnv(fakeEnv); ok && len(os.Args) > 1 {
		os.Exit(fakeVirtiofsd(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeVirtiofsd(mode string, args []string) int {
	if mode == "fail" {
		fmt.Fprintln(os.Stderr, "Error: shared directory is not a directory")
		return 1
	}

	i := slices.Index(args, "--socket-path")
	if i < 0 || i+1 >= len(args) {
		return 2
	}
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", args[i+1])
	if err != nil {
		return 3
	}
	defer func() { _ = l.Close() }()
	time.Sleep(time.Minute)
	return 0
}

// fake returns a Daemon running the test binary as virtiofsd, in mode.
func fake(t *testing.T, mode string) *Daemon {
	t.Helper()
	t.Setenv(fakeEnv, mode)

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(self)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestArgs(t *testing.T) {
	d := &Daemon{binary: "/usr/libexec/virtiofsd"}

	got := d.args(Share{Dir: "/home/me/src", Socket: "/run/fs0.sock", ReadOnly: true})
	want := []string{
		"/usr/libexec/virtiofsd", "--socket-path", "/run/fs0.sock", "--shared-dir", "/home/me/src",
		"--cache", "auto", "--sandbox", "namespace", "--announce-submounts", "--readonly",
	}
	if !slices.Equal(got, want) {
		t.Errorf("args = %q\nwant  %q", got, want)
	}
	if slices.Contains(d.args(Share{Dir: "/src", Socket: "/run/fs0.sock"}), "--readonly") {
		t.Error("a writable share was made read-only")
	}

	// From a mount namespace of its own, the daemon runs it in the host's.
	entered := &Daemon{binary: "virtiofsd", enter: []string{"/usr/bin/nsenter", "--mount=/proc/1/ns/mnt", "--"}}
	if got := entered.args(Share{Dir: "/d", Socket: "/s"}); !slices.Equal(got[:4], []string{
		"/usr/bin/nsenter", "--mount=/proc/1/ns/mnt", "--", "virtiofsd",
	}) {
		t.Errorf("args = %q, want virtiofsd run in PID 1's mount namespace", got)
	}
}

// rootDir is what the fake virtiofsd shares: the root it runs in, as it has
// no sandbox to enter. Checking it needs /proc.
func rootDir(t *testing.T) fs.FileInfo {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("a process's root is read from /proc")
	}

	info, err := os.Stat("/")
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestStart(t *testing.T) {
	d := fake(t, "listen")
	dir := t.TempDir()
	socket := filepath.Join(dir, "fs0.sock")
	// A socket left by an earlier start is replaced.
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := d.Start(t.Context(), Share{
		Dir: "/", DirInfo: rootDir(t), Socket: socket, Log: filepath.Join(dir, "logs", "fs0.log"),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(p.Terminate)

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", socket)
	if err != nil {
		t.Fatalf("nothing listens on the socket: %v", err)
	}
	_ = conn.Close()

	p.Terminate()
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Error("virtiofsd did not end when terminated")
	}
}

func TestStartRefusesSocketPathTooLong(t *testing.T) {
	dir := t.TempDir()
	long := Share{
		Dir:     dir,
		DirInfo: dirInfo(t, dir),
		Socket:  "/" + strings.Repeat("x", maxSocketPath) + ".sock",
		Log:     filepath.Join(dir, "fs0.log"),
	}
	if _, err := fake(t, "listen").Start(t.Context(), long); err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Errorf("Start on a socket path too long to bind = %v, want it refused", err)
	}
}

func TestStartReportsWhyItFailed(t *testing.T) {
	d := fake(t, "fail")
	dir := t.TempDir()

	_, err := d.Start(t.Context(), Share{
		Dir: "/nope", DirInfo: dirInfo(t, dir), Socket: filepath.Join(dir, "fs0.sock"), Log: filepath.Join(dir, "fs0.log"),
	})
	if err == nil || !strings.Contains(err.Error(), "shared directory is not a directory") {
		t.Errorf("Start = %v, want virtiofsd's own error", err)
	}
}

// TestStartRefusesToShareAnotherDirectory checks that virtiofsd is not left
// sharing a directory other than the one checked, as it would once a link
// put in the way of the checked one's path pointed elsewhere.
func TestStartRefusesToShareAnotherDirectory(t *testing.T) {
	rootDir(t)
	d := fake(t, "listen")
	dir := t.TempDir()

	// The fake shares the root it runs in, never the directory checked.
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	p, err := d.Start(ctx, Share{
		Dir: dir, DirInfo: dirInfo(t, dir), Socket: filepath.Join(dir, "fs0.sock"), Log: filepath.Join(dir, "fs0.log"),
	})
	if err == nil {
		p.Terminate()
		t.Fatal("Start shared a directory other than the one checked")
	}
	if !strings.Contains(err.Error(), "did not share the directory checked") {
		t.Errorf("Start = %v, want it refused for sharing another directory", err)
	}
}

// TestStartRefusesAnUncheckedDirectory checks that a share must say which
// directory was checked.
func TestStartRefusesAnUncheckedDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := fake(t, "listen").Start(t.Context(), Share{Dir: dir, Socket: filepath.Join(dir, "fs0.sock"), Log: filepath.Join(dir, "fs0.log")})
	if err == nil || !strings.Contains(err.Error(), "not checked") {
		t.Errorf("Start = %v, want it refused", err)
	}
}

func dirInfo(t *testing.T, dir string) fs.FileInfo {
	t.Helper()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
