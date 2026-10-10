// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"errors"
	"testing"
	"time"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/guest"
	"github.com/konradasb/dicer/internal/hypervisor"
	"github.com/konradasb/dicer/internal/network"
)

func TestStateCanTransitionTo(t *testing.T) {
	tests := []struct {
		from, to State
		want     bool
	}{
		{StateStopped, StateStarting, true},
		{StateStarting, StateRunning, true},
		{StateRunning, StatePaused, true},
		{StatePaused, StateRunning, true},
		{StateRunning, StateStopping, true},
		{StateStopping, StateStopped, true},
		{StateFailed, StateStarting, true},
		{StateFailed, StateStopping, true},
		// An instance that ends is restarted, or left stopped or failed.
		{StateRunning, StateRestarting, true},
		{StatePaused, StateRestarting, true},
		{StateRunning, StateStopped, true},
		{StateRestarting, StateStarting, true},
		{StateRestarting, StateStopping, true},
		{StateStarting, StateRestarting, true},
		// A stopped instance cannot jump straight to running.
		{StateStopped, StateRunning, false},
		{StateStopped, StatePaused, false},
		{StateRunning, StateStarting, false},
		// A restart does not skip starting.
		{StateRestarting, StateRunning, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+" to "+string(tt.to), func(t *testing.T) {
			if got := tt.from.CanTransitionTo(tt.to); got != tt.want {
				t.Errorf("CanTransitionTo = %v, want %v", got, tt.want)
			}
		})
	}
}

// Lowercase is what follows "is" in a message: "instance web is on
// standby", not "is standby".
func TestStateLowercase(t *testing.T) {
	for state, want := range map[State]string{
		StateRunning:    "running",
		StateRestarting: "restarting",
		StateStandby:    "on standby",
	} {
		if got := state.Lowercase(); got != want {
			t.Errorf("%s.Lowercase() = %q, want %q", state, got, want)
		}
	}
}

func TestRestartPolicyValidate(t *testing.T) {
	valid := []RestartPolicy{
		{},
		{Mode: RestartModeNo},
		{Mode: RestartModeOnFailure},
		{Mode: RestartModeOnFailure, MaxRetries: 5},
		{Mode: RestartModeUnlessStopped},
		{Mode: RestartModeAlways},
	}
	for _, p := range valid {
		if err := p.Validate(); err != nil {
			t.Errorf("%+v.Validate() = %v", p, err)
		}
	}

	invalid := []RestartPolicy{
		{Mode: "sometimes"},
		{Mode: RestartModeOnFailure, MaxRetries: -1},
		{Mode: RestartModeAlways, MaxRetries: 3},
		{Mode: RestartModeNo, MaxRetries: 1},
	}
	for _, p := range invalid {
		if err := p.Validate(); !errors.Is(err, errdefs.ErrInvalidArgument) {
			t.Errorf("%+v.Validate() = %v, want an invalid argument", p, err)
		}
	}
}

func TestRestartPolicyString(t *testing.T) {
	tests := []struct {
		p    RestartPolicy
		want string
	}{
		{RestartPolicy{}, "no"},
		{RestartPolicy{Mode: RestartModeNo}, "no"},
		{RestartPolicy{Mode: RestartModeOnFailure}, "on-failure"},
		{RestartPolicy{Mode: RestartModeOnFailure, MaxRetries: 5}, "on-failure:5"},
		{RestartPolicy{Mode: RestartModeUnlessStopped}, "unless-stopped"},
		{RestartPolicy{Mode: RestartModeAlways}, "always"},
	}
	for _, tt := range tests {
		if got := tt.p.String(); got != tt.want {
			t.Errorf("%+v.String() = %q, want %q", tt.p, got, tt.want)
		}
	}
}

func TestRestartPolicyStartsOnBoot(t *testing.T) {
	tests := []struct {
		mode          RestartMode
		stoppedByUser bool
		want          bool
	}{
		{RestartModeNo, false, false},
		{RestartModeOnFailure, false, false},
		{RestartModeUnlessStopped, false, true},
		{RestartModeUnlessStopped, true, false},
		{RestartModeAlways, false, true},
		{RestartModeAlways, true, true},
	}
	for _, tt := range tests {
		if got := (RestartPolicy{Mode: tt.mode}).StartsOnBoot(tt.stoppedByUser); got != tt.want {
			t.Errorf("%s, stopped by user %v: StartsOnBoot = %v, want %v", tt.mode, tt.stoppedByUser, got, tt.want)
		}
	}
}

// TestSpecValidate checks that a definition the daemon could never run as
// written is refused, and one it can is not.
// directoryMount shares a host directory with the guest.
var directoryMount = []Mount{{Type: MountTypeDirectory, Source: "/srv/app", Target: "/app"}}

func TestSpecValidate(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*Spec)
		valid  bool
	}{
		{name: "sizes alone", modify: func(*Spec) {}, valid: true},
		{name: "invalid name", modify: func(s *Spec) { s.Name = "web_1" }},
		{name: "hostname", modify: func(s *Spec) { s.Hostname = "web.example.com" }, valid: true},
		{name: "invalid hostname", modify: func(s *Spec) { s.Hostname = "-web" }},
		{name: "no image", modify: func(s *Spec) { s.ImageRef = "" }},
		{name: "no vCPUs", modify: func(s *Spec) { s.VCPUs = 0 }},
		{name: "no memory", modify: func(s *Spec) { s.MemoryBytes = 0 }},
		{name: "no disk", modify: func(s *Spec) { s.DiskBytes = 0 }},
		{
			name:   "room to grow",
			modify: func(s *Spec) { s.MaxVCPUs, s.MaxMemoryBytes = 4, 4<<30 },
			valid:  true,
		},
		{
			name:   "maximums as much as it asks for",
			modify: func(s *Spec) { s.MaxVCPUs, s.MaxMemoryBytes = 2, 2<<30 },
			valid:  true,
		},
		{name: "fewer max vCPUs than it asks for", modify: func(s *Spec) { s.MaxVCPUs = 1 }},
		{name: "less max memory than it asks for", modify: func(s *Spec) { s.MaxMemoryBytes = 1 << 30 }},
		{name: "negative max vCPUs", modify: func(s *Spec) { s.MaxVCPUs = -1 }},
		{
			name:   "max vCPUs on firecracker",
			modify: func(s *Spec) { s.HypervisorType, s.MaxVCPUs = hypervisor.TypeFirecracker, 4 },
		},
		{
			name:   "max memory on firecracker",
			modify: func(s *Spec) { s.HypervisorType, s.MaxMemoryBytes = hypervisor.TypeFirecracker, 4<<30 },
			valid:  true,
		},
		{
			name: "rate limits",
			modify: func(s *Spec) {
				s.DiskBytesPerSecond, s.DiskIOPS, s.UploadBytesPerSecond, s.DownloadBytesPerSecond = 1, 1, 1, 1
			},
			valid: true,
		},
		{name: "negative disk rate", modify: func(s *Spec) { s.DiskBytesPerSecond = -1 }},
		{name: "negative disk IOPS", modify: func(s *Spec) { s.DiskIOPS = -1 }},
		{name: "negative upload rate", modify: func(s *Spec) { s.UploadBytesPerSecond = -1 }},
		{name: "negative download rate", modify: func(s *Spec) { s.DownloadBytesPerSecond = -1 }},
		{name: "standby after 15 minutes", modify: func(s *Spec) { s.StandbyAfter = 15 * time.Minute }, valid: true},
		// Idleness is judged a minute at a time.
		{name: "standby after 30 seconds", modify: func(s *Spec) { s.StandbyAfter = 30 * time.Second }},
		{name: "negative standby after", modify: func(s *Spec) { s.StandbyAfter = -time.Minute }},
		{name: "user", modify: func(s *Spec) { s.User = "app" }, valid: true},
		{name: "uid and gid", modify: func(s *Spec) { s.User = "1000:1000" }, valid: true},
		{name: "user without a name", modify: func(s *Spec) { s.User = ":app" }},
		{name: "user with an empty group", modify: func(s *Spec) { s.User = "app:" }},
		{name: "user with two groups", modify: func(s *Spec) { s.User = "app:app:app" }},
		{name: "user in the exec init mode", modify: func(s *Spec) { s.User, s.InitMode = "app", guest.InitModeExec }, valid: true},
		// systemd runs as root.
		{name: "user in the systemd init mode", modify: func(s *Spec) { s.User, s.InitMode = "app", guest.InitModeSystemd }},
		{name: "remove on exit", modify: func(s *Spec) { s.RemoveOnExit = true }, valid: true},
		{name: "remove on exit, never restarted", modify: removeOnExitRestarted(RestartModeNo), valid: true},
		// Deleted when it stops and started again when it stops: one of the
		// two would be quietly ignored.
		{name: "remove on exit, always restarted", modify: removeOnExitRestarted(RestartModeAlways)},
		{name: "remove on exit, restarted unless stopped", modify: removeOnExitRestarted(RestartModeUnlessStopped)},
		{name: "remove on exit, restarted on failure", modify: removeOnExitRestarted(RestartModeOnFailure)},
		{
			name:   "a restart policy alone",
			modify: func(s *Spec) { s.Restart = RestartPolicy{Mode: RestartModeAlways} },
			valid:  true,
		},
		{
			name:   "ports",
			modify: func(s *Spec) { s.Ports = []network.PortMapping{{HostPort: 8080, GuestPort: 80}} },
			valid:  true,
		},
		{
			name: "clashing ports",
			modify: func(s *Spec) {
				s.Ports = []network.PortMapping{{HostPort: 8080, GuestPort: 80}, {HostPort: 8080, GuestPort: 81}}
			},
		},
		{
			name: "one port on every address and on one",
			modify: func(s *Spec) {
				s.Ports = []network.PortMapping{
					{HostIP: "10.0.0.1", HostPort: 80, GuestPort: 80},
					{HostPort: 80, GuestPort: 81},
				}
			},
		},
		{
			name: "one port, two protocols",
			modify: func(s *Spec) {
				s.Ports = []network.PortMapping{
					{HostPort: 8080, GuestPort: 80},
					{HostPort: 8080, GuestPort: 80, Protocol: network.ProtocolUDP},
				}
			},
			valid: true,
		},
		{
			name: "one port on two addresses",
			modify: func(s *Spec) {
				s.Ports = []network.PortMapping{
					{HostIP: "10.0.0.1", HostPort: 443, GuestPort: 443},
					{HostIP: "10.0.0.2", HostPort: 443, GuestPort: 8443},
				}
			},
			valid: true,
		},
		{
			name:   "invalid port",
			modify: func(s *Spec) { s.Ports = []network.PortMapping{{GuestPort: 80}} },
		},
		{
			name:   "mounts",
			modify: func(s *Spec) { s.Mounts = []Mount{{Type: MountTypeTmpfs, Target: "/scratch"}} },
			valid:  true,
		},
		{
			name:   "invalid mount",
			modify: func(s *Spec) { s.Mounts = []Mount{{Type: MountTypeTmpfs, Target: "scratch"}} },
		},
		{
			name:   "directory mount",
			modify: func(s *Spec) { s.Mounts = directoryMount },
			valid:  true,
		},
		// Only Cloud Hypervisor has virtio-fs.
		{
			name:   "directory mount on firecracker",
			modify: func(s *Spec) { s.HypervisorType, s.Mounts = hypervisor.TypeFirecracker, directoryMount },
		},
		{
			name:   "directory mount with standby",
			modify: func(s *Spec) { s.StandbyAfter, s.Mounts = 15*time.Minute, directoryMount },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Spec{Name: "web", ImageRef: "alpine", VCPUs: 2, MemoryBytes: 2 << 30, DiskBytes: 10 << 30}
			tt.modify(&s)

			err := s.Validate()
			switch {
			case tt.valid && err != nil:
				t.Errorf("Validate = %v, want nil", err)
			case !tt.valid && !errors.Is(err, errdefs.ErrInvalidArgument):
				t.Errorf("Validate = %v, want an invalid argument", err)
			}
		})
	}
}

// removeOnExitRestarted modifies a spec to be deleted when it stops and
// restarted as mode says.
func removeOnExitRestarted(mode RestartMode) func(*Spec) {
	return func(s *Spec) { s.RemoveOnExit, s.Restart = true, RestartPolicy{Mode: mode} }
}
