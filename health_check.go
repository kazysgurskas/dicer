// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"fmt"
	"time"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// HealthCheck is how an instance's health is checked: one probe, run inside
// the guest by its agent, and when to run it. Unset timings take the
// defaults: every 10s, a 5s timeout, no start period and 3 retries.
type HealthCheck struct {
	// Exactly one probe is set, unless the check is Disabled. Exec is
	// healthy when the command exits 0.
	Exec []string   `json:"exec,omitzero"`
	HTTP *HTTPProbe `json:"http,omitzero"`
	TCP  *TCPProbe  `json:"tcp,omitzero"`

	// Interval is the time between the end of one probe and the start of
	// the next.
	Interval time.Duration `json:"interval,omitzero"`

	// Timeout is how long a probe may take; one that overruns has failed.
	Timeout time.Duration `json:"timeout,omitzero"`

	// StartPeriod is how long after a start failures do not count.
	StartPeriod time.Duration `json:"start_period,omitzero"`

	// Retries is how many failures in a row make the instance unhealthy.
	Retries int `json:"retries,omitzero"`

	// Disabled switches health checking off, the image's check included.
	// Nothing else may be set with it.
	Disabled bool `json:"disabled,omitzero"`
}

// HTTPProbe checks health by asking for a path on the guest's loopback
// address, which is healthy if it answers 2xx or 3xx.
type HTTPProbe struct {
	Port int `json:"port,omitzero"`

	// Path defaults to /.
	Path string `json:"path,omitzero"`
}

// TCPProbe checks health by opening a connection to the guest's loopback
// address, which is healthy if it is accepted.
type TCPProbe struct {
	Port int `json:"port,omitzero"`
}

// Validate returns an error wrapping ErrInvalidArgument if more than one
// probe is set, which the API cannot carry. The daemon checks the rest.
func (c HealthCheck) Validate() error {
	probes := 0
	if len(c.Exec) > 0 {
		probes++
	}
	if c.HTTP != nil {
		probes++
	}
	if c.TCP != nil {
		probes++
	}
	if probes > 1 {
		return fmt.Errorf("%w: a health check has one probe: exec, http or tcp", ErrInvalidArgument)
	}

	return nil
}

// Health is what an instance's health check has found.
type Health struct {
	Status HealthStatus `json:"status,omitzero"`

	// FailingStreak is how many probes in a row have failed.
	FailingStreak int `json:"failing_streak,omitzero"`

	LastCheckTime time.Time `json:"last_check_time,omitzero"`

	// LastOutput is what the last probe said, truncated.
	LastOutput string `json:"last_output,omitzero"`

	// Check is the check being run: the instance's, or its image's, with
	// the defaults filled in.
	Check HealthCheck `json:"check,omitzero"`
}

// HealthStatus is what an instance's health check has found.
type HealthStatus string

// The health statuses.
const (
	// HealthStatusStarting means there is no verdict yet: the workload is
	// in its start period, or has not been probed.
	HealthStatusStarting HealthStatus = "starting"

	// HealthStatusHealthy means the last probe passed.
	HealthStatusHealthy HealthStatus = "healthy"

	// HealthStatusUnhealthy means the check's retries have failed in a row.
	HealthStatusUnhealthy HealthStatus = "unhealthy"
)

var healthStatuses = enum[HealthStatus, dicerdv1.HealthStatus]{"health status", map[HealthStatus]dicerdv1.HealthStatus{
	HealthStatusStarting:  dicerdv1.HealthStatus_HEALTH_STATUS_STARTING,
	HealthStatusHealthy:   dicerdv1.HealthStatus_HEALTH_STATUS_HEALTHY,
	HealthStatusUnhealthy: dicerdv1.HealthStatus_HEALTH_STATUS_UNHEALTHY,
}}

// healthCheckFromProto returns the check p describes, or nil for nil.
func healthCheckFromProto(p *dicerdv1.HealthCheck) *HealthCheck {
	if p == nil {
		return nil
	}

	c := &HealthCheck{
		Interval:    durationFromProto(p.GetInterval()),
		Timeout:     durationFromProto(p.GetTimeout()),
		StartPeriod: durationFromProto(p.GetStartPeriod()),
		Retries:     int(p.GetRetries()),
		Disabled:    p.GetDisabled(),
	}
	switch probe := p.GetProbe().(type) {
	case *dicerdv1.HealthCheck_Exec:
		c.Exec = probe.Exec.GetCommand()
	case *dicerdv1.HealthCheck_Http:
		c.HTTP = &HTTPProbe{Port: int(probe.Http.GetPort()), Path: probe.Http.GetPath()}
	case *dicerdv1.HealthCheck_Tcp:
		c.TCP = &TCPProbe{Port: int(probe.Tcp.GetPort())}
	}

	return c
}

// healthCheckToProto returns c as the API carries it.
func healthCheckToProto(c HealthCheck) (*dicerdv1.HealthCheck, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}

	p := &dicerdv1.HealthCheck{
		Interval:    durationToProto(c.Interval),
		Timeout:     durationToProto(c.Timeout),
		StartPeriod: durationToProto(c.StartPeriod),
		Retries:     int32(c.Retries),
		Disabled:    c.Disabled,
	}
	switch {
	case len(c.Exec) > 0:
		p.Probe = &dicerdv1.HealthCheck_Exec{Exec: &dicerdv1.HealthCheckExec{Command: c.Exec}}
	case c.HTTP != nil:
		p.Probe = &dicerdv1.HealthCheck_Http{Http: &dicerdv1.HealthCheckHTTP{
			Port: uint32(c.HTTP.Port), Path: c.HTTP.Path,
		}}
	case c.TCP != nil:
		p.Probe = &dicerdv1.HealthCheck_Tcp{Tcp: &dicerdv1.HealthCheckTCP{Port: uint32(c.TCP.Port)}}
	}

	return p, nil
}

// healthFromProto returns the health p reports, or nil for nil.
func healthFromProto(p *dicerdv1.Health) *Health {
	if p == nil {
		return nil
	}

	h := &Health{
		Status:        healthStatuses.fromProto(p.GetStatus()),
		FailingStreak: int(p.GetFailingStreak()),
		LastCheckTime: timeFromProto(p.GetLastCheckTime()),
		LastOutput:    p.GetLastOutput(),
	}
	if check := healthCheckFromProto(p.GetCheck()); check != nil {
		h.Check = *check
	}

	return h
}
