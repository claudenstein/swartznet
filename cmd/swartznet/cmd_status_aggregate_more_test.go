package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestStatusTextAggregateBlockCacheMaxFormat covers
// emitAggregateBlock's `if a.RecordCacheMax > 0 { "%d / %d" }`
// arm. Cache max set → "size / max" formatting.
func TestStatusTextAggregateBlockCacheMaxFormat(t *testing.T) {
	t.Parallel()
	agg := &httpapi.AggregateStatusResponse{
		RecordCacheSize: 5,
		RecordCacheMax:  10,
	}
	var buf bytes.Buffer
	if code := emitStatusText(&buf, baseStatus(), agg); code != exitOK {
		t.Fatal(code)
	}
	out := buf.String()
	if !strings.Contains(out, "record cache:         5 / 10") {
		t.Errorf("expected 'record cache: 5 / 10' in output: %s", out)
	}
}

// TestStatusTextAggregateBlockBootstrapPending covers
// emitAggregateBlock's `if a.Bootstrap.Pending > 0` arm — pending
// > 0 emits the "bootstrap pending" line.
func TestStatusTextAggregateBlockBootstrapPending(t *testing.T) {
	t.Parallel()
	agg := &httpapi.AggregateStatusResponse{
		Bootstrap: &httpapi.AggregateBootstrap{
			Anchors:  4,
			Admitted: 2,
			Pending:  3,
		},
	}
	var buf bytes.Buffer
	if code := emitStatusText(&buf, baseStatus(), agg); code != exitOK {
		t.Fatal(code)
	}
	out := buf.String()
	if !strings.Contains(out, "bootstrap pending:    3") {
		t.Errorf("expected 'bootstrap pending: 3' in output: %s", out)
	}
}
