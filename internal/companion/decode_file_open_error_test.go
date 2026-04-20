package companion

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
)

// TestDecodeFileOpenError covers the os.Open error branch of
// (*Subscriber).decodeFile — pointing at a non-existent path
// returns the wrapped Open error and an empty CompanionIndex.
// The Decode-side error branches are exercised by the
// existing serialize tests; this fills in the open-failure path.
func TestDecodeFileOpenError(t *testing.T) {
	t.Parallel()
	s := &Subscriber{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	missing := filepath.Join(t.TempDir(), "no-such-file.json.gz")

	idx, err := s.decodeFile(missing)
	if err == nil {
		t.Errorf("decodeFile on missing path should error, got idx=%+v", idx)
	}
	if len(idx.Torrents) != 0 || idx.Format != "" {
		t.Errorf("idx should be zero-value on Open failure, got %+v", idx)
	}
}
