// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konradasb/dicer"
)

func TestParseEnv(t *testing.T) {
	t.Setenv("DICER_TEST_FROM_SHELL", "shell-value")

	file := filepath.Join(t.TempDir(), "app.env")
	if err := os.WriteFile(file, []byte(
		"# comment\n\nFROM_FILE=file\nOVERRIDDEN=file\nDICER_TEST_FROM_SHELL\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := parseEnv([]string{
		"OVERRIDDEN=flag",
		"LIST=a,b,c",       // commas belong to the value
		"EQUALS=x=y",       // so does any = after the first
		"EMPTY=",           // an empty value is still a value
		"DICER_TEST_UNSET", // a KEY the shell does not have is left out
	}, []string{file})
	if err != nil {
		t.Fatalf("parseEnv: %v", err)
	}

	want := map[string]string{
		"FROM_FILE":             "file",
		"OVERRIDDEN":            "flag",
		"DICER_TEST_FROM_SHELL": "shell-value",
		"LIST":                  "a,b,c",
		"EQUALS":                "x=y",
		"EMPTY":                 "",
	}
	if !maps.Equal(got, want) {
		t.Errorf("env = %v, want %v", got, want)
	}

	for _, bad := range []string{"=value", "BAD KEY=1"} {
		if _, err := parseEnv([]string{bad}, nil); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}

	badFile := filepath.Join(t.TempDir(), "bad.env")
	if err := os.WriteFile(badFile, []byte("OK=1\n=oops\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parseEnv(nil, []string{badFile}); err == nil || !strings.Contains(err.Error(), "bad.env:2") {
		t.Errorf("a bad line should be reported by file and line, got %v", err)
	}
}

func TestParseLabels(t *testing.T) {
	got, err := parseLabels([]string{"team=web", "canary", "expr=a=b"})
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if want := map[string]string{"team": "web", "canary": "", "expr": "a=b"}; !maps.Equal(got, want) {
		t.Errorf("labels = %v, want %v", got, want)
	}
	if _, err := parseLabels([]string{"=x"}); err == nil {
		t.Error("a label with no key should be refused")
	}
}

func TestBuildCreateRequestEnvAndLabels(t *testing.T) {
	got, err := runBuild(t, "web", "-e", "A=1,2", "--env", "B=3", "-l", "team=web")
	if err != nil {
		t.Fatalf("buildCreate: %v", err)
	}
	if want := map[string]string{"A": "1,2", "B": "3"}; !maps.Equal(got.spec.Env, want) {
		t.Errorf("env = %v, want %v", got.spec.Env, want)
	}
	if want := map[string]string{"team": "web"}; !maps.Equal(got.spec.Labels, want) {
		t.Errorf("labels = %v, want %v", got.spec.Labels, want)
	}
}

func TestWantTTY(t *testing.T) {
	for _, tc := range []struct {
		force, never, stdin, stdout, want bool
	}{
		{want: false},
		{stdin: true, stdout: true, want: true},
		// Output piped away: a TTY would mangle it.
		{stdin: true, stdout: false, want: false},
		{force: true, want: true},
		{never: true, stdin: true, stdout: true, want: false},
	} {
		if got := wantTTY(tc.force, tc.never, tc.stdin, tc.stdout); got != tc.want {
			t.Errorf("wantTTY(%+v) = %v", tc, got)
		}
	}
}

func TestShellJoin(t *testing.T) {
	got := shellJoin([]string{"sh", "-c", "echo 'hi' $HOME", ""})
	if want := `sh -c 'echo '\''hi'\'' $HOME' ''`; got != want {
		t.Errorf("shellJoin = %s, want %s", got, want)
	}
}

func TestInstanceFilters(t *testing.T) {
	instance := dicer.Instance{
		Name: "web-1", ImageRef: "nginx:1.27", NetworkName: "default",
		Labels: map[string]string{"team": "web"},
		State:  dicer.InstanceStateRunning,
	}

	for _, tc := range []struct {
		filters []string
		want    bool
	}{
		{nil, true},
		{[]string{"name=web"}, true},
		{[]string{"state=RUNNING"}, true},
		{[]string{"state=stopped"}, false},
		{[]string{"state=stopped", "state=running"}, true},  // any value of one key
		{[]string{"state=running", "network=other"}, false}, // every key
		{[]string{"label=team"}, true},
		{[]string{"label=team=data"}, false},
		{[]string{"image=nginx"}, true},
	} {
		f, err := parseInstanceFilters(tc.filters)
		if err != nil {
			t.Fatalf("parseInstanceFilters(%q): %v", tc.filters, err)
		}
		if got := len(f.apply([]dicer.Instance{instance})) == 1; got != tc.want {
			t.Errorf("filters %q match = %v, want %v", tc.filters, got, tc.want)
		}
	}

	for _, bad := range []string{"state", "state=", "colour=red"} {
		if _, err := parseInstanceFilters([]string{bad}); err == nil {
			t.Errorf("filter %q should be refused", bad)
		}
	}
}

func TestPullFlagRefusesAnUnknownPolicy(t *testing.T) {
	d := newFakeInstanceDaemon()
	serveFakeDaemon(t, d)

	out, err := run(t, "run", "-d", "--pull", "sometimes", "nginx:1.27")
	if err == nil || !strings.Contains(err.Error(), `invalid --pull "sometimes": want missing, always or never`) {
		t.Fatalf("run = %v, want --pull refused\n%s", err, out)
	}
	if d.created != nil {
		t.Error("an instance was created with an unknown pull policy")
	}
}

func TestParseRestartPolicy(t *testing.T) {
	p, err := parseRestartPolicy("on-failure:5")
	if err != nil || p.Mode != dicer.RestartModeOnFailure || p.MaxRetries != 5 {
		t.Errorf("parseRestartPolicy(on-failure:5) = %v, %v", p, err)
	}
	if _, err := parseRestartPolicy("on-failure:many"); err == nil {
		t.Error("parseRestartPolicy accepted a retry limit that is not a number")
	}
}
