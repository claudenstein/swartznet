package swarmsearch

import (
	"encoding/hex"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	pp "github.com/anacrolix/torrent/peer_protocol"
)

// silentLogger returns a logger that discards all output. Used by
// tests that construct a Protocol directly (internal package).
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// validIH returns a 40-char hex string made of the given byte repeated.
func validIH(b byte) string {
	return strings.Repeat(string([]byte{b}), 40)
}

// --- hitsToWire tests ---

func TestHitsToWire_EmptyInput(t *testing.T) {
	t.Parallel()
	out := hitsToWire(nil)
	if len(out) != 0 {
		t.Errorf("hitsToWire(nil) = %d hits, want 0", len(out))
	}
	out = hitsToWire([]LocalHit{})
	if len(out) != 0 {
		t.Errorf("hitsToWire([]) = %d hits, want 0", len(out))
	}
}

func TestHitsToWire_InvalidHexInfohashSkipped(t *testing.T) {
	t.Parallel()
	hits := []LocalHit{
		{DocType: "torrent", InfoHash: "not-valid-hex", Name: "Bad"},
		{DocType: "torrent", InfoHash: "aabb", Name: "TooShort"}, // valid hex but only 2 bytes
		{DocType: "content", InfoHash: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", Name: "BadHexContent"},
	}
	out := hitsToWire(hits)
	if len(out) != 0 {
		t.Errorf("hitsToWire with invalid hashes = %d hits, want 0", len(out))
	}
}

func TestHitsToWire_TorrentHitBasic(t *testing.T) {
	t.Parallel()
	ih := strings.Repeat("aa", 20) // 40-char hex
	hits := []LocalHit{
		{
			DocType:   "torrent",
			InfoHash:  ih,
			Name:      "Ubuntu 24.04",
			SizeBytes: 1024,
			Seeders:   50,
			Leechers:  5,
			Score:     0.75,
			AddedAt:   time.Unix(1700000000, 0),
		},
	}
	out := hitsToWire(hits)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	h := out[0]
	if hex.EncodeToString(h.IH) != ih {
		t.Errorf("IH = %x, want %s", h.IH, ih)
	}
	if h.N != "Ubuntu 24.04" {
		t.Errorf("N = %q, want Ubuntu 24.04", h.N)
	}
	if h.Sz != 1024 {
		t.Errorf("Sz = %d, want 1024", h.Sz)
	}
	if h.S != 50 {
		t.Errorf("S = %d, want 50", h.S)
	}
	if h.L != 5 {
		t.Errorf("L = %d, want 5", h.L)
	}
	if h.Rank != 750 {
		t.Errorf("Rank = %d, want 750 (0.75*1000)", h.Rank)
	}
	if h.T != 1700000000 {
		t.Errorf("T = %d, want 1700000000", h.T)
	}
	if len(h.Matches) != 0 {
		t.Errorf("Matches = %v, want empty for torrent hit", h.Matches)
	}
}

func TestHitsToWire_ContentHitsGroupedByInfohash(t *testing.T) {
	t.Parallel()
	ih := strings.Repeat("bb", 20)
	hits := []LocalHit{
		{DocType: "content", InfoHash: ih, Name: "MyTorrent", SizeBytes: 2048, FileIndex: 0, FilePath: "file0.txt", Score: 0.9},
		{DocType: "content", InfoHash: ih, Name: "MyTorrent", SizeBytes: 2048, FileIndex: 3, FilePath: "dir/file3.txt", Score: 0.6},
	}
	out := hitsToWire(hits)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1 (content hits on same IH grouped)", len(out))
	}
	h := out[0]
	if h.N != "MyTorrent" {
		t.Errorf("N = %q, want MyTorrent", h.N)
	}
	if h.Sz != 2048 {
		t.Errorf("Sz = %d, want 2048", h.Sz)
	}
	if len(h.Matches) != 2 {
		t.Fatalf("len(Matches) = %d, want 2", len(h.Matches))
	}
	if h.Matches[0].FI != 0 || h.Matches[0].FP != "file0.txt" {
		t.Errorf("Matches[0] = {FI:%d FP:%q}, want {0 file0.txt}", h.Matches[0].FI, h.Matches[0].FP)
	}
	if h.Matches[1].FI != 3 || h.Matches[1].FP != "dir/file3.txt" {
		t.Errorf("Matches[1] = {FI:%d FP:%q}, want {3 dir/file3.txt}", h.Matches[1].FI, h.Matches[1].FP)
	}
}

func TestHitsToWire_MixedTorrentAndContentSameInfohash(t *testing.T) {
	t.Parallel()
	ih := strings.Repeat("cc", 20)
	hits := []LocalHit{
		// Content hit comes first, creates the entry.
		{DocType: "content", InfoHash: ih, Name: "", SizeBytes: 0, FileIndex: 1, FilePath: "readme.md", Score: 0.8},
		// Torrent hit on the same IH arrives second — should fill name/size
		// without creating a duplicate.
		{DocType: "torrent", InfoHash: ih, Name: "FullName", SizeBytes: 4096, Seeders: 20, Score: 0.5},
	}
	out := hitsToWire(hits)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1 (dedup by infohash)", len(out))
	}
	h := out[0]
	// The torrent hit fills in the empty name from the content entry.
	if h.N != "FullName" {
		t.Errorf("N = %q, want FullName (torrent hit should backfill)", h.N)
	}
	if h.Sz != 4096 {
		t.Errorf("Sz = %d, want 4096", h.Sz)
	}
	// The content hit's Matches entry must still be present.
	if len(h.Matches) != 1 {
		t.Fatalf("len(Matches) = %d, want 1", len(h.Matches))
	}
	if h.Matches[0].FP != "readme.md" {
		t.Errorf("Matches[0].FP = %q, want readme.md", h.Matches[0].FP)
	}
}

func TestHitsToWire_TorrentThenContentSameInfohash(t *testing.T) {
	t.Parallel()
	ih := strings.Repeat("dd", 20)
	hits := []LocalHit{
		// Torrent hit first — no Matches.
		{DocType: "torrent", InfoHash: ih, Name: "Torrent First", SizeBytes: 8192, Seeders: 10, Score: 0.7},
		// Content hit on same IH arrives second. Since the IH is already
		// in byIH from the torrent entry, the content branch appends a
		// Matches entry to the existing Hit.
		{DocType: "content", InfoHash: ih, FileIndex: 5, FilePath: "data.csv", Score: 0.4},
	}
	out := hitsToWire(hits)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	// The torrent was already registered in byIH. The content hit path
	// is "default" for DocType == "torrent", but since torrent came first
	// and the code has "default" handling that checks byIH... Let me
	// re-read the code. Actually, the torrent hit goes to the "default"
	// branch and gets added with byIH. Then the content hit goes to the
	// "content" branch — it sees the IH in byIH, so it appends a Match.
	h := out[0]
	if h.N != "Torrent First" {
		t.Errorf("N = %q, want 'Torrent First'", h.N)
	}
	if len(h.Matches) != 1 {
		t.Fatalf("len(Matches) = %d, want 1 (content hit appended)", len(h.Matches))
	}
	if h.Matches[0].FI != 5 || h.Matches[0].FP != "data.csv" {
		t.Errorf("Matches[0] = {%d %q}, want {5 data.csv}", h.Matches[0].FI, h.Matches[0].FP)
	}
}

func TestHitsToWire_MultipleDistinctInfohashes(t *testing.T) {
	t.Parallel()
	ih1 := strings.Repeat("11", 20)
	ih2 := strings.Repeat("22", 20)
	hits := []LocalHit{
		{DocType: "torrent", InfoHash: ih1, Name: "First", Score: 0.9},
		{DocType: "torrent", InfoHash: ih2, Name: "Second", Score: 0.5},
	}
	out := hitsToWire(hits)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
}

func TestHitsToWire_DuplicateTorrentHitBackfillsName(t *testing.T) {
	t.Parallel()
	ih := strings.Repeat("ee", 20)
	hits := []LocalHit{
		// First content hit creates entry with empty name.
		{DocType: "content", InfoHash: ih, Name: "", FileIndex: 0, FilePath: "a.txt", Score: 0.5},
		// Second torrent hit on same IH: should backfill name.
		{DocType: "torrent", InfoHash: ih, Name: "Backfilled", SizeBytes: 999, Score: 0.3},
	}
	out := hitsToWire(hits)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	if out[0].N != "Backfilled" {
		t.Errorf("N = %q, want 'Backfilled'", out[0].N)
	}
	if out[0].Sz != 999 {
		t.Errorf("Sz = %d, want 999", out[0].Sz)
	}
}

// --- selectTargets tests ---

// newBareProtocol builds a Protocol without rate-limiter/misbehavior
// for selectTargets testing. We need fine control over the book field.
func newBareProtocol() *Protocol {
	return &Protocol{
		log:   silentLogger(),
		caps:  DefaultCapabilities(),
		peers: make(map[string]*PeerState),
	}
}

func makePeerState(addr string, supported bool) PeerState {
	return PeerState{Addr: addr, Supported: supported}
}

func TestSelectTargets_NoSupportedPeers(t *testing.T) {
	t.Parallel()
	p := newBareProtocol()
	snap := []PeerState{
		makePeerState("1.1.1.1:6881", false),
		makePeerState("2.2.2.2:6881", false),
	}
	targets := p.selectTargets(snap)
	if len(targets) != 0 {
		t.Errorf("selectTargets with no supported = %d, want 0", len(targets))
	}
}

func TestSelectTargets_NoPeerBook_FallbackAll(t *testing.T) {
	t.Parallel()
	p := newBareProtocol()
	// No book attached (p.book == nil).
	snap := []PeerState{
		makePeerState("1.1.1.1:6881", true),
		makePeerState("2.2.2.2:6881", true),
		makePeerState("3.3.3.3:6881", false),
	}
	targets := p.selectTargets(snap)
	if len(targets) != 2 {
		t.Errorf("selectTargets (no book) = %d supported, want 2", len(targets))
	}
}

func TestSelectTargets_ZeroTriedPeers_FallbackAll(t *testing.T) {
	t.Parallel()
	p := newBareProtocol()
	p.book = NewPeerBook(10, 10)
	// Add peers as "new" in the book, but promote none.
	p.book.AddNew("1.1.1.1:6881")
	p.book.AddNew("2.2.2.2:6881")
	p.book.AddNew("3.3.3.3:6881")
	if p.book.TriedCount() != 0 {
		t.Fatal("precondition: expected 0 tried peers")
	}

	snap := []PeerState{
		makePeerState("1.1.1.1:6881", true),
		makePeerState("2.2.2.2:6881", true),
		makePeerState("3.3.3.3:6881", true),
	}
	targets := p.selectTargets(snap)
	// With zero tried, falls back to all supported.
	if len(targets) != 3 {
		t.Errorf("selectTargets (zero tried) = %d, want 3 (fallback)", len(targets))
	}
}

func TestSelectTargets_TriedPeersPlusFeelers(t *testing.T) {
	t.Parallel()
	p := newBareProtocol()
	p.book = NewPeerBook(10, 10)

	// Add 5 peers to new, promote 2 to tried.
	for i := 1; i <= 5; i++ {
		addr := addrN(i)
		p.book.AddNew(addr)
	}
	p.book.Promote(addrN(1)) // tried
	p.book.Promote(addrN(2)) // tried

	if p.book.TriedCount() != 2 {
		t.Fatalf("precondition: tried = %d, want 2", p.book.TriedCount())
	}

	snap := make([]PeerState, 5)
	for i := 1; i <= 5; i++ {
		snap[i-1] = makePeerState(addrN(i), true)
	}

	targets := p.selectTargets(snap)
	// Should get: 2 tried + min(FeelerCount, 3 new) = 2 tried + 2 feelers = 4.
	if len(targets) != 4 {
		t.Errorf("selectTargets = %d, want 4 (2 tried + 2 feelers)", len(targets))
	}
	// Verify the tried peers are included.
	addrs := targetAddrs(targets)
	if !addrs[addrN(1)] || !addrs[addrN(2)] {
		t.Errorf("tried peers missing from targets: %v", addrs)
	}
}

func TestSelectTargets_FeelerCountCap(t *testing.T) {
	t.Parallel()
	p := newBareProtocol()
	p.book = NewPeerBook(10, 100) // maxNew=100 so no eviction

	// 1 tried, 10 new. Feeler cap = FeelerCount(2), so total = 1 + 2 = 3.
	for i := 1; i <= 11; i++ {
		p.book.AddNew(addrN(i))
	}
	p.book.Promote(addrN(1)) // 1 tried, 10 remain new

	snap := make([]PeerState, 11)
	for i := 1; i <= 11; i++ {
		snap[i-1] = makePeerState(addrN(i), true)
	}

	targets := p.selectTargets(snap)
	// 1 tried + FeelerCount(2) = 3.
	if len(targets) != 3 {
		t.Errorf("selectTargets = %d, want 3 (1 tried + 2 feelers)", len(targets))
	}
}

func TestSelectTargets_FewerNewThanFeelerCount(t *testing.T) {
	t.Parallel()
	p := newBareProtocol()
	p.book = NewPeerBook(10, 10)

	// 2 tried, 1 new. FeelerCount=2 but only 1 new available.
	p.book.AddNew(addrN(1))
	p.book.AddNew(addrN(2))
	p.book.AddNew(addrN(3))
	p.book.Promote(addrN(1))
	p.book.Promote(addrN(2))
	// Now: tried={1,2}, new={3}

	snap := []PeerState{
		makePeerState(addrN(1), true),
		makePeerState(addrN(2), true),
		makePeerState(addrN(3), true),
	}

	targets := p.selectTargets(snap)
	// 2 tried + 1 feeler (only 1 new available) = 3.
	if len(targets) != 3 {
		t.Errorf("selectTargets = %d, want 3 (2 tried + 1 feeler)", len(targets))
	}
}

func TestSelectTargets_TriedDisconnected_FallbackAll(t *testing.T) {
	t.Parallel()
	p := newBareProtocol()
	p.book = NewPeerBook(10, 10)

	// Promote peer 1 to tried, then remove it from the book. The
	// book still reports TriedCount() > 0 for addr 1, but the snap
	// only contains peers 2 and 3 (both new/supported).
	p.book.AddNew(addrN(1))
	p.book.AddNew(addrN(2))
	p.book.AddNew(addrN(3))
	p.book.Promote(addrN(1))
	// Peer 1 disconnected — not in the snap.
	snap := []PeerState{
		makePeerState(addrN(2), true),
		makePeerState(addrN(3), true),
	}

	targets := p.selectTargets(snap)
	// Tried set from book = {addr1}, but addr1 is not in snap's
	// supported set. So triedSet has no overlap with supported.
	// Only feelerCandidates: {addr2, addr3}. FeelerCount=2, so
	// targets = 0 tried + 2 feelers = 2.
	// This is >= 1 so it won't fall to the final fallback.
	if len(targets) < 1 {
		t.Errorf("selectTargets = %d, want >= 1", len(targets))
	}
}

// --- mergeResponses tests ---

func TestMergeResponses_Empty(t *testing.T) {
	t.Parallel()
	merged := mergeResponses(nil)
	if len(merged) != 0 {
		t.Errorf("mergeResponses(nil) = %d, want 0", len(merged))
	}
	merged = mergeResponses([]incomingResult{})
	if len(merged) != 0 {
		t.Errorf("mergeResponses([]) = %d, want 0", len(merged))
	}
}

func TestMergeResponses_RankCapAt1000(t *testing.T) {
	t.Parallel()
	// 10 peers each return rank 200 for the same infohash.
	// Sum = 2000, but must be capped at 1000.
	ihBytes := repeatBytes(0xaa, 20)
	var responses []incomingResult
	for i := 0; i < 10; i++ {
		responses = append(responses, incomingResult{
			peer: addrN(i + 1),
			result: Result{
				TxID: 1,
				Hits: []Hit{
					{IH: ihBytes, N: "Capped", Sz: 100, S: 5, Rank: 200},
				},
			},
		})
	}

	merged := mergeResponses(responses)
	if len(merged) != 1 {
		t.Fatalf("len = %d, want 1", len(merged))
	}
	if merged[0].Score != 1000 {
		t.Errorf("Score = %d, want 1000 (capped)", merged[0].Score)
	}
	if len(merged[0].Sources) != 10 {
		t.Errorf("Sources = %d, want 10", len(merged[0].Sources))
	}
}

func TestMergeResponses_DedupByInfohash(t *testing.T) {
	t.Parallel()
	ih1 := repeatBytes(0x11, 20)
	ih2 := repeatBytes(0x22, 20)

	responses := []incomingResult{
		{
			peer: "peer-A",
			result: Result{
				TxID: 1,
				Hits: []Hit{
					{IH: ih1, N: "First", Sz: 100, S: 10, Rank: 300},
					{IH: ih2, N: "Second", Sz: 200, S: 20, Rank: 400},
				},
			},
		},
		{
			peer: "peer-B",
			result: Result{
				TxID: 1,
				Hits: []Hit{
					{IH: ih1, N: "First-B", Sz: 100, S: 15, Rank: 200},
				},
			},
		},
	}

	merged := mergeResponses(responses)
	if len(merged) != 2 {
		t.Fatalf("len = %d, want 2", len(merged))
	}
	// Find ih1 in merged results.
	var m1 *MergedHit
	for i := range merged {
		if merged[i].InfoHash == hex.EncodeToString(ih1) {
			m1 = &merged[i]
			break
		}
	}
	if m1 == nil {
		t.Fatal("ih1 not found in merged")
	}
	// Rank: 300 + 200 = 500.
	if m1.Score != 500 {
		t.Errorf("ih1 Score = %d, want 500", m1.Score)
	}
	// Seeders: max(10, 15) = 15.
	if m1.Seeders != 15 {
		t.Errorf("ih1 Seeders = %d, want 15", m1.Seeders)
	}
	// First non-empty name wins.
	if m1.Name != "First" {
		t.Errorf("ih1 Name = %q, want 'First'", m1.Name)
	}
	// Sources: peer-A and peer-B.
	if len(m1.Sources) != 2 {
		t.Errorf("ih1 Sources = %v, want 2 entries", m1.Sources)
	}
}

func TestMergeResponses_NamePreference(t *testing.T) {
	t.Parallel()
	ihBytes := repeatBytes(0xff, 20)

	responses := []incomingResult{
		{
			peer: "peer-A",
			result: Result{
				TxID: 1,
				Hits: []Hit{
					{IH: ihBytes, N: "", Sz: 0, Rank: 100}, // empty name
				},
			},
		},
		{
			peer: "peer-B",
			result: Result{
				TxID: 1,
				Hits: []Hit{
					{IH: ihBytes, N: "GoodName", Sz: 500, Rank: 100},
				},
			},
		},
		{
			peer: "peer-C",
			result: Result{
				TxID: 1,
				Hits: []Hit{
					{IH: ihBytes, N: "LaterName", Sz: 600, Rank: 100},
				},
			},
		},
	}

	merged := mergeResponses(responses)
	if len(merged) != 1 {
		t.Fatalf("len = %d, want 1", len(merged))
	}
	// First non-empty name wins: peer-B's "GoodName".
	if merged[0].Name != "GoodName" {
		t.Errorf("Name = %q, want 'GoodName' (first non-empty)", merged[0].Name)
	}
	// First non-zero size wins: peer-B's 500.
	if merged[0].Size != 500 {
		t.Errorf("Size = %d, want 500 (first non-zero)", merged[0].Size)
	}
}

func TestMergeResponses_MaxSeeders(t *testing.T) {
	t.Parallel()
	ihBytes := repeatBytes(0xab, 20)

	responses := []incomingResult{
		{peer: "p1", result: Result{Hits: []Hit{{IH: ihBytes, S: 10, Rank: 100}}}},
		{peer: "p2", result: Result{Hits: []Hit{{IH: ihBytes, S: 50, Rank: 100}}}},
		{peer: "p3", result: Result{Hits: []Hit{{IH: ihBytes, S: 30, Rank: 100}}}},
	}

	merged := mergeResponses(responses)
	if len(merged) != 1 {
		t.Fatalf("len = %d, want 1", len(merged))
	}
	if merged[0].Seeders != 50 {
		t.Errorf("Seeders = %d, want 50 (max)", merged[0].Seeders)
	}
}

func TestMergeResponses_SortByScoreThenSeeders(t *testing.T) {
	t.Parallel()
	ih1 := repeatBytes(0x01, 20) // low score
	ih2 := repeatBytes(0x02, 20) // high score
	ih3 := repeatBytes(0x03, 20) // same score as ih2, fewer seeders

	responses := []incomingResult{
		{
			peer: "p1",
			result: Result{
				Hits: []Hit{
					{IH: ih1, N: "Low", S: 100, Rank: 100},
					{IH: ih2, N: "High", S: 50, Rank: 900},
					{IH: ih3, N: "TiedScore", S: 10, Rank: 900},
				},
			},
		},
	}

	merged := mergeResponses(responses)
	if len(merged) != 3 {
		t.Fatalf("len = %d, want 3", len(merged))
	}
	// Sorted: ih2 (score 900, seeders 50) > ih3 (score 900, seeders 10) > ih1 (score 100)
	if merged[0].Name != "High" {
		t.Errorf("merged[0].Name = %q, want 'High'", merged[0].Name)
	}
	if merged[1].Name != "TiedScore" {
		t.Errorf("merged[1].Name = %q, want 'TiedScore'", merged[1].Name)
	}
	if merged[2].Name != "Low" {
		t.Errorf("merged[2].Name = %q, want 'Low'", merged[2].Name)
	}
}

func TestMergeResponses_InvalidIHSkipped(t *testing.T) {
	t.Parallel()
	// An IH that's not 20 bytes should be skipped.
	responses := []incomingResult{
		{
			peer: "p1",
			result: Result{
				Hits: []Hit{
					{IH: []byte{0x01, 0x02}, N: "TooShort", Rank: 100}, // only 2 bytes
					{IH: repeatBytes(0xaa, 20), N: "Valid", Rank: 200},
				},
			},
		},
	}

	merged := mergeResponses(responses)
	if len(merged) != 1 {
		t.Fatalf("len = %d, want 1 (invalid IH skipped)", len(merged))
	}
	if merged[0].Name != "Valid" {
		t.Errorf("Name = %q, want 'Valid'", merged[0].Name)
	}
}

func TestMergeResponses_MatchesUnioned(t *testing.T) {
	t.Parallel()
	ihBytes := repeatBytes(0xdd, 20)

	responses := []incomingResult{
		{
			peer: "p1",
			result: Result{
				Hits: []Hit{
					{IH: ihBytes, N: "WithMatches", Rank: 100, Matches: []FileMatch{
						{FI: 0, FP: "a.txt"},
					}},
				},
			},
		},
		{
			peer: "p2",
			result: Result{
				Hits: []Hit{
					{IH: ihBytes, Rank: 100, Matches: []FileMatch{
						{FI: 3, FP: "b.txt"},
						{FI: 7, FP: "c.txt"},
					}},
				},
			},
		},
	}

	merged := mergeResponses(responses)
	if len(merged) != 1 {
		t.Fatalf("len = %d, want 1", len(merged))
	}
	if len(merged[0].Matches) != 3 {
		t.Errorf("Matches = %d, want 3 (union)", len(merged[0].Matches))
	}
}

// --- selectTargets integration with OnRemoteHandshake ---

func TestSelectTargets_ViaOnRemoteHandshake(t *testing.T) {
	t.Parallel()
	// Use the full New() constructor so the book is wired.
	p := New(silentLogger())

	// Simulate 3 peers completing handshakes. All go into "new"
	// via OnRemoteHandshake → book.AddNew.
	for i := 1; i <= 3; i++ {
		addr := addrN(i)
		p.NotePeerAdded(addr)
		p.OnRemoteHandshake(addr, &pp.ExtendedHandshakeMessage{
			M: map[pp.ExtensionName]pp.ExtensionNumber{
				ExtensionName: 11,
			},
		})
	}

	// No tried peers yet → selectTargets falls back to all supported.
	snap := p.KnownPeers()
	targets := p.selectTargets(snap)
	if len(targets) != 3 {
		t.Errorf("selectTargets (bootstrap) = %d, want 3 (all supported)", len(targets))
	}

	// Promote peer 1 to tried.
	p.PeerBook().Promote(addrN(1))
	snap = p.KnownPeers()
	targets = p.selectTargets(snap)
	// 1 tried + min(FeelerCount, 2 new) = 1 + 2 = 3.
	if len(targets) != 3 {
		t.Errorf("selectTargets (after promote) = %d, want 3", len(targets))
	}
}

// --- helpers ---

func addrN(n int) string {
	return "10.0.0." + itoa(n) + ":6881"
}

func itoa(n int) string {
	// Simple int-to-string without importing strconv.
	if n < 0 {
		return "-" + itoa(-n)
	}
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func repeatBytes(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func targetAddrs(targets []PeerState) map[string]bool {
	m := make(map[string]bool)
	for _, t := range targets {
		m[t.Addr] = true
	}
	return m
}
