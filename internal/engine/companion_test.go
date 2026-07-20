package engine

import (
	"strings"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// TestValidateCompanionInfoBounds pins the fail-closed fetch bounds enforced on
// an untrusted companion info dict: exactly one file, ≤32 MiB, safe filename.
func TestValidateCompanionInfoBounds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		info    *metainfo.Info
		wantErr string
	}{
		{"nil", nil, "no info"},
		{"ok", &metainfo.Info{Name: "swartznet-content-index-v1.json.gz", Length: 1024, PieceLength: 256 << 10}, ""},
		{"two files", &metainfo.Info{Name: "x", PieceLength: 256 << 10, Files: []metainfo.FileInfo{
			{Path: []string{"a"}, Length: 10}, {Path: []string{"b"}, Length: 10},
		}}, "want exactly 1"},
		{"oversize", &metainfo.Info{Name: "x", Length: maxCompanionBytes + 1, PieceLength: 256 << 10}, "exceeds cap"},
		{"empty name", &metainfo.Info{Name: "", Length: 10, PieceLength: 256 << 10}, "unsafe name"},
		{"dotdot", &metainfo.Info{Name: "..", Length: 10, PieceLength: 256 << 10}, "unsafe name"},
		{"slash", &metainfo.Info{Name: "a/b", Length: 10, PieceLength: 256 << 10}, "unsafe name"},
		{"backslash", &metainfo.Info{Name: `a\b`, Length: 10, PieceLength: 256 << 10}, "unsafe name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCompanionInfo(tc.info)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestUnsafeCompanionName(t *testing.T) {
	t.Parallel()
	unsafe := []string{"", ".", "..", "a/b", `a\b`, "/etc/passwd"}
	for _, n := range unsafe {
		if !unsafeCompanionName(n) {
			t.Errorf("%q should be unsafe", n)
		}
	}
	safe := []string{"swartznet-content-index-v1.json.gz", "a.txt", "index-abcdef-v1.json.gz"}
	for _, n := range safe {
		if unsafeCompanionName(n) {
			t.Errorf("%q should be safe", n)
		}
	}
}

// TestPointerAccessorsGatedOnDHTAndSigner: the pointer putter needs the DHT +
// an identity; the getter needs only the DHT.
func TestPointerAccessorsGatedOnDHTAndSigner(t *testing.T) {
	t.Parallel()
	e := layerDEngine(t, nil) // DHT enabled, no signer yet
	if e.PointerGetter() == nil {
		t.Error("getter nil with DHT enabled")
	}
	if e.PointerPutter() != nil {
		t.Error("putter built before a signer was set")
	}
	setTestSigner(t, e)
	if e.PointerPutter() == nil {
		t.Error("putter nil after SetSigner")
	}
}
