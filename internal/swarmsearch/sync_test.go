package swarmsearch

import (
	"crypto/ed25519"
	"strconv"
	"testing"
	"time"

	"github.com/swartznet/swartznet/contracts/ltepwire"
	"github.com/swartznet/swartznet/contracts/record"
)

// signRec builds a validly-signed record for keyword kw under (priv,pub).
func signRec(t *testing.T, priv ed25519.PrivateKey, pub [32]byte, kw string, ihByte byte) LocalRecord {
	t.Helper()
	var ih [20]byte
	ih[0] = ihByte
	r, err := record.SignAndMine(priv, pub, kw, ih, 1712649600, 0)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// reconCaps returns capabilities that advertise reconciliation (bit 9).
func reconCaps() Capabilities {
	return Capabilities{
		Sharing:  ltepwire.Sharing{ShareLocal: 2, FileHits: true, ContentHits: true},
		Services: 0x2ED, // includes BitSetReconciliation
	}
}

// waitRecon blocks until both peers have processed each other's peer_announce
// and see the reconciliation capability.
func waitRecon(t *testing.T, a, b *Protocol, aAddr, bAddr string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if a.peerHasReconciliation(bAddr) && b.peerHasReconciliation(aAddr) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("peers never advertised reconciliation to each other")
}

// TestSyncReconcileBidirectional: A and B each hold records the other lacks;
// after StartSync both caches converge to the union.
func TestSyncReconcileBidirectional(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	var pk [32]byte
	copy(pk[:], pub)

	h := newHarness()
	a, b := New(testLog()), New(testLog())
	defer a.Close()
	defer b.Close()
	h.peers["A"], h.peers["B"] = a, b
	a.SetCapabilitySource(reconCaps)
	b.SetCapabilitySource(reconCaps)
	cacheA, cacheB := NewRecordCache(), NewRecordCache()
	a.SetRecordSource(cacheA)
	a.SetRecordSink(cacheA)
	b.SetRecordSource(cacheB)
	b.SetRecordSink(cacheB)

	// Shared records + A-only + B-only.
	for i := 0; i < 5; i++ {
		r := signRec(t, priv, pk, "shared"+strconv.Itoa(i), byte(i))
		cacheA.Add(r)
		cacheB.Add(r)
	}
	for i := 0; i < 4; i++ {
		cacheA.Add(signRec(t, priv, pk, "aonly"+strconv.Itoa(i), byte(100+i)))
	}
	for i := 0; i < 3; i++ {
		cacheB.Add(signRec(t, priv, pk, "bonly"+strconv.Itoa(i), byte(200+i)))
	}
	// union = 5 + 4 + 3 = 12

	h.connect("A", "B")
	waitRecon(t, a, b, "A", "B")

	sess, err := a.StartSync("B", ltepwire.SyncFilter{}, cacheA.Snapshot())
	if err != nil {
		t.Fatalf("StartSync: %v", err)
	}
	if sess == nil {
		t.Fatal("nil session")
	}

	// Poll until both caches reach the union.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cacheA.Len() == 12 && cacheB.Len() == 12 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cacheA.Len() != 12 {
		t.Errorf("cacheA converged to %d, want 12", cacheA.Len())
	}
	if cacheB.Len() != 12 {
		t.Errorf("cacheB converged to %d, want 12", cacheB.Len())
	}
}

// TestSyncReconcileLargeMultiBatch is the DoD: a 250-record symmetric
// difference converges, and the responder streams MORE than one symbol batch.
func TestSyncReconcileLargeMultiBatch(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	var pk [32]byte
	copy(pk[:], pub)

	h := newHarness()
	a, b := New(testLog()), New(testLog())
	defer a.Close()
	defer b.Close()
	h.peers["A"], h.peers["B"] = a, b
	a.SetCapabilitySource(reconCaps)
	b.SetCapabilitySource(reconCaps)
	cacheA, cacheB := NewRecordCache(), NewRecordCache()
	a.SetRecordSource(cacheA)
	a.SetRecordSink(cacheA)
	b.SetRecordSource(cacheB)
	b.SetRecordSink(cacheB)

	// 100 shared, 125 A-only, 125 B-only (250-record symmetric diff).
	for i := 0; i < 100; i++ {
		r := signRec(t, priv, pk, "shared"+strconv.Itoa(i), byte(i))
		cacheA.Add(r)
		cacheB.Add(r)
	}
	for i := 0; i < 125; i++ {
		cacheA.Add(signRec(t, priv, pk, "aonly"+strconv.Itoa(i), byte(i)))
	}
	for i := 0; i < 125; i++ {
		cacheB.Add(signRec(t, priv, pk, "bonly"+strconv.Itoa(i), byte(i)))
	}
	// union = 100 + 125 + 125 = 350

	h.connect("A", "B")
	waitRecon(t, a, b, "A", "B")

	sess, err := a.StartSync("B", ltepwire.SyncFilter{}, cacheA.Snapshot())
	if err != nil {
		t.Fatalf("StartSync: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cacheA.Len() == 350 && cacheB.Len() == 350 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cacheA.Len() != 350 || cacheB.Len() != 350 {
		t.Fatalf("converged to A=%d B=%d, want 350/350", cacheA.Len(), cacheB.Len())
	}
	_ = sess
	// The responder must have streamed more than one 100-symbol batch.
	bsess := b.lookupSyncSession("A", sess.TxID())
	if bsess != nil && bsess.SymbolsOut() <= ltepwire.MaxSymbolsPerMessage {
		t.Errorf("responder streamed %d symbols, want multi-batch (>%d)", bsess.SymbolsOut(), ltepwire.MaxSymbolsPerMessage)
	}
}

// TestConvergeFloorAndOnceGuard: the initiator does not finalize until it has
// applied convergeSymbolFloor symbols (avoiding premature convergence on a
// short prefix) and then fires exactly once.
func TestConvergeFloorAndOnceGuard(t *testing.T) {
	s := NewSyncSession(1, RoleInitiator, nil) // empty local set → decoder trivially "converged"
	_ = s.ApplyBegin(ltepwire.SyncBegin{TxID: 1, ElementSize: 32})
	var b [32]byte
	feed := func(n int) {
		for i := 0; i < n; i++ {
			idx := func() int { s.mu.Lock(); defer s.mu.Unlock(); return s.symbolsIn }()
			_ = s.ApplySymbols(ltepwire.SyncSymbols{TxID: 1, Index: idx, Symbols: []ltepwire.SyncSymbol{{C: 0, B: b[:]}}})
		}
	}
	// Below the floor: even though the decoder is "converged", do not finalize.
	feed(convergeSymbolFloor - 1)
	if s.ShouldInitiatorConverge() {
		t.Fatal("finalized below the symbol floor (premature convergence)")
	}
	// At/above the floor: finalize exactly once.
	feed(2)
	if !s.ShouldInitiatorConverge() {
		t.Fatal("did not finalize after the floor")
	}
	if s.ShouldInitiatorConverge() {
		t.Error("finalized twice (once-guard broken)")
	}
}

// TestDoneFlagFinalizes: even below the floor, a done=1 batch finalizes (the
// responder exhausted its budget; no more symbols are coming).
func TestDoneFlagFinalizes(t *testing.T) {
	s := NewSyncSession(1, RoleInitiator, nil)
	_ = s.ApplyBegin(ltepwire.SyncBegin{TxID: 1, ElementSize: 32})
	var b [32]byte
	_ = s.ApplySymbols(ltepwire.SyncSymbols{TxID: 1, Index: 0, Done: true, Symbols: []ltepwire.SyncSymbol{{C: 0, B: b[:]}}})
	if !s.ShouldInitiatorConverge() {
		t.Error("a done=1 batch must finalize even below the floor")
	}
}

// TestStartSyncErrors covers the initiator gate order.
func TestStartSyncErrors(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	if _, err := p.StartSync("nobody", ltepwire.SyncFilter{}, nil); err != ErrSyncPeerUnknown {
		t.Errorf("unknown peer = %v, want ErrSyncPeerUnknown", err)
	}
	// Known but no reconciliation capability.
	h := newHarness()
	a, b := p, New(testLog())
	defer b.Close()
	h.peers["A"], h.peers["B"] = a, b
	h.connect("A", "B") // sets Supported + token, but NOT bit 9
	if _, err := a.StartSync("B", ltepwire.SyncFilter{}, nil); err != ErrSyncCapabilityMissing {
		t.Errorf("no-cap peer = %v, want ErrSyncCapabilityMissing", err)
	}
}

// TestSyncFrameWithoutCapabilityCharged: a sync frame from a peer that never
// advertised bit 9 is rejected + charged.
func TestSyncFrameWithoutCapabilityCharged(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	p.OnRemoteHandshake("peer", true, 1) // supported, but no peer_announce → no bit 9
	begin, _ := ltepwire.EncodeSyncBegin(ltepwire.SyncBegin{TxID: 1, LocalCount: 0})
	var rj *ltepwire.Reject
	p.HandleMessage("peer", begin, captureReject(t, &rj))
	if rj == nil || rj.Code != ltepwire.RejectUnsupportedScope {
		t.Fatalf("sync without cap should reject code 2, got %+v", rj)
	}
	if p.ban.Score("peer") != ScoreUnexpectedMessage {
		t.Errorf("score = %d, want %d", p.ban.Score("peer"), ScoreUnexpectedMessage)
	}
}

// TestBudgetNegotiatesDownward: min(peer, own).
func TestBudgetNegotiatesDownward(t *testing.T) {
	s := NewSyncSession(1, RoleResponder, nil)
	if err := s.ApplyBegin(ltepwire.SyncBegin{TxID: 1, ElementSize: 32, MaxSymbols: 500, MaxBytes: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	if s.MaxSymbols() != 500 {
		t.Errorf("maxSymbols = %d, want 500 (lowered)", s.MaxSymbols())
	}
	// A higher peer budget does not raise our own.
	s2 := NewSyncSession(2, RoleResponder, nil)
	_ = s2.ApplyBegin(ltepwire.SyncBegin{TxID: 2, ElementSize: 32, MaxSymbols: 9000})
	if s2.MaxSymbols() != ltepwire.DefaultSyncMaxSymbols {
		t.Errorf("maxSymbols = %d, want %d (own default, not raised)", s2.MaxSymbols(), ltepwire.DefaultSyncMaxSymbols)
	}
}

// TestApplySymbolsReordersAndBuffers: an out-of-order batch is buffered and
// applied once the gap fills (tolerating the async per-frame dispatch); a
// duplicate is dropped; a gap larger than the reorder buffer aborts.
func TestApplySymbolsReordersAndBuffers(t *testing.T) {
	s := NewSyncSession(1, RoleInitiator, nil)
	_ = s.ApplyBegin(ltepwire.SyncBegin{TxID: 1, ElementSize: 32})
	var b [32]byte
	batch := func(idx int) ltepwire.SyncSymbols {
		return ltepwire.SyncSymbols{TxID: 1, Index: idx, Symbols: []ltepwire.SyncSymbol{{C: 0, B: b[:]}}}
	}
	// Batch at index 1 arrives BEFORE index 0 → buffered, no error, symbolsIn stays 0.
	if err := s.ApplySymbols(batch(1)); err != nil {
		t.Fatalf("out-of-order batch should buffer, got %v", err)
	}
	if got := func() int { s.mu.Lock(); defer s.mu.Unlock(); return s.symbolsIn }(); got != 0 {
		t.Fatalf("symbolsIn = %d before the gap fills, want 0", got)
	}
	// Index 0 arrives → applies 0 AND drains the buffered index 1.
	if err := s.ApplySymbols(batch(0)); err != nil {
		t.Fatal(err)
	}
	if got := func() int { s.mu.Lock(); defer s.mu.Unlock(); return s.symbolsIn }(); got != 2 {
		t.Errorf("symbolsIn = %d after drain, want 2", got)
	}
	// A stale/duplicate batch (index < symbolsIn) is dropped idempotently.
	if err := s.ApplySymbols(batch(0)); err != nil {
		t.Errorf("duplicate should drop silently, got %v", err)
	}
	// A gap larger than the reorder buffer aborts.
	s2 := NewSyncSession(2, RoleInitiator, nil)
	_ = s2.ApplyBegin(ltepwire.SyncBegin{TxID: 2, ElementSize: 32})
	var lastErr error
	for i := 1; i <= maxPendingSymbolBatches+2; i++ {
		lastErr = s2.ApplySymbols(ltepwire.SyncSymbols{TxID: 2, Index: i * 10, Symbols: []ltepwire.SyncSymbol{{C: 0, B: b[:]}}})
	}
	if lastErr != ErrSyncDesync {
		t.Errorf("over-full reorder buffer = %v, want ErrSyncDesync", lastErr)
	}
}
