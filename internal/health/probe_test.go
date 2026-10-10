// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package health

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	diceragentv1 "github.com/konradasb/dicer/proto/diceragent/v1"
)

// fakeAgent answers Probe with resp or err, and keeps the request it got.
type fakeAgent struct {
	diceragentv1.AgentServiceClient

	resp *diceragentv1.ProbeResponse
	err  error
	req  *diceragentv1.ProbeRequest
}

func (a *fakeAgent) Probe(
	_ context.Context, req *diceragentv1.ProbeRequest, _ ...grpc.CallOption,
) (*diceragentv1.ProbeResponse, error) {
	a.req = req
	return a.resp, a.err
}

// TestProbeAsksTheAgentForEachKindOfCheck checks that each kind of probe
// reaches the agent as that kind, with the check's timeout.
func TestProbeAsksTheAgentForEachKindOfCheck(t *testing.T) {
	tests := []struct {
		name  string
		check Check
		want  *diceragentv1.ProbeRequest
	}{
		{
			name:  "exec",
			check: Check{Exec: []string{"pg_isready", "-q"}, Timeout: 3 * time.Second},
			want: &diceragentv1.ProbeRequest{
				Timeout: durationpb.New(3 * time.Second),
				Probe: &diceragentv1.ProbeRequest_Exec{
					Exec: &diceragentv1.ExecProbe{Command: []string{"pg_isready", "-q"}},
				},
			},
		},
		{
			name:  "http",
			check: Check{HTTP: &HTTPProbe{Port: 8080, Path: "/healthz"}, Timeout: time.Second},
			want: &diceragentv1.ProbeRequest{
				Timeout: durationpb.New(time.Second),
				Probe: &diceragentv1.ProbeRequest_Http{
					Http: &diceragentv1.HTTPProbe{Port: 8080, Path: "/healthz"},
				},
			},
		},
		{
			name:  "tcp",
			check: Check{TCP: &TCPProbe{Port: 5432}, Timeout: time.Second},
			want: &diceragentv1.ProbeRequest{
				Timeout: durationpb.New(time.Second),
				Probe:   &diceragentv1.ProbeRequest_Tcp{Tcp: &diceragentv1.TCPProbe{Port: 5432}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &fakeAgent{resp: &diceragentv1.ProbeResponse{Healthy: true, Output: "ok"}}

			result, err := Probe(t.Context(), agent, tt.check)
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if !proto.Equal(agent.req, tt.want) {
				t.Errorf("the agent was asked %v, want %v", agent.req, tt.want)
			}
			if !result.Healthy || result.Output != "ok" || result.At.IsZero() {
				t.Errorf("result = %+v, want the agent's healthy answer, timed", result)
			}
		})
	}
}

// TestProbeOfAnAgentThatDoesNotAnswer checks that a probe the agent never
// answered is an unhealthy result that says so, as well as an error.
func TestProbeOfAnAgentThatDoesNotAnswer(t *testing.T) {
	agent := &fakeAgent{err: errors.New("connection refused")}

	result, err := Probe(t.Context(), agent, Check{TCP: &TCPProbe{Port: 80}, Timeout: time.Second})
	if err == nil {
		t.Fatal("Probe succeeded, want the agent's error")
	}
	if result.Healthy || !strings.Contains(result.Output, "did not answer") || result.At.IsZero() {
		t.Errorf("result = %+v, want an unhealthy one saying the agent did not answer", result)
	}
}
