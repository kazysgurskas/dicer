// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"errors"
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestFailedCallsMatchTheirKind checks that each status code is reported as
// the error it stands for, with the daemon's message, and that the status
// is still there to read.
func TestFailedCallsMatchTheirKind(t *testing.T) {
	tests := []struct {
		code codes.Code
		want error
	}{
		{codes.NotFound, ErrNotFound},
		{codes.AlreadyExists, ErrAlreadyExists},
		{codes.InvalidArgument, ErrInvalidArgument},
		{codes.FailedPrecondition, ErrFailedPrecondition},
		{codes.ResourceExhausted, ErrResourceExhausted},
		{codes.Unavailable, ErrUnavailable},
		{codes.PermissionDenied, ErrPermissionDenied},
		{codes.Unimplemented, ErrUnimplemented},
		{codes.Canceled, context.Canceled},
		{codes.DeadlineExceeded, context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.code.String(), func(t *testing.T) {
			err := fromStatus(status.Error(tt.code, "the daemon says why"))
			switch {
			case !errors.Is(err, tt.want):
				t.Errorf("%v does not match %v", err, tt.want)
			case err.Error() != "the daemon says why":
				t.Errorf("message %q, want the daemon's", err.Error())
			case status.Code(err) != tt.code:
				t.Errorf("status.Code = %v, want %v", status.Code(err), tt.code)
			}
		})
	}
}

// TestFailuresThatAreNotStatusesAreReturnedAsTheyAre checks that an error
// that is not a gRPC status, such as the end of a stream, is left alone.
func TestFailuresThatAreNotStatusesAreReturnedAsTheyAre(t *testing.T) {
	if err := fromStatus(io.EOF); err != io.EOF { //nolint:errorlint // it must be the same value
		t.Errorf("fromStatus(io.EOF) = %v", err)
	}
	if err := fromStatus(nil); err != nil {
		t.Errorf("fromStatus(nil) = %v", err)
	}
	if err := fromStatus(status.Error(codes.Internal, "")); err.Error() != "Internal" {
		t.Errorf("an internal error without a message reads %q", err.Error())
	}
}
