// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/konradasb/dicer/internal/errdefs"
)

// agentError passes a guest agent's status through, explaining an
// unimplemented call as an agent that needs the instance restarted to be
// updated.
func agentError(name string, err error) error {
	if status.Code(err) == codes.Unimplemented {
		return errdefs.InvalidState(
			"instance %q runs a guest agent that does not match this daemon; restart the instance to update it", name)
	}

	return err
}
