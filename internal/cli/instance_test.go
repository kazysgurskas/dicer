// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v3"

	"github.com/konradasb/dicer"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// runBuild parses argv through a real create command and returns the create
// it would make, exercising the flag plumbing rather than bypassing it.
func runBuild(t *testing.T, argv ...string) (instanceCreate, error) {
	t.Helper()

	cmd := newInstanceCreateCommand()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	if err := cmd.Flags().Parse(argv); err != nil {
		t.Fatalf("parse flags %v: %v", argv, err)
	}

	return buildCreate(cmd, cmd.Flags().Args())
}

func TestBuildCreateRequestRateLimits(t *testing.T) {
	got, err := runBuild(t, "web", "--disk-rate", "50MiB", "--disk-iops", "1000",
		"--upload-rate", "1MiB/s", "--download-rate", "2MiB")
	if err != nil {
		t.Fatalf("buildCreate: %v", err)
	}
	if got.spec.DiskBytesPerSecond != 50<<20 || got.spec.DiskIOPS != 1000 ||
		got.spec.UploadBytesPerSecond != 1<<20 || got.spec.DownloadBytesPerSecond != 2<<20 {
		t.Errorf("spec = %+v, want the limits given", got.spec)
	}
}

func TestBuildCreateRequestFromFlagsOnly(t *testing.T) {
	got, err := runBuild(t,
		"web", "--image", "alpine:3.21", "--kernel", "k1",
		"--network", "default", "--vcpus", "2", "--memory", "1GiB", "--disk", "5GiB")
	if err != nil {
		t.Fatalf("buildCreate: %v", err)
	}

	if got.spec.Name != "web" {
		t.Errorf("name = %q, want web", got.spec.Name)
	}
	if got.spec.VCPUs != 2 {
		t.Errorf("vcpus = %d, want 2", got.spec.VCPUs)
	}
	if got.spec.MemoryBytes != 1<<30 {
		t.Errorf("memory = %d, want %d", got.spec.MemoryBytes, 1<<30)
	}
	if got.spec.DiskBytes != 5<<30 {
		t.Errorf("disk = %d, want %d", got.spec.DiskBytes, 5<<30)
	}
}

func TestBuildCreateRequestRequiresName(t *testing.T) {
	_, err := runBuild(t, "--image", "alpine", "--kernel", "k", "--network", "default")
	if err == nil {
		t.Fatal("expected an error when no name is given")
	}
}

func TestBuildCreateRequestRejectsBadSizes(t *testing.T) {
	tests := []struct {
		name string
		argv []string
	}{
		{"memory", []string{"web", "--memory", "not-a-size"}},
		{"disk", []string{"web", "--disk", "not-a-size"}},
		{"disk below minimum", []string{"web", "--disk", "1KiB"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := runBuild(t, tt.argv...); err == nil {
				t.Errorf("expected an error for %v", tt.argv)
			}
		})
	}
}

func TestBuildCreateRequestStartFlag(t *testing.T) {
	got, err := runBuild(t, "web", "--image", "alpine", "--kernel", "k", "--network", "default", "--start")
	if err != nil {
		t.Fatalf("buildCreate: %v", err)
	}
	if !got.opts.Start {
		t.Error("--start was not carried into the request")
	}

	got, err = runBuild(t, "web", "--image", "alpine", "--kernel", "k", "--network", "default")
	if err != nil {
		t.Fatalf("buildCreate: %v", err)
	}
	if got.opts.Start {
		t.Error("start should default to false: create records, it does not boot")
	}
}

// TestBuildCreateRequestMountFlags checks each type of mount is parsed, and
// that a file mount carries the contents and mode of the file on this
// machine rather than its path.
func TestBuildCreateRequestMountFlags(t *testing.T) {
	appConf := filepath.Join(t.TempDir(), "app.conf")
	if err := os.WriteFile(appConf, []byte("k=v"), 0o640); err != nil {
		t.Fatal(err)
	}

	got, err := runBuild(t, "web",
		"--mount", "type=tmpfs,target=/scratch",
		"--mount", "source=data,target=/var/lib/data,readonly",
		"--mount", "type=file,source="+appConf+",target=/etc/app.conf,ro")
	if err != nil {
		t.Fatalf("buildCreate: %v", err)
	}

	want := []dicer.Mount{
		{Type: dicer.MountTypeTmpfs, Target: "/scratch"},
		{Type: dicer.MountTypeVolume, Source: "data", Target: "/var/lib/data", ReadOnly: true},
		{Type: dicer.MountTypeFile, Target: "/etc/app.conf", ReadOnly: true, Content: []byte("k=v"), Mode: 0o640},
	}
	if !reflect.DeepEqual(got.spec.Mounts, want) {
		t.Errorf("mounts = %+v, want %+v", got.spec.Mounts, want)
	}
}

// TestBuildCreateRequestDirectoryMounts checks that a directory's source is
// sent as given: it is a path on the daemon's host, not on this machine.
func TestBuildCreateRequestDirectoryMounts(t *testing.T) {
	got, err := runBuild(t, "web",
		"--mount", "type=directory,source=/srv/shared/src,target=/app",
		"--mount", "type=directory,src=/srv/shared/docs,dst=/docs,ro")
	if err != nil {
		t.Fatalf("buildCreateRequest: %v", err)
	}

	want := []dicer.Mount{
		{Type: dicer.MountTypeDirectory, Source: "/srv/shared/src", Target: "/app"},
		{Type: dicer.MountTypeDirectory, Source: "/srv/shared/docs", Target: "/docs", ReadOnly: true},
	}
	if !reflect.DeepEqual(got.spec.Mounts, want) {
		t.Errorf("mounts = %+v, want %+v", got.spec.Mounts, want)
	}
}

func TestBuildCreateRequestRejectsBadMounts(t *testing.T) {
	for _, argv := range [][]string{
		{"--mount", "type=volume,source=data"},
		{"--mount", "type=volume,target=/x,color=red"},
		{"--mount", "type=file,target=/x"},
		{"--mount", "type=file,source=" + t.TempDir() + ",target=/x"},
		{"--mount", "type=file,source=/does/not/exist,target=/x"},
	} {
		if _, err := runBuild(t, append([]string{"web"}, argv...)...); err == nil {
			t.Errorf("%v should be rejected", argv)
		}
	}
}

func TestBuildCreateRequestCommandAfterDash(t *testing.T) {
	got, err := runBuild(t, "web", "--image", "alpine", "--", "sh", "-c", "echo 'hello world'")
	if err != nil {
		t.Fatalf("buildCreate: %v", err)
	}

	want := []string{"sh", "-c", "echo 'hello world'"}
	if !slices.Equal(got.spec.Cmd, want) {
		t.Errorf("cmd = %q, want %q: arguments must pass through unsplit", got.spec.Cmd, want)
	}
	if got.spec.Name != "web" {
		t.Errorf("name = %q, want web", got.spec.Name)
	}
}

func TestBuildCreateRequestPublish(t *testing.T) {
	req, err := runBuild(t, "web", "-p", "8080:80", "--publish", "10.0.0.1:53:53/udp")
	if err != nil {
		t.Fatalf("buildCreate: %v", err)
	}

	got := req.spec.Ports
	if len(got) != 2 ||
		got[0].HostPort != 8080 || got[0].GuestPort != 80 || got[0].Protocol != "" ||
		got[1].HostIP != "10.0.0.1" || got[1].Protocol != dicer.ProtocolUDP {
		t.Errorf("ports = %v, want 8080:80 and 10.0.0.1:53:53/udp", got)
	}
}

func TestBuildCreateRequestRejectsBadPorts(t *testing.T) {
	for _, bad := range []string{"80", "a:80", "8080:0", "1:2:3:4"} {
		if _, err := runBuild(t, "web", "-p", bad); err == nil {
			t.Errorf("--publish %q should be rejected", bad)
		}
	}
}

func TestFormatPorts(t *testing.T) {
	got := formatPorts([]dicer.PortMapping{
		{HostPort: 8080, GuestPort: 80},
		{HostIP: "10.0.0.1", HostPort: 53, GuestPort: 53, Protocol: dicer.ProtocolUDP},
	})
	if want := "8080->80/tcp, 10.0.0.1:53->53/udp"; got != want {
		t.Errorf("formatPorts = %q, want %q", got, want)
	}
}

func TestInitModeFlag(t *testing.T) {
	got, err := runBuild(t, "web", "--image", "debian", "--init-mode", "systemd")
	if err != nil {
		t.Fatal(err)
	}
	if got.spec.InitMode != dicer.InitModeSystemd {
		t.Errorf("--init-mode: init mode = %q, want systemd", got.spec.InitMode)
	}

	// Left out, it is the daemon's to default.
	if got, _ := runBuild(t, "web", "--image", "debian"); got.spec.InitMode != "" {
		t.Errorf("no --init-mode: init mode = %q, want it unset", got.spec.InitMode)
	}
}

func TestCommandLinesShowTheInitModeOnlyWhenChosen(t *testing.T) {
	for mode, want := range map[dicer.InitMode][]string{
		"":                    {"the image's"},
		dicer.InitModeAuto:    {"the image's"},
		dicer.InitModeSystemd: {"the image's", "in systemd mode"},
	} {
		got := commandLines(dicer.Instance{InitMode: mode})
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("init mode %q: %q, want %q", mode, got, want)
		}
	}
}

// --rm is part of the instance's definition, not of the request that made
// it: the daemon does the deleting, so it must be recorded.
func TestBuildCreateRequestRemoveOnExit(t *testing.T) {
	got, err := runBuild(t, "web", "--image", "alpine", "--rm")
	if err != nil {
		t.Fatalf("buildCreate: %v", err)
	}
	if !got.spec.RemoveOnExit {
		t.Error("--rm was not carried into the definition")
	}

	if got, _ := runBuild(t, "web", "--image", "alpine"); got.spec.RemoveOnExit {
		t.Error("an instance that did not ask to be deleted says it did")
	}
}

func TestPsIsInstanceList(t *testing.T) {
	serveFakeDaemon(t, newFakeInstanceDaemon(fakeInstances()...))

	ps, err := run(t, "ps")
	if err != nil {
		t.Fatalf("ps: %v\n%s", err, ps)
	}
	ls, err := run(t, "instance", "ls")
	if err != nil {
		t.Fatalf("instance ls: %v\n%s", err, ls)
	}

	if ps != ls {
		t.Errorf("dicer ps and dicer instance ls differ:\n%s\n---\n%s", ps, ls)
	}
	for _, want := range []string{"NAME", "web", "db", "cache", "8080->80/tcp"} {
		if !strings.Contains(ps, want) {
			t.Errorf("ps output is missing %q:\n%s", want, ps)
		}
	}

	// -a is taken for Docker's sake, and changes nothing.
	if all, err := run(t, "ps", "-a"); err != nil || all != ps {
		t.Errorf("ps -a = %q, %v; want what ps shows", all, err)
	}
}

func TestPsQuietAndFilters(t *testing.T) {
	serveFakeDaemon(t, newFakeInstanceDaemon(fakeInstances()...))

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"ps", "-q"}, "cache\ndb\nweb\n"},
		{[]string{"ps", "-q", "--filter", "state=running"}, "web\n"},
		{[]string{"ps", "-q", "-f", "state=Running", "-f", "state=paused"}, "cache\nweb\n"},
		{[]string{"ps", "-q", "--filter", "label=team"}, "db\nweb\n"},
		{[]string{"ps", "-q", "--filter", "label=team=data"}, "db\n"},
		{[]string{"ps", "-q", "--filter", "network=default", "--filter", "name=w"}, "web\n"},
		{[]string{"ps", "-q", "--filter", "image=redis"}, "cache\n"},
		{[]string{"ps", "--format", "{{.Name}}:{{.State}}", "--filter", "state=stopped"}, "db:Stopped\n"},
	} {
		out, err := run(t, tc.args...)
		if err != nil {
			t.Errorf("%v: %v\n%s", tc.args, err, out)
			continue
		}
		if out != tc.want {
			t.Errorf("%v = %q, want %q", tc.args, out, tc.want)
		}
	}

	if out, err := run(t, "ps", "--filter", "colour=red"); err == nil || !strings.Contains(err.Error(), "name, state") {
		t.Errorf("an unknown filter key should be refused, naming the known ones: %v\n%s", err, out)
	}
}

// TestPsYAMLGivesTheRecords checks that YAML gives whole records, which
// have no columns to pick.
func TestPsYAMLGivesTheRecords(t *testing.T) {
	serveFakeDaemon(t, newFakeInstanceDaemon(fakeInstances()...))

	out, err := run(t, "ps", "--format", "yaml", "--filter", "name=web")
	if err != nil {
		t.Fatalf("ps: %v\n%s", err, out)
	}

	var records []map[string]any
	if err := yaml.Unmarshal([]byte(out), &records); err != nil {
		t.Fatalf("not YAML: %v\n%s", err, out)
	}
	if len(records) != 1 || records[0]["name"] != "web" || records[0]["ip"] != "10.0.0.5" ||
		records[0]["memory_bytes"] != 1<<30 {
		t.Errorf("records = %v, want web's record, with its memory in bytes", records)
	}

	if out, err := run(t, "ps", "--format", "yaml", "-c", "name,ip"); err == nil {
		t.Errorf("ps --format yaml -c succeeded, want columns refused for records:\n%s", out)
	}
}

func TestRun(t *testing.T) {
	d := newFakeInstanceDaemon()
	serveFakeDaemon(t, d)

	out, err := run(t, "run", "-d", "--name", "web", "-p", "8080:80", "-e", "A=1,2", "-m", "1GiB",
		"nginx:1.27", "nginx", "-g", "daemon off;")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	req := d.created
	want := &dicerdv1.CreateInstanceRequest{
		Name:        "web",
		ImageRef:    "nginx:1.27",
		Cmd:         []string{"nginx", "-g", "daemon off;"},
		Start:       true,
		Vcpus:       1,
		MemoryBytes: 1 << 30,
		DiskBytes:   10 << 30,
		Env:         map[string]string{"A": "1,2"},
		Ports:       []*dicerdv1.PortMapping{{HostPort: 8080, GuestPort: 80}},
		PullPolicy:  dicerdv1.PullPolicy_PULL_POLICY_MISSING,
	}
	if !proto.Equal(req, want) {
		t.Errorf("request = %v\nwant      %v", req, want)
	}
	if !regexp.MustCompile(`Instance web started in [0-9.]+m?s \(10\.0\.0\.9\)`).MatchString(out) {
		t.Errorf("output = %q, want it to say how long the start took and the address", out)
	}

	// The image was pulled first, so its download could be shown, and then
	// the instance created.
	if want := []string{"pull nginx:1.27"}; !slices.Equal(d.calls, want) {
		t.Errorf("calls = %q, want %q", d.calls, want)
	}
	if !strings.Contains(out, "Image nginx:1.27 pulled in") {
		t.Errorf("a download should be reported: %q", out)
	}
}

// TestPullFlagSaysWhenTheImageIsPulled checks that run and create pull the
// image themselves as --pull says, so that its progress shows, and leave the
// daemon only to find it; and that --pull never leaves the daemon to refuse
// an image the host lacks.
func TestPullFlagSaysWhenTheImageIsPulled(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		cached    bool
		wantCalls []string
		wantPull  dicerdv1.PullPolicy
	}{
		{
			name:      "missing pulls an image the host lacks",
			args:      []string{"run", "-d", "--name", "web", "nginx:1.27"},
			wantCalls: []string{"pull nginx:1.27"},
			wantPull:  dicerdv1.PullPolicy_PULL_POLICY_MISSING,
		},
		{
			name:     "missing uses the image held",
			args:     []string{"run", "-d", "--name", "web", "--pull", "missing", "nginx:1.27"},
			cached:   true,
			wantPull: dicerdv1.PullPolicy_PULL_POLICY_MISSING,
		},
		{
			name:      "always pulls the image held",
			args:      []string{"run", "-d", "--name", "web", "--pull", "always", "nginx:1.27"},
			cached:    true,
			wantCalls: []string{"pull nginx:1.27"},
			wantPull:  dicerdv1.PullPolicy_PULL_POLICY_MISSING,
		},
		{
			name:     "never leaves it to the daemon",
			args:     []string{"run", "-d", "--name", "web", "--pull", "never", "nginx:1.27"},
			wantPull: dicerdv1.PullPolicy_PULL_POLICY_NEVER,
		},
		{
			name:      "create pulls too",
			args:      []string{"create", "web", "-i", "nginx:1.27"},
			wantCalls: []string{"pull nginx:1.27"},
			wantPull:  dicerdv1.PullPolicy_PULL_POLICY_MISSING,
		},
		{
			name:     "create honours never",
			args:     []string{"create", "web", "-i", "nginx:1.27", "--pull", "never"},
			wantPull: dicerdv1.PullPolicy_PULL_POLICY_NEVER,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newFakeInstanceDaemon()
			d.cached["nginx:1.27"] = tt.cached
			serveFakeDaemon(t, d)

			if out, err := run(t, tt.args...); err != nil {
				t.Fatalf("%s: %v\n%s", tt.args[0], err, out)
			}

			if !slices.Equal(d.calls, tt.wantCalls) {
				t.Errorf("calls = %q, want %q", d.calls, tt.wantCalls)
			}
			if got := d.created.GetPullPolicy(); got != tt.wantPull {
				t.Errorf("created with pull policy %v, want %v", got, tt.wantPull)
			}
		})
	}
}

func TestRunNamesAfterTheImage(t *testing.T) {
	d := newFakeInstanceDaemon()
	serveFakeDaemon(t, d)

	if out, err := run(t, "run", "-d", "ghcr.io/acme/Web_App:2@sha256:abc", "--", "serve"); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	name := d.created.GetName()
	if !strings.HasPrefix(name, "web-app-") || len(name) != len("web-app-")+4 {
		t.Errorf("name = %q, want web-app- and a four-character suffix", name)
	}
	if !slices.Equal(d.created.GetCmd(), []string{"serve"}) {
		t.Errorf("cmd = %q, want the -- dropped", d.created.GetCmd())
	}
}

func TestUpdateSendsOnlyWhatChanged(t *testing.T) {
	d := newFakeInstanceDaemon(fakeInstances()...)
	serveFakeDaemon(t, d)

	if out, err := run(t, "update", "db", "--memory", "2GiB", "--restart", "always", "--init-mode", "exec",
		"-l", "tier=gold"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}

	memory := int64(2 << 30)
	want := &dicerdv1.UpdateInstanceRequest{
		Name:          "db",
		MemoryBytes:   &memory,
		RestartPolicy: &dicerdv1.RestartPolicy{Mode: dicerdv1.RestartMode_RESTART_MODE_ALWAYS},
		InitMode:      dicerdv1.InitMode_INIT_MODE_EXEC,
		Labels:        map[string]string{"tier": "gold"},
	}
	if !proto.Equal(d.updated, want) {
		t.Errorf("request = %v\nwant      %v", d.updated, want)
	}

	if _, err := run(t, "update", "db"); err == nil || !strings.Contains(err.Error(), "needs something to change") {
		t.Errorf("an update with nothing to change should say so, got %v", err)
	}
}

func TestUpdateSetsAndRemovesMaximums(t *testing.T) {
	d := newFakeInstanceDaemon(fakeInstances()...)
	serveFakeDaemon(t, d)

	if out, err := run(t, "update", "db", "--max-vcpus", "8", "--max-memory", "0"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	vcpus, memory := int32(8), int64(0)
	want := &dicerdv1.UpdateInstanceRequest{Name: "db", MaxVcpus: &vcpus, MaxMemoryBytes: &memory}
	if !proto.Equal(d.updated, want) {
		t.Errorf("request = %v\nwant      %v", d.updated, want)
	}
}

func TestUpdateSetsAndRemovesRateLimits(t *testing.T) {
	d := newFakeInstanceDaemon(fakeInstances()...)
	serveFakeDaemon(t, d)

	if out, err := run(t, "update", "db", "--disk-rate", "50MiB/s", "--disk-iops", "1000",
		"--download-rate", "0"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	diskRate, iops, download := int64(50<<20), int64(1000), int64(0)
	want := &dicerdv1.UpdateInstanceRequest{
		Name: "db", DiskBytesPerSecond: &diskRate, DiskIops: &iops, DownloadBytesPerSecond: &download,
	}
	if !proto.Equal(d.updated, want) {
		t.Errorf("request = %v\nwant      %v", d.updated, want)
	}

	if _, err := run(t, "update", "db", "--upload-rate", "fast"); err == nil ||
		!strings.Contains(err.Error(), "invalid --upload-rate") {
		t.Errorf("an invalid rate should be refused, got %v", err)
	}
}

// TestInspectWithoutEvents checks that inspect shows an instance to a token
// that cannot read events, without them.
func TestInspectWithoutEvents(t *testing.T) {
	daemon := newFakeInstanceDaemon(fakeInstances()...)
	daemon.eventsErr = status.Error(codes.PermissionDenied, `token "ci" lacks scope events:read`)
	serveFakeDaemon(t, daemon)

	out, err := run(t, "inspect", "web")
	if err != nil {
		t.Fatalf("inspect: %v\n%s", err, out)
	}
	if !strings.Contains(out, "● web") || strings.Contains(out, "Events") {
		t.Errorf("inspect = \n%s\nwant the instance without events", out)
	}
}

func TestInspect(t *testing.T) {
	serveFakeDaemon(t, newFakeInstanceDaemon(fakeInstances()...))

	out, err := run(t, "inspect", "web")
	if err != nil {
		t.Fatalf("inspect: %v\n%s", err, out)
	}
	for _, want := range []string{
		"● web — docker.io/library/nginx:1.27\n",
		"     Active: running\n",
		"    Command: nginx -g 'daemon off;'\n",
		"    Machine: 2 vCPUs, 1 GiB memory, 10 GiB disk\n",
		"      Ports: 8080->80/tcp\n             10.1.0.1:5353->53/udp\n",
		"        Env: A=1\n             B=2\n",
		"     Labels: team=web\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("inspect output is missing %q:\n%s", want, out)
		}
	}
	// What an instance does not have is left out, not shown as "-".
	if strings.Contains(out, "Volumes") || strings.Contains(out, "\x1b[") {
		t.Errorf("inspect output lists an empty field, or is coloured when piped:\n%s", out)
	}

	out, err = run(t, "inspect", "web", "db", "--format", "json")
	if err != nil {
		t.Fatalf("inspect json: %v\n%s", err, out)
	}
	var records []map[string]any
	if err := json.Unmarshal([]byte(out), &records); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	// The whole record, as the client has it: its field names, and its
	// enumerations as the CLI writes them.
	if len(records) != 2 {
		t.Fatalf("records = %v, want one per instance", records)
	}
	if r := records[0]; r["image_ref"] != "docker.io/library/nginx:1.27" || r["memory_bytes"] != float64(1<<30) ||
		r["state"] != "running" || r["ip"] != "10.0.0.5" {
		t.Errorf("record = %v", r)
	}
	if records[1]["name"] != "db" {
		t.Errorf("second record = %v", records[1])
	}

	if out, err := run(t, "inspect", "web", "--format", "{{.IP}}"); err != nil || out != "10.0.0.5\n" {
		t.Errorf("inspect --format '{{.IP}}' = %q, %v", out, err)
	}
}

func TestPsCompactByDefault(t *testing.T) {
	instances := fakeInstances()
	instances[0].StartTime = timestamppb.New(time.Now().Add(-3 * time.Minute))
	serveFakeDaemon(t, newFakeInstanceDaemon(instances...))

	out, err := run(t, "ps")
	if err != nil {
		t.Fatalf("ps: %v\n%s", err, out)
	}
	header, _, _ := strings.Cut(out, "\n")
	if got := strings.Fields(header); !slices.Equal(got, []string{"NAME", "IMAGE", "STATUS", "IP", "PORTS"}) {
		t.Errorf("header = %q, want the compact columns", got)
	}
	if !strings.Contains(out, "Up 3 minutes") {
		t.Errorf("a running instance should say how long it has been up:\n%s", out)
	}

	out, err = run(t, "ps", "--wide")
	if err != nil {
		t.Fatalf("ps --wide: %v\n%s", err, out)
	}
	for _, col := range []string{"STATE", "VCPU", "MEMORY", "DISK", "NETWORK", "CREATED"} {
		if !strings.Contains(out, col) {
			t.Errorf("ps --wide is missing column %s:\n%s", col, out)
		}
	}

	// Structured output has every column, whatever a table shows.
	out, err = run(t, "ps", "--format", "{{.VCPU}} {{.State}}", "-f", "name=web")
	if err != nil || out != "2 Running\n" {
		t.Errorf("template output = %q, %v", out, err)
	}
}

func TestRunWithCachedImageSaysNothingOfIt(t *testing.T) {
	d := newFakeInstanceDaemon()
	d.cached["alpine:3.21"] = true
	serveFakeDaemon(t, d)

	out, err := run(t, "run", "-d", "--name", "a", "alpine:3.21")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.Contains(out, "pulled") || strings.Contains(out, "Resolving") {
		t.Errorf("an image already on the host was reported on:\n%s", out)
	}
	// Nor is its registry asked about it, which may be unreachable, or
	// limiting the host's pulls.
	if slices.Contains(d.calls, "pull alpine:3.21") {
		t.Errorf("calls = %v, want no pull of an image the host holds", d.calls)
	}
}

func TestInstanceStatusForAnInstanceThatEnded(t *testing.T) {
	ago := time.Now().Add(-2 * time.Minute)
	soon := time.Now().Add(30 * time.Second)

	tests := []struct {
		instance dicer.Instance
		want     string
	}{
		{dicer.Instance{State: dicer.InstanceStateStopped, ExitCode: new(0), FinishTime: ago}, "Exited (0) 2 minutes ago"},
		{
			dicer.Instance{State: dicer.InstanceStateFailed, StateError: "exit code 1", ExitCode: new(1), FinishTime: ago},
			"Exited (1) 2 minutes ago",
		},
		{dicer.Instance{State: dicer.InstanceStateFailed, StateError: "the guest reset"}, "Failed: the guest reset"},
		{dicer.Instance{State: dicer.InstanceStateRestarting, RestartCount: 3, NextRestartTime: soon}, "Restarting (3) in 2"},
		{dicer.Instance{State: dicer.InstanceStateRestarting, RestartCount: 1}, "Restarting (1)"},
		{dicer.Instance{State: dicer.InstanceStateStopped}, "Stopped"},
	}
	for _, tt := range tests {
		// The wait before a restart is only compared as far as it does not
		// depend on how long the test takes.
		if got := instanceStatus(tt.instance); !strings.HasPrefix(got, tt.want) ||
			(tt.instance.NextRestartTime.IsZero() && got != tt.want) {
			t.Errorf("instanceStatus(%v) = %q, want %q", tt.instance, got, tt.want)
		}
	}
}
