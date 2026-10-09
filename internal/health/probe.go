// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package health

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	diceragentv1 "github.com/konradasb/dicer/proto/diceragent/v1"
)

// agentGrace is added to a probe's timeout for the round trip to the guest
// agent.
const agentGrace = 2 * time.Second

// Result is what one probe found.
type Result struct {
	Healthy bool
	Output  string
	At      time.Time
}

// Probe asks a guest's agent to run check once. A failing probe is a result;
// the error is for an agent that did not answer.
func Probe(ctx context.Context, agent diceragentv1.AgentServiceClient, check Check) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, check.Timeout+agentGrace)
	defer cancel()

	resp, err := agent.Probe(ctx, probeRequest(check))
	if err != nil {
		return Result{Output: "the guest agent did not answer: " + err.Error(), At: time.Now()}, err
	}
	return Result{Healthy: resp.GetHealthy(), Output: resp.GetOutput(), At: time.Now()}, nil
}

// probeRequest is check as the agent takes it.
func probeRequest(check Check) *diceragentv1.ProbeRequest {
	req := &diceragentv1.ProbeRequest{Timeout: durationpb.New(check.Timeout)}

	switch {
	case len(check.Exec) > 0:
		req.Probe = &diceragentv1.ProbeRequest_Exec{Exec: &diceragentv1.ExecProbe{Command: check.Exec}}
	case check.HTTP != nil:
		req.Probe = &diceragentv1.ProbeRequest_Http{Http: &diceragentv1.HTTPProbe{
			Port: uint32(check.HTTP.Port), Path: check.HTTP.Path,
		}}
	case check.TCP != nil:
		req.Probe = &diceragentv1.ProbeRequest_Tcp{Tcp: &diceragentv1.TCPProbe{Port: uint32(check.TCP.Port)}}
	}
	return req
}
