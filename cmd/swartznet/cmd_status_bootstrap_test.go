package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

func TestStatusTextRendersBootstrapBlock(t *testing.T) {
	agg := &httpapi.AggregateStatusResponse{
		Bootstrap: &httpapi.AggregateBootstrap{Anchors: 5, Admitted: 12},
	}
	var buf bytes.Buffer
	if code := emitStatusText(&buf, baseStatus(), agg); code != exitOK {
		t.Fatal(code)
	}
	out := buf.String()
	for _, want := range []string{
		"bootstrap anchors:    5",
		"bootstrap admitted:   12",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
}

// When Bootstrap is nil the two lines are omitted.
func TestStatusTextOmitsBootstrapWhenNil(t *testing.T) {
	agg := &httpapi.AggregateStatusResponse{} // Bootstrap nil
	var buf bytes.Buffer
	if code := emitStatusText(&buf, baseStatus(), agg); code != exitOK {
		t.Fatal(code)
	}
	out := buf.String()
	if strings.Contains(out, "bootstrap") {
		t.Errorf("bootstrap lines should be omitted when probe is nil: %s", out)
	}
}
