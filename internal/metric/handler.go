// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package metric

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Handler returns an http.Handler that serves the registry's metrics in the
// Prometheus exposition format.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{
		ErrorLog:          scrapeErrorLog{logger: r.logger},
		ErrorHandling:     promhttp.ContinueOnError,
		EnableOpenMetrics: true,
	})
}

// scrapeErrorLog adapts this daemon's logger to the one promhttp expects.
type scrapeErrorLog struct {
	logger *slog.Logger
}

// Println logs an error promhttp met serving a scrape.
func (l scrapeErrorLog) Println(v ...any) {
	l.logger.Error("serving scrape", "error", fmt.Sprint(v...))
}
