package engine_test

import (
	"strings"
	"testing"
)

// TestAddMagnetZeroInfoHashRejected guards against a regression where a
// magnet URI with an all-zero infohash reached anacrolix/torrent and
// triggered a panic deep inside AddTorrentOpt → panicif.Zero. That panic
// unwound through the CLI's deferred daemon.Close, tearing down the
// HTTP API seconds after it started. Callers now see a clean error
// here and never reach the anacrolix client.
func TestAddMagnetZeroInfoHashRejected(t *testing.T) {
	t.Parallel()
	eng, cleanup := newAddTorrentFileEngine(t)
	defer cleanup()

	uri := "magnet:?xt=urn:btih:0000000000000000000000000000000000000000"
	h, err := eng.AddMagnet(uri)
	if err == nil {
		t.Fatalf("expected error for zero-infohash magnet, got handle %v", h)
	}
	if !strings.Contains(err.Error(), "zero infohash") {
		t.Errorf("err = %q, want it to mention 'zero infohash'", err.Error())
	}
}

// TestAddMagnetMalformedRejected covers the parse-failure branch added
// alongside the zero-infohash guard — a URI that is not a magnet at all
// should surface as a parse error rather than reaching the client.
func TestAddMagnetMalformedRejected(t *testing.T) {
	t.Parallel()
	eng, cleanup := newAddTorrentFileEngine(t)
	defer cleanup()

	_, err := eng.AddMagnet("not-a-magnet-uri")
	if err == nil {
		t.Fatal("expected error for non-magnet URI")
	}
	if !strings.Contains(err.Error(), "parse magnet") {
		t.Errorf("err = %q, want it to mention 'parse magnet'", err.Error())
	}
}
