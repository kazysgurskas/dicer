// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/hostfs"
	"github.com/konradasb/dicer/internal/hypervisor"
	"github.com/konradasb/dicer/internal/process"
	"github.com/konradasb/dicer/internal/virtiofs"
)

// fakeShares stands in for virtiofsd: each share is a process that lives
// until it is terminated, and what it was asked to share is recorded.
type fakeShares struct {
	mu       sync.Mutex
	started  []startedShare
	procs    []*process.Process
	startErr error
}

type startedShare struct {
	socket, dir string
	readOnly    bool
}

func (f *fakeShares) Start(ctx context.Context, s virtiofs.Share) (*process.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.startErr != nil && len(f.started) > 0 {
		// The first share starts; the second does not.
		return nil, f.startErr
	}
	p, err := process.Start(exec.CommandContext(ctx, "sleep", "60"))
	if err != nil {
		return nil, err
	}
	f.started = append(f.started, startedShare{socket: s.Socket, dir: s.Dir, readOnly: s.ReadOnly})
	f.procs = append(f.procs, p)
	return p, nil
}

// ended reports whether every share's process has ended.
func (f *fakeShares) ended(t *testing.T) bool {
	t.Helper()
	for _, p := range f.procs {
		select {
		case <-p.Done():
		case <-time.After(5 * time.Second):
			return false
		}
	}
	return true
}

// withDirectories gives the harness's instance two host directories to
// mount, the second read-only, under a directory its manager allows.
func withDirectories(t *testing.T, h *harness) (string, string) {
	t.Helper()
	allowed := t.TempDir()
	src, docs := filepath.Join(allowed, "src"), filepath.Join(allowed, "docs")
	for _, dir := range []string{src, docs} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.manager.allowedDirectories = hostfs.AllowedDirectories{allowed}
	h.instance.Mounts = []Mount{
		{Type: MountTypeDirectory, Source: src, Target: "/app"},
		{Type: MountTypeDirectory, Source: docs, Target: "/docs", ReadOnly: true},
	}
	h.store.instances[h.instance.Name] = h.instance
	return src, docs
}

func TestStartSharesDirectories(t *testing.T) {
	h := newHarness(t)
	shares := &fakeShares{}
	h.manager.shares = shares
	src, docs := withDirectories(t, h)

	h.start(t)

	runDir := h.manager.runtimeDir(h.instance.ID)
	want := []startedShare{
		{socket: filepath.Join(runDir, "fs0.sock"), dir: src},
		{socket: filepath.Join(runDir, "fs1.sock"), dir: docs, readOnly: true},
	}
	if !slices.Equal(shares.started, want) {
		t.Errorf("shares = %+v\nwant     %+v", shares.started, want)
	}

	// The VMM, which runs in the runtime directory, is given the sockets by
	// name.
	fs := h.starter.spec.Filesystems
	if len(fs) != 2 || fs[0].Tag != "dicerfs0" || fs[0].Socket != "fs0.sock" ||
		fs[1].Tag != "dicerfs1" || fs[1].Socket != "fs1.sock" {
		t.Errorf("filesystems = %+v, want a device on each share's socket", fs)
	}
}

func TestResolveMountsSharesDirectories(t *testing.T) {
	manager, store, _ := newTestManager(t)
	instance := seedInstance(t, store, "web")
	dir := t.TempDir()
	manager.allowedDirectories = hostfs.AllowedDirectories{dir}
	instance.Mounts = []Mount{{Type: MountTypeDirectory, Source: dir, Target: "/app", ReadOnly: true}}

	resolved, err := manager.resolveMounts(instance)
	mounts, disks, shares := resolved.guest, resolved.disks, resolved.shares
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 0 {
		t.Errorf("disks = %+v, want none: a directory is no disk", disks)
	}
	if len(mounts) != 1 || mounts[0].Directory == nil || mounts[0].Directory.Tag != "dicerfs0" || !mounts[0].ReadOnly {
		t.Errorf("mounts = %+v, want the share mounted read-only by its tag", mounts)
	}
	if len(shares) != 1 || shares[0].source.Path != dir || shares[0].tag != "dicerfs0" {
		t.Errorf("shares = %+v, want the directory", shares)
	}

	// What is not there, is a file, or is not allowed cannot be shared.
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{filepath.Join(dir, "missing"), file, t.TempDir()} {
		instance.Mounts[0].Source = source
		if _, err := manager.resolveMounts(instance); err == nil {
			t.Errorf("resolveMounts shared %s", source)
		}
	}
}

// TestStartRefusesDirectoriesNotAllowed checks that an instance can mount
// only a directory the daemon's configuration allows, so that a request
// alone cannot reach the host's files.
func TestStartRefusesDirectoriesNotAllowed(t *testing.T) {
	h := newHarness(t)
	shares := &fakeShares{}
	h.manager.shares = shares
	withDirectories(t, h)
	h.manager.allowedDirectories = nil

	err := h.manager.Start(context.Background(), h.instance.Name)
	if !errors.Is(err, errdefs.ErrInvalidArgument) || !strings.Contains(err.Error(), "mounts.allowed_directories") {
		t.Errorf("Start = %v, want the directory refused as not allowed", err)
	}
	if len(shares.started) != 0 {
		t.Errorf("shares started = %+v, want none", shares.started)
	}
}

func TestStartWithoutVirtiofsd(t *testing.T) {
	h := newHarness(t)
	h.manager.shares = nil
	withDirectories(t, h)

	err := h.manager.Start(context.Background(), h.instance.Name)
	if !errors.Is(err, errNoShares) {
		t.Errorf("Start = %v, want errNoShares", err)
	}
	if h.starter.vmmCount() != 0 {
		t.Error("a VMM was started for an instance whose directories cannot be shared")
	}
}

func TestStartStopsSharesWhenItFails(t *testing.T) {
	// The VMM does not start: the shares already started are stopped.
	h := newHarness(t)
	shares := &fakeShares{}
	h.manager.shares = shares
	withDirectories(t, h)
	h.starter.startErr = errors.New("no KVM")

	if err := h.manager.Start(context.Background(), h.instance.Name); err == nil {
		t.Fatal("Start succeeded with a VMM that does not start")
	}
	if len(shares.procs) != 2 || !shares.ended(t) {
		t.Errorf("%d shares were started, and not all were stopped when the start failed", len(shares.procs))
	}

	// The second share does not start: the first is stopped.
	h2 := newHarness(t)
	shares2 := &fakeShares{startErr: errors.New("virtiofsd exited")}
	h2.manager.shares = shares2
	withDirectories(t, h2)

	err := h2.manager.Start(context.Background(), h2.instance.Name)
	if err == nil || !strings.Contains(err.Error(), "mount on /docs") {
		t.Errorf("Start = %v, want the failing share named", err)
	}
	if len(shares2.procs) != 1 || !shares2.ended(t) {
		t.Error("the share that started was not stopped when the next one failed")
	}
	if h2.starter.vmmCount() != 0 {
		t.Error("a VMM was started though a share failed")
	}
}

func TestStartRefusesDirectoriesOnFirecracker(t *testing.T) {
	h := newHarness(t)
	h.manager.shares = &fakeShares{}
	withDirectories(t, h)
	h.instance.HypervisorType = hypervisor.TypeFirecracker
	h.store.instances[h.instance.Name] = h.instance
	h.manager.starters[hypervisor.TypeFirecracker] = h.manager.starters[hypervisor.TypeCloudHypervisor]

	err := h.manager.Start(context.Background(), h.instance.Name)
	if err == nil || !strings.Contains(err.Error(), "directory mounts need cloud-hypervisor") {
		t.Errorf("Start on Firecracker = %v, want directory mounts refused", err)
	}
}

// TestFreezingRefusesSharedDirectories checks that an instance that shares a
// directory is not frozen with its memory, as a memory snapshot or on
// standby, as the hypervisor cannot save the device; stopped, its disk can
// be snapshotted.
func TestFreezingRefusesSharedDirectories(t *testing.T) {
	h := newHarness(t)
	h.manager.shares = &fakeShares{}
	withDirectories(t, h)
	h.start(t)

	_, err := h.manager.CreateSnapshot(t.Context(), h.instance.Name, "before")
	if !errors.Is(err, errdefs.ErrInvalidState) || !strings.Contains(err.Error(), "mounts a host directory") {
		t.Errorf("CreateSnapshot of the running instance = %v, want it refused", err)
	}
	if err := h.manager.Standby(t.Context(), h.instance.Name); !errors.Is(err, errdefs.ErrInvalidState) {
		t.Errorf("Standby = %v, want it refused", err)
	}

	if err := h.manager.Stop(t.Context(), h.instance.Name); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	snapshot, err := h.manager.CreateSnapshot(t.Context(), h.instance.Name, "stopped")
	if err != nil || snapshot.Kind != SnapshotKindDisk {
		t.Errorf("CreateSnapshot of the stopped instance = %+v, %v; want a disk snapshot", snapshot, err)
	}
}
