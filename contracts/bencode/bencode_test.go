package bencode

import (
	"bytes"
	"strings"
	"testing"

	abencode "github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// singleFileTorrent is a canonical single-file fixture built by hand so the
// bytes are frozen in-source (a golden vector, not a generated artifact).
// Note dict values carry no length prefix — the info value is the dict bytes
// directly.
func singleFileTorrent() []byte {
	info := "d6:lengthi96e4:name11:fixture.bin12:piece lengthi32768e6:pieces20:aaaaaaaaaaaaaaaaaaaae"
	return []byte("d8:announce20:http://tr.invalid/an4:info" + info + "e")
}

func TestParseSingleFile(t *testing.T) {
	m, err := ParseMetainfo(singleFileTorrent())
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "fixture.bin" || m.PieceLength != 32768 || m.TotalLength != 96 {
		t.Fatalf("view = %+v", m)
	}
	if len(m.Files) != 1 || m.Files[0].Length != 96 || len(m.Files[0].Path) != 0 {
		t.Fatalf("files = %+v", m.Files)
	}
	if m.Announce != "http://tr.invalid/an" {
		t.Fatalf("announce = %q", m.Announce)
	}
	if len(m.InfoHashHex()) != 40 {
		t.Fatalf("infohash = %q", m.InfoHashHex())
	}
}

// TestInfoHashMatchesAnacrolix guards codec drift against the library the
// engine actually feeds: our raw-bytes SHA1 must equal anacrolix's.
func TestInfoHashMatchesAnacrolix(t *testing.T) {
	raw := singleFileTorrent()
	m, err := ParseMetainfo(raw)
	if err != nil {
		t.Fatal(err)
	}
	mi, err := metainfo.Load(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if m.InfoHashHex() != mi.HashInfoBytes().HexString() {
		t.Fatalf("infohash drift: ours %s, anacrolix %s", m.InfoHashHex(), mi.HashInfoBytes().HexString())
	}
}

// TestSignedTwinSharesInfohash is the load-bearing signing property: adding
// top-level snet.* keys must not move the infohash.
func TestSignedTwinSharesInfohash(t *testing.T) {
	plain := singleFileTorrent()
	d, err := DecodeDict(plain)
	if err != nil {
		t.Fatal(err)
	}
	pk, _ := abencode.Marshal("PUBKEYPUBKEYPUBKEYPUBKEYPUBKEYPU")
	sig, _ := abencode.Marshal(strings.Repeat("S", 64))
	d["snet.pubkey"] = Bytes(pk)
	d["snet.sig"] = Bytes(sig)
	signed, err := EncodeDict(d)
	if err != nil {
		t.Fatal(err)
	}
	mPlain, err := ParseMetainfo(plain)
	if err != nil {
		t.Fatal(err)
	}
	mSigned, err := ParseMetainfo(signed)
	if err != nil {
		t.Fatal(err)
	}
	if mPlain.InfoHash != mSigned.InfoHash {
		t.Fatal("signed twin changed the infohash")
	}
	if !bytes.Equal(mPlain.InfoBytes, mSigned.InfoBytes) {
		t.Fatal("info bytes not byte-identical across the twin")
	}
}

// TestRoundTripPreservesUnknownKeys: unknown top-level keys survive
// byte-identically for canonical inputs.
func TestRoundTripPreservesUnknownKeys(t *testing.T) {
	raw := []byte("d3:cow3:moo4:info" + "d6:lengthi1e4:name1:x12:piece lengthi16384e6:pieces20:aaaaaaaaaaaaaaaaaaaa" + "e7:unknown5:valuee")
	d, err := DecodeDict(raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := EncodeDict(d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatalf("round trip changed bytes:\n in: %q\nout: %q", raw, out)
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
		want string
	}{
		{"trailing garbage", append(singleFileTorrent(), 'x'), "trailing data"},
		{"truncated", singleFileTorrent()[:10], "decode metainfo"},
		{"missing info", []byte("d8:announce3:fooe"), "missing info dict"},
		{"not a dict", []byte("le"), "decode metainfo"},
		{"empty", nil, "decode metainfo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseMetainfo(tc.raw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}
