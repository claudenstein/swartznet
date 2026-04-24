package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// Minimal base status response — the Aggregate-specific tests
// don't care about the upper blocks, they just need something
// that renders without errors.
func baseStatus() *httpapi.StatusResponse {
	return &httpapi.StatusResponse{
		Local: httpapi.LocalStatus{Indexed: true, DocCount: 0},
	}
}

func TestStatusTextOmitsAggregateWhenNil(t *testing.T) {
	var buf bytes.Buffer
	if code := emitStatusText(&buf, baseStatus(), nil); code != exitOK {
		t.Fatalf("emitStatusText returned %d, want exitOK", code)
	}
	out := buf.String()
	if strings.Contains(out, "Aggregate") {
		t.Errorf("Aggregate block should not appear when agg=nil; out=%q", out)
	}
}

func TestStatusTextRendersAggregateBlock(t *testing.T) {
	agg := &httpapi.AggregateStatusResponse{
		PPMIEnabled:      true,
		KnownIndexers:    3,
		RecordSourceKind: "cache",
		RecordCacheSize:  42,
		ServicesAdvertised: "00000000000002ef",
	}
	var buf bytes.Buffer
	if code := emitStatusText(&buf, baseStatus(), agg); code != exitOK {
		t.Fatal(code)
	}
	out := buf.String()
	for _, want := range []string{
		"Aggregate (Layer D v0.5",
		"PPMI enabled:         true",
		"known indexers:       3",
		"record source:        cache",
		"record cache size:    42",
		"services advertised:  0x00000000000002ef",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
}

// When PPMI is disabled the block still renders — just with
// PPMI enabled: false. Confirms nothing is conditional on the flag.
func TestStatusTextAggregateBlockWithoutPPMI(t *testing.T) {
	agg := &httpapi.AggregateStatusResponse{
		PPMIEnabled:   false,
		KnownIndexers: 0,
	}
	var buf bytes.Buffer
	if code := emitStatusText(&buf, baseStatus(), agg); code != exitOK {
		t.Fatal(code)
	}
	out := buf.String()
	if !strings.Contains(out, "PPMI enabled:         false") {
		t.Errorf("expected 'PPMI enabled: false' in output: %s", out)
	}
	// RecordSourceKind empty → no "record source" line.
	if strings.Contains(out, "record source:") {
		t.Errorf("record-source line should be omitted when kind is empty: %s", out)
	}
}

// A record-source with zero cache size still renders the kind
// without the size line.
func TestStatusTextAggregateBlockEmptyCache(t *testing.T) {
	agg := &httpapi.AggregateStatusResponse{
		RecordSourceKind: "custom",
		RecordCacheSize:  0,
	}
	var buf bytes.Buffer
	if code := emitStatusText(&buf, baseStatus(), agg); code != exitOK {
		t.Fatal(code)
	}
	out := buf.String()
	if !strings.Contains(out, "record source:        custom") {
		t.Errorf("expected custom source kind: %s", out)
	}
	if strings.Contains(out, "record cache size") {
		t.Errorf("cache-size line should be omitted when size is 0: %s", out)
	}
}
