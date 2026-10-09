// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/konradasb/dicer/internal/metric/metrictest"
)

func TestUnaryMetricsInterceptorCountsByCode(t *testing.T) {
	const method = "/dicerd.v1.DaemonService/StartInstance"

	tests := []struct {
		name     string
		handler  grpc.UnaryHandler
		wantCode string
	}{
		{
			name:     "success",
			handler:  func(context.Context, any) (any, error) { return "ok", nil },
			wantCode: codes.OK.String(),
		},
		{
			name: "status error keeps its code",
			handler: func(context.Context, any) (any, error) {
				return nil, status.Error(codes.NotFound, "no such instance")
			},
			wantCode: codes.NotFound.String(),
		},
		{
			// A handler returning a plain error is Unknown to the client,
			// so that is what the metric must say too.
			name: "plain error is unknown",
			handler: func(context.Context, any) (any, error) {
				return nil, errors.New("boom")
			},
			wantCode: codes.Unknown.String(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewServer(Config{})
			interceptor := s.UnaryMetricsInterceptor()

			_, _ = interceptor(
				context.Background(), nil,
				&grpc.UnaryServerInfo{FullMethod: method},
				tt.handler,
			)

			if got := testutil.ToFloat64(s.metrics.requests.WithLabelValues(method, tt.wantCode)); got != 1 {
				t.Errorf("requests{method=%q, code=%q} = %v, want 1", method, tt.wantCode, got)
			}
			if got := testutil.CollectAndCount(s.metrics.duration); got != 1 {
				t.Errorf("duration series = %d, want 1", got)
			}
		})
	}
}

func TestUnaryMetricsInterceptorPassesTheResponseThrough(t *testing.T) {
	s := NewServer(Config{})

	wantErr := status.Error(codes.InvalidArgument, "bad name")
	resp, err := s.UnaryMetricsInterceptor()(
		context.Background(), "request",
		&grpc.UnaryServerInfo{FullMethod: "/svc/Method"},
		func(context.Context, any) (any, error) { return "response", wantErr },
	)

	if resp != "response" {
		t.Errorf("response = %v, want %q", resp, "response")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want %v", err, wantErr)
	}
}

func TestStreamMetricsInterceptorCountsCalls(t *testing.T) {
	const method = "/dicerd.v1.DaemonService/GetInstanceLogs"

	s := NewServer(Config{})

	err := s.StreamMetricsInterceptor()(
		nil, nil,
		&grpc.StreamServerInfo{FullMethod: method},
		func(any, grpc.ServerStream) error { return status.Error(codes.Canceled, "client left") },
	)
	if status.Code(err) != codes.Canceled {
		t.Fatalf("error = %v, want a Canceled status", err)
	}

	if got := testutil.ToFloat64(s.metrics.requests.WithLabelValues(method, codes.Canceled.String())); got != 1 {
		t.Errorf("requests{method=%q, code=Canceled} = %v, want 1", method, got)
	}
}

func TestMetricsMatchTheirDescriptions(t *testing.T) {
	s := NewServer(Config{})
	s.recordCall("/dicerd.v1.InstanceService/Start", nil, time.Second)

	metrictest.CheckDescriptions(t, s, MetricDescriptions())
}
