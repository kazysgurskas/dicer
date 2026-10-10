// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/konradasb/dicer/internal/instance"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

// TestLoadConfigMissingFileUsesDefaults covers first run: the defaults are a
// working single-host configuration, so a missing file must not be an error.
func TestLoadConfigMissingFileUsesDefaults(t *testing.T) {
	cfg, err := loadConfig(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	want := defaultConfig()
	if cfg.DataDir != want.DataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, want.DataDir)
	}
	if cfg.Server.Socket.Path != want.Server.Socket.Path {
		t.Errorf("Server.Socket.Path = %q, want %q", cfg.Server.Socket.Path, want.Server.Socket.Path)
	}
	// The API is served on the network only when asked to be.
	if cfg.Server.ServesTCP() {
		t.Error("the API listens on TCP by default")
	}
	if cfg.LogLevel != want.LogLevel {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, want.LogLevel)
	}
}

func TestLoadConfigOverridesDefaults(t *testing.T) {
	path := writeConfig(t, `
data_dir: /srv/dicer
server:
  listen: 0.0.0.0:7443
  socket:
    path: /tmp/dicer.sock
log_level: debug
network:
  uplink_interface: eth1
`)

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.DataDir != "/srv/dicer" {
		t.Errorf("DataDir = %q, want /srv/dicer", cfg.DataDir)
	}
	if cfg.Server.Socket.Path != "/tmp/dicer.sock" {
		t.Errorf("Server.Socket.Path = %q, want /tmp/dicer.sock", cfg.Server.Socket.Path)
	}
	if cfg.Server.Listen != "0.0.0.0:7443" {
		t.Errorf("Server.Listen = %q, want 0.0.0.0:7443", cfg.Server.Listen)
	}
	if cfg.Network.UplinkInterface != "eth1" {
		t.Errorf("UplinkInterface = %q, want eth1", cfg.Network.UplinkInterface)
	}
}

// TestLoadConfigPartialKeepsDefaults checks that a file setting one key does
// not blank out everything it does not mention.
func TestLoadConfigPartialKeepsDefaults(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, "log_level: warn\n"))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want warn", cfg.LogLevel)
	}
	if cfg.DataDir != defaultConfig().DataDir {
		t.Errorf("DataDir = %q, want the default to survive", cfg.DataDir)
	}
	if cfg.Server.Socket.Mode != defaultConfig().Server.Socket.Mode {
		t.Errorf("Server.Socket.Mode = %o, want the default to survive", cfg.Server.Socket.Mode)
	}
}

func TestLoadConfigDNSIsOnUnlessTurnedOff(t *testing.T) {
	if !defaultConfig().Network.DNS {
		t.Error("DNS is off by default, want it on")
	}

	cfg, err := loadConfig(writeConfig(t, "network:\n  dns: false\n"))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Network.DNS {
		t.Error("network.dns: false left DNS on")
	}
	if cfg.Network.UploadBurstMultiplier != defaultConfig().Network.UploadBurstMultiplier {
		t.Error("turning DNS off lost the network section's other defaults")
	}
}

// A key the daemon does not know is refused rather than ignored, so that a
// typo cannot silently leave a setting at its default.
func TestLoadConfigRejectsUnknownKeys(t *testing.T) {
	if _, err := loadConfig(writeConfig(t, "listen: /tmp/dicer.sock\n")); err == nil {
		t.Error("expected an error for an unknown key")
	}
}

func TestLoadConfigAcceptsEmptyFile(t *testing.T) {
	if _, err := loadConfig(writeConfig(t, "")); err != nil {
		t.Errorf("loadConfig of an empty file: %v", err)
	}
}

func TestLoadConfigRejectsMalformedYAML(t *testing.T) {
	if _, err := loadConfig(writeConfig(t, "{{{not yaml")); err == nil {
		t.Error("expected an error for malformed YAML")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{name: "defaults are valid", mutate: func(*Config) {}},
		{name: "debug level", mutate: func(c *Config) { c.LogLevel = "debug" }},
		{name: "error level", mutate: func(c *Config) { c.LogLevel = "error" }},
		{name: "unknown log level", mutate: func(c *Config) { c.LogLevel = "verbose" }, wantErr: true},
		{name: "empty log level", mutate: func(c *Config) { c.LogLevel = "" }, wantErr: true},
		{name: "missing data dir", mutate: func(c *Config) { c.DataDir = "" }, wantErr: true},
		{name: "missing socket", mutate: func(c *Config) { c.Server.Socket.Path = "" }, wantErr: true},
		{name: "tcp listener", mutate: func(c *Config) { c.Server.Listen = "0.0.0.0:7443" }},
		{name: "tcp without port", mutate: func(c *Config) { c.Server.Listen = "0.0.0.0" }, wantErr: true},
		{name: "tcp with named port", mutate: func(c *Config) { c.Server.Listen = "0.0.0.0:https" }, wantErr: true},
		{name: "tcp on any port", mutate: func(c *Config) { c.Server.Listen = "0.0.0.0:0" }, wantErr: true},
		{name: "certificate and key", mutate: func(c *Config) { c.Server.CrtFile, c.Server.KeyFile = "c", "k" }},
		{name: "certificate alone", mutate: func(c *Config) { c.Server.CrtFile = "c" }, wantErr: true},
		{name: "key alone", mutate: func(c *Config) { c.Server.KeyFile = "k" }, wantErr: true},
		{name: "socket group by id", mutate: func(c *Config) { c.Server.Socket.Group = "0" }},
		{name: "unknown socket group", mutate: func(c *Config) { c.Server.Socket.Group = "no-such-group" }, wantErr: true},
		{name: "no keepalive interval", mutate: func(c *Config) { c.Server.Keepalive.Interval = 0 }, wantErr: true},
		{name: "negative keepalive timeout", mutate: func(c *Config) { c.Server.Keepalive.Timeout = -time.Second }, wantErr: true},
		{name: "no keepalive client interval", mutate: func(c *Config) { c.Server.Keepalive.MinClientInterval = 0 }, wantErr: true},
		{name: "registry password", mutate: func(c *Config) {
			c.Registries = map[string]RegistryConfig{"ghcr.io": {Username: "bot", Password: "x"}}
		}},
		{name: "registry password file", mutate: func(c *Config) {
			c.Registries = map[string]RegistryConfig{"ghcr.io": {Username: "bot", PasswordFile: "/etc/x"}}
		}},
		{name: "registry helper", mutate: func(c *Config) {
			c.Registries = map[string]RegistryConfig{"ghcr.io": {CredentialHelper: "ecr-login"}}
		}},
		{name: "registry without username", mutate: func(c *Config) {
			c.Registries = map[string]RegistryConfig{"ghcr.io": {Password: "x"}}
		}, wantErr: true},
		{name: "registry without password", mutate: func(c *Config) {
			c.Registries = map[string]RegistryConfig{"ghcr.io": {Username: "bot"}}
		}, wantErr: true},
		{name: "registry password twice", mutate: func(c *Config) {
			c.Registries = map[string]RegistryConfig{"ghcr.io": {Username: "bot", Password: "x", PasswordFile: "/etc/x"}}
		}, wantErr: true},
		{name: "registry helper and password", mutate: func(c *Config) {
			c.Registries = map[string]RegistryConfig{"ghcr.io": {CredentialHelper: "ecr-login", Username: "bot"}}
		}, wantErr: true},
		{name: "allowed directories", mutate: func(c *Config) {
			c.Mounts.AllowedDirectories = []string{"/srv/shared", "/home/dev/projects"}
		}},
		{name: "relative allowed directory", mutate: func(c *Config) {
			c.Mounts.AllowedDirectories = []string{"srv/shared"}
		}, wantErr: true},
		{name: "root allowed", mutate: func(c *Config) {
			c.Mounts.AllowedDirectories = []string{"/"}
		}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Error("expected an error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	if _, err := loadConfig(writeConfig(t, "log_level: verbose\n")); err == nil {
		t.Error("expected loadConfig to validate what it parsed")
	}
}

func TestNoDirectoriesAreAllowedByDefault(t *testing.T) {
	if dirs := defaultConfig().Mounts.AllowedDirectories; len(dirs) != 0 {
		t.Errorf("Mounts.AllowedDirectories = %v by default, want none: a request must not reach the host's files unless its administrator says so", dirs)
	}
}

func TestMetricsAreOffByDefault(t *testing.T) {
	cfg := defaultConfig()

	if cfg.Metrics.Enable {
		t.Error("metrics are enabled by default; the daemon should open no network listener unless asked")
	}
	if cfg.Metrics.Listen != defaultMetricsListen {
		t.Errorf("Metrics.Listen = %q, want %q", cfg.Metrics.Listen, defaultMetricsListen)
	}
}

func TestLoadConfigEnablesMetrics(t *testing.T) {
	path := writeConfig(t, `
metrics:
  enable: true
  listen: 0.0.0.0:9101
`)

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if !cfg.Metrics.Enable {
		t.Error("Metrics.Enable = false, want true")
	}
	if cfg.Metrics.Listen != "0.0.0.0:9101" {
		t.Errorf("Metrics.Listen = %q, want 0.0.0.0:9101", cfg.Metrics.Listen)
	}
}

func TestValidateRejectsBadMetricsConfig(t *testing.T) {
	tests := []struct {
		name    string
		metrics MetricsConfig
		wantErr bool
	}{
		{
			name:    "disabled is never invalid",
			metrics: MetricsConfig{},
		},
		{
			name:    "valid",
			metrics: MetricsConfig{Enable: true, Listen: "127.0.0.1:9101"},
		},
		{
			name:    "no listen address",
			metrics: MetricsConfig{Enable: true},
			wantErr: true,
		},
		{
			name:    "address without a port",
			metrics: MetricsConfig{Enable: true, Listen: "127.0.0.1"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.Metrics = tt.metrics

			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestResourcesDefaults(t *testing.T) {
	cfg := defaultConfig()

	capacity, err := cfg.Resources.capacity(4, 32<<30)
	if err != nil {
		t.Fatalf("capacity: %v", err)
	}

	// vCPUs shared four to a CPU; memory not overcommitted, less 1GiB.
	want := instance.Resources{VCPUs: 16, MemoryBytes: 31 << 30}
	if got := capacity.Allocatable(); got != want {
		t.Errorf("allocatable = %+v, want %+v", got, want)
	}
}

func TestResourcesReserveMustLeaveSomething(t *testing.T) {
	cfg := defaultConfig()

	if _, err := cfg.Resources.capacity(4, 1<<30); err == nil {
		t.Error("a reserve of all the host's memory was accepted")
	}
}

func TestValidateResources(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ResourcesConfig)
	}{
		{"zero cpu overcommit", func(r *ResourcesConfig) { r.CPUOvercommit = 0 }},
		{"negative memory overcommit", func(r *ResourcesConfig) { r.MemoryOvercommit = -1 }},
		{"negative reserve", func(r *ResourcesConfig) { r.ReservedMemoryBytes = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultConfig()
			tc.mutate(&cfg.Resources)
			if err := cfg.Validate(); err == nil {
				t.Error("Validate accepted it")
			}
		})
	}
}

func TestLoadConfigResources(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, `
resources:
  cpu_overcommit: 2
  memory_overcommit: 1.5
`))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.Resources.CPUOvercommit != 2 || cfg.Resources.MemoryOvercommit != 1.5 {
		t.Errorf("resources = %+v, want the file's ratios", cfg.Resources)
	}
	// What the file does not mention keeps its default.
	if cfg.Resources.ReservedMemoryBytes != defaultReservedMemoryBytes {
		t.Errorf("reserve = %d, want the default", cfg.Resources.ReservedMemoryBytes)
	}
}

func TestLoadConfigImageGC(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, `
images:
  gc_max_unused_age: 168h
  gc_max_size: 50GiB
  gc_interval: 30m
`))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	got := cfg.Images.gcPolicy()
	if got.MaxUnusedAge != 168*time.Hour || got.MaxSize != 50<<30 || !got.Enabled() {
		t.Errorf("policy = %+v, want a week and 50GiB", got)
	}
	if cfg.Images.GCInterval != 30*time.Minute {
		t.Errorf("interval = %v, want 30m", cfg.Images.GCInterval)
	}

	// A size in plain bytes is taken too.
	cfg, err = loadConfig(writeConfig(t, "images:\n  gc_max_size: 1073741824\n"))
	if err != nil || cfg.Images.gcPolicy().MaxSize != 1<<30 {
		t.Errorf("gc_max_size in bytes = %+v, %v", cfg.Images, err)
	}
}

// Garbage collection is off unless a limit is set, and runs hourly when one
// is.
func TestImageGCIsOffByDefault(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, "log_level: info\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Images.gcPolicy().Enabled() {
		t.Errorf("policy = %+v, want nothing collected", cfg.Images.gcPolicy())
	}
	if cfg.Images.GCInterval != time.Hour {
		t.Errorf("interval = %v, want an hour", cfg.Images.GCInterval)
	}
}

func TestLoadConfigRejectsBadImageGC(t *testing.T) {
	for _, body := range []string{
		"images:\n  gc_max_size: lots\n",
		"images:\n  gc_max_unused_age: a week\n",
		"images:\n  gc_max_unused_age: -1h\n",
		"images:\n  gc_interval: 0s\n",
		"images:\n  gc_max_unused_age: 7d\n",
	} {
		if _, err := loadConfig(writeConfig(t, body)); err == nil {
			t.Errorf("loadConfig accepted %q", body)
		}
	}
}

// A keepalive setting given replaces its default and leaves the others.
func TestLoadConfigKeepalive(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, "server:\n  keepalive:\n    interval: 1m\n"))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	want := KeepaliveConfig{
		Interval:          time.Minute,
		Timeout:           defaultKeepaliveTimeout,
		MinClientInterval: defaultKeepaliveMinClientInterval,
	}
	if cfg.Server.Keepalive != want {
		t.Errorf("keepalive = %+v, want %+v", cfg.Server.Keepalive, want)
	}
}

func TestLoadConfigEvents(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, "log_level: info\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Events.MaxCount != 10000 || cfg.Events.MaxAge != 0 {
		t.Errorf("default events = %+v, want 10000 kept, of any age", cfg.Events)
	}

	cfg, err = loadConfig(writeConfig(t, "events:\n  max_count: 500\n  max_age: 720h\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Events.MaxCount != 500 || cfg.Events.MaxAge != 720*time.Hour {
		t.Errorf("events = %+v, want 500 kept, for 30 days", cfg.Events)
	}

	for _, body := range []string{"events:\n  max_count: 0\n", "events:\n  max_age: -1h\n"} {
		if _, err := loadConfig(writeConfig(t, body)); err == nil {
			t.Errorf("loadConfig accepted %q", body)
		}
	}
}

func TestServerConfigListenerPort(t *testing.T) {
	tests := []struct {
		name   string
		listen string
		want   int
	}{
		{"unset", "", 0},
		{"all addresses", "0.0.0.0:9000", 9000},
		{"one address", "10.0.0.1:7443", 7443},
		{"no host", ":9000", 9000},
		{"IPv6", "[::]:9000", 9000},
		{"no port", "0.0.0.0", 0},
		{"named port", "0.0.0.0:https", 0},
		{"port 0", "0.0.0.0:0", 0},
		{"port out of range", "0.0.0.0:70000", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (ServerConfig{Listen: tt.listen}).ListenerPort(); got != tt.want {
				t.Errorf("ServerConfig{Listen: %q}.ListenerPort() = %d, want %d", tt.listen, got, tt.want)
			}
		})
	}
}

// configReference is the documentation's page for the configuration, which
// make docs-gen generates from Config's doc comments.
const configReference = "../../docs/content/docs/reference/configuration.md"

// TestConfigReferenceStatesTheDefaults keeps the defaults the configuration
// reference states in step with defaultConfig: every key with a default
// names it in its entry.
func TestConfigReferenceStatesTheDefaults(t *testing.T) {
	page, err := os.ReadFile(configReference)
	if err != nil {
		t.Fatal(err)
	}

	defaults := reflect.ValueOf(defaultConfig())
	for _, key := range configKeys(defaults.Type(), "") {
		want := documentedDefault(lookupField(defaults, strings.Split(key, ".")))
		if want == "" {
			continue
		}

		entry, ok := referenceEntry(string(page), key)
		if !ok {
			t.Errorf("the configuration reference has no entry for %s: run make docs-gen", key)
			continue
		}
		if !strings.Contains(entry, "Unset is "+want) && !strings.Contains(entry, "Unset is `"+want+"`") {
			t.Errorf("the configuration reference does not say %s is %s when unset:\n%s", key, want, entry)
		}
	}
}

// referenceEntry returns the text of a key's entry on the reference page.
func referenceEntry(page, key string) (string, bool) {
	_, entry, ok := strings.Cut(page, "\n### `"+key+"` {#")
	if !ok {
		return "", false
	}
	entry, _, _ = strings.Cut(entry, "\n#")

	return entry, true
}

// configKeys returns the dotted YAML path of every leaf key in t.
func configKeys(t reflect.Type, prefix string) []string {
	var keys []string
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		if f.Type.Kind() == reflect.Struct && f.Type.PkgPath() == t.PkgPath() {
			keys = append(keys, configKeys(f.Type, prefix+name+".")...)
			continue
		}
		keys = append(keys, prefix+name)
	}
	return keys
}

// lookupField returns the field of v at a dotted YAML path.
func lookupField(v reflect.Value, path []string) reflect.Value {
	for _, name := range path {
		t := v.Type()
		for i := range t.NumField() {
			if key, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ","); key == name {
				v = v.Field(i)
				break
			}
		}
	}
	return v
}

// documentedDefault returns a default as the reference writes it, or "" for
// one that is the zero value, which the reference describes in words.
func documentedDefault(v reflect.Value) string {
	if v.IsZero() {
		return ""
	}

	switch x := v.Interface().(type) {
	case time.Duration:
		s := x.String()
		s = strings.TrimSuffix(s, "0s")
		return strings.TrimSuffix(s, "0m")
	case uint32:
		return fmt.Sprintf("%#o", x)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}
