// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/konradasb/dicer"
	"github.com/konradasb/dicer/internal/cli/printer"
	"github.com/konradasb/dicer/internal/humanize"
)

// The test guest's defaults: small, and quick to pull and boot.
const (
	defaultDoctorImage   = "docker.io/library/busybox:1.37"
	defaultDoctorTimeout = 2 * time.Minute
	doctorOnlineURL      = "http://example.com/"
)

// What the test guest prints, which is how its run is judged.
const (
	doctorBooted  = "dicer-doctor: booted"
	doctorOnline  = "dicer-doctor: online"
	doctorOffline = "dicer-doctor: offline"
)

// doctorCheckNames are how the daemon's host checks are shown.
var doctorCheckNames = map[string]string{
	"kvm":           "KVM",
	"ip_forwarding": "IP forwarding",
	"firewall":      "Firewall",
	"tools":         "Tools",
	"uplink":        "Uplink",
	"disk":          "Disk",
}

func newDoctorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that the host can run instances, and boot a test guest",
		Long: "Checks that the daemon's host can run instances and reach them: KVM,\n" +
			"IPv4 forwarding, the firewall, the tools the daemon runs, its uplink and\n" +
			"its free disk. It then boots a small test guest on each hypervisor the\n" +
			"daemon carries, has it run a command and reach the internet, and deletes\n" +
			"it. Each problem comes with what to do about it.\n\n" +
			"It exits with an error if a check failed. A warning is something that may\n" +
			"go wrong, such as little free disk, and does not.\n\n" +
			"The test guest's image is pulled if the host does not have it: on a host\n" +
			"that cannot reach Docker Hub, name another with --image.",
		Example: "  dicer doctor\n" +
			"  dicer doctor --host-only\n" +
			"  dicer doctor --image registry.example.com/busybox:1.37",
		Args: noArgs,
		RunE: runDoctor,
	}

	cmd.Flags().String("image", defaultDoctorImage, "Image of the test guest: one with sh and wget, such as busybox")
	cmd.Flags().Bool("host-only", false, "Check the host only, without booting a test guest")
	cmd.Flags().Duration("timeout", defaultDoctorTimeout, "Give up on a test guest that has not run its command after this long")
	cmd.Flags().Bool("keep", false, "Keep a test guest that failed, to look into, rather than delete it")
	cmd.Flags().String("format", "table", "Output format: table, json or yaml")
	_ = cmd.RegisterFlagCompletionFunc("format", completeObjectFormats)

	return cmd
}

// doctorReport is what 'dicer doctor' found.
type doctorReport struct {
	Host   dicer.HostInfo    `json:"host"`
	Checks []dicer.HostCheck `json:"checks"`
	// ChecksError is why the host could not be checked, if it could not.
	ChecksError string             `json:"checks_error,omitzero"`
	Guests      []doctorGuestCheck `json:"guests,omitzero"`
}

// doctorGuestCheck is what one test guest showed.
type doctorGuestCheck struct {
	Hypervisor dicer.HypervisorType  `json:"hypervisor"`
	Version    string                `json:"version"`
	Status     dicer.HostCheckStatus `json:"status"`
	// Detail is what happened, in a line.
	Detail string `json:"detail"`
	// Console is the guest's last console lines, for one that failed.
	Console []string `json:"console,omitzero"`
	// Online reports whether the guest reached the internet, if it was
	// asked to.
	Online *bool `json:"online,omitzero"`
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	image, _ := cmd.Flags().GetString("image")
	hostOnly, _ := cmd.Flags().GetBool("host-only")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	keep, _ := cmd.Flags().GetBool("keep")
	format, _ := cmd.Flags().GetString("format")

	client, cleanup, err := newClient(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	host, err := client.HostInfo(ctx)
	if err != nil {
		return err
	}
	report := doctorReport{Host: host}

	report.Checks, err = client.CheckHost(ctx)
	switch {
	case errors.Is(err, dicer.ErrUnimplemented):
		report.ChecksError = "the daemon is too old to check its host: upgrade it"
	case err != nil:
		report.ChecksError = err.Error()
	}

	if !hostOnly {
		for _, hv := range host.Hypervisors {
			report.Guests = append(report.Guests, checkTestGuest(ctx, client, hv, image, timeout, keep))
		}
	}

	if !printer.IsTable(format) {
		if err := writeStructured(cmd.OutOrStdout(), format, report); err != nil {
			return err
		}
	} else if err := writeDoctor(cmd.OutOrStdout(), report); err != nil {
		return err
	}

	if failed := report.failures(); failed > 0 {
		return fmt.Errorf("%s found", humanize.Count(failed, "problem"))
	}
	return nil
}

// online returns whether the first test guest that ran its command reached
// the internet, or nil if none ran it. One guest says as much as all of
// them: they share the host's network.
func (r doctorReport) online() *bool {
	for _, g := range r.Guests {
		if g.Online != nil {
			return g.Online
		}
	}
	return nil
}

// failures returns how many checks failed.
func (r doctorReport) failures() int {
	n := 0
	if r.ChecksError != "" {
		n++
	}
	for _, c := range r.Checks {
		if c.Status == dicer.HostCheckFailed {
			n++
		}
	}
	for _, g := range r.Guests {
		if g.Status == dicer.HostCheckFailed {
			n++
		}
	}
	return n
}

// warnings returns how many checks warned.
func (r doctorReport) warnings() int {
	n := 0
	for _, c := range r.Checks {
		if c.Status == dicer.HostCheckWarning {
			n++
		}
	}
	for _, g := range r.Guests {
		if g.Status == dicer.HostCheckWarning {
			n++
		}
	}
	if online := r.online(); online != nil && !*online {
		n++
	}
	return n
}

// checkTestGuest boots a test guest on hv, has it run a command and try to
// reach the internet. It deletes the guest, unless keep is set and the guest
// failed.
func checkTestGuest(
	ctx context.Context, client *dicer.Client, hv dicer.HypervisorInfo, image string,
	timeout time.Duration, keep bool,
) (result doctorGuestCheck) {
	// Named, so that the deferred deletion can say a failed guest was kept.
	result = doctorGuestCheck{Hypervisor: hv.Type, Status: dicer.HostCheckFailed}
	if len(hv.Versions) > 0 {
		result.Version = hv.Versions[0]
	}

	script := fmt.Sprintf("echo %s; if wget -q -T 5 -O /dev/null %s; then echo %s; else echo %s; fi",
		doctorBooted, doctorOnlineURL, doctorOnline, doctorOffline)
	name := "doctor-" + randomHex(3)
	spec := dicer.InstanceSpec{
		Name:           name,
		ImageRef:       image,
		HypervisorType: hv.Type,
		VCPUs:          1,
		MemoryBytes:    512 << 20,
		DiskBytes:      1 << 30,
		Cmd:            []string{"sh", "-c", script},
	}

	if _, err := client.Instances.Create(ctx, spec, dicer.CreateOptions{}); err != nil {
		result.Detail = "cannot create the test guest: " + err.Error()
		if errors.Is(err, dicer.ErrNotFound) || strings.Contains(err.Error(), "pull") {
			result.Detail += "; name an image the host can pull with --image"
		}
		return result
	}
	defer func() {
		if keep && result.Status == dicer.HostCheckFailed {
			result.Detail += fmt.Sprintf("; kept as %s, delete it with dicer rm -f %s", name, name)
			return
		}
		deleteCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = client.Instances.Delete(deleteCtx, name, dicer.DeleteOptions{Force: true})
	}()

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	waiter, err := client.Instances.Waiter(waitCtx, name, dicer.WaitOptions{NextStop: true})
	if err != nil {
		result.Detail = "cannot wait for the test guest: " + err.Error()
		return result
	}
	defer func() { _ = waiter.Close() }()

	started := time.Now()
	if _, err := client.Instances.Start(waitCtx, name); err != nil {
		result.Detail = "did not start: " + err.Error()
		result.Console = guestConsole(ctx, client, name)
		return result
	}

	code, err := waiter.Wait()
	took := time.Since(started)
	console := guestConsole(ctx, client, name)
	booted := slices.Contains(console, doctorBooted)

	switch {
	case errors.Is(err, context.DeadlineExceeded) || waitCtx.Err() != nil:
		result.Detail = "did not run its command within " + humanize.Duration(timeout)
	case err != nil:
		result.Detail = "cannot wait for the test guest: " + err.Error()
	case !booted:
		result.Detail = fmt.Sprintf("ended without running its command (exit code %d)", code)
	case code != 0:
		result.Detail = fmt.Sprintf("ran its command, which exited with code %d", code)
	default:
		result.Status = dicer.HostCheckOK
		result.Detail = "booted, ran a command and stopped in " + humanize.Duration(took)
	}

	if booted {
		reached := slices.Contains(console, doctorOnline)
		result.Online = &reached
	}
	if result.Status == dicer.HostCheckFailed {
		result.Console = consoleExcerpt(withoutDoctorLines(console))
	}
	return result
}

// consoleExcerpt returns what of a failed guest's console says most about
// why: the kernel's panic, with the lines before it, or else its last
// lines. A guest that rebooted over and over may have stopped anywhere in
// its boot, so its last lines can say nothing.
func consoleExcerpt(lines []string) []string {
	const n = 5
	for i, line := range lines {
		if strings.Contains(line, "Kernel panic") {
			return lines[max(0, i-n+2):min(len(lines), i+1)]
		}
	}
	return lastLines(lines, n)
}

// guestConsole returns the lines of an instance's console log, or none if
// it cannot be read.
func guestConsole(ctx context.Context, client *dicer.Client, name string) []string {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	r, err := client.Instances.Logs(ctx, name, dicer.LogOptions{TailLines: 200})
	if err != nil {
		return nil
	}
	defer func() { _ = r.Close() }()
	data, _ := io.ReadAll(r)

	var lines []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// withoutDoctorLines returns lines without those the test guest printed for
// doctor itself.
func withoutDoctorLines(lines []string) []string {
	return slices.DeleteFunc(slices.Clone(lines), func(l string) bool {
		return strings.HasPrefix(l, "dicer-doctor:")
	})
}

// lastLines returns at most the last n of lines.
func lastLines(lines []string, n int) []string {
	return lines[max(0, len(lines)-n):]
}

// writeDoctor writes what 'dicer doctor' found: a line for each check, with
// what to do about each that did not pass, then a summary.
func writeDoctor(w io.Writer, r doctorReport) error {
	p := paletteFor(w)
	// Built whole, then written once, so that only the one write can fail.
	var b strings.Builder
	mark := map[dicer.HostCheckStatus]string{
		dicer.HostCheckOK:      p.paint(ansiGreen, "✓"),
		dicer.HostCheckWarning: p.paint(ansiYellow, "!"),
		dicer.HostCheckFailed:  p.paint(ansiRed, "✗"),
	}
	line := func(status dicer.HostCheckStatus, name, detail string) {
		fmt.Fprintf(&b, "  %s %-28s %s\n", mark[status], name, detail)
	}
	more := func(text string) {
		fmt.Fprintf(&b, "    %-28s %s\n", "", text)
	}

	fmt.Fprintf(&b, "%s %s\n\n", p.bold(r.Host.Hostname), "(dicer "+r.Host.Version+")")
	fmt.Fprintln(&b, p.bold("Host"))
	if r.ChecksError != "" {
		line(dicer.HostCheckFailed, "Checks", r.ChecksError)
	}
	for _, c := range r.Checks {
		name := doctorCheckNames[c.Name]
		if name == "" {
			name = c.Name
		}
		line(c.Status, name, c.Detail)
		if c.Hint != "" {
			more(c.Hint)
		}
	}

	if len(r.Guests) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, p.bold("Test guests"))
	}
	for _, g := range r.Guests {
		line(g.Status, strings.TrimSpace(string(g.Hypervisor)+" "+g.Version), g.Detail)
		for _, l := range g.Console {
			more(l)
		}
	}
	switch online := r.online(); {
	case online == nil:
	case *online:
		line(dicer.HostCheckOK, "Network", "a test guest reached the internet")
	default:
		line(dicer.HostCheckWarning, "Network", "a test guest could not reach "+doctorOnlineURL)
		more("check that the host is online, and the firewall and uplink above")
	}
	fmt.Fprintln(&b)
	failed, warned := r.failures(), r.warnings()
	switch {
	case failed > 0:
		fmt.Fprintln(&b, p.paint(ansiRed, humanize.Count(failed, "problem")+" found."))
	case warned > 0:
		fmt.Fprintln(&b, p.paint(ansiYellow, "No problems, "+humanize.Count(warned, "warning")+"."))
	default:
		fmt.Fprintln(&b, p.paint(ansiGreen, "No problems found."))
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// randomHex returns n random bytes, hex-encoded.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
