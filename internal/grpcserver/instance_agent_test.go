// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcserver

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/konradasb/dicer/internal/errdefs"
	diceragentv1 "github.com/konradasb/dicer/proto/diceragent/v1"
)

// An agent without a method was installed by a different daemon build when
// the instance booted. The caller is told how to fix that rather than that
// the method is unimplemented.
func TestAgentErrorExplainsMissingMethod(t *testing.T) {
	err := agentError("web", status.Error(codes.Unimplemented, "unknown method CopyIn"))

	wantClass(t, err, errdefs.ErrInvalidState)
	if !strings.Contains(err.Error(), "restart the instance") {
		t.Errorf("message = %q, want it to say how to update the agent", err)
	}
}

// Anything else the agent says reaches the client as it said it.
func TestAgentErrorPassesThrough(t *testing.T) {
	sent := toStatus(agentError("web", status.Error(codes.NotFound, "/srv/x: no such file or directory")))

	if s := status.Convert(sent); s.Code() != codes.NotFound || s.Message() != "/srv/x: no such file or directory" {
		t.Errorf("sent %v, want the agent's status unchanged", s)
	}
}

// infoAgent is a guest agent that answers Info alone.
type infoAgent struct {
	diceragentv1.AgentServiceClient

	resp *diceragentv1.InfoResponse
	err  error
}

func (a infoAgent) Info(context.Context, *diceragentv1.InfoRequest, ...grpc.CallOption) (*diceragentv1.InfoResponse, error) {
	return a.resp, a.err
}

// An agent supports what its Info lists, and one too old to have Info
// supports nothing, rather than failing the call.
func TestAgentSupportsWhatItsInfoLists(t *testing.T) {
	const feature = diceragentv1.AgentFeature_AGENT_FEATURE_EXEC_USER

	tests := []struct {
		name  string
		agent infoAgent
		want  bool
	}{
		{"listed", infoAgent{resp: &diceragentv1.InfoResponse{Features: []diceragentv1.AgentFeature{feature}}}, true},
		{"not listed", infoAgent{resp: &diceragentv1.InfoResponse{}}, false},
		{"no Info", infoAgent{err: status.Error(codes.Unimplemented, "unknown method Info")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := agentSupports(t.Context(), tt.agent, feature)
			if err != nil || got != tt.want {
				t.Errorf("agentSupports = %v, %v; want %v", got, err, tt.want)
			}
		})
	}

	_, err := agentSupports(t.Context(), infoAgent{err: status.Error(codes.Unavailable, "connection reset")}, feature)
	wantClass(t, err, errdefs.ErrUnavailable)
}
