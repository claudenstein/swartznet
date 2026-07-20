package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/identity"
	"github.com/swartznet/swartznet/internal/signing"
)

func createContent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.bin")
	if err := os.WriteFile(path, []byte(strings.Repeat("z", 2048)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCreateUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no args", nil, "usage: swartznet create <file-or-folder> -o <output.torrent>"},
		{"missing -o", []string{"some-root"}, "swartznet: -o <output.torrent> is required"},
		{"identity without sign", []string{"-o", "x.torrent", "--identity", "/k", "some-root"}, "swartznet: --identity is only used with --sign (pass --sign to sign, or drop --identity)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := cmdCreate(tc.args, &stdout, &stderr); code != exitUsage {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestCreatePlainOutput(t *testing.T) {
	root := createContent(t)
	out := filepath.Join(t.TempDir(), "out.torrent")
	var stdout, stderr bytes.Buffer
	if code := cmdCreate([]string{"-o", out, root}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"Hashing " + root + "...\n",
		"✓ Created " + out + "\n",
		"  InfoHash: ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "Signing with identity") {
		t.Fatal("plain create must not print a signing line")
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signing.Verify(raw); err != signing.ErrNotSigned {
		t.Fatalf("plain output verify = %v, want ErrNotSigned", err)
	}
}

func TestCreateSignedOutput(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "xdg")) // hermetic default identity
	root := createContent(t)
	out := filepath.Join(t.TempDir(), "signed.torrent")
	var stdout, stderr bytes.Buffer
	if code := cmdCreate([]string{"-o", out, "--sign", root}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	// Signing line prints BEFORE hashing.
	got := stdout.String()
	signIdx := strings.Index(got, "Signing with identity ")
	hashIdx := strings.Index(got, "Hashing ")
	if signIdx < 0 || hashIdx < 0 || signIdx > hashIdx {
		t.Fatalf("output order wrong: %q", got)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signing.Verify(raw)
	if err != nil {
		t.Fatalf("signed output verify = %v", err)
	}
	if !strings.Contains(got, "Signing with identity "+sig.PubKeyHex()) {
		t.Fatalf("printed identity does not match verified pubkey: %q", got)
	}
}

// TestCreateTwinInfohashViaCLI is the DoD as the user sees it: create then
// create --sign on the same content print the same InfoHash line.
func TestCreateTwinInfohashViaCLI(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "xdg"))
	root := createContent(t)
	dir := t.TempDir()

	infohashOf := func(args ...string) string {
		var stdout, stderr bytes.Buffer
		if code := cmdCreate(args, &stdout, &stderr); code != exitOK {
			t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
		}
		for _, line := range strings.Split(stdout.String(), "\n") {
			if strings.HasPrefix(line, "  InfoHash: ") {
				return strings.TrimPrefix(line, "  InfoHash: ")
			}
		}
		t.Fatal("no InfoHash line")
		return ""
	}
	plain := infohashOf("-o", filepath.Join(dir, "p.torrent"), root)
	signed := infohashOf("-o", filepath.Join(dir, "s.torrent"), "--sign", root)
	if plain != signed || len(plain) != 40 {
		t.Fatalf("twin infohashes: %q vs %q", plain, signed)
	}
}

// TestCreateSignExplicitMissingIdentityFailsClosed: an explicit --identity
// path that doesn't exist is a hard error — never mint a key that would
// orphan the torrent under an untrusted pubkey.
func TestCreateSignExplicitMissingIdentityFailsClosed(t *testing.T) {
	root := createContent(t)
	missing := filepath.Join(t.TempDir(), "nope.key")
	var stdout, stderr bytes.Buffer
	code := cmdCreate([]string{"-o", filepath.Join(t.TempDir(), "x.torrent"), "--sign", "--identity", missing, root}, &stdout, &stderr)
	if code != exitRuntime {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "swartznet: load identity:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("create minted a key at an explicit path")
	}
	// No output file either — identity fails before hashing.
	if strings.Contains(stdout.String(), "Hashing") {
		t.Fatal("hashing ran despite identity failure")
	}
}

func TestCreateSignExplicitValidIdentity(t *testing.T) {
	root := createContent(t)
	keyFile := filepath.Join(t.TempDir(), "elsewhere.key")
	id, err := identity.Load(keyFile, true)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "s.torrent")
	var stdout, stderr bytes.Buffer
	if code := cmdCreate([]string{"-o", out, "--sign", "--identity", keyFile, root}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signing.Verify(raw)
	if err != nil || sig.PubKeyHex() != id.PublicKeyHex() {
		t.Fatalf("verify = %v, pubkey %s want %s", err, sig.PubKeyHex(), id.PublicKeyHex())
	}
}

func TestCreateBadPieceLength(t *testing.T) {
	root := createContent(t)
	var stdout, stderr bytes.Buffer
	code := cmdCreate([]string{"-o", filepath.Join(t.TempDir(), "x.torrent"), "--piece-kib", "7", root}, &stdout, &stderr)
	if code != exitRuntime {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "power of two ≥ 16 KiB") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
