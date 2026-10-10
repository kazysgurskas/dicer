// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/guest"
	"github.com/konradasb/dicer/internal/health"
	"github.com/konradasb/dicer/internal/humanize"
	"github.com/konradasb/dicer/internal/hypervisor"
	"github.com/konradasb/dicer/internal/image/reference"
	"github.com/konradasb/dicer/internal/naming"
	"github.com/konradasb/dicer/internal/network"
)

// Instance is a virtual machine. Spec is the desired state, persisted across
// reboots. Status is the observed state, kept in the runtime directory and
// empty after a reboot.
type Instance struct {
	Spec   Spec   `json:"spec"`
	Status Status `json:"status"`
}

// Spec is the persistent definition of a virtual machine. Referenced kernels,
// networks and mounts are resolved at each start.
type Spec struct {
	ID       string `yaml:"id" json:"id"`
	Name     string `yaml:"name" json:"name"`
	Hostname string `yaml:"hostname,omitempty" json:"hostname,omitempty"`
	ImageRef string `yaml:"image_ref" json:"image_ref"`

	// ImageDigest is the digest ImageRef resolved to when the instance was
	// created. The instance always boots that image: pulling ImageRef again
	// does not change it.
	ImageDigest string `yaml:"image_digest" json:"image_digest"`

	HypervisorType    hypervisor.Type `yaml:"hypervisor_type,omitempty" json:"hypervisor_type,omitempty"`
	HypervisorVersion string          `yaml:"hypervisor_version,omitempty" json:"hypervisor_version,omitempty"`
	KernelName        string          `yaml:"kernel_name" json:"kernel_name"`
	KernelArgs        string          `yaml:"kernel_args,omitempty" json:"kernel_args,omitempty"`
	VCPUs             int             `yaml:"vcpus" json:"vcpus"`
	MemoryBytes       int64           `yaml:"memory_bytes" json:"memory_bytes"`
	DiskBytes         int64           `yaml:"disk_bytes" json:"disk_bytes"`
	NetworkName       string          `yaml:"network_name" json:"network_name"`
	StaticIP          string          `yaml:"static_ip,omitempty" json:"static_ip,omitempty"`

	// MaxVCPUs and MaxMemoryBytes are the most the instance can be resized
	// to while it runs: it boots with room for them. Zero leaves no room.
	MaxVCPUs       int   `yaml:"max_vcpus,omitempty" json:"max_vcpus,omitempty"`
	MaxMemoryBytes int64 `yaml:"max_memory_bytes,omitempty" json:"max_memory_bytes,omitempty"`

	// DiskBytesPerSecond and DiskIOPS limit the bytes and operations per
	// second each of the instance's disks is read and written at.
	// UploadBytesPerSecond and DownloadBytesPerSecond limit the bytes per
	// second its guest sends and receives. Zero is unlimited.
	DiskBytesPerSecond     int64 `yaml:"disk_bytes_per_second,omitempty" json:"disk_bytes_per_second,omitempty"`
	DiskIOPS               int64 `yaml:"disk_iops,omitempty" json:"disk_iops,omitempty"`
	UploadBytesPerSecond   int64 `yaml:"upload_bytes_per_second,omitempty" json:"upload_bytes_per_second,omitempty"`
	DownloadBytesPerSecond int64 `yaml:"download_bytes_per_second,omitempty" json:"download_bytes_per_second,omitempty"`

	// StandbyAfter is how long the instance may be idle, running but doing
	// next to nothing, before it is put on standby. Zero is never.
	StandbyAfter time.Duration `yaml:"standby_after,omitempty" json:"standby_after,omitempty"`

	Ports  []network.PortMapping `yaml:"ports,omitempty" json:"ports,omitempty"`
	Mounts []Mount               `yaml:"mounts,omitempty" json:"mounts,omitempty"`
	Env    map[string]string     `yaml:"env,omitempty" json:"env,omitempty"`
	Cmd    []string              `yaml:"cmd,omitempty" json:"cmd,omitempty"`

	// InitMode is how the guest starts the command: auto, exec or systemd.
	// Empty is auto: systemd if the command is systemd, else exec.
	InitMode guest.InitMode `yaml:"init_mode,omitempty" json:"init_mode,omitempty"`

	// User is who the workload runs as in the exec init mode: user, uid,
	// user:group or uid:gid, looked up in the guest's /etc/passwd and
	// /etc/group. Empty is the image's USER, or root if it has none.
	User string `yaml:"user,omitempty" json:"user,omitempty"`

	Labels  map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Restart RestartPolicy     `yaml:"restart,omitempty" json:"restart,omitzero"`

	// HealthCheck is how the instance's health is checked, overriding its
	// image's; a Disabled one switches the image's off. Nil means the
	// image's, if it declares one.
	HealthCheck *health.Check `yaml:"healthcheck,omitempty" json:"health_check,omitempty"`

	CreatedAt time.Time `yaml:"created_at" json:"created_at,omitzero"`
	UpdatedAt time.Time `yaml:"updated_at" json:"updated_at,omitzero"`

	// RemoveOnExit deletes the instance once it stops, unless its restart
	// policy will start it again.
	RemoveOnExit bool `yaml:"remove_on_exit,omitempty" json:"remove_on_exit,omitempty"`

	// StoppedByUser records that a user stopped the instance and has not
	// started it since. An unless-stopped instance is not started at boot
	// while it is set.
	StoppedByUser bool `yaml:"stopped_by_user,omitempty" json:"stopped_by_user,omitempty"`
}

// Resources returns what the instance asks for.
func (s Spec) Resources() Resources {
	return Resources{VCPUs: s.VCPUs, MemoryBytes: s.MemoryBytes}
}

// MaxResources returns the most the instance can hold: its maximums where
// set, otherwise what it asks for.
func (s Spec) MaxResources() Resources {
	return Resources{VCPUs: max(s.VCPUs, s.MaxVCPUs), MemoryBytes: max(s.MemoryBytes, s.MaxMemoryBytes)}
}

// PinnedImageRef returns the reference the instance boots its image by: the
// repository ImageRef names, at ImageDigest.
func (s Spec) PinnedImageRef() (string, error) {
	ref, err := reference.Parse(s.ImageRef)
	if err != nil {
		return "", fmt.Errorf("image %q: %w", s.ImageRef, err)
	}
	return ref.Repository() + "@" + s.ImageDigest, nil
}

// Validate returns an invalid argument error if the instance cannot be run
// as defined, as far as the definition alone can tell: an invalid name or
// hostname, no image, a size it cannot have, a maximum below what it asks
// for or that its hypervisor cannot honour, a negative rate limit, a user
// that is malformed or that systemd cannot run as, a request to be deleted
// when it stops that its restart policy contradicts, or invalid or clashing
// ports or mounts.
func (s Spec) Validate() error {
	if err := naming.Validate(s.Name); err != nil {
		return err
	}
	if err := naming.ValidateHostname(s.Hostname); err != nil {
		return err
	}

	switch {
	case s.ImageRef == "":
		return errdefs.InvalidArgument("an instance needs an image")
	case s.VCPUs <= 0:
		return errdefs.InvalidArgument("an instance needs at least 1 vCPU")
	case s.MemoryBytes <= 0:
		return errdefs.InvalidArgument("an instance needs more than 0 bytes of memory")
	case s.DiskBytes <= 0:
		return errdefs.InvalidArgument("an instance needs a disk of more than 0 bytes")
	case s.MaxVCPUs < 0 || s.MaxVCPUs > 0 && s.MaxVCPUs < s.VCPUs:
		return errdefs.InvalidArgument("max_vcpus %d is below the instance's %s",
			s.MaxVCPUs, humanize.Count(s.VCPUs, "vCPU"))
	case s.MaxMemoryBytes < 0 || s.MaxMemoryBytes > 0 && s.MaxMemoryBytes < s.MemoryBytes:
		return errdefs.InvalidArgument("max_memory_bytes %s is below the instance's %s memory",
			humanize.Bytes(s.MaxMemoryBytes), humanize.Bytes(s.MemoryBytes))
	case s.MaxVCPUs > 0 && s.EffectiveHypervisorType() == hypervisor.TypeFirecracker:
		return errdefs.InvalidArgument("firecracker cannot add vCPUs to a running guest: leave max_vcpus unset, " +
			"or use cloud-hypervisor")
	case s.DiskBytesPerSecond < 0 || s.DiskIOPS < 0 || s.UploadBytesPerSecond < 0 || s.DownloadBytesPerSecond < 0:
		return errdefs.InvalidArgument("a rate limit cannot be negative: give 0 for no limit")
	case s.StandbyAfter != 0 && s.StandbyAfter < MinStandbyAfter:
		return errdefs.InvalidArgument("standby_after %s is too short: idleness is judged a minute at a time, "+
			"so give %s or more, or 0 for never", s.StandbyAfter, MinStandbyAfter)
	case s.HasDirectoryMount() && s.EffectiveHypervisorType() != hypervisor.TypeCloudHypervisor:
		return errdefs.InvalidArgument("directory mounts need %s: %s cannot share a directory with its guest",
			hypervisor.TypeCloudHypervisor, s.EffectiveHypervisorType())
	case s.HasDirectoryMount() && s.StandbyAfter != 0:
		return errdefs.InvalidArgument("an instance that mounts a host directory cannot be put on standby: " +
			"leave standby_after unset")
	case s.User != "" && !validUser(s.User):
		return errdefs.InvalidArgument("invalid user %q: give user, uid, user:group or uid:gid", s.User)
	case s.User != "" && s.InitMode == guest.InitModeSystemd:
		return errdefs.InvalidArgument("systemd runs as root, so the systemd init mode cannot run as user %q: "+
			"leave the user unset, or use the exec init mode", s.User)
	case s.RemoveOnExit && s.Restart.Restarts():
		return errdefs.InvalidArgument(
			"an instance cannot be deleted when it stops and restarted when it stops: "+
				"the restart policy is %s, so drop it or drop the request to delete it", s.Restart)
	}

	for i, port := range s.Ports {
		if err := port.Validate(); err != nil {
			return err
		}
		for _, prev := range s.Ports[:i] {
			if port.Overlaps(prev) {
				return errdefs.InvalidArgument("port %s overlaps port %s", port, prev)
			}
		}
	}
	return validateMounts(s.Mounts)
}

// validUser reports whether user has the form user, uid, user:group or
// uid:gid.
func validUser(user string) bool {
	name, group, hasGroup := strings.Cut(user, ":")
	if hasGroup && (group == "" || strings.Contains(group, ":")) {
		return false
	}
	return name != ""
}

// VolumeMount returns the mount by which the instance attaches the named
// volume, and false if it attaches none.
func (s Spec) VolumeMount(name string) (Mount, bool) {
	for _, m := range s.Mounts {
		if m.Type == MountTypeVolume && m.Source == name {
			return m, true
		}
	}

	return Mount{}, false
}

// EffectiveHypervisorType returns the hypervisor the instance runs on,
// filling in the default for a spec that names none.
func (s Spec) EffectiveHypervisorType() hypervisor.Type {
	if s.HypervisorType == "" {
		return hypervisor.DefaultType
	}

	return s.HypervisorType
}

// Status is the observed state of an instance. It lives under the runtime
// directory, so a host reboot discards it.
type Status struct {
	InstanceID string `json:"instance_id,omitempty"`
	State      State  `json:"state"`
	StateError string `json:"state_error,omitempty"`

	VMMPID               *int   `json:"hypervisor_pid,omitempty"`
	HypervisorSocketPath string `json:"hypervisor_socket_path,omitempty"`
	HypervisorVersion    string `json:"hypervisor_version,omitempty"`
	VsockCID             int64  `json:"vsock_cid,omitempty"`
	VsockPath            string `json:"vsock_path,omitempty"`

	// VCPUs and MemoryBytes are what the instance was admitted with. The
	// spec may have been edited since.
	VCPUs       int   `json:"vcpus,omitempty"`
	MemoryBytes int64 `json:"memory_bytes,omitempty"`

	// ImageDigest is the image the guest booted from.
	ImageDigest string `json:"image_digest,omitempty"`

	// IP and MAC are the instance's address, held while it is defined.
	IP  string `json:"ip,omitempty"`
	MAC string `json:"mac,omitempty"`

	// HealthCheck is the check this run was started with, or nil for none.
	HealthCheck *health.Check `json:"health_check,omitempty"`

	// Health is what that check has found, or nil if there is none.
	Health *health.Health `json:"health,omitempty"`

	StartedAt time.Time `json:"started_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`

	// ExitCode is the exit code the guest reported when it last ended on
	// its own, if it reported one.
	ExitCode *int `json:"exit_code,omitempty"`

	// FinishedAt is when the guest last ended without being asked to.
	FinishedAt time.Time `json:"finished_at,omitzero"`

	// RestartCount is how many times in a row the restart policy has
	// started the instance again. A start a user asks for resets it.
	RestartCount int `json:"restart_count,omitempty"`

	// NextRestartAt is when a Restarting instance is due to start again.
	NextRestartAt time.Time `json:"next_restart_at,omitzero"`
}

// HeldResources returns what the instance holds of the host's CPU and
// memory, which is nothing unless its state says it holds anything.
func (s Status) HeldResources() Resources {
	if !s.State.HoldsResources() {
		return Resources{}
	}

	return Resources{VCPUs: s.VCPUs, MemoryBytes: s.MemoryBytes}
}

// MinStandbyAfter is the shortest InstanceSpec.StandbyAfter: how idle an
// instance is, is judged a minute at a time.
const MinStandbyAfter = time.Minute

// State is the lifecycle state of an instance.
type State string

const (
	// StateStopped means the instance is defined but not running. A freshly
	// created instance starts here.
	StateStopped State = "Stopped"

	// StateStarting means a start is in progress. An instance found in this
	// state at daemon boot crashed mid-start and is cleaned up.
	StateStarting State = "Starting"

	// StateRunning means the VM is executing.
	StateRunning State = "Running"

	// StatePaused means the vCPUs are halted but the VM is resident.
	StatePaused State = "Paused"

	// StateStandby means the guest is frozen to disk, its VMM ended:
	// it holds no CPU or memory, but keeps its address, host ports and
	// writable volumes, and a start resumes it where it was.
	StateStandby State = "Standby"

	// StateStopping means a shutdown is in progress.
	StateStopping State = "Stopping"

	// StateRestarting means the instance ended without being asked to, and
	// its restart policy will start it again at
	// InstanceStatus.NextRestartAt. It holds nothing in the meantime.
	StateRestarting State = "Restarting"

	// StateFailed means the last operation failed; see
	// InstanceStatus.StateError.
	StateFailed State = "Failed"
)

// States returns every lifecycle state, in the order an instance normally moves
// through them.
func States() []State {
	return []State{
		StateStopped,
		StateStarting,
		StateRunning,
		StatePaused,
		StateStandby,
		StateStopping,
		StateRestarting,
		StateFailed,
	}
}

// allowedTransitions maps each state to the states it may move to.
var allowedTransitions = map[State][]State{
	StateStopped: {StateStarting},
	StateStarting: {
		StateRunning, StateRestarting, StateFailed,
	},
	StateRunning: {
		StatePaused, StateStopping, StateStopped,
		StateRestarting, StateFailed,
	},
	StatePaused: {
		StateRunning, StateStopping, StateStopped,
		StateRestarting, StateFailed,
	},
	StateStandby:  {StateStarting},
	StateStopping: {StateStopped, StateFailed},
	StateRestarting: {
		StateStarting, StateStopping, StateFailed,
	},
	StateFailed: {
		StateStarting, StateStopping, StateStopped,
	},
}

// CanTransitionTo reports whether a transition to target is allowed.
func (s State) CanTransitionTo(target State) bool {
	return slices.Contains(allowedTransitions[s], target)
}

// HoldsResources reports whether an instance in the state holds the CPU and
// memory it was admitted with: starting, running or paused.
func (s State) HoldsResources() bool {
	return s == StateStarting || s.IsActive()
}

// HoldsPortsAndVolumes reports whether an instance in the state keeps its
// published host ports and writable volumes to itself: one that holds
// resources, is stopping, or is on standby, to resume with them.
func (s State) HoldsPortsAndVolumes() bool {
	return s.HoldsResources() || s == StateStopping || s == StateStandby
}

// IsActive reports whether the state implies a live VMM.
func (s State) IsActive() bool {
	return s == StateRunning || s == StatePaused
}

// String returns the state as it is written: "Running".
func (s State) String() string { return string(s) }

// Lowercase returns the state as a sentence says it after "is": "running",
// not "Running", and "on standby".
func (s State) Lowercase() string {
	if s == StateStandby {
		return "on standby"
	}
	return strings.ToLower(string(s))
}

// RestartMode is when an instance is started again without being asked.
type RestartMode string

// The restart modes, as Docker names them.
const (
	// RestartModeNo leaves an instance that ended as it is.
	RestartModeNo RestartMode = "no"

	// RestartModeOnFailure restarts an instance whose end was not clean.
	RestartModeOnFailure RestartMode = "on-failure"

	// RestartModeUnlessStopped restarts an instance however it ended, and
	// starts it when the daemon starts, unless it was last stopped by a
	// user.
	RestartModeUnlessStopped RestartMode = "unless-stopped"

	// RestartModeAlways restarts an instance however it ended, and starts it
	// when the daemon starts, even if it was last stopped by a user.
	RestartModeAlways RestartMode = "always"
)

// RestartPolicy is what the daemon does when an instance ends without being
// asked to. Restarts use exponential backoff.
type RestartPolicy struct {
	Mode RestartMode `yaml:"mode,omitempty" json:"mode,omitempty"`

	// MaxRetries is how many times in a row an on-failure instance is
	// restarted before it is left Failed. Zero means no limit.
	MaxRetries int `yaml:"max_retries,omitempty" json:"max_retries,omitempty"`
}

// Validate returns an invalid argument error if the daemon cannot follow
// the policy.
func (p RestartPolicy) Validate() error {
	switch p.Mode {
	case "", RestartModeNo, RestartModeUnlessStopped, RestartModeAlways:
		if p.MaxRetries != 0 {
			return errdefs.InvalidArgument("a retry limit applies only to the on-failure restart policy")
		}
	case RestartModeOnFailure:
		if p.MaxRetries < 0 {
			return errdefs.InvalidArgument("the retry limit cannot be negative")
		}
	default:
		return errdefs.InvalidArgument("unknown restart policy %q: want no, on-failure[:N], unless-stopped or always", p.Mode)
	}

	return nil
}

// Restarts reports whether the policy ever starts an instance again.
func (p RestartPolicy) Restarts() bool {
	return p.Mode != "" && p.Mode != RestartModeNo
}

// StartsOnBoot reports whether an instance with this policy is started when
// the daemon starts.
func (p RestartPolicy) StartsOnBoot(stoppedByUser bool) bool {
	switch p.Mode {
	case RestartModeAlways:
		return true
	case RestartModeUnlessStopped:
		return !stoppedByUser
	default:
		return false
	}
}

// String is the policy as the CLI and API take it: "on-failure:5".
func (p RestartPolicy) String() string {
	if p.Mode == "" {
		return string(RestartModeNo)
	}
	if p.MaxRetries > 0 {
		return fmt.Sprintf("%s:%d", p.Mode, p.MaxRetries)
	}

	return string(p.Mode)
}
