package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestEmitJSONHappyPath covers emitJSON's success path —
// json.Encoder produces a parseable indented JSON document.
func TestEmitJSONHappyPath(t *testing.T) {
	t.Parallel()
	res := &indexer.SearchResponse{
		Total: 1,
		Hits: []indexer.SearchHit{
			{DocType: "torrent", InfoHash: validIH, Name: "x", Score: 0.5},
		},
		Took: 25 * time.Millisecond,
	}
	var stdout, stderr bytes.Buffer
	if code := emitJSON(&stdout, res, &stderr); code != exitOK {
		t.Fatalf("emitJSON exit = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"InfoHash"`) {
		t.Errorf("expected 'InfoHash' field in JSON: %s", stdout.String())
	}
}

// TestEmitJSONWriteErr covers emitJSON's `enc.Encode err →
// reportRunErr` arm. failingWriter always returns ErrShortWrite so
// json.Encoder bubbles up the failure.
func TestEmitJSONWriteErr(t *testing.T) {
	t.Parallel()
	res := &indexer.SearchResponse{Total: 0, Hits: nil}
	var stderr bytes.Buffer
	if code := emitJSON(failingWriter{}, res, &stderr); code == exitOK {
		t.Errorf("expected non-zero exit on write err, stderr=%q", stderr.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errFailingWriter }

var errFailingWriter = stubErr("failing-writer-err")

type stubErr string

func (e stubErr) Error() string { return string(e) }

// TestEmitTextNoResults covers emitText's `(no results — try ...)`
// arm.
func TestEmitTextNoResults(t *testing.T) {
	t.Parallel()
	res := &indexer.SearchResponse{Total: 0, Hits: nil}
	var buf bytes.Buffer
	if code := emitText(&buf, res, "ubuntu"); code != exitOK {
		t.Fatal(code)
	}
	if !strings.Contains(buf.String(), "no results") {
		t.Errorf("expected 'no results' in output: %s", buf.String())
	}
}

// TestEmitTextContentDoc covers the `case "content":` arm of the
// hit switch.
func TestEmitTextContentDoc(t *testing.T) {
	t.Parallel()
	res := &indexer.SearchResponse{
		Total: 1,
		Hits: []indexer.SearchHit{{
			DocType:   "content",
			InfoHash:  validIH,
			Score:     0.7,
			FilePath:  "a/b.txt",
			FileSize:  4096,
			Mime:      "text/plain",
			Extractor: "plaintext",
		}},
	}
	var buf bytes.Buffer
	if code := emitText(&buf, res, "q"); code != exitOK {
		t.Fatal(code)
	}
	if !strings.Contains(buf.String(), "[content]") {
		t.Errorf("expected [content] tag: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "extractor=plaintext") {
		t.Errorf("expected extractor field: %s", buf.String())
	}
}

// TestEmitTextTorrentDocSingleTracker covers the default arm of
// the hit switch + the `len(h.Trackers) > 0` branch with exactly
// one tracker (no '+N more' suffix).
func TestEmitTextTorrentDocSingleTracker(t *testing.T) {
	t.Parallel()
	res := &indexer.SearchResponse{
		Total: 1,
		Hits: []indexer.SearchHit{{
			DocType:   "torrent",
			InfoHash:  validIH,
			Score:     0.4,
			Name:      "movie.mkv",
			SizeBytes: 8192,
			FileCount: 2,
			Trackers:  []string{"udp://tracker.example.com:6969"},
		}},
	}
	var buf bytes.Buffer
	if code := emitText(&buf, res, "q"); code != exitOK {
		t.Fatal(code)
	}
	out := buf.String()
	if !strings.Contains(out, "[torrent]") {
		t.Errorf("expected [torrent] tag: %s", out)
	}
	if !strings.Contains(out, "tracker: udp://tracker.example.com:6969") {
		t.Errorf("expected tracker URL line: %s", out)
	}
	if strings.Contains(out, "+0 more") {
		t.Errorf("did not expect '+0 more' suffix for single tracker: %s", out)
	}
}

// TestEmitTextTorrentDocMultiTracker covers the `+N more` suffix
// branch.
func TestEmitTextTorrentDocMultiTracker(t *testing.T) {
	t.Parallel()
	res := &indexer.SearchResponse{
		Total: 1,
		Hits: []indexer.SearchHit{{
			DocType:  "torrent",
			InfoHash: validIH,
			Trackers: []string{"udp://a:1", "udp://b:2", "udp://c:3"},
		}},
	}
	var buf bytes.Buffer
	if code := emitText(&buf, res, "q"); code != exitOK {
		t.Fatal(code)
	}
	if !strings.Contains(buf.String(), "+2 more") {
		t.Errorf("expected '+2 more' suffix: %s", buf.String())
	}
}
