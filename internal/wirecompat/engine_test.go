package wirecompat

// Deterministic single-engine harness tests: no second torrent client, no
// wall-clock races beyond bounded polls — CI -race safe. The two-engine
// transfer scenarios live in the scenarios subpackage.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/engine"
)

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestSeedShowsProgressWithoutPeers pins the VerifyData DoD: a fresh seed
// reaches 100% before any peer ever connects, because the engine rehashes in
// the background instead of trusting anacrolix's lazy verify.
func TestSeedShowsProgressWithoutPeers(t *testing.T) {
	c := NewCluster(t, 1)
	node := c.Nodes[0]
	mi, payload := BuildFixture(t, filepath.Join(node.DataDir, "content"), "fixture.bin", 96*1024)

	h, err := node.Eng.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(node.DataDir, "content", "fixture.bin"))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "verify to complete", func() bool {
		return h.T.BytesCompleted() == int64(len(payload))
	})
	snaps := node.Eng.TorrentSnapshots()
	if len(snaps) != 1 || snaps[0].Status != "seeding" || snaps[0].Progress != 1.0 {
		t.Fatalf("snapshot = %+v, want seeding at 100%%", snaps)
	}
}

// TestSeedInPlaceRealBasename pins the "downloading 0%" fix: content whose
// on-disk basename differs from info.Name still verifies in place.
func TestSeedInPlaceRealBasename(t *testing.T) {
	c := NewCluster(t, 1)
	node := c.Nodes[0]
	outside := t.TempDir() // content OUTSIDE DataDir
	mi, payload := BuildFixture(t, outside, "real-name.bin", 64*1024)

	h, err := node.Eng.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(outside, "real-name.bin"))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "in-place verify", func() bool {
		return h.T.BytesCompleted() == int64(len(payload))
	})
}

// TestRestartResumesSession pins session persist/restore: a completed
// torrent survives an engine restart at 100% with zero peers.
func TestRestartResumesSession(t *testing.T) {
	c := NewCluster(t, 1)
	node := c.Nodes[0]
	contentDir := filepath.Join(node.DataDir, "content")
	mi, payload := BuildFixture(t, contentDir, "fixture.bin", 96*1024)

	h, err := node.Eng.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(contentDir, "fixture.bin"))
	if err != nil {
		t.Fatal(err)
	}
	ih := h.InfoHashHex()
	waitFor(t, 10*time.Second, "seed verify", func() bool {
		return h.T.BytesCompleted() == int64(len(payload))
	})
	if err := node.Eng.Close(); err != nil {
		t.Fatal(err)
	}

	// Second engine over the same DataDir restores and re-verifies.
	cfg := clusterNodeConfig(node.DataDir)
	eng2, err := engine.New(t.Context(), cfg, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	if err := eng2.RestoreSession(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, "restored torrent to re-verify", func() bool {
		for _, s := range eng2.TorrentSnapshots() {
			if s.InfoHash == ih && s.Progress == 1.0 && s.Status == "seeding" {
				return true
			}
		}
		return false
	})
}

// TestPausedStateSurvivesRestart: a paused torrent restores paused and never
// races into Normal priority.
func TestPausedStateSurvivesRestart(t *testing.T) {
	c := NewCluster(t, 1)
	node := c.Nodes[0]
	contentDir := filepath.Join(node.DataDir, "content")
	mi, _ := BuildFixture(t, contentDir, "fixture.bin", 32*1024)
	h, err := node.Eng.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(contentDir, "fixture.bin"))
	if err != nil {
		t.Fatal(err)
	}
	ih := h.InfoHashHex()
	<-h.T.GotInfo()
	if err := node.Eng.PauseTorrent(ih); err != nil {
		t.Fatal(err)
	}
	if err := node.Eng.Close(); err != nil {
		t.Fatal(err)
	}

	eng2, err := engine.New(t.Context(), clusterNodeConfig(node.DataDir), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	if err := eng2.RestoreSession(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "restored snapshot", func() bool {
		snaps := eng2.TorrentSnapshots()
		return len(snaps) == 1 && snaps[0].InfoHash == ih
	})
	snaps := eng2.TorrentSnapshots()
	if !snaps[0].Paused || snaps[0].Status != "paused" {
		t.Fatalf("restored snapshot = %+v, want paused", snaps[0])
	}
}

// TestCorruptSessionEntrySkipped: one bad row never blocks the rest.
func TestCorruptSessionEntrySkipped(t *testing.T) {
	c := NewCluster(t, 1)
	node := c.Nodes[0]
	contentDir := filepath.Join(node.DataDir, "content")
	mi, _ := BuildFixture(t, contentDir, "fixture.bin", 32*1024)
	h, err := node.Eng.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(contentDir, "fixture.bin"))
	if err != nil {
		t.Fatal(err)
	}
	ih := h.InfoHashHex()
	if err := node.Eng.Close(); err != nil {
		t.Fatal(err)
	}

	// Prepend a sibling entry with an unsafe torrent-file name via real JSON
	// manipulation (the file is indented; string splicing would corrupt it).
	sessPath := filepath.Join(node.DataDir, "session.json")
	raw, err := os.ReadFile(sessPath)
	if err != nil {
		t.Fatal(err)
	}
	var sess map[string]any
	if err := json.Unmarshal(raw, &sess); err != nil {
		t.Fatal(err)
	}
	evil := map[string]any{
		"infohash":     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"added_via":    "file",
		"torrent_file": "../evil.torrent",
		"indexing":     true,
	}
	sess["torrents"] = append([]any{evil}, sess["torrents"].([]any)...)
	bad, err := json.Marshal(sess)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessPath, bad, 0o644); err != nil {
		t.Fatal(err)
	}

	eng2, err := engine.New(t.Context(), clusterNodeConfig(node.DataDir), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	if err := eng2.RestoreSession(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "good entry restored", func() bool {
		for _, s := range eng2.TorrentSnapshots() {
			if s.InfoHash == ih {
				return true
			}
		}
		return false
	})
	for _, s := range eng2.TorrentSnapshots() {
		if s.InfoHash == "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
			t.Fatal("unsafe session entry was restored")
		}
	}
}

// TestQueueCapAndAllNonePriority pins one §6 slot defect: a queued torrent
// promotes when the active one's files all go to "none". (The completion-
// triggered promotion has its own test below.)
func TestQueueCapAndAllNonePriority(t *testing.T) {
	c := NewCluster(t, 1)
	node := c.Nodes[0]
	node.Eng.SetMaxActiveDownloads(1)

	dirA := filepath.Join(node.DataDir, "a")
	dirB := filepath.Join(node.DataDir, "b")
	miA, _ := BuildFixture(t, dirA, "a.bin", 32*1024)
	miB, _ := BuildFixture(t, dirB, "b.bin", 32*1024)

	// A occupies the single slot (incomplete: no in-place content, plain
	// metainfo add with data stored under DataDir but never written).
	hA, err := node.Eng.AddTorrentMetaInfo(miA)
	if err != nil {
		t.Fatal(err)
	}
	<-hA.T.GotInfo()
	waitFor(t, 5*time.Second, "A active", func() bool {
		for _, f := range hA.T.Files() {
			if f.Priority() != 0 { // PiecePriorityNone == 0
				return true
			}
		}
		return false
	})

	hB, err := node.Eng.AddTorrentMetaInfo(miB)
	if err != nil {
		t.Fatal(err)
	}
	<-hB.T.GotInfo()
	waitFor(t, 5*time.Second, "B queued", func() bool {
		for _, s := range node.Eng.TorrentSnapshots() {
			if s.InfoHash == hB.InfoHashHex() && s.Queued {
				return true
			}
		}
		return false
	})

	// Setting every file of A to "none" frees the slot; B must promote.
	for i := range hA.T.Files() {
		if err := node.Eng.SetFilePriority(hA.InfoHashHex(), i, "none"); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, 5*time.Second, "B promoted after A went all-none", func() bool {
		for _, s := range node.Eng.TorrentSnapshots() {
			if s.InfoHash == hB.InfoHashHex() && !s.Queued {
				return true
			}
		}
		return false
	})
}

// snapQueued reads a torrent's queued flag through the public snapshot API.
func snapQueued(e *engine.Engine, ih string) bool {
	for _, s := range e.TorrentSnapshots() {
		if s.InfoHash == ih {
			return s.Queued
		}
	}
	return false
}

// TestCompletionPromotesQueued pins THE §6 headline queue fix: completing a
// torrent promotes the next queued one unconditionally — no Bloom filter is
// consulted anywhere on this path (the legacy's only completion-promotion
// site returned early on a nil Bloom, stranding the slot).
func TestCompletionPromotesQueued(t *testing.T) {
	c := NewCluster(t, 1)
	node := c.Nodes[0]
	node.Eng.SetMaxActiveDownloads(1)

	staging := t.TempDir() // payload built OUTSIDE DataDir so A starts incomplete
	miA, payload := BuildFixture(t, staging, "a.bin", 32*1024)
	miB, _ := BuildFixture(t, filepath.Join(staging, "b"), "b.bin", 32*1024)

	hA, err := node.Eng.AddTorrentMetaInfo(miA)
	if err != nil {
		t.Fatal(err)
	}
	<-hA.T.GotInfo()
	waitFor(t, 5*time.Second, "A active", func() bool {
		for _, s := range node.Eng.TorrentSnapshots() {
			if s.InfoHash == hA.InfoHashHex() && !s.Queued && s.Status == "downloading" {
				return true
			}
		}
		return false
	})

	hB, err := node.Eng.AddTorrentMetaInfo(miB)
	if err != nil {
		t.Fatal(err)
	}
	<-hB.T.GotInfo()
	waitFor(t, 5*time.Second, "B queued", func() bool {
		return snapQueued(node.Eng, hB.InfoHashHex())
	})

	// Complete A: drop the payload where default storage looks and rehash.
	if err := os.WriteFile(filepath.Join(node.DataDir, "a.bin"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := hA.T.VerifyDataContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "B promoted after A completed", func() bool {
		return !snapQueued(node.Eng, hB.InfoHashHex())
	})
}

// TestResumeOverCapStopsDownloading pins the review-found cap violation: a
// torrent resumed over a full cap queues AND its file priorities reset, so
// it cannot keep transferring outside the cap.
func TestResumeOverCapStopsDownloading(t *testing.T) {
	c := NewCluster(t, 1)
	node := c.Nodes[0]
	node.Eng.SetMaxActiveDownloads(1)

	staging := t.TempDir()
	miA, _ := BuildFixture(t, staging, "a.bin", 32*1024)
	miB, _ := BuildFixture(t, filepath.Join(staging, "b"), "b.bin", 32*1024)

	hA, err := node.Eng.AddTorrentMetaInfo(miA)
	if err != nil {
		t.Fatal(err)
	}
	<-hA.T.GotInfo()
	ihA := hA.InfoHashHex()
	waitFor(t, 5*time.Second, "A active", func() bool {
		files, _ := node.Eng.TorrentFiles(ihA)
		return len(files) > 0 && files[0].Priority == "normal"
	})

	hB, err := node.Eng.AddTorrentMetaInfo(miB)
	if err != nil {
		t.Fatal(err)
	}
	<-hB.T.GotInfo()

	if err := node.Eng.PauseTorrent(ihA); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "B promoted after A paused", func() bool {
		return !snapQueued(node.Eng, hB.InfoHashHex())
	})
	if err := node.Eng.ResumeTorrent(ihA); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "A queued after resume over full cap", func() bool {
		return snapQueued(node.Eng, hA.InfoHashHex())
	})
	// The load-bearing assertion: A's priorities are back to none, so it
	// cannot transfer from the queue.
	files, err := node.Eng.TorrentFiles(ihA)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Priority != "none" {
			t.Fatalf("queued-over-cap torrent still has file priority %q — downloading outside the cap", f.Priority)
		}
	}
}

// TestDoubleAddReturnsSameHandle: duplicate adds dedupe silently.
func TestDoubleAddReturnsSameHandle(t *testing.T) {
	c := NewCluster(t, 1)
	node := c.Nodes[0]
	contentDir := filepath.Join(node.DataDir, "content")
	mi, _ := BuildFixture(t, contentDir, "fixture.bin", 32*1024)
	h1, err := node.Eng.AddTorrentMetaInfo(mi)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := node.Eng.AddTorrentMetaInfo(mi)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatal("duplicate add returned a different handle")
	}
}

// TestFileCompleteEventsExactlyOnceWithReplay: a completed seed dispatches
// one event per file, replayed to late subscribers.
func TestFileCompleteEventsExactlyOnceWithReplay(t *testing.T) {
	c := NewCluster(t, 1)
	node := c.Nodes[0]
	contentDir := filepath.Join(node.DataDir, "content")
	mi, payload := BuildFixture(t, contentDir, "fixture.bin", 96*1024)
	h, err := node.Eng.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(contentDir, "fixture.bin"))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "verify", func() bool {
		return h.T.BytesCompleted() == int64(len(payload))
	})

	// A LATE subscriber still sees the completion via the replay buffer.
	sub := h.SubscribeFileEvents()
	select {
	case ev := <-sub:
		if ev.Path != "fixture.bin" || ev.Size != int64(len(payload)) {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late subscriber got no replayed file-complete event")
	}
	// Exactly once: no second event arrives.
	select {
	case ev := <-sub:
		t.Fatalf("unexpected second event: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}
