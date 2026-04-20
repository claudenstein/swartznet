package companion_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/indexer"
)

type okPointerGetter struct{ ih [20]byte }

func (g okPointerGetter) GetInfohashPointer(_ context.Context, _ [32]byte, _ []byte) ([20]byte, error) {
	return g.ih, nil
}

type pathFetcher struct{ path string }

func (f pathFetcher) FetchCompanionTorrent(_ context.Context, _ [20]byte) (string, error) {
	return f.path, nil
}

type nopSubIngester struct{}

func (nopSubIngester) IndexTorrent(_ indexer.TorrentDoc) error { return nil }
func (nopSubIngester) IndexContent(_ indexer.ContentDoc) error { return nil }

// TestSubscriberSyncDecodeError covers Sync's
// `decodeFile returns err → res.Err = "decode <path>: ..."`
// branch. The other branches (pointer-error, fetch-error,
// ingest-error, success) are exercised by integration tests; the
// decode-error path was missing.
func TestSubscriberSyncDecodeError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	garbage := filepath.Join(dir, "not.gz")
	if err := os.WriteFile(garbage, []byte("not a gzip stream"), 0o644); err != nil {
		t.Fatal(err)
	}

	sub, err := companion.NewSubscriber(
		okPointerGetter{},
		pathFetcher{path: garbage},
		nopSubIngester{},
		companion.DefaultSubscriberOptions(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	var pub [32]byte
	pub[0] = 0xAB
	res := sub.Sync(context.Background(), pub)
	if res.Err == nil {
		t.Fatal("Sync should error when companion file is not gzip")
	}
	if !strings.Contains(res.Err.Error(), "decode") {
		t.Errorf("err = %q, want it to wrap 'decode'", res.Err.Error())
	}
}
