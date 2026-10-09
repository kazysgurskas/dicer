// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcserver

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"github.com/konradasb/dicer/internal/metric"
)

// The metrics of the calls the API serves.
var (
	requestsMetric = metric.Description{
		Name:   "dicer_grpc_requests_total",
		Type:   metric.TypeCounter,
		Labels: []string{"method", "code"},
		Help:   "gRPC calls served, by full method and response code.",
		Doc:    "`code` is the gRPC status code, such as `OK` or `NotFound`.",
		Group:  metric.GroupAPI,
	}
	requestDurationMetric = metric.Description{
		Name:   "dicer_grpc_request_duration_seconds",
		Type:   metric.TypeHistogram,
		Labels: []string{"method"},
		Help:   "Time a gRPC call took, from the first byte to the final status.",
		Doc:    "For a stream, such as `dicer logs -f`, it is how long the client stayed.",
		Group:  metric.GroupAPI,
	}
)

// MetricDescriptions returns the descriptions of every metric the API server
// serves, for the reference.
func MetricDescriptions() []metric.Description {
	return []metric.Description{requestsMetric, requestDurationMetric}
}

// metrics are the counters a Server keeps of the calls it serves.
type metrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newMetrics() metrics {
	return metrics{
		requests: metric.NewCounterVec(requestsMetric),
		// Most calls are a file read and a reply. Starting an instance and
		// pulling an image are the long tail, and a log stream runs for as
		// long as the client stays.
		duration: metric.NewHistogramVec(requestDurationMetric, prometheus.ExponentialBuckets(0.001, 4, 9)),
	}
}

// unaryMetricsInterceptor times unary calls and counts them by method and
// response code.
func (s *Server) unaryMetricsInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		started := time.Now()
		resp, err := handler(ctx, req)
		s.recordCall(info.FullMethod, err, time.Since(started))

		return resp, err
	}
}

// streamMetricsInterceptor does the same for streaming calls. Their duration
// is the lifetime of the stream, which for a log follow is however long the
// client stayed -- worth knowing, but not a latency.
func (s *Server) streamMetricsInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler,
	) error {
		started := time.Now()
		err := handler(srv, ss)
		s.recordCall(info.FullMethod, err, time.Since(started))

		return err
	}
}

// recordCall records one finished call. The code label comes from the gRPC
// status, so a handler returning a plain error is counted as Unknown -- which
// is what the client sees.
func (s *Server) recordCall(method string, err error, d time.Duration) {
	s.metrics.requests.WithLabelValues(method, status.Code(err).String()).Inc()
	s.metrics.duration.WithLabelValues(method).Observe(d.Seconds())
}

// Describe implements prometheus.Collector.
func (s *Server) Describe(ch chan<- *prometheus.Desc) {
	s.metrics.requests.Describe(ch)
	s.metrics.duration.Describe(ch)
}

// Collect implements prometheus.Collector.
func (s *Server) Collect(ch chan<- prometheus.Metric) {
	s.metrics.requests.Collect(ch)
	s.metrics.duration.Collect(ch)
}
