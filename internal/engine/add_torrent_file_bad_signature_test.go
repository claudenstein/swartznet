package engine_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// TestAddTorrentFileBadSignature covers the
// `verr != nil && !errors.Is(verr, signing.ErrNotSigned) → Warn`
// branch of AddTorrentFile. We craft a .torrent file that
// contains the snet.pubkey + snet.sig signing fields but with a
// bogus signature, so signing.VerifyBytes returns ErrBadSignature
// rather than ErrNotSigned. AddTorrentFile must accept the
// torrent (the signature is informational, not a gate) and log
// the rejected-signature warning. The Handle's SignedBy stays
// empty since verification failed.
func TestAddTorrentFileBadSignature(t *testing.T) {
	t.Parallel()
	eng, cleanup := newAddTorrentFileEngine(t)
	defer cleanup()

	dir := t.TempDir()
	body := []byte(fillTo(32 * 1024))
	info := metainfo.Info{
		Name:        "x.bin",
		Length:      int64(len(body)),
		PieceLength: 16384,
	}
	pieces, err := metainfo.GeneratePieces(bytes.NewReader(body), info.PieceLength, nil)
	if err != nil {
		t.Fatal(err)
	}
	info.Pieces = pieces
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bogusSig := make([]byte, ed25519.SignatureSize)
	if _, err := rand.Read(bogusSig); err != nil {
		t.Fatal(err)
	}
	mi := map[string]bencode.Bytes{
		"info":        infoBytes,
		"snet.pubkey": mustBencodeString(t, string(pub)),
		"snet.sig":    mustBencodeString(t, string(bogusSig)),
	}
	miBytes, err := bencode.Marshal(mi)
	if err != nil {
		t.Fatal(err)
	}
	torrentPath := filepath.Join(dir, "bad-sig.torrent")
	if err := os.WriteFile(torrentPath, miBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	h, err := eng.AddTorrentFile(torrentPath)
	if err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}
	for _, snap := range eng.TorrentSnapshots() {
		if snap.InfoHash == h.T.InfoHash().HexString() && snap.SignedBy != "" {
			t.Errorf("SignedBy should remain empty on bad signature, got %q", snap.SignedBy)
		}
	}
}

// mustBencodeString returns the bencoded form of s as
// bencode.Bytes — used to embed string-shaped values into a
// hand-built metainfo dict.
func mustBencodeString(t *testing.T, s string) bencode.Bytes {
	t.Helper()
	out, err := bencode.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
