// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcserver

import (
	"fmt"

	"google.golang.org/grpc"

	"github.com/konradasb/dicer/internal/instance"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// WaitInstance waits for an instance to stop, and sends how it ended.
func (h *instanceHandler) WaitInstance(
	req *dicerdv1.WaitInstanceRequest, stream grpc.ServerStreamingServer[dicerdv1.WaitInstanceResponse],
) error {
	w, err := h.instanceManager.Waiter(req.GetName(), instance.WaitOptions{ID: req.GetId(), NextStop: req.GetNextStop()})
	if err != nil {
		return err
	}
	defer w.Close()

	// Tells a caller about to start the instance that it can.
	if err := stream.SendHeader(nil); err != nil {
		return fmt.Errorf("send headers: %w", err)
	}

	status, err := w.Wait(stream.Context())
	if err != nil {
		return err
	}

	resp := &dicerdv1.WaitInstanceResponse{
		State:      instanceStates.toProto(status.State),
		StateError: status.StateError,
	}
	if status.ExitCode != nil {
		resp.ExitCode = new(int32(*status.ExitCode))
	}
	return stream.Send(resp)
}
