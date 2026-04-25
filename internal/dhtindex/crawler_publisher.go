package dhtindex

import (
	"errors"

	"github.com/swartznet/swartznet/internal/signing"
)

// PublisherFromMetainfo extracts the publisher pubkey from a
// raw .torrent metainfo byte slice, classified into the shape
// daemon.Bootstrap.CandidateFromCrawl expects.
//
// Return contract:
//
//	(pk, true,  nil)     metainfo is signed and the signature verifies
//	(pk, false, nil)     metainfo is signed but the signature fails
//	([32]{}, false, nil) metainfo is unsigned (no snet.pubkey field)
//	([32]{}, false, err) malformed metainfo (bencode decode failed)
//
// A Channel-B crawler typically calls this for each infohash it
// discovers via SampleInfohashes, then forwards the non-zero
// pubkey into Bootstrap.CandidateFromCrawl(pk, sigValid). The
// Bootstrap layer handles the admission decision; this helper
// just does the classification.
//
// The unsigned case is *not* an error — most torrents on the
// mainline DHT are unsigned, so surfacing them as errors would
// drown the crawler's logs.
func PublisherFromMetainfo(raw []byte) (pk [32]byte, sigValid bool, err error) {
	sig, verr := signing.VerifyBytes(raw)
	if verr == nil {
		// Signed + valid: copy the 32-byte pubkey out of the
		// Signature struct.
		pk = sig.PubKey
		sigValid = true
		return pk, sigValid, nil
	}
	if errors.Is(verr, signing.ErrNotSigned) {
		// No snet.pubkey field — swallow and return a zero
		// pubkey so the caller can cheaply filter out the
		// (vast) majority of unsigned torrents.
		return [32]byte{}, false, nil
	}
	if errors.Is(verr, signing.ErrBadSignature) {
		// Signed but signature fails. We still return the
		// claimed pubkey so downstream reputation can log /
		// demote the publisher. The caller MUST pass
		// sigValid=false to Bootstrap.CandidateFromCrawl so
		// the admission-threshold check stays honest.
		pk = sig.PubKey
		sigValid = false
		return pk, sigValid, nil
	}
	// Anything else — malformed metainfo, truncated bencode —
	// bubbles up so operators can spot mis-serving nodes in
	// crawler logs.
	return [32]byte{}, false, verr
}
