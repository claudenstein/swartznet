package companion

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestRefreshOnceWriteErrorRecordsFailure covers the
// `WriteCompanionFiles err → recordFailure("write")` branch of
// refreshOnce. Build a Publisher pointed at an opts.Dir whose
// parent is a regular file so MkdirAll inside WriteCompanionFiles
// fails; refreshOnce must record the wrapped "write" failure.
//
// Skipped on Windows because path-into-file semantics differ.
func TestRefreshOnceWriteErrorRecordsFailure(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("path-into-file semantics differ on Windows")
	}
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "rwerr.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: strings.Repeat("a", 40),
		Name:     "ubuntu",
	}); err != nil {
		t.Fatal(err)
	}

	// Plant a regular file at would-be parent so MkdirAll fails
	// for a path that lives under it.
	root := t.TempDir()
	parent := filepath.Join(root, "blocker")
	if err := os.WriteFile(parent, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := NewPublisher(idx,
		nopPointerPutter{},
		nopTorrentSeeder{},
		PublisherOptions{Dir: filepath.Join(parent, "child")},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	p.refreshOnce(context.Background())

	st := p.Status()
	if !strings.Contains(st.LastError, "write") {
		t.Errorf("LastError = %q, want it to wrap 'write'", st.LastError)
	}
}
