package companion_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
)

// TestIngestReaderRejectsGarbage covers the Decode-error branch
// of IngestReader: non-gzip input must surface the wrapped
// "decode" error rather than passing through to ingest.
func TestIngestReaderRejectsGarbage(t *testing.T) {
	t.Parallel()
	sub, err := companion.NewSubscriber(&fakeGetter{}, &fakeFetcher{}, &recorderIngester{},
		companion.DefaultSubscriberOptions(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}

	_, _, _, err = sub.IngestReader(strings.NewReader("not gzip"))
	if err == nil {
		t.Error("IngestReader on garbage input should error")
	}
}

// TestIngestReaderSkipsBadInfoHashEntries covers the "bad
// infohash" skip branch in ingest. Build a valid CompanionIndex
// payload that contains one entry with a bad-length infohash;
// the subscriber should skip it (debug log) and not call
// IndexTorrent for it, while still ingesting the surrounding
// good entries.
func TestIngestReaderSkipsBadInfoHashEntries(t *testing.T) {
	t.Parallel()
	rec := &recorderIngester{}
	sub, err := companion.NewSubscriber(&fakeGetter{}, &fakeFetcher{}, rec,
		companion.DefaultSubscriberOptions(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}

	idx := companion.CompanionIndex{
		Publisher: "abc",
		Torrents: []companion.TorrentRecord{
			{InfoHash: "good-but-too-short"}, // skip
			{InfoHash: "1111111111111111111111111111111111111111", Name: "good"},
			{InfoHash: ""}, // also skip
		},
	}
	encoded, err := companion.Encode(idx)
	if err != nil {
		t.Fatal(err)
	}

	_, tCount, _, err := sub.IngestReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("IngestReader: %v", err)
	}
	if tCount != 1 {
		t.Errorf("torrents indexed = %d, want 1 (only the well-formed row)", tCount)
	}
	got, _ := rec.snapshot()
	if len(got) != 1 || got[0].InfoHash != "1111111111111111111111111111111111111111" {
		t.Errorf("recorder torrents = %+v, want only the well-formed row", got)
	}
}
