// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

// Package hostcheck checks that a host can run Dicer's instances and reach
// them: KVM, IPv4 forwarding, the firewall, the tools the daemon runs, its
// uplink and its free disk. It only reads, and changes nothing.
package hostcheck

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"

	"github.com/konradasb/dicer/internal/humanize"
)

// Status is what a check found.
type Status string

// The statuses.
const (
	// OK means all is well.
	OK Status = "ok"
	// Warning means something may go wrong, such as little free disk.
	Warning Status = "warning"
	// Failed means instances cannot boot, or cannot be reached, until it is
	// fixed.
	Failed Status = "failed"
)

// lowDisk is the free space under which the data directory's disk is
// warned about: an image or two, and the overlay disks guests fill.
const lowDisk = 2 << 30

// Result is what one check found.
type Result struct {
	// Name is what was checked: kvm, ip_forwarding, firewall, tools, uplink
	// or disk.
	Name   string
	Status Status
	// Detail is what was found, in a line.
	Detail string
	// Hint is what to do about it. It is empty for a check that passed.
	Hint string
}

// Config is what the checks need to know of the daemon's configuration.
type Config struct {
	// DataDir is the daemon's data directory, whose disk is checked.
	DataDir string
	// UplinkInterface is network.uplink_interface, or empty to find the
	// uplink by the default route.
	UplinkInterface string
}

// Checker checks the host.
type Checker struct {
	cfg Config

	// What the checks read and run, which tests replace.
	kvmPath       string
	ipForwardPath string
	routesPath    string
	goarch        string
	readFile      func(path string) ([]byte, error)
	openRDWR      func(path string) error
	run           func(ctx context.Context, name string, args ...string) (string, error)
	lookPath      func(file string) (string, error)
	freeBytes     func(path string) (int64, error)
	interfaces    func() ([]string, error)
}

// New returns a Checker of this host.
func New(cfg Config) *Checker {
	return &Checker{
		cfg:           cfg,
		kvmPath:       "/dev/kvm",
		ipForwardPath: "/proc/sys/net/ipv4/ip_forward",
		routesPath:    "/proc/net/route",
		goarch:        runtime.GOARCH,
		readFile:      os.ReadFile,
		openRDWR:      openRDWR,
		run:           run,
		lookPath:      exec.LookPath,
		freeBytes:     freeBytes,
		interfaces:    interfaceNames,
	}
}

// Check runs every check, in order, and returns what each found.
func (c *Checker) Check(ctx context.Context) []Result {
	return []Result{
		c.kvm(ctx),
		c.ipForwarding(),
		c.firewall(ctx),
		c.tools(),
		c.uplink(),
		c.disk(),
	}
}

// kvm checks that /dev/kvm can be used, and says whether the host is itself
// a virtual machine, which needs nested virtualisation for it.
func (c *Checker) kvm(ctx context.Context) Result {
	r := Result{Name: "kvm"}
	vm := c.virtualMachine(ctx)

	if _, err := os.Stat(c.kvmPath); err != nil {
		r.Status, r.Detail = Failed, c.kvmPath+" does not exist"
		r.Hint = c.missingKVMHint(vm)
		return r
	}
	if err := c.openRDWR(c.kvmPath); err != nil {
		r.Status, r.Detail = Failed, fmt.Sprintf("%s cannot be opened: %v", c.kvmPath, err)
		r.Hint = "check that the daemon runs as root, and that nothing else holds KVM exclusively"
		return r
	}

	r.Status, r.Detail = OK, c.kvmPath+" is usable"
	if vm != "" {
		r.Detail += fmt.Sprintf(", in a virtual machine (%s) with nested virtualisation", vm)
	}
	return r
}

// missingKVMHint says how to get KVM on a host without it, which is a
// virtual machine of the kind vm names, or none if vm is empty.
func (c *Checker) missingKVMHint(vm string) string {
	switch {
	case vm == "apple":
		return "this is a virtual machine on a Mac, which has KVM only on Apple M3 or later, " +
			"with macOS 15 or later, and nested virtualisation turned on"
	case vm != "":
		return fmt.Sprintf("this is a virtual machine (%s) without nested virtualisation: "+
			"turn it on where the machine is defined, or run Dicer on bare metal", vm)
	case c.goarch == "amd64":
		return "turn virtualisation (VT-x or AMD-V) on in the firmware, then load the kvm_intel or kvm_amd module"
	default:
		return "load the kvm module; if it does not load, the CPU or firmware does not offer virtualisation"
	}
}

// virtualMachine returns the kind of virtual machine the host is, as
// systemd-detect-virt names it, or "" for bare metal or if it cannot tell.
func (c *Checker) virtualMachine(ctx context.Context) string {
	out, err := c.run(ctx, "systemd-detect-virt", "--vm")
	if vm := strings.TrimSpace(out); err == nil && vm != "none" {
		return vm
	}
	return ""
}

// ipForwarding checks that the host forwards IPv4, which guests' traffic
// to anywhere but the host needs.
func (c *Checker) ipForwarding() Result {
	r := Result{Name: "ip_forwarding"}

	data, err := c.readFile(c.ipForwardPath)
	switch {
	case err != nil:
		r.Status, r.Detail = Failed, fmt.Sprintf("cannot read %s: %v", c.ipForwardPath, err)
	case strings.TrimSpace(string(data)) != "1":
		r.Status, r.Detail = Failed, "IPv4 forwarding is off, so guests cannot reach anything but the host"
		r.Hint = "turn it on with sysctl -w net.ipv4.ip_forward=1, and keep it on with a file in /etc/sysctl.d"
	default:
		r.Status, r.Detail = OK, "IPv4 forwarding is on"
	}
	return r
}

// firewall checks that iptables works, and that firewalld, where it runs,
// has the dicer zone.
func (c *Checker) firewall(ctx context.Context) Result {
	r := Result{Name: "firewall"}

	version, err := c.run(ctx, "iptables", "--version")
	if err != nil {
		r.Status, r.Detail = Failed, "iptables cannot be run: "+firstLine(version, err)
		r.Hint = "install iptables: the daemon sets guests' NAT and isolation up with it"
		return r
	}
	if out, err := c.run(ctx, "iptables", "-w", "-n", "-L", "FORWARD"); err != nil {
		r.Status, r.Detail = Failed, "iptables cannot read the rules: "+firstLine(out, err)
		r.Hint = "check that the daemon runs as root, and that the kernel has its netfilter modules"
		return r
	}
	r.Status, r.Detail = OK, "iptables works"
	if backend := iptablesBackend(version); backend != "" {
		r.Detail = fmt.Sprintf("iptables (%s) works", backend)
	}

	if state, err := c.run(ctx, "firewall-cmd", "--state"); err != nil || strings.TrimSpace(state) != "running" {
		return r
	}
	if _, err := c.run(ctx, "firewall-cmd", "--info-zone=dicer"); err != nil {
		r.Status = Warning
		r.Detail += ", but firewalld runs without the dicer zone, so it may drop guests' traffic"
		r.Hint = "install the zone, as the package does: copy build/package/firewalld-zone.xml " +
			"to /etc/firewalld/zones/dicer.xml, then run firewall-cmd --reload"
		return r
	}
	r.Detail += ", and firewalld has the dicer zone"
	return r
}

// iptablesBackend returns the backend iptables --version names, such as
// nf_tables, or "".
func iptablesBackend(version string) string {
	_, rest, ok := strings.Cut(version, "(")
	if !ok {
		return ""
	}
	backend, _, ok := strings.Cut(rest, ")")
	if !ok {
		return ""
	}
	return backend
}

// tools checks that the programs the daemon runs to build guests' disks
// are installed.
func (c *Checker) tools() Result {
	r := Result{Name: "tools"}

	var missing, packages []string
	for _, tool := range []struct{ name, pkg string }{
		{"mkfs.erofs", "erofs-utils"},
		{"mke2fs", "e2fsprogs"},
	} {
		if _, err := c.lookPath(tool.name); err != nil {
			missing = append(missing, tool.name)
			packages = append(packages, tool.pkg)
		}
	}

	if len(missing) > 0 {
		r.Status, r.Detail = Failed, strings.Join(missing, " and ")+" cannot be found, so no image can be converted"
		r.Hint = "install " + strings.Join(packages, " and ")
		return r
	}
	r.Status, r.Detail = OK, "mkfs.erofs and mke2fs are installed"
	return r
}

// uplink checks that guests' traffic has an interface to leave by: the one
// the configuration names, or the default route's.
func (c *Checker) uplink() Result {
	r := Result{Name: "uplink"}

	names, err := c.interfaces()
	if err != nil {
		r.Status, r.Detail = Failed, "cannot list the host's interfaces: "+err.Error()
		return r
	}

	if name := c.cfg.UplinkInterface; name != "" {
		if !slices.Contains(names, name) {
			r.Status = Failed
			r.Detail = fmt.Sprintf("network.uplink_interface names %s, which this host does not have", name)
			r.Hint = "set it to one of " + strings.Join(names, ", ") + ", or leave it unset to use the default route's"
			return r
		}
		r.Status, r.Detail = OK, name+", as network.uplink_interface names"
		return r
	}

	name, err := c.defaultRouteInterface()
	if err != nil || name == "" {
		r.Status, r.Detail = Failed, "the host has no default route, so guests' traffic has nowhere to leave by"
		r.Hint = "give the host a default route, or name the interface in network.uplink_interface"
		return r
	}
	r.Status, r.Detail = OK, name+", by the default route"
	return r
}

// defaultRouteInterface returns the interface of the IPv4 default route,
// from the kernel's routing table, or "" if there is none.
func (c *Checker) defaultRouteInterface() (string, error) {
	data, err := c.readFile(c.routesPath)
	if err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Scan() // the header
	for scanner.Scan() {
		// Iface, Destination, Gateway, Flags, RefCnt, Use, Metric, Mask, ...
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 8 && fields[1] == "00000000" && fields[7] == "00000000" {
			return fields[0], nil
		}
	}
	return "", scanner.Err()
}

// disk checks how much the data directory's disk can still take.
func (c *Checker) disk() Result {
	r := Result{Name: "disk"}

	free, err := c.freeBytes(c.cfg.DataDir)
	if err != nil {
		r.Status, r.Detail = Failed, fmt.Sprintf("cannot read %s's disk: %v", c.cfg.DataDir, err)
		return r
	}

	r.Detail = fmt.Sprintf("%s free in %s", humanize.Bytes(free), c.cfg.DataDir)
	if free < lowDisk {
		r.Status = Warning
		r.Hint = "free some space: dicer image prune removes images no instance uses"
		return r
	}
	r.Status = OK
	return r
}

// firstLine returns the first line of out, or of err if out is empty.
func firstLine(out string, err error) string {
	if line, _, _ := strings.Cut(strings.TrimSpace(out), "\n"); line != "" {
		return line
	}
	return err.Error()
}

// openRDWR opens path for reading and writing, and closes it.
func openRDWR(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return f.Close()
}

// run runs a program and returns what it printed.
func run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

// interfaceNames returns the names of the host's network interfaces.
func interfaceNames() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		names = append(names, iface.Name)
	}
	return names, nil
}
