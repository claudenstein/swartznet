package daemon

import "testing"

// TestBootstrapEndorsementSinkRoutes — the adapter forwards
// NoteEndorsement(endorser, candidate) to Bootstrap.IngestEndorsement.
// 1-line wrapper; this exists so coverage pinpoints the type
// (struct{boot *Bootstrap}) for callers grepping the codebase.
func TestBootstrapEndorsementSinkRoutes(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	boot, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)
	sink := bootstrapEndorsementSink{boot: boot}

	endorser := pubkeyBytes("e-1")
	cand := pubkeyBytes("c-1")
	sink.NoteEndorsement(endorser, cand)

	if !boot.IsPending(cand) {
		t.Error("after NoteEndorsement, candidate must be pending on the bootstrap")
	}
}

// TestBootstrapPublisherObserverRoutes — the adapter forwards
// NotePublisherSeen(pubkey) to Bootstrap.CandidateFromCrawl
// with sigValid=true (per-record sigs are already verified by
// the swarmsearch handler).
func TestBootstrapPublisherObserverRoutes(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	boot, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)
	obs := bootstrapPublisherObserver{boot: boot}

	pub := pubkeyBytes("publisher-seen")
	obs.NotePublisherSeen(pub)

	// Without bloom/tracker the candidate stays pending (admission
	// gate doesn't clear), but it must be in the observed set.
	if !boot.IsPending(pub) {
		t.Error("after NotePublisherSeen, pubkey must be pending on the bootstrap")
	}
}
