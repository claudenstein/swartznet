package companion_test

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
)

// TestDecodeMaterializesNilTorrents covers Decode's defensive
// `out.Torrents == nil → []TorrentRecord{}` substitution. A JSON
// payload that omits the "torrents" field decodes to nil; Decode
// must hand back an empty slice so consumers can range without
// nil-checking.
func TestDecodeMaterializesNilTorrents(t *testing.T) {
	t.Parallel()
	body := []byte(`{"version":1,"format":"swartznet-content-index"}`)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := companion.Decode(&buf)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Torrents == nil {
		t.Errorf("Torrents should be non-nil empty slice, got nil")
	}
	if len(got.Torrents) != 0 {
		t.Errorf("Torrents len = %d, want 0", len(got.Torrents))
	}
}
