// Package admission is the deny-by-default publisher-admission gate.
//
// It replaces the legacy cold-start bootstrap whose placeholder
// policy admitted any unknown publisher: unknown pubkeys score the
// neutral prior 0.5, and the legacy rules used `Score >= 0.3` for
// crawl candidates and `Score >= 0.5` for endorsers, so every
// valid-sig publisher cleared the bar with zero vetting and three
// freshly-generated Sybil endorsers cleared the endorsement
// threshold for free.
//
// The inversion here is fail-closed: a candidate is admitted ONLY
// when a typed Rule affirmatively clears it. The composed default
// (DefaultPolicy) denies an unknown 0.5-scored candidate and denies
// three fresh 0.5-scored endorsers. "Strong" earned signal is
// strictly ABOVE the unknown prior (Score > 0.5) or a seed/anchor
// membership — a fresh 0.5 never counts.
//
// Boundary: this package consumes reputation semantics through the
// narrow ReputationView interface and knows nothing of HTTP, the
// wire, Bleve, or the DHT. Its count accessors (AnchorCount,
// AdmittedCount, PendingCount) satisfy the httpapi /aggregate probe
// interface structurally, so httpapi keeps its zero-subsystem-import
// law.
//
// Slice 5 lands the engine, the deny-by-default policy, and the
// count accessors. The live feeder channels (anchor PPMI fetch,
// BEP-51 crawl, endorsement gossip) arrive in Slice 12; the public
// ingest methods (AdmitAnchor, CandidateFromCrawl, IngestEndorsement)
// are shaped now so Slice 12 wires channels without re-architecting.
package admission
