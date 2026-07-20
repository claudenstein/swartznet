package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestCreateTorrentAdapter exercises the daemon's CreateTorrent collaborator: a
// plain (unsigned, un-seeded) create writes a .torrent and returns an infohash,
// and a sign request fails closed when no identity is loaded. The engine is only
// needed for the seed path, so a nil engine is fine here.
func TestCreateTorrentAdapter(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(src, []byte("hello create adapter"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "f.torrent")

	res, err := createTorrent(nil, nil, "test", httpapi.CreateTorrentParams{Root: src, Output: out})
	if err != nil {
		t.Fatalf("createTorrent: %v", err)
	}
	if res.InfoHash == "" {
		t.Error("no infohash returned")
	}
	if res.Seeded {
		t.Error("seeded=true without a seed request")
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf(".torrent not written: %v", err)
	}

	// Sign requested but no identity → fail closed, before hashing.
	if _, err := createTorrent(nil, nil, "test", httpapi.CreateTorrentParams{Root: src, Output: out, Sign: true}); err == nil || !strings.Contains(err.Error(), "no identity") {
		t.Errorf("sign-without-identity: err = %v, want a 'no identity' error", err)
	}

	// A missing output directory fails fast (pre-flight) before hashing.
	badOut := filepath.Join(dir, "does-not-exist", "x.torrent")
	if _, err := createTorrent(nil, nil, "test", httpapi.CreateTorrentParams{Root: src, Output: badOut}); err == nil || !strings.Contains(err.Error(), "output directory does not exist") {
		t.Errorf("bad output dir: err = %v, want 'output directory does not exist'", err)
	}
}
