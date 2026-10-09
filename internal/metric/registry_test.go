// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package metric

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildInfoCarriesTheBuild(t *testing.T) {
	body := scrape(t, New(Config{Version: "v1.2.3", Commit: "abc123"}))

	if !strings.Contains(body, `version="v1.2.3"`) || !strings.Contains(body, `commit="abc123"`) {
		t.Errorf("build info does not carry the build:\n%s", body)
	}
}

// The endpoint serves this daemon's registry alone, so a dependency that
// registers into the default registry cannot appear on it.
func TestHandlerServesOnlyOurRegistry(t *testing.T) {
	body := scrape(t, New(Config{}))

	if !strings.Contains(body, "dicer_build_info") {
		t.Errorf("scrape is missing this daemon's own metrics:\n%s", body)
	}
	if strings.Contains(body, "promhttp_metric_handler") {
		t.Errorf("scrape carries the default registry's metrics:\n%s", body)
	}
}

// TestRegistryServesWhatItDescribes checks the metrics a Registry serves of
// its own are those Descriptions lists, as it lists them.
func TestRegistryServesWhatItDescribes(t *testing.T) {
	r := New(Config{})

	listed := map[string]Description{}
	for _, d := range Descriptions() {
		if err := d.Validate(); err != nil {
			t.Error(err)
		}
		listed[d.Name] = d
	}

	families, err := r.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	served := 0
	for _, f := range families {
		if !strings.HasPrefix(f.GetName(), "dicer_") {
			continue
		}
		served++

		d, ok := listed[f.GetName()]
		switch {
		case !ok:
			t.Errorf("%s is served but not described", f.GetName())
		case Type(strings.ToLower(f.GetType().String())) != d.Type:
			t.Errorf("%s is described as a %s and served as a %s", d.Name, d.Type, f.GetType())
		case f.GetHelp() != d.Help:
			t.Errorf("%s's help is %q, and served as %q", d.Name, d.Help, f.GetHelp())
		}
	}
	if served != len(listed) {
		t.Errorf("%d metrics served, %d described", served, len(listed))
	}
}

// scrape returns what a Prometheus server would read from the endpoint.
func scrape(t *testing.T, r *Registry) string {
	t.Helper()

	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d, want %d", rec.Code, http.StatusOK)
	}

	return rec.Body.String()
}
