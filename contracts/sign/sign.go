// Package sign is the frozen torrent-signing contract: the 34-byte signing
// payload, the ed25519 primitives over it, and the three-way verification
// taxonomy every UI keys on. Stdlib-only; knows nothing of bencode or files.
//
// Versioning is a new domain prefix ("SN-TORRENT-V2|") plus new field names —
// never in-band negotiation.
package sign

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
)

// Domain is the signing domain prefix. The trailing pipe is decorative but
// byte-exact: the payload is exactly 34 bytes.
const Domain = "SN-TORRENT-V1|"

// ErrNotSigned reports a torrent without snet signature fields — a benign,
// normal outcome, not a tamper signal.
var ErrNotSigned = errors.New("signing: torrent is not signed")

// ErrBadSignature reports a signature that fails cryptographic verification
// — a firm tamper signal. The accompanying Signature is still fully
// populated so UIs can show the claimed key.
var ErrBadSignature = errors.New("signing: signature does not verify")

// Signature is a parsed torrent signature. PubKeyHex's lowercase form is the
// publisher identity used across sessions, trust lists, and search.
type Signature struct {
	PubKey   [32]byte
	Sig      [64]byte
	InfoHash [20]byte
}

// PubKeyHex returns the public key as 64 lowercase hex characters.
func (s Signature) PubKeyHex() string {
	return hex.EncodeToString(s.PubKey[:])
}

// Payload builds the exact 34-byte signing payload:
// "SN-TORRENT-V1|" ‖ SHA1(info-dict bytes).
func Payload(infoHash [20]byte) []byte {
	out := make([]byte, 0, len(Domain)+len(infoHash))
	out = append(out, Domain...)
	out = append(out, infoHash[:]...)
	return out
}

// Sign signs the payload for infoHash. The length check is load-bearing:
// ed25519.Sign panics on a wrong-length key.
func Sign(priv ed25519.PrivateKey, infoHash [20]byte) ([]byte, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("signing: bad private key length %d", len(priv))
	}
	return ed25519.Sign(priv, Payload(infoHash)), nil
}

// Verify checks a signature. The taxonomy is deliberately three-way and must
// never be collapsed (SPEC §5.6):
//   - wrong pubkey/sig LENGTH → a plain error that is NEITHER sentinel
//   - cryptographic failure → ErrBadSignature with Signature still populated
//   - success → Signature and nil
//
// (The ErrNotSigned case — missing fields — is detected by the caller that
// parses the torrent; this primitive only sees extracted values.)
func Verify(pub, sig []byte, infoHash [20]byte) (Signature, error) {
	if len(pub) != ed25519.PublicKeySize {
		return Signature{}, fmt.Errorf("signing: bad pubkey length %d", len(pub))
	}
	if len(sig) != ed25519.SignatureSize {
		return Signature{}, fmt.Errorf("signing: bad sig length %d", len(sig))
	}
	var out Signature
	copy(out.PubKey[:], pub)
	copy(out.Sig[:], sig)
	out.InfoHash = infoHash
	if !ed25519.Verify(ed25519.PublicKey(pub), Payload(infoHash), sig) {
		return out, ErrBadSignature
	}
	return out, nil
}
