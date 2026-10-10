// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package printer

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// fakeRows is a Printable of two instances.
type fakeRows struct{}

func (fakeRows) Columns() []string { return []string{"Name", "State", "IP"} }

func (fakeRows) Rows() []map[string]any {
	return []map[string]any{
		{"Name": "web", "State": "Running", "IP": "10.0.0.5"},
		{"Name": "db", "State": "Stopped", "IP": "-"},
	}
}

func (fakeRows) Records() any {
	return []fakeRecord{
		{Name: "web", State: "running", IP: "10.0.0.5", MemoryBytes: 1 << 30},
		{Name: "db", State: "stopped", MemoryBytes: 1 << 30},
	}
}

// fakeRecord is what a row of fakeRows is made from.
type fakeRecord struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	IP          string `json:"ip,omitempty"`
	MemoryBytes int64  `json:"memory_bytes"`
}

func TestPrintTable(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(fakeRows{}, &buf, Options{Format: "table"}); err != nil {
		t.Fatalf("Print: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"web", "db", "Running", "10.0.0.5"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output is missing %q:\n%s", want, out)
		}
	}
}

// TestPrintWritesRecordsAsJSONAndYAML checks that JSON and YAML show the
// records rows are made from, with their raw values and JSON names, and
// whole numbers whole.
func TestPrintWritesRecordsAsJSONAndYAML(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Print(fakeRows{}, &buf, Options{Format: format}); err != nil {
				t.Fatalf("Print: %v", err)
			}

			var records []map[string]any
			if err := yaml.Unmarshal(buf.Bytes(), &records); err != nil {
				t.Fatalf("output is not valid %s: %v\n%s", format, err, buf.String())
			}
			if len(records) != 2 || records[0]["name"] != "web" || records[0]["memory_bytes"] != 1<<30 {
				t.Errorf("records = %v, want the two records, as their JSON tags name them", records)
			}
			if _, ok := records[1]["ip"]; ok {
				t.Errorf("records[1] = %v, want its empty IP left out", records[1])
			}
			if strings.Contains(buf.String(), "e+09") {
				t.Errorf("output writes a whole number in floating point:\n%s", buf.String())
			}
		})
	}
}

// TestPrintRefusesColumnsOfRecords checks that columns, which are a
// table's, cannot be picked from whole records.
func TestPrintRefusesColumnsOfRecords(t *testing.T) {
	err := Print(fakeRows{}, &bytes.Buffer{}, Options{Format: "json", Columns: []string{"Name"}})
	if !errors.Is(err, ErrColumnsOfRecords) {
		t.Errorf("Print = %v, want ErrColumnsOfRecords", err)
	}
}

func TestPrintShowsOnlySelectedColumns(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(fakeRows{}, &buf, Options{Format: "table", Columns: []string{"Name"}}); err != nil {
		t.Fatalf("Print: %v", err)
	}

	if out := buf.String(); !strings.Contains(out, "web") || strings.Contains(out, "STATE") {
		t.Errorf("table = %q, want the Name column alone", out)
	}
}

func TestPrintRejectsUnknownColumn(t *testing.T) {
	var buf bytes.Buffer
	err := Print(fakeRows{}, &buf, Options{Format: "table", Columns: []string{"Nope"}})
	if err == nil {
		t.Fatal("expected an error for an unknown column")
	}
	// The message should tell the user what they can pick instead.
	for _, want := range []string{"Nope", "Name", "State", "IP"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestPrintRejectsUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(fakeRows{}, &buf, Options{Format: "xml"}); err == nil {
		t.Error("expected an error for an unsupported format")
	}
}

func TestPrintAcceptsFormatAliases(t *testing.T) {
	for _, format := range []string{"table", "text", "TABLE", "JSON", "yaml", "yml"} {
		var buf bytes.Buffer
		if err := Print(fakeRows{}, &buf, Options{Format: format}); err != nil {
			t.Errorf("format %q should be accepted: %v", format, err)
		}
	}
}

func TestPrintTemplateRunsOncePerRow(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(fakeRows{}, &buf, Options{Format: "{{.Name}}={{.State | lower}}"}); err != nil {
		t.Fatalf("Print: %v", err)
	}

	if got, want := buf.String(), "web=running\ndb=stopped\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestPrintRejectsMalformedTemplate(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(fakeRows{}, &buf, Options{Format: "{{.Name"}); err == nil {
		t.Error("expected an error for a malformed template")
	}
}

func TestPrintMatchesColumnsIgnoringCase(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(fakeRows{}, &buf, Options{Format: "table", Columns: []string{"name", "ip"}}); err != nil {
		t.Fatalf("Print: %v", err)
	}

	// The headers are spelled as the columns are, not as they were asked
	// for.
	if out := buf.String(); !strings.HasPrefix(out, "NAME") || !strings.Contains(out, "10.0.0.5") {
		t.Errorf("table = %q", out)
	}
}

func TestPrintTemplateAlignsTabs(t *testing.T) {
	var buf bytes.Buffer
	// A literal backslash-t, as a shell passes it through single quotes.
	if err := Print(fakeRows{}, &buf, Options{Format: `{{.Name}}\t{{.IP}}`}); err != nil {
		t.Fatalf("Print: %v", err)
	}

	if got, want := buf.String(), "web  10.0.0.5\ndb   -\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestPrintStructuredWritesJSONOrYAML(t *testing.T) {
	v := map[string]any{"host": map[string]any{"name": "compute-1"}}

	for _, format := range []string{"json", "yaml"} {
		var buf bytes.Buffer
		if err := PrintStructured(v, &buf, format); err != nil {
			t.Fatalf("PrintStructured(%s): %v", format, err)
		}

		var got map[string]map[string]string
		if err := yaml.Unmarshal(buf.Bytes(), &got); err != nil || got["host"]["name"] != "compute-1" {
			t.Errorf("PrintStructured(%s) wrote %q (%v)", format, buf.String(), err)
		}
	}

	for _, format := range []string{"table", "{{.Name}}", "xml"} {
		if err := PrintStructured(v, &bytes.Buffer{}, format); err == nil {
			t.Errorf("PrintStructured(%s) succeeded, want only JSON and YAML", format)
		}
	}
}

func TestPrintStructuredWritesNoListAsAnEmptyOne(t *testing.T) {
	var none []fakeRecord
	for _, format := range []string{"json", "yaml"} {
		var buf bytes.Buffer
		if err := PrintStructured(none, &buf, format); err != nil {
			t.Fatalf("PrintStructured(%s): %v", format, err)
		}
		if got := strings.TrimSpace(buf.String()); got != "[]" {
			t.Errorf("PrintStructured(%s) wrote %q, want []", format, got)
		}
	}
}

func TestFormatPredicates(t *testing.T) {
	for _, tt := range []struct {
		format            string
		table, structured bool
	}{
		{"table", true, false},
		{"TEXT", true, false},
		{"json", false, true},
		{"yml", false, true},
		{"{{.Name}}", false, false},
		{"xml", false, false},
	} {
		if got := IsTable(tt.format); got != tt.table {
			t.Errorf("IsTable(%q) = %v, want %v", tt.format, got, tt.table)
		}
		if got := IsStructured(tt.format); got != tt.structured {
			t.Errorf("IsStructured(%q) = %v, want %v", tt.format, got, tt.structured)
		}
	}
}
