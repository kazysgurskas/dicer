// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package agent

import (
	"context"

	diceragentv1 "github.com/konradasb/dicer/proto/diceragent/v1"
)

// Info implements diceragentv1.AgentServiceServer: it says what this agent
// supports.
func (s *server) Info(context.Context, *diceragentv1.InfoRequest) (*diceragentv1.InfoResponse, error) {
	return &diceragentv1.InfoResponse{
		Features: []diceragentv1.AgentFeature{diceragentv1.AgentFeature_AGENT_FEATURE_EXEC_USER},
	}, nil
}
