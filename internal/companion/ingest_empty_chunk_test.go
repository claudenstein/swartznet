package companion_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/indexer"
)

type ingestStubGetter struct{}

func (ingestStubGetter) GetInfohashPointer(_ context.Context, _ [32]byte, _ []byte) ([20]byte, error) {
	return [20]byte{}, nil
}

type ingestStubFetcher struct{}

func (ingestStubFetcher) FetchCompanionTorrent(_ context.Context, _ [20]byte) (string, error) {
	return "", nil
}

// recordingIngester just counts how many torrents and content
// docs it sees so the test can assert that the empty-text chunk
// was skipped while the non-empty siblings were ingested.
type recordingIngester struct {
	torrents int
	contents int
}

func (r *recordingIngester) IndexTorrent(_ indexer.TorrentDoc) error {
	r.torrents++
	return nil
}
func (r *recordingIngester) IndexContent(_ indexer.ContentDoc) error {
	r.contents++
	return nil
}

// TestSubscriberIngestSkipsEmptyChunk covers the
// `ch.Text == "" → continue` branch of Subscriber.ingest. A
// companion index with one torrent whose file has both an
// empty-text chunk and a non-empty chunk must result in exactly
// one IndexContent call.
func TestSubscriberIngestSkipsEmptyChunk(t *testing.T) {
	t.Parallel()
	idx := companion.CompanionIndex{
		Publisher: "deadbeef",
		Torrents: []companion.TorrentRecord{
			{
				InfoHash: strings.Repeat("a", 40),
				Name:     "ubuntu",
				Size:     6 << 30,
				Files: []companion.FileRecord{
					{
						Index: 0,
						Path:  "release-notes.txt",
						Chunks: []companion.ContentChunk{
							{Text: ""}, // skipped
							{Text: "real text"},
						},
					},
				},
			},
		},
	}
	encoded, err := companion.Encode(idx)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	rec := &recordingIngester{}
	sub, err := companion.NewSubscriber(
		ingestStubGetter{}, ingestStubFetcher{}, rec,
		companion.DefaultSubscriberOptions(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := sub.IngestReader(bytes.NewReader(encoded)); err != nil {
		t.Fatalf("IngestReader: %v", err)
	}
	if rec.torrents != 1 {
		t.Errorf("torrents = %d, want 1", rec.torrents)
	}
	if rec.contents != 1 {
		t.Errorf("contents = %d, want 1 (empty-text chunk should be skipped)", rec.contents)
	}
}
