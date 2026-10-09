// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

func TestResizeSendsOnlyWhatChanged(t *testing.T) {
	d := newFakeInstanceDaemon(fakeInstances()...)
	serveFakeDaemon(t, d)

	out, err := run(t, "resize", "web", "--memory", "2GiB")
	if err != nil {
		t.Fatalf("resize: %v\n%s", err, out)
	}
	memory := int64(2 << 30)
	if want := (&dicerdv1.ResizeInstanceRequest{Name: "web", MemoryBytes: &memory}); !proto.Equal(d.resized, want) {
		t.Errorf("request = %v\nwant      %v", d.resized, want)
	}
	if !strings.Contains(out, "Instance web resized to") || !strings.Contains(out, "2 GiB memory") {
		t.Errorf("output = %q, want the new sizes", out)
	}

	if _, err := run(t, "resize", "web"); err == nil || !strings.Contains(err.Error(), "needs --vcpus, --memory or both") {
		t.Errorf("a resize with nothing to change should say so, got %v", err)
	}
}
