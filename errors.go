// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The kinds of failure a call can end with. Match them with errors.Is:
//
//	if errors.Is(err, dicer.ErrNotFound) {
//		...
//	}
//
// The error's message is the daemon's, written for a person to read.
var (
	// ErrNotFound means the resource named does not exist.
	ErrNotFound = errors.New("not found")

	// ErrAlreadyExists means a resource with that name exists already.
	ErrAlreadyExists = errors.New("already exists")

	// ErrInvalidArgument means the request is malformed, whatever the host's
	// state.
	ErrInvalidArgument = errors.New("invalid argument")

	// ErrFailedPrecondition means the resource is in the wrong state for the
	// call, such as deleting a running instance.
	ErrFailedPrecondition = errors.New("failed precondition")

	// ErrResourceExhausted means the host has too little left: CPU or memory
	// for an instance, or addresses on a network.
	ErrResourceExhausted = errors.New("resource exhausted")

	// ErrUnavailable means something the call needs cannot be reached: the
	// daemon, a registry or a guest's agent. Trying again may work.
	ErrUnavailable = errors.New("unavailable")

	// ErrPermissionDenied means the call was refused: the token's scopes do
	// not allow it, or the guest refused, such as a file it protects.
	ErrPermissionDenied = errors.New("permission denied")

	// ErrUnimplemented means the daemon, or an instance's guest agent, is too
	// old for the call.
	ErrUnimplemented = errors.New("unimplemented")

	// ErrUnauthenticated means the daemon's TCP listener refused the call's
	// token: it was deleted or rotated, or was never this daemon's.
	ErrUnauthenticated = errors.New("unauthenticated")
)

// statusErrors pairs each status code with the error it is reported as.
var statusErrors = map[codes.Code]error{
	codes.NotFound:           ErrNotFound,
	codes.AlreadyExists:      ErrAlreadyExists,
	codes.InvalidArgument:    ErrInvalidArgument,
	codes.FailedPrecondition: ErrFailedPrecondition,
	codes.ResourceExhausted:  ErrResourceExhausted,
	codes.Unavailable:        ErrUnavailable,
	codes.PermissionDenied:   ErrPermissionDenied,
	codes.Unimplemented:      ErrUnimplemented,
	codes.Unauthenticated:    ErrUnauthenticated,
	codes.Canceled:           context.Canceled,
	codes.DeadlineExceeded:   context.DeadlineExceeded,
}

// statusError is a failed call: the daemon's message, matching the error its
// status code stands for.
type statusError struct {
	status *status.Status
}

// fromStatus returns err as the client reports it: a gRPC status becomes a
// statusError, and anything else, such as io.EOF, is returned as it is.
func fromStatus(err error) error {
	if err == nil {
		return nil
	}
	s, ok := status.FromError(err)
	if !ok {
		return err
	}

	return &statusError{status: s}
}

// Error returns the daemon's message.
func (e *statusError) Error() string {
	if msg := e.status.Message(); msg != "" {
		return msg
	}
	return e.status.Code().String()
}

// Unwrap returns the error the status code stands for, such as ErrNotFound,
// or nil for a code with none.
func (e *statusError) Unwrap() error {
	return statusErrors[e.status.Code()]
}

// GRPCStatus returns the status the call ended with, so that status.Code
// still reads it.
func (e *statusError) GRPCStatus() *status.Status {
	return e.status
}
