package swarmsearch

import (
	"crypto/ed25519"
	"crypto/rand"
	"sync"
	"testing"
)

// capturePublisherObserver records every NotePublisherSeen call.
type capturePublisherObserver struct {
	mu   sync.Mutex
	seen []([32]byte)
}

func (c *capturePublisherObserver) NotePublisherSeen(pubkey [32]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, pubkey)
}

func (c *capturePublisherObserver) snapshot() [][32]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][32]byte, len(c.seen))
	copy(out, c.seen)
	return out
}

// mkSignedSyncRecord produces a wire-format SyncRecord whose
// signature verifies under the returned pubkey. Useful for
// handler-level tests that exercise ingestSyncRecords.
func mkSignedSyncRecord(t *testing.T, kw string, ihByte byte, ts int64) (SyncRecord, [32]byte) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var pk [32]byte
	copy(pk[:], pub)

	var local LocalRecord
	local.Pk = pk
	local.Kw = kw
	local.Ih[0] = ihByte
	local.T = ts
	// Sign.
	// The swarmsearch verifyLocalRecordSig expects the canonical
	// message, which the test file constructs inline.
	msg := signingMessage(local)
	sig := ed25519.Sign(priv, msg)
	copy(local.Sig[:], sig)

	wr := SyncRecord{
		Pk:  pk[:],
		Kw:  kw,
		Ih:  local.Ih[:],
		T:   ts,
		Pow: 0,
		Sig: local.Sig[:],
	}
	return wr, pk
}

// signingMessage mirrors verifyLocalRecordSig's message construction
// so tests can produce valid signatures without exporting the
// helper from the package.
func signingMessage(r LocalRecord) []byte {
	msg := make([]byte, 0, 32+len(r.Kw)+20+8+10)
	msg = append(msg, r.Pk[:]...)
	msg = append(msg, r.Kw...)
	msg = append(msg, r.Ih[:]...)
	var ts [8]byte
	for i := 0; i < 8; i++ {
		ts[i] = byte(r.T >> (8 * i))
	}
	msg = append(msg, ts[:]...)
	// Pow is 0 (varint encodes to a single byte 0x00); same in
	// the ingestion path's verifyLocalRecordSig.
	msg = append(msg, 0x00)
	return msg
}

// Setter/getter round-trip.
func TestSetPublisherObserver(t *testing.T) {
	p := New(nil)
	if p.PublisherObserver() != nil {
		t.Fatal("fresh Protocol should have no PublisherObserver")
	}
	obs := &capturePublisherObserver{}
	p.SetPublisherObserver(obs)
	if p.PublisherObserver() != obs {
		t.Error("PublisherObserver() should return attached")
	}
	p.SetPublisherObserver(nil)
	if p.PublisherObserver() != nil {
		t.Error("nil should detach")
	}
}

// ingestSyncRecords routes pubkeys to the observer when attached.
// De-duplicates within one frame.
func TestIngestSyncRecordsNotifiesPublisher(t *testing.T) {
	p := New(nil)
	obs := &capturePublisherObserver{}
	p.SetPublisherObserver(obs)
	sink := NewRecordCache()
	p.SetRecordSink(sink)

	// Two valid records signed by the same pubkey + one valid
	// record signed by a different pubkey → observer should see
	// TWO distinct pubkeys, once each.
	r1, pkA := mkSignedSyncRecord(t, "k1", 0x01, 1)
	r2 := SyncRecord{Pk: r1.Pk, Kw: r1.Kw, Ih: r1.Ih, T: r1.T, Sig: r1.Sig} // duplicate key
	r3, pkB := mkSignedSyncRecord(t, "k3", 0x03, 3)

	p.ingestSyncRecords("peer-x", sink, []SyncRecord{r1, r2, r3})

	got := obs.snapshot()
	if len(got) != 2 {
		t.Fatalf("observer saw %d pubkeys, want 2 (de-duped)", len(got))
	}
	// Order isn't guaranteed; assert set membership.
	seen := map[[32]byte]bool{}
	for _, p := range got {
		seen[p] = true
	}
	if !seen[pkA] || !seen[pkB] {
		t.Errorf("expected both pkA and pkB observed; got %v", seen)
	}
}

// A record with a bad signature does NOT trigger NotePublisherSeen.
// Signature is the filter; unsigned junk can't admit a pubkey
// into the admission pipeline.
func TestIngestSyncRecordsBadSigSkipsObserver(t *testing.T) {
	p := New(nil)
	obs := &capturePublisherObserver{}
	p.SetPublisherObserver(obs)
	sink := NewRecordCache()
	p.SetRecordSink(sink)

	r, _ := mkSignedSyncRecord(t, "x", 0x01, 1)
	r.Sig[0] ^= 0xFF // tamper

	p.ingestSyncRecords("peer-y", sink, []SyncRecord{r})

	if n := len(obs.snapshot()); n != 0 {
		t.Errorf("observer saw %d pubkeys after bad-sig frame; want 0", n)
	}
	if sink.Len() != 0 {
		t.Errorf("cache should be empty (record dropped), has %d", sink.Len())
	}
}

// nil observer is safe.
func TestIngestSyncRecordsNilObserver(t *testing.T) {
	p := New(nil)
	// No observer attached.
	sink := NewRecordCache()
	p.SetRecordSink(sink)

	r, _ := mkSignedSyncRecord(t, "x", 0x01, 1)
	// Must not panic.
	p.ingestSyncRecords("peer-z", sink, []SyncRecord{r})

	if sink.Len() != 1 {
		t.Errorf("cache len = %d, want 1 (record should still ingest)", sink.Len())
	}
}
