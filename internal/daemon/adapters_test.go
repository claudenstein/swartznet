package daemon

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/indexer"
)

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

func newAdapterSubscriberWorker(t *testing.T) *companion.SubscriberWorker {
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

func TestCompanionAdapterNilPublisher(t *testing.T) {
	t.Parallel()
	a := newCompanionAdapter(nil, nil, "")

	st := a.PublisherStatus()
	if st.PubKeyHex != "" || st.PublishedCount != 0 || st.LastInfoHash != "" {
		t.Errorf("nil publisher should yield zero status, got %+v", st)
	}
	if err := a.RefreshNow(); err == nil {
		t.Error("RefreshNow with nil publisher should return error")
	}
}

func TestCompanionAdapterNilSubscriber(t *testing.T) {
	t.Parallel()
	a := newCompanionAdapter(nil, nil, "")

	if rows := a.SubscriberStatus(); rows != nil {
		t.Errorf("nil subscriber should yield nil rows, got %v", rows)
	}
	var pk [32]byte
	pk[0] = 0xaa
	if err := a.Follow(pk, "x"); err == nil {
		t.Error("Follow with nil subscriber should return error")
	}
	if err := a.Unfollow(pk); err == nil {
		t.Error("Unfollow with nil subscriber should return error")
	}
}

func TestCompanionAdapterSubscriberStatusPopulated(t *testing.T) {
	t.Parallel()
	w := newAdapterSubscriberWorker(t)
	var pk [32]byte
	pk[0] = 0x11
	w.Follow(pk, "alice")

	a := newCompanionAdapter(nil, w, "")

	rows := a.SubscriberStatus()
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.Label != "alice" {
		t.Errorf("Label = %q, want alice", row.Label)
	}
	if row.PubKeyHex != hex.EncodeToString(pk[:]) {
		t.Errorf("PubKeyHex = %q, want %q", row.PubKeyHex, hex.EncodeToString(pk[:]))
	}
	// No sync has run yet: GeneratedAt zero, no last-sync time, no pointer, no error.
	if row.GeneratedAt != 0 {
		t.Errorf("GeneratedAt should be zero, got %d", row.GeneratedAt)
	}
	if !row.LastSyncAt.IsZero() {
		t.Errorf("LastSyncAt should be zero, got %v", row.LastSyncAt)
	}
	if row.PointerInfoHash != "" {
		t.Errorf("PointerInfoHash should be empty, got %q", row.PointerInfoHash)
	}
	if row.LastError != "" {
		t.Errorf("LastError should be empty, got %q", row.LastError)
	}
}

func TestCompanionAdapterFollowPersists(t *testing.T) {
	t.Parallel()
	w := newAdapterSubscriberWorker(t)
	path := filepath.Join(t.TempDir(), "follows.json")
	a := newCompanionAdapter(nil, w, path)

	var pkA, pkB [32]byte
	pkA[0] = 0x01
	pkB[0] = 0x02

	if err := a.Follow(pkA, "alice"); err != nil {
		t.Fatalf("Follow pkA: %v", err)
	}
	if err := a.Follow(pkB, ""); err != nil {
		t.Fatalf("Follow pkB: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read follow file: %v", err)
	}
	var entries []followEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		t.Fatalf("parse follow file: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	// Order is map-iteration dependent; sort for stable assertion.
	sort.Slice(entries, func(i, j int) bool { return entries[i].PubKey < entries[j].PubKey })
	if entries[0].PubKey != hex.EncodeToString(pkA[:]) || entries[0].Label != "alice" {
		t.Errorf("entry[0] = %+v", entries[0])
	}
	if entries[1].PubKey != hex.EncodeToString(pkB[:]) || entries[1].Label != "" {
		t.Errorf("entry[1] = %+v", entries[1])
	}

	// Unfollow rewrites the file without the removed entry.
	if err := a.Unfollow(pkA); err != nil {
		t.Fatalf("Unfollow: %v", err)
	}
	body, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	entries = nil
	if err := json.Unmarshal(body, &entries); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(entries) != 1 || entries[0].PubKey != hex.EncodeToString(pkB[:]) {
		t.Errorf("after unfollow, entries = %+v", entries)
	}

	// Tempfile must not be left behind after a successful rename.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tempfile should be gone, stat err = %v", err)
	}
}

func TestCompanionAdapterFollowEmptyPathSkipsPersist(t *testing.T) {
	t.Parallel()
	w := newAdapterSubscriberWorker(t)
	a := newCompanionAdapter(nil, w, "")

	var pk [32]byte
	pk[0] = 0x42
	if err := a.Follow(pk, "test"); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if got := len(w.Following()); got != 1 {
		t.Errorf("worker should have 1 follow, got %d", got)
	}
	// No file written — nothing to stat; we only verify no panic and no error.
}

func TestCompanionAdapterPersistRenameFailure(t *testing.T) {
	t.Parallel()
	w := newAdapterSubscriberWorker(t)
	// Point followPath at a directory — os.Rename cannot replace a
	// non-empty directory with a regular file, so persistFollows
	// must surface the rename error.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "follows.json")
	if err := os.Mkdir(blocker, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocker, "sentinel"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newCompanionAdapter(nil, w, blocker)

	var pk [32]byte
	pk[0] = 0x99
	err := a.Follow(pk, "doomed")
	if err == nil {
		t.Fatal("Follow should return an error when persist fails")
	}
	// In-memory worker should still have registered the follow even though
	// the on-disk persist failed — the adapter mutates first, then persists.
	if _, ok := w.Following()[pk]; !ok {
		t.Error("in-memory follow should be registered despite persist failure")
	}
}
