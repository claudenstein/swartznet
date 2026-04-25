package swarmsearch

import (
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"testing"
)

// TestOnSyncRecordsRoutesValidRecordsToSink — happy-path
// success arm of onSyncRecords. Build a session in
// PhaseSymbolsFlowing (ApplyBegin then ProduceSymbols
// advances it), feed it a SyncRecords frame with one
// well-formed record signed by a real key, and verify the
// recordSink's Add was called.
func TestOnSyncRecordsRoutesValidRecordsToSink(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())

	// Wire a recording sink.
	cache := NewRecordCache()
	p.SetRecordSink(cache)

	// Build a session that's in a phase where ApplyRecords
	// won't reject (PhaseSymbolsFlowing).
	sess := NewSyncSession(1, RoleInitiator, nil)
	if _, err := sess.Begin(SyncFilter{}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	p.registerSyncSession("p:hh", sess)

	// Mint a real signed record. Use the test-only
	// signingMessage helper (publisher_observer_test.go) which
	// mirrors the production verifyLocalRecordSig construction.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var rec LocalRecord
	copy(rec.Pk[:], pub)
	rec.Kw = "ubuntu"
	rec.Ih[0] = 0xAB
	rec.T = 100
	sig := ed25519.Sign(priv, signingMessage(rec))
	copy(rec.Sig[:], sig)

	wireRec := SyncRecord{
		Pk:  rec.Pk[:],
		Kw:  rec.Kw,
		Ih:  rec.Ih[:],
		T:   rec.T,
		Pow: rec.Pow,
		Sig: rec.Sig[:],
	}
	p.onSyncRecords("p:hh", SyncRecords{TxID: 1, Records: []SyncRecord{wireRec}})

	if cache.Len() != 1 {
		t.Errorf("sink received %d records, want 1", cache.Len())
	}
}
