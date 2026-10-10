// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nrednav/cuid2"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"gvisor.dev/gvisor/pkg/cleanup"

	"github.com/konradasb/dicer/internal/diskfile"
	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
	"github.com/konradasb/dicer/internal/guest"
	"github.com/konradasb/dicer/internal/health"
	"github.com/konradasb/dicer/internal/humanize"
	"github.com/konradasb/dicer/internal/hypervisor"
	"github.com/konradasb/dicer/internal/image"
	"github.com/konradasb/dicer/internal/image/reference"
	"github.com/konradasb/dicer/internal/naming"
	"github.com/konradasb/dicer/internal/process"
)

// SnapshotKind is what a Snapshot holds.
type SnapshotKind string

const (
	// SnapshotKindMemory is a running or paused guest frozen to disk: its
	// memory and device state, with its overlay disk as it was at the same
	// moment. Restoring it resumes the guest where it was.
	SnapshotKindMemory SnapshotKind = "memory"

	// SnapshotKindDisk is a stopped instance's overlay disk alone. Restoring
	// it rolls the disk back, and the guest boots from it afresh.
	SnapshotKindDisk SnapshotKind = "disk"
)

// Snapshot is an instance frozen to disk. It is a resource of its own: it
// outlives the instance it was taken from, which may since have been
// renamed, changed or deleted. It never holds the instance's volumes.
type Snapshot struct {
	ID   string       `yaml:"id" json:"id"`
	Name string       `yaml:"name" json:"name"`
	Kind SnapshotKind `yaml:"kind" json:"kind"`

	// Instance is the definition of the instance the snapshot was taken
	// from, as it was then.
	Instance Spec `yaml:"instance" json:"instance"`

	// IP and MAC are the guest's address on its network, which a memory
	// snapshot's guest keeps. A disk snapshot has neither.
	IP  string `yaml:"ip,omitempty" json:"ip,omitempty"`
	MAC string `yaml:"mac,omitempty" json:"mac,omitempty"`

	// HypervisorType and HypervisorVersion took a memory snapshot, and are
	// the only ones that can restore it. A disk snapshot has neither.
	HypervisorType    hypervisor.Type `yaml:"hypervisor_type,omitempty" json:"hypervisor_type,omitempty"`
	HypervisorVersion string          `yaml:"hypervisor_version,omitempty" json:"hypervisor_version,omitempty"`

	// VCPUs, MemoryBytes and ImageDigest are what a memory snapshot's guest
	// ran with, which a restore is admitted on and boots the image of. They
	// can differ from Instance's after a resize.
	VCPUs       int    `yaml:"vcpus,omitempty" json:"vcpus,omitempty"`
	MemoryBytes int64  `yaml:"memory_bytes,omitempty" json:"memory_bytes,omitempty"`
	ImageDigest string `yaml:"image_digest,omitempty" json:"image_digest,omitempty"`

	CreatedAt time.Time `yaml:"created_at" json:"created_at"`

	// SizeBytes is the space the snapshot occupies, measured when read.
	SizeBytes int64 `yaml:"-" json:"-"`
}

// createSnapshot is CreateSnapshot, for an instance its caller has looked up.
func (m *Manager) createSnapshot(
	ctx context.Context, instance Spec, name string,
) (_ Snapshot, err error) {
	started := time.Now()
	defer func() { m.observeOperation(operationCreateSnapshot, started, err) }()

	if name == "" {
		name = instance.Name + "-" + strings.ToLower(started.UTC().Format("20060102t150405z"))
	}
	if err := naming.Validate(name); err != nil {
		return Snapshot{}, err
	}
	// Checked again as the snapshot is recorded; this spares taking it.
	if _, err := m.store.Snapshot(name); err == nil {
		return Snapshot{}, errdefs.Exists("snapshot %q already exists", name)
	}

	staged, err := m.store.StageSnapshot()
	if err != nil {
		return Snapshot{}, err
	}
	// On success the directory has been moved into place, so this removes
	// only what a failure left.
	defer func() { _ = os.RemoveAll(staged) }()

	snapshot, paused, err := m.writeSnapshot(ctx, instance, staged)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.ID, snapshot.Name, snapshot.CreatedAt = cuid2.Generate(), name, started

	if err := m.store.CreateSnapshot(snapshot, staged); err != nil {
		return Snapshot{}, err
	}
	snapshot.SizeBytes, _ = diskfile.AllocatedBytesUnder(m.snapshotDir(snapshot))

	m.recordSnapshotCreated(snapshot, time.Since(started), paused)
	m.logger.InfoContext(ctx, "created snapshot",
		"instance", instance.Name, "snapshot", name, "kind", snapshot.Kind,
		"size_bytes", snapshot.SizeBytes, "paused_seconds", paused.Seconds())

	return snapshot, nil
}

// CreateSnapshot freezes an instance to disk as a snapshot called name, or,
// if name is empty, after the instance and the time. See writeSnapshot for
// what it holds.
func (m *Manager) CreateSnapshot(ctx context.Context, nameOrID, name string) (Snapshot, error) {
	instance, err := m.store.Instance(nameOrID)
	if err != nil {
		return Snapshot{}, err
	}
	return m.createSnapshot(ctx, instance, name)
}

// writeSnapshot freezes an instance into dir. It returns the snapshot that
// dir then holds, without an ID, name or time, and how long the guest was
// paused. Of a running or paused instance it writes a memory snapshot, and
// pauses a running one while it does. Of a stopped or failed one it writes a
// disk snapshot. A memory snapshot of an instance that can write to a volume
// is refused, because the volume is not in the snapshot and would not match
// what the restored guest remembers of it. So is a memory snapshot of an
// instance that mounts a host directory, because the hypervisor cannot save
// its device.
func (m *Manager) writeSnapshot(
	ctx context.Context, instance Spec, dir string,
) (Snapshot, time.Duration, error) {
	lock := m.lock(instance.ID)
	lock.Lock()
	defer lock.Unlock()
	if err := m.rereadDefinition(&instance); err != nil {
		return Snapshot{}, 0, err
	}

	status, err := m.statusOf(instance)
	if err != nil {
		return Snapshot{}, 0, err
	}

	snapshot := Snapshot{Instance: instance}
	switch status.State {
	case StateRunning, StatePaused:
		if instance.HasDirectoryMount() {
			return Snapshot{}, 0, errdefs.InvalidState("instance %q mounts a host directory, which a memory "+
				"snapshot cannot hold; stop it first", instance.Name)
		}
		if slices.ContainsFunc(instance.Mounts, func(mount Mount) bool {
			return mount.Type == MountTypeVolume && !mount.ReadOnly
		}) {
			return Snapshot{}, 0, errdefs.InvalidState("instance %q can write to a volume, which a snapshot does not hold; "+
				"stop it first, or mount its volumes read-only", instance.Name)
		}
		allocation, err := m.allocationOf(instance)
		if err != nil {
			return Snapshot{}, 0, err
		}
		snapshot.Kind = SnapshotKindMemory
		snapshot.IP, snapshot.MAC = allocation.IP, allocation.MAC
		snapshot.HypervisorType, snapshot.HypervisorVersion = instance.EffectiveHypervisorType(), status.HypervisorVersion
		snapshot.VCPUs, snapshot.MemoryBytes = status.VCPUs, status.MemoryBytes
		snapshot.ImageDigest = status.ImageDigest

		paused, err := m.writeMemorySnapshot(ctx, instance, status, dir)
		if err != nil {
			return Snapshot{}, 0, err
		}
		return snapshot, paused, nil
	case StateStopped, StateFailed:
		if _, err := os.Stat(m.overlayDiskPath(instance)); errors.Is(err, os.ErrNotExist) {
			return Snapshot{}, 0, errdefs.InvalidState("instance %q has never started, so it has no disk to copy", instance.Name)
		}
		snapshot.Kind = SnapshotKindDisk

		if err := diskfile.Copy(m.overlayDiskPath(instance), filepath.Join(dir, overlayDiskFile)); err != nil {
			return Snapshot{}, 0, fmt.Errorf("copy overlay disk: %w", err)
		}
		return snapshot, 0, nil
	default:
		return Snapshot{}, 0, errdefs.InvalidState("instance %q is %s; snapshot or fork it once it is running, paused or stopped",
			instance.Name, status.State.Lowercase())
	}
}

// writeMemorySnapshot writes a running or paused guest's memory, device
// state and overlay disk into dir, and returns how long the guest was paused
// for it: not at all if it already was.
func (m *Manager) writeMemorySnapshot(
	ctx context.Context, instance Spec, status Status, dir string,
) (paused time.Duration, err error) {
	hv, err := m.connect(instance, status)
	if err != nil {
		return 0, err
	}
	if err := requireCapability(instance, hv.Capabilities().SupportsSnapshot, "snapshots"); err != nil {
		return 0, err
	}

	running := status.State == StateRunning
	snapshotCtx, cancel := context.WithTimeout(ctx, memoryTransferTimeout(status.MemoryBytes))
	defer cancel()
	pausedAt, err := snapshotVM(snapshotCtx, hv, running, dir)
	if err != nil {
		return 0, err
	}
	if running {
		defer func() {
			paused = time.Since(pausedAt)
			if err := hv.ResumeVM(context.WithoutCancel(ctx)); err != nil {
				m.logger.ErrorContext(ctx, "could not resume instance after snapshot",
					"instance", instance.Name, "error", err)
			}
		}()
	}

	// Copied while the guest is still paused, so that the disk matches its
	// memory. Where the filesystem cannot reflink, this is a full copy, and
	// the guest stays paused for all of it.
	if err := diskfile.Copy(m.overlayDiskPath(instance), filepath.Join(dir, overlayDiskFile)); err != nil {
		return 0, fmt.Errorf("copy overlay disk: %w", err)
	}

	return 0, nil
}

// restoringMemoryPollInterval is how often snapshotVM tries again while the
// hypervisor is still restoring the guest's memory.
const restoringMemoryPollInterval = 250 * time.Millisecond

// snapshotVM writes a guest's state into dir, pausing it first if it is
// running, and returns the time it paused the guest. On success the guest is
// left paused. On failure, a guest it paused is resumed.
//
// A guest restored moments ago cannot be snapshotted until the hypervisor
// has restored all of its memory. snapshotVM tries again until it can, or
// ctx is done, and lets a running guest run while it waits.
func snapshotVM(ctx context.Context, hv hypervisor.Hypervisor, running bool, dir string) (time.Time, error) {
	for {
		if running {
			if err := hv.PauseVM(ctx); err != nil {
				return time.Time{}, fmt.Errorf("pause instance: %w", err)
			}
		}
		pausedAt := time.Now()

		err := hv.SnapshotVM(ctx, dir)
		if err == nil {
			return pausedAt, nil
		}
		if running {
			if resumeErr := hv.ResumeVM(context.WithoutCancel(ctx)); resumeErr != nil {
				return time.Time{}, errors.Join(err, fmt.Errorf("resume instance: %w", resumeErr))
			}
		}
		if !errors.Is(err, hypervisor.ErrRestoringMemory) {
			return time.Time{}, err
		}

		select {
		case <-ctx.Done():
			return time.Time{}, fmt.Errorf("%w: %w", err, ctx.Err())
		case <-time.After(restoringMemoryPollInterval):
		}
	}
}

// recordSnapshotCreated records the event of a snapshot taken in took,
// pausing its guest for paused.
func (m *Manager) recordSnapshotCreated(snapshot Snapshot, took, paused time.Duration) {
	attrs := map[string]string{
		"instance":   snapshot.Instance.Name,
		"kind":       string(snapshot.Kind),
		"size_bytes": strconv.FormatInt(snapshot.SizeBytes, 10),
	}
	message := fmt.Sprintf("Took %s snapshot of instance %s in %s", snapshot.Kind, snapshot.Instance.Name, humanize.Duration(took))
	if paused > 0 {
		attrs["paused_seconds"] = strconv.FormatFloat(paused.Seconds(), 'f', 3, 64)
		message += ", pausing it for " + humanize.Duration(paused)
	}
	m.recordSnapshot(snapshot, event.ActionCreated, message+": "+humanize.Bytes(snapshot.SizeBytes), attrs)
}

// memoryTransferTimeout bounds writing or reading a guest's memory of the
// given size: a minute, and a second more for every 100 MiB, which a disk
// doing 100 MiB/s keeps up with.
func memoryTransferTimeout(memoryBytes int64) time.Duration {
	return time.Minute + time.Duration(memoryBytes/(100<<20))*time.Second
}

// Snapshots returns every snapshot, oldest first.
func (m *Manager) Snapshots() []Snapshot {
	snapshots := m.store.Snapshots()
	for i := range snapshots {
		snapshots[i].SizeBytes, _ = diskfile.AllocatedBytesUnder(m.snapshotDir(snapshots[i]))
	}
	slices.SortFunc(snapshots, func(a, b Snapshot) int { return a.CreatedAt.Compare(b.CreatedAt) })

	return snapshots
}

// Snapshot returns a snapshot by name or ID, or an errdefs.ErrNotFound error
// if there is none.
func (m *Manager) Snapshot(nameOrID string) (Snapshot, error) {
	snapshot, err := m.store.Snapshot(nameOrID)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.SizeBytes, _ = diskfile.AllocatedBytesUnder(m.snapshotDir(snapshot))

	return snapshot, nil
}

// deleteSnapshot is DeleteSnapshot, for a snapshot its caller has looked up.
func (m *Manager) deleteSnapshot(ctx context.Context, snapshot Snapshot) (err error) {
	started := time.Now()
	defer func() { m.observeOperation(operationDeleteSnapshot, started, err) }()

	// A restore from the snapshot holds its instance's lock.
	lock := m.lock(snapshot.Instance.ID)
	lock.Lock()
	defer lock.Unlock()

	if err := m.store.DeleteSnapshot(snapshot.ID); err != nil {
		return err
	}

	m.recordSnapshot(snapshot, event.ActionDeleted, "Deleted snapshot of instance "+snapshot.Instance.Name,
		map[string]string{"instance": snapshot.Instance.Name})
	m.logger.InfoContext(ctx, "deleted snapshot", "instance", snapshot.Instance.Name, "snapshot", snapshot.Name)

	return nil
}

// DeleteSnapshot removes a snapshot and its files.
func (m *Manager) DeleteSnapshot(ctx context.Context, nameOrID string) error {
	snapshot, err := m.store.Snapshot(nameOrID)
	if err != nil {
		return err
	}
	return m.deleteSnapshot(ctx, snapshot)
}

// restoreSnapshot is RestoreSnapshot, for a snapshot its caller has looked up.
func (m *Manager) restoreSnapshot(ctx context.Context, snapshot Snapshot) (_ Spec, err error) {
	started := time.Now()
	defer func() { m.observeOperation(operationRestoreSnapshot, started, err) }()

	lock := m.lock(snapshot.Instance.ID)
	lock.Lock()
	defer lock.Unlock()

	instance, err := m.store.Instance(snapshot.Instance.ID)
	if errors.Is(err, errdefs.ErrNotFound) {
		return Spec{}, errdefs.NotFound("instance %q, which snapshot %q was taken of, has been deleted",
			snapshot.Instance.Name, snapshot.Name)
	}
	if err != nil {
		return Spec{}, err
	}

	status, err := m.statusOf(instance)
	if err != nil {
		return Spec{}, err
	}
	if status.State.IsActive() {
		return Spec{}, errdefs.InvalidState("instance %q is %s; stop it before restoring a snapshot",
			instance.Name, status.State.Lowercase())
	}
	// Its frozen guest resumes at the next start, on the overlay disk as it
	// left it. A restore would change that disk under it, or run another
	// guest in its place.
	if status.State == StateStandby {
		return Spec{}, errdefs.InvalidState("instance %q is on standby; "+
			"stop it, which discards its frozen guest, before restoring a snapshot", instance.Name)
	}
	m.cancelRestart(instance.ID)

	switch snapshot.Kind {
	case SnapshotKindMemory:
		err = m.restoreMemory(ctx, instance, snapshot)
	case SnapshotKindDisk:
		err = m.restoreDisk(ctx, instance, snapshot)
	default:
		err = fmt.Errorf("snapshot %q is of unknown kind %q", snapshot.Name, snapshot.Kind)
	}
	if err != nil {
		return Spec{}, err
	}

	m.logger.InfoContext(ctx, "restored snapshot",
		"instance", instance.Name, "snapshot", snapshot.Name, "kind", snapshot.Kind)
	return instance, nil
}

// RestoreSnapshot puts the instance snapshot was taken from back as it was
// then, discarding whatever it has written to its disk since, and returns
// the instance. The instance must be stopped: one on standby is refused,
// since its frozen guest would be lost. A memory snapshot resumes its guest
// where it was; a disk snapshot leaves it stopped, to boot from the disk at
// its next start.
func (m *Manager) RestoreSnapshot(ctx context.Context, nameOrID string) (Spec, error) {
	snapshot, err := m.store.Snapshot(nameOrID)
	if err != nil {
		return Spec{}, err
	}
	return m.restoreSnapshot(ctx, snapshot)
}

// restoreDisk rolls instance's overlay disk back to a disk snapshot's.
func (m *Manager) restoreDisk(ctx context.Context, instance Spec, snapshot Snapshot) error {
	// Copy replaces the disk only once it has all of the snapshot's.
	if err := diskfile.Copy(m.snapshotOverlayDiskPath(snapshot), m.overlayDiskPath(instance)); err != nil {
		return fmt.Errorf("restore overlay disk: %w", err)
	}

	m.record(instance, event.ActionSnapshotRestored, fmt.Sprintf("Rolled back disk to snapshot %q taken %s",
		snapshot.Name, snapshot.CreatedAt.Local().Format(time.DateTime)), map[string]string{"snapshot": snapshot.Name})
	m.logger.DebugContext(ctx, "restored overlay disk", "instance", instance.Name, "snapshot", snapshot.Name)

	return nil
}

// restoreMemory resumes instance from a memory snapshot of it: its guest
// where it was, on its disk as it was. It is refused if the instance's mounts
// or address have changed since, which the guest still has as they were.
func (m *Manager) restoreMemory(ctx context.Context, instance Spec, snapshot Snapshot) error {
	started := time.Now()

	if !slices.EqualFunc(instance.Mounts, snapshot.Instance.Mounts, Mount.Equal) {
		return errdefs.InvalidState("instance %q's mounts have changed since snapshot %q was taken, "+
			"and its guest expects them as they were; change them back to restore it", instance.Name, snapshot.Name)
	}
	allocation, err := m.allocationOf(instance)
	if err != nil && !errors.Is(err, errdefs.ErrNotFound) {
		return err
	}
	if allocation.IP != snapshot.IP || allocation.MAC != snapshot.MAC {
		return errdefs.InvalidState("instance %q no longer has the address %s, which the guest in snapshot %q has; "+
			"a memory snapshot can be restored only where it was taken, or forked", instance.Name, snapshot.IP, snapshot.Name)
	}

	if err := m.resume(ctx, instance, m.frozenSnapshot(snapshot)); err != nil {
		return err
	}

	m.record(instance, event.ActionSnapshotRestored, fmt.Sprintf("Restored instance from snapshot %q taken %s in %s: memory and disk rolled back",
		snapshot.Name, snapshot.CreatedAt.Local().Format(time.DateTime), humanize.Duration(time.Since(started))),
		map[string]string{"snapshot": snapshot.Name})
	return nil
}

// frozenGuest is a guest frozen to disk, to be resumed: a memory snapshot's,
// or an instance's on standby.
type frozenGuest struct {
	// snapshot is what the guest ran with.
	snapshot Snapshot
	// dir holds the hypervisor's files.
	dir string
	// overlay is a copy of the guest's disk to resume it on, or empty to
	// resume it on the instance's own disk, as standby left it.
	overlay string
}

// frozenSnapshot returns the guest a memory snapshot holds.
func (m *Manager) frozenSnapshot(snapshot Snapshot) frozenGuest {
	return frozenGuest{snapshot: snapshot, dir: m.snapshotDir(snapshot), overlay: m.snapshotOverlayDiskPath(snapshot)}
}

// resume runs instance from a frozen guest: its own, or, as a fork,
// another's. It records the instance running. On failure the instance is
// marked failed, and keeps the disk it had. The caller must hold the
// instance lock.
func (m *Manager) resume(ctx context.Context, instance Spec, frozen frozenGuest) (err error) {
	snapshot := frozen.snapshot
	starter, err := m.snapshotStarter(snapshot)
	if err != nil {
		return err
	}

	need := Resources{VCPUs: snapshot.VCPUs, MemoryBytes: snapshot.MemoryBytes}
	if err := m.admit(instance, need); err != nil {
		return err
	}
	m.setStoppedByUser(ctx, instance, false)
	defer func() {
		if err != nil {
			m.fail(instance.ID, err)
		}
	}()

	image, err := m.snapshotImage(ctx, snapshot)
	if err != nil {
		return err
	}

	vmm, undo, err := m.restore(ctx, instance, frozen, starter, image)
	if err != nil {
		return err
	}
	cu := cleanup.Make(undo)
	defer cu.Clean()

	run := runRecord{
		hypervisorVersion: snapshot.HypervisorVersion,
		held:              need,
		imageDigest:       snapshot.ImageDigest,
		vsockCID:          vsockCID(snapshot.Instance.ID),
		healthCheck:       health.EffectiveCheck(instance.HealthCheck, image.HealthCheck),
	}
	running, err := m.recordRunning(instance, vmm, run)
	if err != nil {
		return err
	}

	cu.Release()
	_ = os.Remove(m.keptOverlayDiskPath(instance))
	m.supervise(ctx, instance, vmm, running)
	return nil
}

// restore recreates the disks and TAP device a frozen guest's devices refer
// to, restores the VMM and resumes the guest. It tells the guest the time
// and, if it is a fork of another instance's, its own identity. The returned
// function undoes all of it, putting back the overlay disk the instance had.
func (m *Manager) restore(
	ctx context.Context, instance Spec, frozen frozenGuest, starter hypervisor.Starter, image *image.Image,
) (*process.Process, func(), error) {
	snapshot := frozen.snapshot
	cu := cleanup.Make(func() {})
	defer cu.Clean()

	if err := m.prepareRuntimeDir(instance); err != nil {
		return nil, nil, err
	}
	cu.Add(func() { _ = m.removeRuntimeDir(instance.ID) })

	if frozen.overlay != "" {
		// The disk is kept under a second name until the restore succeeds.
		// One already there is from a restore a crash interrupted. A fork
		// has no disk yet to keep.
		overlay, kept := m.overlayDiskPath(instance), m.keptOverlayDiskPath(instance)
		_ = os.Remove(kept)
		switch err := os.Link(overlay, kept); {
		case err == nil:
			cu.Add(func() { _ = os.Rename(kept, overlay) })
		case !errors.Is(err, fs.ErrNotExist):
			return nil, nil, fmt.Errorf("keep overlay disk: %w", err)
		}

		if err := diskfile.Copy(frozen.overlay, overlay); err != nil {
			return nil, nil, fmt.Errorf("restore overlay disk: %w", err)
		}
	}

	// The restored VMM keeps the disks it was snapshotted with; only the
	// config disk is written afresh.
	mounts, err := m.resolveMounts(instance)
	if err != nil {
		return nil, nil, err
	}

	setup, err := m.setupNetwork(ctx, instance)
	if err != nil {
		return nil, nil, err
	}
	cu.Add(setup.cleanup)

	// A fork wakes with the address of the instance it is a copy of, which
	// may still be using it: nothing it sends may reach the network until it
	// has its own.
	forked := instance.ID != snapshot.Instance.ID
	if forked {
		if err := m.hostNetwork.DisconnectTAP(ctx, &setup.network, instance.ID); err != nil {
			return nil, nil, err
		}
	}

	// The restored guest has already booted once.
	if err := m.writeGuestDisks(ctx, instance, starter, image, mounts.guest, setup, guest.Status{Boots: 1}); err != nil {
		return nil, nil, err
	}

	restoreCtx, cancel := context.WithTimeout(ctx, memoryTransferTimeout(snapshot.MemoryBytes))
	defer cancel()
	spec := hypervisor.RestoreSpec{Console: hypervisor.ConsoleConfig{Path: serialLogFile}, TAPDevice: setup.nic.TAPDevice}
	vmm, hv, err := starter.RestoreVM(restoreCtx, m.hypervisorSocketPath(instance.ID), frozen.dir, spec)
	if err != nil {
		return nil, nil, fmt.Errorf("restore vm: %w", err)
	}
	cu.Add(vmm.Terminate)

	// Hypervisors restore a guest paused.
	if err := hv.ResumeVM(ctx); err != nil {
		return nil, nil, fmt.Errorf("resume restored instance: %w", err)
	}
	// A guest resumed from standby runs on its own disk, so what was frozen
	// no longer matches it and must not be resumed again, even if the rest
	// fails. The VMM keeps open what it still reads, so the files can go.
	if frozen.overlay == "" {
		if err := os.RemoveAll(frozen.dir); err != nil {
			m.logger.WarnContext(ctx, "cannot remove a resumed instance's standby", "instance", instance.Name, "error", err)
		}
	}

	// The guest's clock stood still in the snapshot. One whose agent is too
	// old to set it is left behind, which is no reason to fail.
	if err := m.setGuestClock(ctx, m.vsockPath(instance.ID), time.Now()); err != nil {
		m.logger.WarnContext(ctx, "cannot set the restored guest's clock", "instance", instance.Name, "error", err)
	}

	if forked {
		if err := m.setGuestIdentity(ctx, m.vsockPath(instance.ID), guestIdentity(instance, setup)); err != nil {
			if grpcstatus.Code(err) == codes.Unimplemented {
				return nil, nil, errdefs.InvalidState("the guest of instance %q has an agent too old to take another identity; "+
					"restart that instance, then fork it or a new snapshot of it", snapshot.Instance.Name)
			}
			return nil, nil, fmt.Errorf("give the guest its own identity: %w", err)
		}
		if err := m.hostNetwork.ConnectTAP(ctx, &setup.network, instance.ID); err != nil {
			return nil, nil, err
		}
	}

	return vmm, cu.Release(), nil
}

// snapshotImage returns the image a memory snapshot's guest booted from,
// pulling it by digest if needed.
func (m *Manager) snapshotImage(ctx context.Context, snapshot Snapshot) (*image.Image, error) {
	ref, err := reference.Parse(snapshot.Instance.ImageRef)
	if err != nil {
		return nil, fmt.Errorf("image %q: %w", snapshot.Instance.ImageRef, err)
	}
	pinned := ref.Repository() + "@" + snapshot.ImageDigest

	image, err := m.images.Ensure(ctx, pinned, image.PullPolicyMissing)
	if err != nil {
		return nil, fmt.Errorf("get image %q: %w", pinned, err)
	}
	return image, nil
}
