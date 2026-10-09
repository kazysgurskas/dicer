// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/konradasb/dicer/internal/metric"
	"github.com/konradasb/dicer/internal/version"
)

const (
	// metricsPath is where the Prometheus endpoint is served.
	metricsPath = "/metrics"

	// metricsShutdownTimeout bounds how long a shutdown waits for an
	// in-flight scrape.
	metricsShutdownTimeout = 2 * time.Second

	// metricsReadHeaderTimeout bounds how long a scraper may take to send
	// its request headers.
	metricsReadHeaderTimeout = 5 * time.Second
)

// newMetrics builds the registry the daemon serves its metrics from, whether
// or not the endpoint is served. The managers' metrics are registered on it
// once they exist: see registerMetrics.
func (d *daemon) newMetrics() *metric.Registry {
	return metric.New(metric.Config{
		Version: version.Version,
		Commit:  version.Commit,
		Logger:  d.logger.With("component", "metrics"),
	})
}

// registerMetrics registers each manager's metrics, and the DNS servers' if
// there are any. It is called once the managers exist. The API server's are
// registered in listenAPI, which makes it.
func (d *daemon) registerMetrics() {
	d.metrics.Register(d.networkManager)
	d.metrics.Register(d.instanceManager)
	d.metrics.Register(d.imageManager)
	d.metrics.Register(d.kernelManager)
	d.metrics.Register(d.volumeManager)
	if d.dnsServers != nil {
		d.metrics.Register(d.dnsServers)
	}
}

// serveMetrics serves the metrics endpoint until ctx is cancelled. It returns
// at once if the endpoint is disabled.
func (d *daemon) serveMetrics(ctx context.Context) error {
	cfg := d.cfg.Metrics
	if !cfg.Enable {
		return nil
	}

	logger := d.logger.With("component", "metrics")

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}

	mux := http.NewServeMux()
	mux.Handle(metricsPath, d.metrics.Handler())

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: metricsReadHeaderTimeout,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("serving metrics", "listen", cfg.Listen, "path", metricsPath)

		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("serve metrics: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	// ctx is already done, so the drain needs its own deadline.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), metricsShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("metrics server did not shut down cleanly", "error", err)
	}

	return <-serveErr
}
