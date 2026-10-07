// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"errors"
	"io"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// LogOptions pick the log Instances.Logs reads, and how much of it.
type LogOptions struct {
	// Source is the log to read. Empty means the guest's console.
	Source LogSource

	// TailLines limits the output to the last lines. Zero means all of it.
	TailLines int

	// Follow keeps the log open, reading new output until the instance
	// stops.
	Follow bool
}

// LogSource is one of the logs an instance produces. Both are kept with the
// instance, across stops and restarts, until it is deleted.
type LogSource string

// The log sources.
const (
	// LogSourceGuest is the guest's serial console: the kernel's boot
	// messages, dicer-init's, and whatever the workload writes to the
	// console.
	LogSourceGuest LogSource = "guest"

	// LogSourceHypervisor is the hypervisor's own log, which explains a
	// guest that crashed or never booted.
	LogSourceHypervisor LogSource = "hypervisor"
)

var logSources = enum[LogSource, dicerdv1.LogSource]{"log source", map[LogSource]dicerdv1.LogSource{
	LogSourceGuest:      dicerdv1.LogSource_LOG_SOURCE_GUEST,
	LogSourceHypervisor: dicerdv1.LogSource_LOG_SOURCE_HYPERVISOR,
}}

// Logs returns a reader of an instance's log. It must be closed when done.
// Reading it returns the error the call failed with, such as ErrNotFound,
// and io.EOF at the end of the log.
func (s *Instances) Logs(ctx context.Context, name string, opts LogOptions) (io.ReadCloser, error) {
	req, err := getInstanceLogsRequest(name, opts)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	stream, err := s.api.GetInstanceLogs(ctx, req)
	if err != nil {
		cancel()
		return nil, fromStatus(err)
	}

	return &streamReader{
		receive: func() ([]byte, error) {
			chunk, err := stream.Recv()
			return chunk.GetData(), err
		},
		cancel: cancel,
	}, nil
}

// getInstanceLogsRequest returns the request for the log of the instance
// name that opts picks.
func getInstanceLogsRequest(name string, opts LogOptions) (*dicerdv1.GetInstanceLogsRequest, error) {
	source, err := logSources.toProto(opts.Source)
	if err != nil {
		return nil, err
	}

	return &dicerdv1.GetInstanceLogsRequest{
		Name:      name,
		Source:    source,
		TailLines: int32(opts.TailLines),
		Follow:    opts.Follow,
	}, nil
}

// streamReader reads the bytes a stream's messages carry as one stream.
type streamReader struct {
	// receive returns the next message's bytes, and io.EOF after the last.
	receive func() ([]byte, error)
	cancel  context.CancelFunc

	buf []byte

	// err is the error the stream ended with, io.EOF if it ended cleanly.
	err error
}

// Read reads the stream's bytes.
func (r *streamReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		chunk, err := r.receive()
		if err != nil {
			r.err = fromStatus(err)
			continue
		}
		r.buf = chunk
	}

	n := copy(p, r.buf)
	r.buf = r.buf[n:]

	return n, nil
}

// failure returns the error the stream failed with, or nil if it has not
// failed.
func (r *streamReader) failure() error {
	if errors.Is(r.err, io.EOF) {
		return nil
	}
	return r.err
}

// Close ends the stream.
func (r *streamReader) Close() error {
	r.cancel()
	return nil
}
