// Package signing implements infohash-preserving .torrent signing: the
// signature rides in two OPTIONAL top-level keys (snet.pubkey / snet.sig),
// never inside the info dict — moving them inside would fork the swarm. All
// work happens on raw bytes via the contracts codecs, so the info dict never
// round-trips a typed struct (a typed round-trip would change the infohash
// and break everything).
package signing

import (
	"crypto/sha1"
	"fmt"

	abencode "github.com/anacrolix/torrent/bencode"

	"github.com/swartznet/swartznet/contracts/bencode"
	"github.com/swartznet/swartznet/contracts/sign"
	"github.com/swartznet/swartznet/internal/identity"
)

// Re-exported sentinels: consumers use errors.Is against these.
var (
	ErrNotSigned    = sign.ErrNotSigned
	ErrBadSignature = sign.ErrBadSignature
)

// Sign signs raw .torrent bytes with the node identity, returning the signed
// bytes. Existing snet.* keys are REPLACED — re-signing overwrites; there is
// no refusal. Only the top-level key set changes; the info value bytes pass
// through verbatim, so signed and unsigned twins share one infohash.
func Sign(raw []byte, signer identity.Signer) ([]byte, error) {
	top, err := bencode.DecodeDict(raw)
	if err != nil {
		return nil, fmt.Errorf("signing: decode metainfo: %w", err)
	}
	infoRaw, ok := top["info"]
	if !ok || len(infoRaw) == 0 {
		return nil, fmt.Errorf("signing: metainfo missing info dict")
	}
	infoHash := sha1.Sum(infoRaw)
	sigBytes := signer.Sign(sign.Payload(infoHash))
	pub := signer.Public()

	pubEnc, err := abencode.Marshal(string(pub))
	if err != nil {
		return nil, fmt.Errorf("signing: marshal pubkey: %w", err)
	}
	sigEnc, err := abencode.Marshal(string(sigBytes))
	if err != nil {
		return nil, fmt.Errorf("signing: marshal sig: %w", err)
	}
	top["snet.pubkey"] = bencode.Bytes(pubEnc)
	top["snet.sig"] = bencode.Bytes(sigEnc)

	out, err := bencode.EncodeDict(top)
	if err != nil {
		return nil, fmt.Errorf("signing: re-encode: %w", err)
	}
	return out, nil
}

// Verify checks raw .torrent bytes. The taxonomy (SPEC §5.6) is three-way:
//   - no/partial snet fields → ErrNotSigned (benign; zero Signature)
//   - undecodable fields or wrong lengths → plain errors, NEITHER sentinel
//   - crypto failure → ErrBadSignature with the Signature still populated
//   - success → the Signature and nil
func Verify(raw []byte) (sign.Signature, error) {
	top, err := bencode.DecodeDict(raw)
	if err != nil {
		return sign.Signature{}, fmt.Errorf("signing: decode metainfo: %w", err)
	}
	infoRaw, ok := top["info"]
	if !ok || len(infoRaw) == 0 {
		// Deliberately BEFORE the not-signed check: a dict with snet fields
		// but no info is malformed, not merely unsigned.
		return sign.Signature{}, fmt.Errorf("signing: metainfo missing info dict")
	}
	pubRaw, pubOK := top["snet.pubkey"]
	sigRaw, sigOK := top["snet.sig"]
	if !pubOK || !sigOK {
		// One key without the other is treated as unsigned too.
		return sign.Signature{}, ErrNotSigned
	}
	var pub string
	if err := abencode.Unmarshal(pubRaw, &pub); err != nil {
		return sign.Signature{}, fmt.Errorf("signing: decode pubkey: %w", err)
	}
	var sigStr string
	if err := abencode.Unmarshal(sigRaw, &sigStr); err != nil {
		return sign.Signature{}, fmt.Errorf("signing: decode sig: %w", err)
	}
	return sign.Verify([]byte(pub), []byte(sigStr), sha1.Sum(infoRaw))
}
