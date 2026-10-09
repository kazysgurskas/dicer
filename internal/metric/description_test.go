// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package metric

import "testing"

func TestDescriptionValidate(t *testing.T) {
	valid := Description{Name: "dicer_things", Type: TypeGauge, Help: "Things.", Group: GroupDaemon}

	tests := []struct {
		name   string
		modify func(*Description)
		valid  bool
	}{
		{"a gauge", func(*Description) {}, true},
		{"a counter", func(d *Description) { d.Name, d.Type = "dicer_things_total", TypeCounter }, true},
		{"a histogram", func(d *Description) { d.Name, d.Type = "dicer_thing_seconds", TypeHistogram }, true},
		{"no prefix", func(d *Description) { d.Name = "things" }, false},
		{"upper case", func(d *Description) { d.Name = "dicer_Things" }, false},
		{"unknown type", func(d *Description) { d.Type = "summary" }, false},
		{"no help", func(d *Description) { d.Help = "" }, false},
		{"no group", func(d *Description) { d.Group = "" }, false},
		{"a counter without _total", func(d *Description) { d.Type = TypeCounter }, false},
		{"a gauge with _total", func(d *Description) { d.Name = "dicer_things_total" }, false},
		{"a histogram's suffix", func(d *Description) { d.Name = "dicer_things_count" }, false},
		{"the instance label", func(d *Description) { d.Labels = []string{"instance"} }, false},
		{"the job label", func(d *Description) { d.Labels = []string{"job"} }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := valid
			tt.modify(&d)

			err := d.Validate()
			switch {
			case tt.valid && err != nil:
				t.Errorf("Validate() = %v, want nil", err)
			case !tt.valid && err == nil:
				t.Errorf("Validate() = nil, want an error for %+v", d)
			}
		})
	}
}
