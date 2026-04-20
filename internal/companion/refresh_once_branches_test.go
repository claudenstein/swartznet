package companion

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestRefreshOnceEmptyIndexRecordsFailure covers the
// `len(idx.Torrents) == 0 → recordFailure(empty index)` branch
// of refreshOnce. Build a Publisher backed by a freshly-opened
// (empty) Bleve index and call refreshOnce directly; the
// publisher state should reflect the "nothing to publish"
// failure rather than attempting any seed/put.
func TestRefreshOnceEmptyIndexRecordsFailure(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "empty.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	p, err := NewPublisher(idx,
		nopPointerPutter{},
		nopTorrentSeeder{},
		PublisherOptions{Dir: t.TempDir()},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	p.refreshOnce(context.Background())

	st := p.Status()
	if st.LastError == "" {
		t.Errorf("LastError should record the empty-index failure, got %+v", st)
	}
	if !strings.Contains(st.LastError, "nothing to publish") {
		t.Errorf("LastError = %q, want it to mention 'nothing to publish'", st.LastError)
	}
}

// TestRefreshOnceBuildErrorRecordsFailure covers the
// `BuildFromIndex returns err → recordFailure("build")` branch.
// Close the underlying index so BuildFromIndex's first call
// (AllTorrentDocs) returns "indexer: closed".
func TestRefreshOnceBuildErrorRecordsFailure(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "closed.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	p, err := NewPublisher(idx,
		nopPointerPutter{},
		nopTorrentSeeder{},
		PublisherOptions{Dir: t.TempDir()},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	p.refreshOnce(context.Background())

	st := p.Status()
	if !strings.Contains(st.LastError, "build") {
		t.Errorf("LastError = %q, want it to mention 'build'", st.LastError)
	}
}
