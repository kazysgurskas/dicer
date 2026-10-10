// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// execStream is the daemon's end of an exec call.
type execStream = grpc.BidiStreamingServer[dicerdv1.ExecInstanceRequest, dicerdv1.ExecInstanceResponse]

// upperCaser is a guest command that writes its standard input back in
// upper case, says "done" on standard error and exits with the code it is
// given. It records the start message it got.
func upperCaser(code int32, start **dicerdv1.ExecInstanceStart) func(execStream) error {
	return func(stream execStream) error {
		first, err := stream.Recv()
		if err != nil {
			return err
		}
		*start = first.GetStart()

		var input []byte
		for {
			req, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			input = append(input, req.GetStdin()...)
		}

		for _, resp := range []*dicerdv1.ExecInstanceResponse{
			{Payload: &dicerdv1.ExecInstanceResponse_Stdout{Stdout: bytes.ToUpper(input)}},
			{Payload: &dicerdv1.ExecInstanceResponse_Stderr{Stderr: []byte("done")}},
			{Payload: &dicerdv1.ExecInstanceResponse_ExitCode{ExitCode: code}},
		} {
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
		return nil
	}
}

// TestCommandRunsWithInputAndOutput checks that a command gets what the Cmd
// says, its standard input included, and that its output reaches the
// writers given for it.
func TestCommandRunsWithInputAndOutput(t *testing.T) {
	var start *dicerdv1.ExecInstanceStart
	c := connect(t, &fakeDaemon{execInstance: upperCaser(0, &start)})

	var stdout, stderr bytes.Buffer
	cmd := c.Instances.Command("web", "tr", "a-z", "A-Z")
	cmd.Stdin = strings.NewReader("hello")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Dir = "/srv"
	cmd.Env = map[string]string{"DEBUG": "1"}
	cmd.User = "app:staff"
	cmd.Timeout = 1500 * time.Millisecond

	if err := cmd.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stdout.String() != "HELLO" || stderr.String() != "done" {
		t.Errorf("stdout %q and stderr %q, want HELLO and done", stdout.String(), stderr.String())
	}

	switch {
	case start.GetName() != "web":
		t.Errorf("ran in %q, want web", start.GetName())
	case strings.Join(start.GetCommand(), " ") != "tr a-z A-Z":
		t.Errorf("ran %q", start.GetCommand())
	case start.GetCwd() != "/srv" || start.GetEnv()["DEBUG"] != "1":
		t.Errorf("ran in %q with %v", start.GetCwd(), start.GetEnv())
	case start.GetUser() != "app:staff":
		t.Errorf("ran as %q, want app:staff", start.GetUser())
	case start.GetTimeoutSeconds() != 2:
		t.Errorf("a timeout of 1.5s was sent as %ds, want it rounded up to 2s", start.GetTimeoutSeconds())
	}
}

// TestCommandExitingOtherThanZeroIsAnExitError checks that a command's exit
// status is reported as an *ExitError, which Output gives its standard
// error.
func TestCommandExitingOtherThanZeroIsAnExitError(t *testing.T) {
	var start *dicerdv1.ExecInstanceStart
	c := connect(t, &fakeDaemon{execInstance: upperCaser(3, &start)})

	cmd := c.Instances.Command("web")
	cmd.Stdin = strings.NewReader("out")
	out, err := cmd.Output(t.Context())

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Output = %v, want an *ExitError", err)
	}
	if exitErr.Code != 3 || string(exitErr.Stderr) != "done" || string(out) != "OUT" {
		t.Errorf("exit %d with stderr %q and output %q, want 3, done and OUT", exitErr.Code, exitErr.Stderr, out)
	}
}

// TestCommandWithoutStdinReadsEndOfInput checks that a command given no
// standard input is not left waiting for some.
func TestCommandWithoutStdinReadsEndOfInput(t *testing.T) {
	var start *dicerdv1.ExecInstanceStart
	c := connect(t, &fakeDaemon{execInstance: upperCaser(0, &start)})

	out, err := c.Instances.Command("web", "cat").Output(t.Context())
	if err != nil || len(out) != 0 {
		t.Errorf("Output = %q, %v; want nothing", out, err)
	}
}

// TestCommandFailures checks how a command that could not be run to its
// end is reported.
func TestCommandFailures(t *testing.T) {
	tests := []struct {
		name   string
		daemon func(execStream) error
		want   error
	}{
		{
			name:   "instance not found",
			daemon: func(execStream) error { return status.Error(codes.NotFound, "no instance web") },
			want:   ErrNotFound,
		},
		{
			name:   "guest unreachable",
			daemon: func(execStream) error { return status.Error(codes.Unavailable, "the guest agent did not answer") },
			want:   ErrUnavailable,
		},
		{
			name: "no exit status",
			daemon: func(stream execStream) error {
				return stream.Send(&dicerdv1.ExecInstanceResponse{
					Payload: &dicerdv1.ExecInstanceResponse_Stdout{Stdout: []byte("partial")},
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := connect(t, &fakeDaemon{execInstance: tt.daemon})

			err := c.Instances.Command("web", "true").Run(t.Context())
			var exitErr *ExitError
			switch {
			case err == nil, errors.As(err, &exitErr):
				t.Fatalf("Run = %v, want a failure", err)
			case tt.want != nil && !errors.Is(err, tt.want):
				t.Errorf("Run = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestCommandWithANegativeTimeoutIsNotStarted checks that a timeout the
// guest cannot honour is refused before anything is sent.
func TestCommandWithANegativeTimeoutIsNotStarted(t *testing.T) {
	c := connect(t, &fakeDaemon{})

	cmd := c.Instances.Command("web", "true")
	cmd.Timeout = -time.Second
	if err := cmd.Run(t.Context()); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("Run = %v, want ErrInvalidArgument", err)
	}
}
