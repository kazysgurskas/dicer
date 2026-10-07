// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// timeFromProto returns the time t holds, or the zero time for nil.
func timeFromProto(t *timestamppb.Timestamp) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.AsTime()
}

// timeToProto returns t as the API holds it, or nil for the zero time.
func timeToProto(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// durationFromProto returns the duration d holds, or zero for nil.
func durationFromProto(d *durationpb.Duration) time.Duration {
	if d == nil {
		return 0
	}
	return d.AsDuration()
}

// durationToProto returns d as the API holds it, or nil for zero, which
// the API reads as unset.
func durationToProto(d time.Duration) *durationpb.Duration {
	if d == 0 {
		return nil
	}
	return durationpb.New(d)
}

// convertAll converts each element of in with convert.
func convertAll[In, Out any](in []In, convert func(In) Out) []Out {
	if len(in) == 0 {
		return nil
	}
	out := make([]Out, len(in))
	for i, v := range in {
		out[i] = convert(v)
	}
	return out
}

// Ptr returns a pointer to v, for the fields of an InstanceUpdate:
//
//	update := dicer.InstanceUpdate{VCPUs: dicer.Ptr(4)}
func Ptr[T any](v T) *T {
	return &v
}
