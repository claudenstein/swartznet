package dhtindex_test

import (
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/swartznet/swartznet/internal/dhtindex"
)

// ppmiWire mirrors the on-the-wire shape of dhtindex.PPMIValue
// without using the canonical encoder, so we can construct
// invalid payloads (wrong-sized ih/commit/topics/next_pk).
type ppmiWire struct {
	IH     []byte `bencode:"ih"`
	Commit []byte `bencode:"commit,omitempty"`
	Topics []byte `bencode:"topics,omitempty"`
	Ts     int64  `bencode:"ts"`
	NextPk []byte `bencode:"next_pk,omitempty"`
}

func mkBadPPMI(t *testing.T, w ppmiWire) []byte {
	t.Helper()
	raw, err := bencode.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestDecodePPMIRejectsBadIH — IH must be exactly 20 bytes.
func TestDecodePPMIRejectsBadIH(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, 10, 30} {
		raw := mkBadPPMI(t, ppmiWire{IH: make([]byte, n)})
		if _, err := dhtindex.DecodePPMI(raw); err == nil {
			t.Errorf("DecodePPMI with %d-byte ih should error", n)
		}
	}
}

// TestDecodePPMIRejectsBadCommit — Commit (when present) must
// be exactly 32 bytes; 0 means absent.
func TestDecodePPMIRejectsBadCommit(t *testing.T) {
	t.Parallel()
	raw := mkBadPPMI(t, ppmiWire{
		IH:     make([]byte, 20),
		Commit: make([]byte, 16),
	})
	if _, err := dhtindex.DecodePPMI(raw); err == nil {
		t.Error("DecodePPMI with 16-byte commit should error")
	}
}

// TestDecodePPMIRejectsBadTopics — Topics (when present) must
// be exactly 32 bytes.
func TestDecodePPMIRejectsBadTopics(t *testing.T) {
	t.Parallel()
	raw := mkBadPPMI(t, ppmiWire{
		IH:     make([]byte, 20),
		Topics: make([]byte, 8),
	})
	if _, err := dhtindex.DecodePPMI(raw); err == nil {
		t.Error("DecodePPMI with 8-byte topics should error")
	}
}

// TestDecodePPMIRejectsBadNextPk — NextPk (when present) must
// be exactly 32 bytes.
func TestDecodePPMIRejectsBadNextPk(t *testing.T) {
	t.Parallel()
	raw := mkBadPPMI(t, ppmiWire{
		IH:     make([]byte, 20),
		NextPk: make([]byte, 64),
	})
	if _, err := dhtindex.DecodePPMI(raw); err == nil {
		t.Error("DecodePPMI with 64-byte next_pk should error")
	}
}
