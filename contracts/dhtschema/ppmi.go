package dhtschema

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

// PPMISaltSeed is the human-auditable ASCII seed for the PPMI (Publisher-Pointer
// Mutable Item) DHT salt. The actual salt is SHA256(seed) — a "double hash":
// SHA-256 turns the seed into the 32-byte salt, and the BEP-44 target then
// SHA-1s (pubkey || salt). Every SwartzNet publisher uses this same salt, so a
// passive DHT observer cannot fingerprint publishers by salt — discrimination
// is at the pubkey, never the salt.
const PPMISaltSeed = "snet.index"

// PPMISalt is SHA256(PPMISaltSeed) — the 32-byte BEP-44 salt for the Aggregate
// pointer. Frozen cross-implementation constant.
var PPMISalt = func() []byte {
	sum := sha256.Sum256([]byte(PPMISaltSeed))
	return sum[:]
}()

// MaxPPMIValueBytes is the BEP-44 mutable-item cap shared with the keyword item.
const MaxPPMIValueBytes = MaxValueBytes

// PPMIValue is the BEP-44 pointer to a publisher's merged (SNAGG) Aggregate
// index. It carries the companion torrent infohash plus the tree fingerprint
// commit, so a reader can verify the pointed-to tree matches the advertised
// commit before trusting its records. Frozen wire contract: bencode key order
// is alphabetical (commit, ih, next_pk, topics, ts); omitempty fields vanish
// when zero-length, so a minimal item is just ih + ts.
type PPMIValue struct {
	IH     []byte `bencode:"ih"`                // REQUIRED, exactly 20 bytes (companion torrent infohash)
	Commit []byte `bencode:"commit,omitempty"`  // 0 or 32 bytes == Trailer.TreeFingerprint
	Topics []byte `bencode:"topics,omitempty"`  // 0 or 32 bytes; reserved cuckoo-filter digest
	Ts     int64  `bencode:"ts"`                // REQUIRED unix seconds; stamped at encode if zero
	NextPk []byte `bencode:"next_pk,omitempty"` // 0 or 32 bytes; reserved key rotation, empty in v1
}

// EncodePPMI bencodes v after validating field widths. Ts is stamped with the
// current time when zero (so output is non-reproducible then — pin Ts for
// golden vectors). The ≤1000-byte cap is inclusive.
func EncodePPMI(v PPMIValue) ([]byte, error) {
	if len(v.IH) != 20 {
		return nil, fmt.Errorf("dhtschema: PPMI ih %d bytes, want 20", len(v.IH))
	}
	if l := len(v.Commit); l != 0 && l != 32 {
		return nil, fmt.Errorf("dhtschema: PPMI commit %d bytes, want 0 or 32", l)
	}
	if l := len(v.Topics); l != 0 && l != 32 {
		return nil, fmt.Errorf("dhtschema: PPMI topics %d bytes, want 0 or 32", l)
	}
	if l := len(v.NextPk); l != 0 && l != 32 {
		return nil, fmt.Errorf("dhtschema: PPMI next_pk %d bytes, want 0 or 32", l)
	}
	if v.Ts == 0 {
		v.Ts = time.Now().Unix()
	}
	out, err := bencode.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("dhtschema: encode PPMI: %w", err)
	}
	if len(out) > MaxPPMIValueBytes {
		return nil, fmt.Errorf("dhtschema: encoded PPMI %d bytes exceeds BEP-44 cap %d", len(out), MaxPPMIValueBytes)
	}
	return out, nil
}

// DecodePPMI parses an untrusted PPMI value. It rejects empty/oversize payloads
// BEFORE unmarshal (like the keyword decoder) and validates every field width.
func DecodePPMI(payload []byte) (PPMIValue, error) {
	if len(payload) == 0 {
		return PPMIValue{}, fmt.Errorf("dhtschema: empty PPMI value")
	}
	if len(payload) > MaxPPMIValueBytes {
		return PPMIValue{}, fmt.Errorf("dhtschema: PPMI value %d bytes exceeds BEP-44 cap %d", len(payload), MaxPPMIValueBytes)
	}
	var v PPMIValue
	if err := decodeBounded(payload, &v); err != nil {
		return v, fmt.Errorf("dhtschema: decode PPMI: %w", err)
	}
	if len(v.IH) != 20 {
		return v, fmt.Errorf("dhtschema: PPMI ih %d bytes, want 20", len(v.IH))
	}
	if l := len(v.Commit); l != 0 && l != 32 {
		return v, fmt.Errorf("dhtschema: PPMI commit %d bytes, want 0 or 32", l)
	}
	if l := len(v.Topics); l != 0 && l != 32 {
		return v, fmt.Errorf("dhtschema: PPMI topics %d bytes, want 0 or 32", l)
	}
	if l := len(v.NextPk); l != 0 && l != 32 {
		return v, fmt.Errorf("dhtschema: PPMI next_pk %d bytes, want 0 or 32", l)
	}
	return v, nil
}
