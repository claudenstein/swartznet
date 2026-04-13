// Package signing adds optional ed25519 publisher signatures to
// .torrent files. The signature binds the publisher's public key
// to the torrent's infohash, letting downloaders verify that a
// `.torrent` file carrying that infohash was authored by the
// holder of the corresponding private key.
//
// # Wire format
//
// Two new optional top-level fields are added to the .torrent
// metainfo dictionary alongside the standard `info`, `announce`,
// etc.:
//
//	snet.pubkey  32-byte ed25519 public key
//	snet.sig     64-byte ed25519 signature
//
// The signature payload is:
//
//	"SN-TORRENT-V1|" || <20-byte infohash (SHA-1 of the info dict)>
//
// The "SN-TORRENT-V1" prefix is a domain separator: without it, a
// signature over an infohash could be replayed as a signature over
// arbitrary 20-byte strings, which is not a problem today but is
// cheap insurance for future uses of the same key.
//
// Compatibility: vanilla BitTorrent clients already ignore unknown
// top-level metainfo fields. A signed .torrent file loads and
// downloads normally in qBittorrent, Transmission, libtorrent, and
// anacrolix/torrent. Only SwartzNet reads the signing fields.
package signing

import (
	"crypto/ed25519"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/anacrolix/torrent/bencode"
)

// Domain is the prefix prepended to the infohash before signing.
// Kept as a package-level variable so tests can verify both ends
// use the same value, but callers MUST NOT change it at runtime.
const Domain = "SN-TORRENT-V1|"

// Signature is the result of verifying a signed .torrent file.
type Signature struct {
	// PubKey is the 32-byte ed25519 public key the signature was
	// produced with.
	PubKey [32]byte
	// Sig is the 64-byte ed25519 signature.
	Sig [64]byte
	// InfoHash is the 20-byte SHA-1 of the info dict that was
	// signed.
	InfoHash [20]byte
}

// PubKeyHex returns the 64-char lowercase hex form of the public
// key — the same representation SwartzNet uses everywhere else.
func (s Signature) PubKeyHex() string {
	const hextab = "0123456789abcdef"
	out := make([]byte, 64)
	for i, b := range s.PubKey {
		out[i*2] = hextab[b>>4]
		out[i*2+1] = hextab[b&0x0f]
	}
	return string(out)
}

// ErrNotSigned is returned when a .torrent file has no signing
// fields. It's a normal outcome, not a hard error — most
// third-party torrents are unsigned.
var ErrNotSigned = errors.New("signing: torrent is not signed")

// ErrBadSignature is returned when signing fields are present but
// fail verification (tampered file, wrong key, truncated).
var ErrBadSignature = errors.New("signing: signature does not verify")

// SignBytes takes the raw bencoded bytes of a .torrent file,
// computes the info-hash, signs "Domain || infohash" with priv,
// and returns new bytes with `snet.pubkey` + `snet.sig` added to
// the top-level dict. Any existing snet.pubkey/snet.sig are
// replaced.
func SignBytes(raw []byte, priv ed25519.PrivateKey) ([]byte, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("signing: bad private key length %d", len(priv))
	}

	var mi map[string]bencode.Bytes
	if err := bencode.Unmarshal(raw, &mi); err != nil {
		return nil, fmt.Errorf("signing: decode metainfo: %w", err)
	}
	infoBytes, ok := mi["info"]
	if !ok || len(infoBytes) == 0 {
		return nil, errors.New("signing: metainfo missing info dict")
	}

	infoHash := sha1.Sum(infoBytes)
	sig := ed25519.Sign(priv, signingPayload(infoHash))
	pub := priv.Public().(ed25519.PublicKey)

	pubBytes, err := bencode.Marshal(string(pub))
	if err != nil {
		return nil, fmt.Errorf("signing: marshal pubkey: %w", err)
	}
	sigBytes, err := bencode.Marshal(string(sig))
	if err != nil {
		return nil, fmt.Errorf("signing: marshal sig: %w", err)
	}
	mi["snet.pubkey"] = pubBytes
	mi["snet.sig"] = sigBytes

	out, err := bencode.Marshal(mi)
	if err != nil {
		return nil, fmt.Errorf("signing: re-encode: %w", err)
	}
	return out, nil
}

// SignFile is SignBytes applied to a .torrent file on disk.
// Writes atomically via tempfile + rename.
func SignFile(path string, priv ed25519.PrivateKey) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("signing: read %s: %w", path, err)
	}
	signed, err := SignBytes(raw, priv)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, signed, 0o644); err != nil {
		return fmt.Errorf("signing: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("signing: rename: %w", err)
	}
	return nil
}

// VerifyBytes extracts signing fields from raw metainfo bytes and
// verifies them against the info-hash. Returns ErrNotSigned if no
// signing fields are present, ErrBadSignature if present but fail
// verification.
func VerifyBytes(raw []byte) (Signature, error) {
	var mi map[string]bencode.Bytes
	if err := bencode.Unmarshal(raw, &mi); err != nil {
		return Signature{}, fmt.Errorf("signing: decode metainfo: %w", err)
	}
	infoBytes, ok := mi["info"]
	if !ok || len(infoBytes) == 0 {
		return Signature{}, errors.New("signing: metainfo missing info dict")
	}
	pubRaw, pubOK := mi["snet.pubkey"]
	sigRaw, sigOK := mi["snet.sig"]
	if !pubOK || !sigOK {
		return Signature{}, ErrNotSigned
	}

	var pub string
	if err := bencode.Unmarshal(pubRaw, &pub); err != nil {
		return Signature{}, fmt.Errorf("signing: decode pubkey: %w", err)
	}
	var sig string
	if err := bencode.Unmarshal(sigRaw, &sig); err != nil {
		return Signature{}, fmt.Errorf("signing: decode sig: %w", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return Signature{}, fmt.Errorf("signing: bad pubkey length %d", len(pub))
	}
	if len(sig) != ed25519.SignatureSize {
		return Signature{}, fmt.Errorf("signing: bad sig length %d", len(sig))
	}

	out := Signature{InfoHash: sha1.Sum(infoBytes)}
	copy(out.PubKey[:], pub)
	copy(out.Sig[:], sig)

	if !ed25519.Verify(ed25519.PublicKey(pub), signingPayload(out.InfoHash), []byte(sig)) {
		return out, ErrBadSignature
	}
	return out, nil
}

// VerifyFile is VerifyBytes applied to a file on disk.
func VerifyFile(path string) (Signature, error) {
	f, err := os.Open(path)
	if err != nil {
		return Signature{}, fmt.Errorf("signing: open %s: %w", path, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		return Signature{}, fmt.Errorf("signing: read %s: %w", path, err)
	}
	return VerifyBytes(raw)
}

// signingPayload is the byte slice that ed25519 signs / verifies.
// Kept as a small helper so Sign and Verify use byte-identical
// constructions; accidental divergence would silently break
// verification for every signed torrent.
func signingPayload(infoHash [20]byte) []byte {
	payload := make([]byte, 0, len(Domain)+20)
	payload = append(payload, Domain...)
	payload = append(payload, infoHash[:]...)
	return payload
}
