// Publisher-Pointer Mutable Item (PPMI) — the per-publisher
// BEP-44 primitive that replaces per-keyword items in the
// "Aggregate" redesign.
//
// See docs/research/PROPOSAL.md §2.1 and docs/research/SPEC.md §3.1
// for the normative spec. Summary:
//
//   target = SHA1(publisher_pubkey || PPMISalt)
//   v      = bencoded PPMIValue { ih, commit, topics?, ts, next_pk? }
//
// PPMISalt is a fixed SHA-256 hash of the bytestring "snet.index"
// (double-hashing per SPEC §1 of track A/C). A passive DHT-node
// observer learns nothing about which publishers are SwartzNet
// publishers just by watching salts stream past — every
// SwartzNet publisher uses the same salt, so the per-publisher
// discrimination happens at the pubkey, not the salt.

package dhtindex

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

// PPMISaltSeed is the plaintext bytestring fed into SHA-256 to
// produce PPMISalt. Kept as a named constant so auditors can
// reproduce the derivation without having to hexdump binaries.
const PPMISaltSeed = "snet.index"

// PPMISalt is the 32-byte salt used for every SwartzNet PPMI
// BEP-44 target. Computed once at package init from
// SHA256(PPMISaltSeed).
var PPMISalt = func() []byte {
	sum := sha256.Sum256([]byte(PPMISaltSeed))
	return sum[:]
}()

// PPMIValue is the bencoded payload stored under a publisher's
// PPMI target. It is deliberately small — well under the 1000-byte
// BEP-44 value cap — so a BEP-44 put succeeds on storage nodes
// that enforce tight size limits.
//
// The heavy index data lives in the companion torrent whose
// infohash is IH. A reader fetches the PPMI, verifies the
// BEP-44 signature, fetches the companion torrent, and verifies
// Commit matches SHA-256 over the canonical record stream in
// the trailer.
type PPMIValue struct {
	// IH is the BEP-3 infohash of the publisher's current
	// companion index torrent.
	IH []byte `bencode:"ih"`

	// Commit binds this pointer to a specific record set. Equal
	// to companion.Trailer.TreeFingerprint — which is
	// SHA256(canonical record stream) computed over the sorted
	// leaf records. Empty only during dual-write migration
	// (PROPOSAL §6 phase 1).
	Commit []byte `bencode:"commit,omitempty"`

	// Topics is an optional 32-byte cuckoo-filter digest
	// summarising the keyword prefixes this publisher covers.
	// Readers MAY use it to skip publishers whose topics don't
	// overlap a query. Empty when the publisher chose not to
	// publish a summary.
	Topics []byte `bencode:"topics,omitempty"`

	// Ts is the unix timestamp at which this snapshot was
	// generated. Informational; BEP-44's seq is the canonical
	// ordering.
	Ts int64 `bencode:"ts"`

	// NextPk is reserved for Tor-v3-style key rotation, mirroring
	// the KeywordValue field. Empty in v1; rotation logic lands
	// in v1.1.
	NextPk []byte `bencode:"next_pk,omitempty"`
}

// MaxPPMIValueBytes is the BEP-44 cap. PPMI values are much
// smaller in practice (~100 bytes) but we enforce the ceiling for
// defence-in-depth; a future field addition MUST NOT push the
// encoded value past this.
const MaxPPMIValueBytes = MaxValueBytes

// EncodePPMI serialises a PPMIValue. Fills Ts from the clock when
// the caller left it zero so every written item carries a fresh
// timestamp.
func EncodePPMI(v PPMIValue) ([]byte, error) {
	if len(v.IH) != 20 {
		return nil, fmt.Errorf("dhtindex: PPMI ih %d bytes, want 20", len(v.IH))
	}
	if len(v.Commit) != 0 && len(v.Commit) != 32 {
		return nil, fmt.Errorf("dhtindex: PPMI commit %d bytes, want 0 or 32", len(v.Commit))
	}
	if len(v.Topics) != 0 && len(v.Topics) != 32 {
		return nil, fmt.Errorf("dhtindex: PPMI topics %d bytes, want 0 or 32", len(v.Topics))
	}
	if len(v.NextPk) != 0 && len(v.NextPk) != 32 {
		return nil, fmt.Errorf("dhtindex: PPMI next_pk %d bytes, want 0 or 32", len(v.NextPk))
	}
	if v.Ts == 0 {
		v.Ts = time.Now().Unix()
	}
	out, err := bencode.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("dhtindex: encode PPMI: %w", err)
	}
	if len(out) > MaxPPMIValueBytes {
		return nil, fmt.Errorf("dhtindex: encoded PPMI %d bytes exceeds BEP-44 cap %d",
			len(out), MaxPPMIValueBytes)
	}
	return out, nil
}

// DecodePPMI parses a bencoded PPMI value retrieved from the DHT.
// Size fields are validated; the caller is still responsible for
// verifying the BEP-44 signature at the transport layer and for
// fetching the companion torrent + verifying Commit once it
// arrives.
func DecodePPMI(payload []byte) (PPMIValue, error) {
	if len(payload) == 0 {
		return PPMIValue{}, errors.New("dhtindex: empty PPMI value")
	}
	var v PPMIValue
	if err := bencode.Unmarshal(payload, &v); err != nil {
		return v, fmt.Errorf("dhtindex: decode PPMI: %w", err)
	}
	if len(v.IH) != 20 {
		return v, fmt.Errorf("dhtindex: decoded PPMI ih %d bytes, want 20", len(v.IH))
	}
	if len(v.Commit) != 0 && len(v.Commit) != 32 {
		return v, fmt.Errorf("dhtindex: decoded PPMI commit %d bytes, want 0 or 32", len(v.Commit))
	}
	if len(v.Topics) != 0 && len(v.Topics) != 32 {
		return v, fmt.Errorf("dhtindex: decoded PPMI topics %d bytes, want 0 or 32", len(v.Topics))
	}
	if len(v.NextPk) != 0 && len(v.NextPk) != 32 {
		return v, fmt.Errorf("dhtindex: decoded PPMI next_pk %d bytes, want 0 or 32", len(v.NextPk))
	}
	return v, nil
}

// EstimatePPMISize returns the bencoded length of v. Cheap
// helper used by callers that want to fail-fast before hitting
// the DHT.
func EstimatePPMISize(v PPMIValue) int {
	out, err := bencode.Marshal(v)
	if err != nil {
		return MaxPPMIValueBytes + 1
	}
	return len(out)
}
