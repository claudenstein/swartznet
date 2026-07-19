package swarmsearch

import (
	"testing"

	"github.com/swartznet/swartznet/contracts/ltepwire"
)

// decodeReplyResult runs handleQuery with the given caps + hits and returns the
// decoded Result frame the responder put on the wire.
func decodeReplyResult(t *testing.T, caps ltepwire.Sharing, hits []LocalHit, scope string) ltepwire.Result {
	t.Helper()
	p := New(testLog())
	t.Cleanup(func() { p.Close() })
	p.SetCapabilitySource(func() Capabilities { return Capabilities{Sharing: caps} })
	p.SetSearcher(&fakeSearcher{total: len(hits), hits: hits})

	var frame []byte
	reply := func(f []byte) error { frame = append([]byte(nil), f...); return nil }
	q, err := ltepwire.EncodeQuery(ltepwire.Query{TxID: 1, Q: "ubuntu", Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	p.handleQuery("peer", q, reply)
	if frame == nil {
		t.Fatal("responder produced no result frame")
	}
	res, err := ltepwire.DecodeResult(frame)
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return res
}

const capsTestIH = "1111111111111111111111111111111111111111"

func capsTestHits() []LocalHit {
	return []LocalHit{
		{DocType: "torrent", InfoHash: capsTestIH, Name: "ubuntu.iso", SizeBytes: 100, Score: 0.9},
		{DocType: "content", InfoHash: capsTestIH, Name: "ubuntu.iso", FileIndex: 2, FilePath: "/home/user/secret.pdf", Score: 0.8},
	}
}

// TestQueryResponseWithholdsContentWhenContentHitsOff pins the round-6 fix: a
// node that shares local NAMES but not content/file info must not leak content
// matches or file paths — even for a scope="n" query that unsupportedScope
// always accepts. The name hit is returned; the content match is withheld.
func TestQueryResponseWithholdsContentWhenContentHitsOff(t *testing.T) {
	res := decodeReplyResult(t,
		ltepwire.Sharing{ShareLocal: 2, FileHits: false, ContentHits: false},
		capsTestHits(), "n")

	if len(res.Hits) != 1 || res.Hits[0].N != "ubuntu.iso" {
		t.Fatalf("name hit not returned: %+v", res.Hits)
	}
	if len(res.Hits[0].Matches) != 0 {
		t.Errorf("content/file matches leaked despite ContentHits=false: %+v", res.Hits[0].Matches)
	}
}

// TestQueryResponseWithholdsContentOnlyTorrent: a torrent that surfaced ONLY via
// content must not appear at all when ContentHits is off.
func TestQueryResponseWithholdsContentOnlyTorrent(t *testing.T) {
	res := decodeReplyResult(t,
		ltepwire.Sharing{ShareLocal: 2, FileHits: false, ContentHits: false},
		[]LocalHit{{DocType: "content", InfoHash: capsTestIH, Name: "ubuntu.iso", FileIndex: 0, FilePath: "/secret.pdf", Score: 0.7}},
		"n")
	if len(res.Hits) != 0 {
		t.Errorf("a content-only torrent leaked despite ContentHits=false: %+v", res.Hits)
	}
}

// TestQueryResponseStripsFilePathWhenFileHitsOff: with ContentHits on but
// FileHits off, the content match is shared but its per-file PATH is stripped
// (the file index survives).
func TestQueryResponseStripsFilePathWhenFileHitsOff(t *testing.T) {
	res := decodeReplyResult(t,
		ltepwire.Sharing{ShareLocal: 2, FileHits: false, ContentHits: true},
		capsTestHits(), "n")

	if len(res.Hits) != 1 || len(res.Hits[0].Matches) != 1 {
		t.Fatalf("content match dropped despite ContentHits=true: %+v", res.Hits)
	}
	m := res.Hits[0].Matches[0]
	if m.FP != "" {
		t.Errorf("file path leaked despite FileHits=false: %q", m.FP)
	}
	if m.FI != 2 {
		t.Errorf("file index lost: got %d, want 2", m.FI)
	}
}

// TestQueryResponseFullShareKeepsEverything guards against over-filtering: with
// both caps on, content matches AND file paths are shared.
func TestQueryResponseFullShareKeepsEverything(t *testing.T) {
	res := decodeReplyResult(t,
		ltepwire.Sharing{ShareLocal: 2, FileHits: true, ContentHits: true},
		capsTestHits(), "nfc")
	if len(res.Hits) != 1 || len(res.Hits[0].Matches) != 1 {
		t.Fatalf("full-share dropped a content match: %+v", res.Hits)
	}
	if res.Hits[0].Matches[0].FP != "/home/user/secret.pdf" {
		t.Errorf("full-share stripped the file path: %q", res.Hits[0].Matches[0].FP)
	}
}
