package engine

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/identity"
	"github.com/swartznet/swartznet/internal/signing"
)

func createFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "content.bin")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCreateTorrentDeterministicInfohash(t *testing.T) {
	root := createFixture(t)
	out1 := filepath.Join(t.TempDir(), "a.torrent")
	out2 := filepath.Join(t.TempDir(), "b.torrent")
	ih1, _, err := CreateTorrentFile(CreateTorrentOptions{Root: root}, out1)
	if err != nil {
		t.Fatal(err)
	}
	ih2, _, err := CreateTorrentFile(CreateTorrentOptions{Root: root}, out2)
	if err != nil {
		t.Fatal(err)
	}
	// The .torrent bytes differ (creation date) but the infohash is a pure
	// function of content + name + piece length + private.
	if ih1 != ih2 {
		t.Fatalf("infohash not deterministic: %s vs %s", ih1, ih2)
	}
	if len(ih1) != 40 || ih1 != strings.ToLower(ih1) {
		t.Fatalf("infohash shape: %q", ih1)
	}
}

func TestCreateTorrentNameOverrideChangesInfohash(t *testing.T) {
	root := createFixture(t)
	out := t.TempDir()
	ihPlain, _, err := CreateTorrentFile(CreateTorrentOptions{Root: root}, filepath.Join(out, "p.torrent"))
	if err != nil {
		t.Fatal(err)
	}
	ihNamed, _, err := CreateTorrentFile(CreateTorrentOptions{Root: root, Name: "renamed"}, filepath.Join(out, "n.torrent"))
	if err != nil {
		t.Fatal(err)
	}
	if ihPlain == ihNamed {
		t.Fatal("--name must change the infohash (it lives inside info)")
	}
}

func TestCreateTorrentPrivateChangesInfohash(t *testing.T) {
	root := createFixture(t)
	out := t.TempDir()
	ihPlain, _, err := CreateTorrentFile(CreateTorrentOptions{Root: root}, filepath.Join(out, "p.torrent"))
	if err != nil {
		t.Fatal(err)
	}
	ihPriv, _, err := CreateTorrentFile(CreateTorrentOptions{Root: root, Private: true}, filepath.Join(out, "pr.torrent"))
	if err != nil {
		t.Fatal(err)
	}
	if ihPlain == ihPriv {
		t.Fatal("--private must change the infohash (BEP-27 key lives inside info)")
	}
}

func TestCreateTorrentErrors(t *testing.T) {
	if _, err := CreateTorrent(CreateTorrentOptions{}); err == nil ||
		!strings.Contains(err.Error(), "CreateTorrent requires opts.Root") {
		t.Fatalf("err = %v", err)
	}
	if _, err := CreateTorrent(CreateTorrentOptions{Root: filepath.Join(t.TempDir(), "missing")}); err == nil ||
		!strings.Contains(err.Error(), "stat root:") {
		t.Fatalf("err = %v", err)
	}
}

// TestCreateTorrentPieceLengthValidation pins the fixed doc-vs-code gap: the
// legacy documented "power of two ≥ 16 KiB" but never enforced it.
func TestCreateTorrentPieceLengthValidation(t *testing.T) {
	root := createFixture(t)
	for _, bad := range []int64{1024, 8 * 1024, 17 * 1024, 33000} {
		_, err := CreateTorrent(CreateTorrentOptions{Root: root, PieceLength: bad})
		if err == nil || !strings.Contains(err.Error(), "power of two ≥ 16 KiB") {
			t.Errorf("piece length %d: err = %v", bad, err)
		}
	}
	if _, err := CreateTorrent(CreateTorrentOptions{Root: root, PieceLength: 32 * 1024}); err != nil {
		t.Fatalf("valid piece length rejected: %v", err)
	}
}

// TestCreateSignedTwinSharesInfohash is the DoD headline: create then
// create --sign yield one infohash, and the signed file verifies.
func TestCreateSignedTwinSharesInfohash(t *testing.T) {
	root := createFixture(t)
	out := t.TempDir()
	id, err := identity.Load(filepath.Join(t.TempDir(), "identity.key"), true)
	if err != nil {
		t.Fatal(err)
	}
	signer := id.Signer()

	ihPlain, plainBytes, err := CreateTorrentFile(CreateTorrentOptions{Root: root}, filepath.Join(out, "plain.torrent"))
	if err != nil {
		t.Fatal(err)
	}
	ihSigned, signedBytes, err := CreateTorrentFile(CreateTorrentOptions{Root: root, SignWith: &signer}, filepath.Join(out, "signed.torrent"))
	if err != nil {
		t.Fatal(err)
	}
	if ihPlain != ihSigned {
		t.Fatalf("twin infohash mismatch: %s vs %s", ihPlain, ihSigned)
	}
	if _, err := signing.Verify(plainBytes); err != signing.ErrNotSigned {
		t.Fatalf("plain verify = %v, want ErrNotSigned", err)
	}
	sig, err := signing.Verify(signedBytes)
	if err != nil {
		t.Fatalf("signed verify = %v", err)
	}
	if sig.PubKeyHex() != id.PublicKeyHex() {
		t.Fatalf("verified %s, want %s", sig.PubKeyHex(), id.PublicKeyHex())
	}
	// The written file matches the returned bytes byte-for-byte.
	onDisk, err := os.ReadFile(filepath.Join(out, "signed.torrent"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != string(signedBytes) {
		t.Fatal("returned bytes differ from the written file")
	}
}

// TestAddSignedTorrentSetsSignedBy: verify-at-add wires SignedBy end to end,
// including session persistence.
func TestAddSignedTorrentSetsSignedBy(t *testing.T) {
	e := testEngine(t)
	root := createFixture(t)
	id, err := identity.Load(filepath.Join(t.TempDir(), "identity.key"), true)
	if err != nil {
		t.Fatal(err)
	}
	signer := id.Signer()
	_, signedBytes, err := CreateTorrentFile(
		CreateTorrentOptions{Root: root, SignWith: &signer},
		filepath.Join(t.TempDir(), "s.torrent"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := e.AddTorrentBytes(signedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if h.SignedBy() != id.PublicKeyHex() {
		t.Fatalf("SignedBy = %q, want %s", h.SignedBy(), id.PublicKeyHex())
	}
	// Session round-trip.
	found := false
	for _, ent := range e.sess.list() {
		if ent.InfoHash == h.InfoHashHex() {
			found = true
			if ent.SignedBy != id.PublicKeyHex() {
				t.Fatalf("session signed_by = %q", ent.SignedBy)
			}
		}
	}
	if !found {
		t.Fatal("no session entry")
	}
}

// TestCreateDirectoryRoot: a directory yields a multi-file torrent named
// after the dir (or the --name override), and an empty dir fails closed.
func TestCreateDirectoryRoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "album")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(strings.Repeat("d", 512)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mi, err := CreateTorrent(CreateTorrentOptions{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "album" || len(info.Files) != 2 {
		t.Fatalf("info = name %q, %d files", info.Name, len(info.Files))
	}

	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateTorrent(CreateTorrentOptions{Root: empty}); err == nil ||
		!strings.Contains(err.Error(), "root directory contains no files") {
		t.Fatalf("empty dir: err = %v", err)
	}
}

// TestSignatureLogEvents pins the frozen log surface: event names, attr
// keys, and that the rejected err is genuinely ErrBadSignature.
func TestSignatureLogEvents(t *testing.T) {
	rec := &recordingLogHandler{}
	cfg := config.Config{
		DataDir:    filepath.Join(t.TempDir(), "data"),
		ListenPort: 0,
		DisableDHT: true,
	}
	e, err := New(context.Background(), cfg, slog.New(rec))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })

	root := createFixture(t)
	id, err := identity.Load(filepath.Join(t.TempDir(), "identity.key"), true)
	if err != nil {
		t.Fatal(err)
	}
	signer := id.Signer()
	_, signedBytes, err := CreateTorrentFile(
		CreateTorrentOptions{Root: root, SignWith: &signer},
		filepath.Join(t.TempDir(), "s.torrent"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddTorrentBytes(signedBytes); err != nil {
		t.Fatal(err)
	}
	verified := rec.find("engine.torrent_signature_verified")
	if verified == nil {
		t.Fatal("no verified event")
	}
	if verified.attrs["pubkey"] != id.PublicKeyHex() || verified.attrs["info_hash"] == "" {
		t.Fatalf("verified attrs = %v", verified.attrs)
	}

	flipped := append([]byte(nil), signedBytes...)
	flipped[len(flipped)-5] ^= 0x01
	// Different infohash needed? No — same infohash dedups but the verify
	// (and its log) runs before registration, so the event still fires.
	if _, err := e.AddTorrentBytes(flipped); err != nil {
		t.Fatal(err)
	}
	rejected := rec.find("engine.torrent_signature_rejected")
	if rejected == nil {
		t.Fatal("no rejected event")
	}
	if !strings.Contains(rejected.attrs["err"], "signature does not verify") {
		t.Fatalf("rejected err attr = %q, want ErrBadSignature text", rejected.attrs["err"])
	}
}

type capturedRecord struct {
	msg   string
	attrs map[string]string
}

type recordingLogHandler struct {
	mu      sync.Mutex
	records []capturedRecord
}

func (h *recordingLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingLogHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]string{}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, capturedRecord{msg: r.Message, attrs: attrs})
	return nil
}
func (h *recordingLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingLogHandler) WithGroup(string) slog.Handler      { return h }
func (h *recordingLogHandler) find(msg string) *capturedRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.records {
		if h.records[i].msg == msg {
			return &h.records[i]
		}
	}
	return nil
}

// TestAddBadSignatureAddsAnyway pins D20: a byte-flipped signature still adds
// the torrent — with empty SignedBy.
func TestAddBadSignatureAddsAnyway(t *testing.T) {
	e := testEngine(t)
	root := createFixture(t)
	id, err := identity.Load(filepath.Join(t.TempDir(), "identity.key"), true)
	if err != nil {
		t.Fatal(err)
	}
	signer := id.Signer()
	_, signedBytes, err := CreateTorrentFile(
		CreateTorrentOptions{Root: root, SignWith: &signer},
		filepath.Join(t.TempDir(), "s.torrent"))
	if err != nil {
		t.Fatal(err)
	}
	flipped := append([]byte(nil), signedBytes...)
	flipped[len(flipped)-5] ^= 0x01 // inside the snet.sig value

	h, err := e.AddTorrentBytes(flipped)
	if err != nil {
		t.Fatalf("bad signature must not gate the add: %v", err)
	}
	if h.SignedBy() != "" {
		t.Fatalf("SignedBy = %q, want empty on bad signature", h.SignedBy())
	}
}

// TestDuplicateSignedAddUpgradesSignedBy pins the sticky Q37 semantics: an
// unsigned handle upgrades when signed bytes for the same infohash arrive;
// an unsigned re-add never blanks it.
func TestDuplicateSignedAddUpgradesSignedBy(t *testing.T) {
	e := testEngine(t)
	root := createFixture(t)
	id, err := identity.Load(filepath.Join(t.TempDir(), "identity.key"), true)
	if err != nil {
		t.Fatal(err)
	}
	signer := id.Signer()
	out := t.TempDir()
	_, plainBytes, err := CreateTorrentFile(CreateTorrentOptions{Root: root}, filepath.Join(out, "p.torrent"))
	if err != nil {
		t.Fatal(err)
	}
	_, signedBytes, err := CreateTorrentFile(CreateTorrentOptions{Root: root, SignWith: &signer}, filepath.Join(out, "s.torrent"))
	if err != nil {
		t.Fatal(err)
	}

	h1, err := e.AddTorrentBytes(plainBytes)
	if err != nil {
		t.Fatal(err)
	}
	if h1.SignedBy() != "" {
		t.Fatal("plain add must be unsigned")
	}
	h2, err := e.AddTorrentBytes(signedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if h2 != h1 {
		t.Fatal("same infohash must dedupe")
	}
	if h1.SignedBy() != id.PublicKeyHex() {
		t.Fatalf("signed duplicate did not upgrade SignedBy: %q", h1.SignedBy())
	}
	// Unsigned re-add never blanks (sticky).
	if _, err := e.AddTorrentBytes(plainBytes); err != nil {
		t.Fatal(err)
	}
	if h1.SignedBy() != id.PublicKeyHex() {
		t.Fatal("unsigned re-add blanked SignedBy")
	}
}
