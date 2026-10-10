// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package hostcheck

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHost is a host the checks read through their seams: what it runs
// answers from commands, and its files from files.
type fakeHost struct {
	commands map[string]string // by command line; missing ones fail
	files    map[string]string
	tools    map[string]bool
	free     int64
	ifaces   []string
	kvmOpens bool
}

// healthyHost returns a host every check passes on.
func healthyHost() *fakeHost {
	return &fakeHost{
		commands: map[string]string{
			"systemd-detect-virt --vm":  "none\n",
			"iptables --version":        "iptables v1.8.10 (nf_tables)\n",
			"iptables -w -n -L FORWARD": "Chain FORWARD (policy ACCEPT)\n",
		},
		files: map[string]string{
			"ip_forward": "1\n",
			"route": "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
				"eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
				"eth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n",
		},
		tools:    map[string]bool{"mkfs.erofs": true, "mke2fs": true},
		free:     50 << 30,
		ifaces:   []string{"lo", "eth0", "dicer-default"},
		kvmOpens: true,
	}
}

// checker returns a Checker of h, with /dev/kvm present unless noKVM.
func (h *fakeHost) checker(t *testing.T, cfg Config, noKVM bool) *Checker {
	t.Helper()
	dir := t.TempDir()
	kvm := filepath.Join(dir, "kvm")
	if !noKVM {
		if err := os.WriteFile(kvm, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "/var/lib/dicer"
	}

	c := New(cfg)
	c.kvmPath, c.ipForwardPath, c.routesPath, c.goarch = kvm, "ip_forward", "route", "amd64"
	c.readFile = func(path string) ([]byte, error) {
		if data, ok := h.files[path]; ok {
			return []byte(data), nil
		}
		return nil, fs.ErrNotExist
	}
	c.openRDWR = func(string) error {
		if h.kvmOpens {
			return nil
		}
		return fs.ErrPermission
	}
	c.run = func(_ context.Context, name string, args ...string) (string, error) {
		if out, ok := h.commands[strings.Join(append([]string{name}, args...), " ")]; ok {
			return out, nil
		}
		return "", errors.New("exit status 1")
	}
	c.lookPath = func(file string) (string, error) {
		if h.tools[file] {
			return "/usr/bin/" + file, nil
		}
		return "", errors.New("not found")
	}
	c.freeBytes = func(string) (int64, error) { return h.free, nil }
	c.interfaces = func() ([]string, error) { return h.ifaces, nil }
	return c
}

// result returns the result of the check called name.
func result(t *testing.T, results []Result, name string) Result {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no %s check in %v", name, results)
	return Result{}
}

func TestAHealthyHostPassesEveryCheck(t *testing.T) {
	results := healthyHost().checker(t, Config{}, false).Check(t.Context())

	var names []string
	for _, r := range results {
		names = append(names, r.Name)
		if r.Status != OK || r.Hint != "" {
			t.Errorf("%s = %s %q (hint %q), want ok with no hint", r.Name, r.Status, r.Detail, r.Hint)
		}
	}
	if got, want := strings.Join(names, ","), "kvm,ip_forwarding,firewall,tools,uplink,disk"; got != want {
		t.Errorf("checks = %s, want %s", got, want)
	}
	if got := result(t, results, "firewall").Detail; got != "iptables (nf_tables) works" {
		t.Errorf("firewall detail = %q", got)
	}
	if got := result(t, results, "uplink").Detail; got != "eth0, by the default route" {
		t.Errorf("uplink detail = %q", got)
	}
}

func TestKVM(t *testing.T) {
	tests := []struct {
		name       string
		vm         string
		noKVM      bool
		cannotOpen bool
		goarch     string
		status     Status
		want       string // in the detail or the hint
	}{
		{name: "bare metal", status: OK, want: "is usable"},
		{name: "nested", vm: "kvm", status: OK, want: "in a virtual machine (kvm) with nested virtualisation"},
		{name: "a VM without nesting", vm: "vmware", noKVM: true, status: Failed, want: "turn it on where the machine is defined"},
		{name: "a Mac's VM", vm: "apple", noKVM: true, status: Failed, want: "Apple M3 or later"},
		{name: "x86 bare metal without KVM", noKVM: true, goarch: "amd64", status: Failed, want: "VT-x or AMD-V"},
		{name: "arm64 bare metal without KVM", noKVM: true, goarch: "arm64", status: Failed, want: "load the kvm module"},
		{name: "cannot be opened", cannotOpen: true, status: Failed, want: "cannot be opened"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := healthyHost()
			if tt.vm != "" {
				h.commands["systemd-detect-virt --vm"] = tt.vm + "\n"
			}
			h.kvmOpens = !tt.cannotOpen
			c := h.checker(t, Config{}, tt.noKVM)
			if tt.goarch != "" {
				c.goarch = tt.goarch
			}

			r := c.kvm(t.Context())
			if r.Status != tt.status || !strings.Contains(r.Detail+" "+r.Hint, tt.want) {
				t.Errorf("kvm = %s %q (hint %q), want %s mentioning %q", r.Status, r.Detail, r.Hint, tt.status, tt.want)
			}
		})
	}
}

func TestProblems(t *testing.T) {
	tests := []struct {
		name   string
		cfg    Config
		change func(h *fakeHost)
		check  string
		status Status
		want   string // in the detail or the hint
	}{
		{
			name:   "forwarding off",
			change: func(h *fakeHost) { h.files["ip_forward"] = "0\n" },
			check:  "ip_forwarding", status: Failed, want: "sysctl -w net.ipv4.ip_forward=1",
		},
		{
			name:   "no iptables",
			change: func(h *fakeHost) { delete(h.commands, "iptables --version") },
			check:  "firewall", status: Failed, want: "install iptables",
		},
		{
			name:   "iptables cannot read the rules",
			change: func(h *fakeHost) { delete(h.commands, "iptables -w -n -L FORWARD") },
			check:  "firewall", status: Failed, want: "cannot read the rules",
		},
		{
			name: "firewalld without the zone",
			change: func(h *fakeHost) {
				h.commands["firewall-cmd --state"] = "running\n"
			},
			check: "firewall", status: Warning, want: "firewalld-zone.xml",
		},
		{
			name: "firewalld with the zone",
			change: func(h *fakeHost) {
				h.commands["firewall-cmd --state"] = "running\n"
				h.commands["firewall-cmd --info-zone=dicer"] = "dicer\n"
			},
			check: "firewall", status: OK, want: "firewalld has the dicer zone",
		},
		{
			name:   "no mkfs.erofs",
			change: func(h *fakeHost) { delete(h.tools, "mkfs.erofs") },
			check:  "tools", status: Failed, want: "install erofs-utils",
		},
		{
			name:   "no default route",
			change: func(h *fakeHost) { h.files["route"] = "Iface\tDestination\n" },
			check:  "uplink", status: Failed, want: "network.uplink_interface",
		},
		{
			name:   "a configured uplink",
			cfg:    Config{UplinkInterface: "eth0"},
			change: func(*fakeHost) {},
			check:  "uplink", status: OK, want: "as network.uplink_interface names",
		},
		{
			name:   "a configured uplink the host lacks",
			cfg:    Config{UplinkInterface: "bond0"},
			change: func(*fakeHost) {},
			check:  "uplink", status: Failed, want: "one of lo, eth0, dicer-default",
		},
		{
			name:   "little disk",
			change: func(h *fakeHost) { h.free = 1 << 30 },
			check:  "disk", status: Warning, want: "dicer image prune",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := healthyHost()
			tt.change(h)

			r := result(t, h.checker(t, tt.cfg, false).Check(t.Context()), tt.check)
			if r.Status != tt.status || !strings.Contains(r.Detail+" "+r.Hint, tt.want) {
				t.Errorf("%s = %s %q (hint %q), want %s mentioning %q", tt.check, r.Status, r.Detail, r.Hint, tt.status, tt.want)
			}
		})
	}
}
