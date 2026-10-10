// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// Cmd is a command to run in an instance, as an exec.Cmd is one to run on
// this machine. Make one with Instances.Command, set its fields, and call
// Run, Output or Start. A Cmd cannot be reused once started.
//
// The command needs the instance to be running. If its guest is still
// booting, the command waits up to 30 seconds for its agent to answer
// before failing with ErrUnavailable.
type Cmd struct {
	// Instance is the instance to run the command in, by name or ID.
	Instance string

	// Args are the command and its arguments. Empty runs /bin/sh.
	Args []string

	// Env is added to the command's environment.
	Env map[string]string

	// Dir is the working directory inside the guest. Empty leaves the
	// guest's default.
	Dir string

	// User is who the command runs as: user, uid, user:group or uid:gid, as
	// the guest's /etc/passwd and /etc/group define them. The command gets
	// the user's groups, and its home directory as HOME unless Env sets it.
	// Empty is the user the workload runs as, which is root in the systemd
	// init mode. An unknown user fails with ErrInvalidArgument. A guest
	// whose agent is too old to switch users fails with
	// ErrFailedPrecondition until the instance is restarted.
	User string

	// Timeout kills the command if it runs for longer, and it then exits
	// with status 124. It is counted in whole seconds, rounded up. Zero is
	// no limit.
	Timeout time.Duration

	// TTY runs the command on a pseudo-terminal. Its standard error then
	// arrives on Stdout.
	TTY bool

	// Rows is the pseudo-terminal's height. Zero leaves the guest's default.
	Rows int

	// Columns is the pseudo-terminal's width. Zero leaves the guest's
	// default.
	Columns int

	// Stdin is the command's standard input. Nil gives it none: it reads
	// end of file at once. The end of Stdin, or an error reading it, ends
	// the command's input. Wait does not wait for Stdin to be read: a read
	// that blocks after the command has exited is left to finish on its
	// own.
	Stdin io.Reader

	// Stdout receives the command's standard output. Nil discards it.
	Stdout io.Writer

	// Stderr receives the command's standard error. Nil discards it, except
	// that Output collects it into the *ExitError it returns.
	Stderr io.Writer

	api    dicerdv1.DaemonServiceClient
	stream grpc.BidiStreamingClient[dicerdv1.ExecInstanceRequest, dicerdv1.ExecInstanceResponse]

	// mu serialises the stream's sends, which gRPC forbids from running
	// concurrently.
	mu sync.Mutex

	// done is closed once the command has exited, or the stream failed, and
	// err is set.
	done chan struct{}
	err  error
}

// ExitError reports a command that exited with a status other than 0.
type ExitError struct {
	// Code is the command's exit status.
	Code int

	// Stderr is the command's standard error, if Output collected it.
	Stderr []byte
}

// Error returns the command's exit status.
func (e *ExitError) Error() string {
	return "exit status " + strconv.Itoa(e.Code)
}

// Command returns a Cmd that runs args in an instance: the command and its
// arguments, or /bin/sh if there are none.
//
//	out, err := c.Instances.Command("web", "cat", "/etc/os-release").Output(ctx)
func (s *Instances) Command(name string, args ...string) *Cmd {
	return &Cmd{Instance: name, Args: args, api: s.api}
}

// Run starts the command and waits for it to exit. It returns nil if the
// command exited 0, an *ExitError if it exited otherwise, and the error the
// call failed with if it could not run the command to its end.
func (c *Cmd) Run(ctx context.Context) error {
	if err := c.Start(ctx); err != nil {
		return err
	}
	return c.Wait()
}

// Output runs the command and returns its standard output. Unless Stderr is
// set, the command's standard error is collected into the *ExitError
// returned for a command that did not exit 0.
func (c *Cmd) Output(ctx context.Context) ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("output: Stdout is already set")
	}

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	collectStderr := c.Stderr == nil
	if collectStderr {
		c.Stderr = &stderr
	}

	err := c.Run(ctx)
	var exitErr *ExitError
	if errors.As(err, &exitErr) && collectStderr {
		exitErr.Stderr = stderr.Bytes()
	}

	return stdout.Bytes(), err
}

// Start starts the command, without waiting for it. Wait waits for it to
// exit. Cancelling ctx kills the command.
func (c *Cmd) Start(ctx context.Context) error {
	if c.done != nil {
		return errors.New("start: the command has already been started")
	}
	start, err := execInstanceStart(c)
	if err != nil {
		return err
	}

	stream, err := c.api.ExecInstance(ctx)
	if err != nil {
		return fromStatus(err)
	}
	c.stream = stream
	c.done = make(chan struct{})

	// If the daemon has already ended the call, receiving says why.
	err = c.send(&dicerdv1.ExecInstanceRequest{Payload: &dicerdv1.ExecInstanceRequest_Start{Start: start}})
	if err != nil && !errors.Is(err, io.EOF) {
		return fromStatus(err)
	}

	if c.Stdin == nil {
		c.closeStdin()
	} else {
		go c.sendStdin()
	}
	go c.receiveOutput()

	return nil
}

// Wait waits for a started command to exit, and returns what Run does.
func (c *Cmd) Wait() error {
	if c.done == nil {
		return errors.New("wait: the command has not been started")
	}
	<-c.done
	return c.err
}

// Resize tells a command on a pseudo-terminal that the terminal is now rows
// by columns in size.
func (c *Cmd) Resize(rows, columns int) error {
	if c.done == nil {
		return errors.New("resize: the command has not been started")
	}
	return fromStatus(c.send(&dicerdv1.ExecInstanceRequest{
		Payload: &dicerdv1.ExecInstanceRequest_Resize{
			Resize: &dicerdv1.ExecInstanceResize{Rows: uint32(rows), Cols: uint32(columns)},
		},
	}))
}

// execInstanceStart returns the message that starts the command c
// describes.
func execInstanceStart(c *Cmd) (*dicerdv1.ExecInstanceStart, error) {
	if c.Timeout < 0 {
		return nil, fmt.Errorf("%w: the timeout %s is negative", ErrInvalidArgument, c.Timeout)
	}
	// A part of a second is rounded up, so that a short timeout never
	// becomes no limit at all.
	seconds := (c.Timeout + time.Second - 1) / time.Second
	if seconds > math.MaxInt32 {
		return nil, fmt.Errorf("%w: the timeout %s is too long", ErrInvalidArgument, c.Timeout)
	}

	return &dicerdv1.ExecInstanceStart{
		Name:           c.Instance,
		Command:        c.Args,
		Tty:            c.TTY,
		Cwd:            c.Dir,
		TimeoutSeconds: int32(seconds),
		Rows:           uint32(c.Rows),
		Cols:           uint32(c.Columns),
		Env:            c.Env,
		User:           c.User,
	}, nil
}

// sendStdin sends Stdin to the command until it ends, or the command does.
func (c *Cmd) sendStdin() {
	buf := make([]byte, 32<<10)
	for {
		n, err := c.Stdin.Read(buf)
		if n > 0 {
			sendErr := c.send(&dicerdv1.ExecInstanceRequest{
				Payload: &dicerdv1.ExecInstanceRequest_Stdin{Stdin: bytes.Clone(buf[:n])},
			})
			if sendErr != nil {
				return
			}
		}
		if err != nil {
			c.closeStdin()
			return
		}
	}
}

// closeStdin tells the command there is no more input.
func (c *Cmd) closeStdin() {
	c.mu.Lock()
	defer c.mu.Unlock()

	_ = c.stream.CloseSend()
}

// send sends one message, serialised against the others.
func (c *Cmd) send(req *dicerdv1.ExecInstanceRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.stream.Send(req)
}

// receiveOutput writes the command's output to Stdout and Stderr until it
// exits, and records what Wait returns.
func (c *Cmd) receiveOutput() {
	defer close(c.done)

	var (
		code     *int
		writeErr error
	)
	write := func(w io.Writer, p []byte) {
		if w == nil || writeErr != nil {
			return
		}
		_, writeErr = w.Write(p)
	}

	for {
		resp, err := c.stream.Recv()
		switch {
		case errors.Is(err, io.EOF) && code == nil:
			c.err = errors.New("the command's stream ended without an exit status")
			return
		case errors.Is(err, io.EOF) && *code != 0:
			c.err = &ExitError{Code: *code}
			return
		case errors.Is(err, io.EOF):
			c.err = writeErr
			return
		case err != nil:
			c.err = fromStatus(err)
			return
		}

		switch p := resp.GetPayload().(type) {
		case *dicerdv1.ExecInstanceResponse_Stdout:
			write(c.Stdout, p.Stdout)
		case *dicerdv1.ExecInstanceResponse_Stderr:
			write(c.Stderr, p.Stderr)
		case *dicerdv1.ExecInstanceResponse_ExitCode:
			code = new(int(p.ExitCode))
		}
	}
}
