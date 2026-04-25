package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestEmitStatusTextRendersPublisherKeywords covers the
// per-keyword block of emitStatusText, which the existing
// status tests skipped (they all set Keywords=nil so the
// "(no keywords published yet)" branch fires). Three sub-
// cases exercise the three rendering arms inside the loop:
//
//   - LastError set       → state=ERR: <err> rendered
//   - LastError empty     → state=ok rendered
//   - LastPublished empty → "never" placeholder rendered
func TestEmitStatusTextRendersPublisherKeywords(t *testing.T) {
	resp := &httpapi.StatusResponse{
		Local: httpapi.LocalStatus{Indexed: true, DocCount: 1},
		Publisher: httpapi.PublisherStatus{
			PubKey:        "abc123",
			TotalKeywords: 3,
			TotalHits:     7,
			Keywords: []httpapi.PublisherKeywordEntry{
				{
					Keyword:       "kw-good",
					HitsCount:     2,
					PublishCount:  3,
					LastPublished: "2026-04-25T00:00:00Z",
					LastError:     "",
				},
				{
					Keyword:       "kw-error",
					HitsCount:     5,
					PublishCount:  1,
					LastPublished: "",
					LastError:     "boom: dht error",
				},
				{
					Keyword:      "kw-never-published",
					HitsCount:    0,
					PublishCount: 0,
				},
			},
		},
	}

	var buf bytes.Buffer
	if code := emitStatusText(&buf, resp, nil); code != exitOK {
		t.Fatalf("emitStatusText returned %d, want exitOK", code)
	}
	out := buf.String()

	for _, want := range []string{
		"per-keyword:",
		"kw-good",
		"state=ok",
		"kw-error",
		"state=ERR: boom: dht error",
		"kw-never-published",
		"last=never",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output: %q", want, out)
		}
	}
}
