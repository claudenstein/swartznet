package daemon_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/indexer"
)

// stubGetter / stubFetcher / stubIngester satisfy companion.NewSubscriber's
// collaborator nil-checks; LoadFollowFile only ever calls
// SubscriberWorker.Follow, so none of these methods are invoked.
type stubGetter struct{}

func (stubGetter) GetInfohashPointer(_ context.Context, _ [32]byte, _ []byte) ([20]byte, error) {
	return [20]byte{}, nil
}

type stubFetcher struct{}

func (stubFetcher) FetchCompanionTorrent(_ context.Context, _ [20]byte) (string, error) {
	return "", nil
}

type stubIngester struct{}

func (stubIngester) IndexTorrent(_ indexer.TorrentDoc) error { return nil }
func (stubIngester) IndexContent(_ indexer.ContentDoc) error { return nil }

func newTestSubscriberWorker(t *testing.T) *companion.SubscriberWorker {
	t.Helper()
	sub, err := companion.NewSubscriber(stubGetter{}, stubFetcher{}, stubIngester{},
		companion.DefaultSubscriberOptions(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewSubscriber: %v", err)
	}
	w, err := companion.NewSubscriberWorker(sub)
	if err != nil {
		t.Fatalf("NewSubscriberWorker: %v", err)
	}
	return w
}

func TestLoadFollowFileMissingIsNotError(t *testing.T) {
	t.Parallel()
	w := newTestSubscriberWorker(t)
	var stderr bytes.Buffer

	n := daemon.LoadFollowFile(w, filepath.Join(t.TempDir(), "does-not-exist.json"), &stderr)

	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be silent for missing file, got %q", stderr.String())
	}
	if got := len(w.Following()); got != 0 {
		t.Errorf("worker should have no follows, got %d", got)
	}
}

func TestLoadFollowFileMalformedJSON(t *testing.T) {
	t.Parallel()
	w := newTestSubscriberWorker(t)
	path := filepath.Join(t.TempDir(), "follows.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer

	n := daemon.LoadFollowFile(w, path, &stderr)

	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
	if !strings.Contains(stderr.String(), "parse") {
		t.Errorf("stderr should mention parse error, got %q", stderr.String())
	}
}

func TestLoadFollowFileMixedEntries(t *testing.T) {
	t.Parallel()
	w := newTestSubscriberWorker(t)

	var goodA, goodB [32]byte
	goodA[0] = 0x01
	goodB[0] = 0x02

	body := `[
		{"pubkey":"` + hex.EncodeToString(goodA[:]) + `","label":"alice"},
		{"pubkey":"not-hex","label":"bad-hex"},
		{"pubkey":"abcd","label":"too-short"},
		{"pubkey":"` + hex.EncodeToString(goodB[:]) + `"}
	]`
	path := filepath.Join(t.TempDir(), "follows.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer

	n := daemon.LoadFollowFile(w, path, &stderr)

	if n != 2 {
		t.Errorf("n = %d, want 2 (two valid entries)", n)
	}
	follows := w.Following()
	if len(follows) != 2 {
		t.Errorf("worker has %d follows, want 2", len(follows))
	}
	if follows[goodA] != "alice" {
		t.Errorf("goodA label = %q, want alice", follows[goodA])
	}
	if _, ok := follows[goodB]; !ok {
		t.Error("goodB not registered")
	}
	// Each malformed row should produce a warning line.
	if got := strings.Count(stderr.String(), "bad pubkey"); got != 2 {
		t.Errorf("expected 2 bad-pubkey warnings, got %d (stderr=%q)", got, stderr.String())
	}
}

func TestLoadFollowFileEmptyArray(t *testing.T) {
	t.Parallel()
	w := newTestSubscriberWorker(t)
	path := filepath.Join(t.TempDir(), "follows.json")
	if err := os.WriteFile(path, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer

	n := daemon.LoadFollowFile(w, path, &stderr)

	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be silent for empty array, got %q", stderr.String())
	}
}
