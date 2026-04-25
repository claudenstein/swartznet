package swarmsearch

import (
	"testing"

	"github.com/anacrolix/torrent/bencode"
)

// TestEncodeSyncNeedCapOverflow — the cap branch fires when
// the caller hands in more IDs than the spec allows.
func TestEncodeSyncNeedCapOverflow(t *testing.T) {
	t.Parallel()
	ids := make([][]byte, MaxNeedIDsPerMessage+1)
	for i := range ids {
		ids[i] = make([]byte, 32)
	}
	if _, err := EncodeSyncNeed(SyncNeed{TxID: 1, IDs: ids}); err == nil {
		t.Error("expected cap error")
	}
}

// TestEncodeSyncRecordsCapOverflow — same pattern for records.
func TestEncodeSyncRecordsCapOverflow(t *testing.T) {
	t.Parallel()
	good := SyncRecord{Pk: make([]byte, 32), Ih: make([]byte, 20), Sig: make([]byte, 64)}
	recs := make([]SyncRecord, MaxRecordsPerMessage+1)
	for i := range recs {
		recs[i] = good
	}
	if _, err := EncodeSyncRecords(SyncRecords{TxID: 1, Records: recs}); err == nil {
		t.Error("expected cap error")
	}
}

// TestEncodeSyncRecordsRejectsBadIH — separate branch from the
// existing pk-wrong test.
func TestEncodeSyncRecordsRejectsBadIH(t *testing.T) {
	t.Parallel()
	r := SyncRecord{Pk: make([]byte, 32), Ih: make([]byte, 10), Sig: make([]byte, 64)}
	if _, err := EncodeSyncRecords(SyncRecords{TxID: 1, Records: []SyncRecord{r}}); err == nil {
		t.Error("expected error for 10-byte ih")
	}
}

// TestEncodeSyncRecordsRejectsBadSig — third length-check branch.
func TestEncodeSyncRecordsRejectsBadSig(t *testing.T) {
	t.Parallel()
	r := SyncRecord{Pk: make([]byte, 32), Ih: make([]byte, 20), Sig: make([]byte, 32)}
	if _, err := EncodeSyncRecords(SyncRecords{TxID: 1, Records: []SyncRecord{r}}); err == nil {
		t.Error("expected error for 32-byte sig")
	}
}

// TestDecodeSyncSymbolsRejectsEmptySymbolsField — a wire frame
// that arrives with an empty symbols array (e.g. a misbehaving
// peer or a corrupted on-the-wire frame) is rejected by the
// decoder so callers don't have to guard for it.
func TestDecodeSyncSymbolsRejectsEmptySymbolsField(t *testing.T) {
	t.Parallel()
	// Hand-craft a wire frame with msg_type=5 but zero-length
	// symbols. EncodeSyncSymbols rejects this, so we marshal a
	// custom struct.
	type custom struct {
		MsgType int           `bencode:"msg_type"`
		TxID    uint32        `bencode:"txid"`
		Symbols []SyncSymbol  `bencode:"symbols"`
		Index   uint32        `bencode:"idx"`
	}
	raw, err := bencode.Marshal(custom{
		MsgType: MsgTypeSyncSymbols,
		TxID:    1,
		Symbols: []SyncSymbol{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSyncSymbols(raw); err == nil {
		t.Error("DecodeSyncSymbols should reject zero symbols")
	}
}

// TestDecodeSyncSymbolsRejectsOversize — over-cap symbols
// reach the decode-side cap check.
func TestDecodeSyncSymbolsRejectsOversize(t *testing.T) {
	t.Parallel()
	syms := make([]SyncSymbol, MaxSymbolsPerMessage+1)
	for i := range syms {
		syms[i] = SyncSymbol{DataXOR: make([]byte, 32)}
	}
	type custom struct {
		MsgType int          `bencode:"msg_type"`
		TxID    uint32       `bencode:"txid"`
		Symbols []SyncSymbol `bencode:"symbols"`
	}
	raw, err := bencode.Marshal(custom{
		MsgType: MsgTypeSyncSymbols,
		TxID:    1,
		Symbols: syms,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSyncSymbols(raw); err == nil {
		t.Error("DecodeSyncSymbols should reject oversize symbols")
	}
}

// TestDecodeSyncNeedRejectsCapAndShortIDs — exercises both
// post-decode validation arms in DecodeSyncNeed.
func TestDecodeSyncNeedRejectsCapAndShortIDs(t *testing.T) {
	t.Parallel()
	type customNeed struct {
		MsgType int      `bencode:"msg_type"`
		TxID    uint32   `bencode:"txid"`
		IDs     [][]byte `bencode:"ids"`
	}

	// Cap overflow.
	caps := make([][]byte, MaxNeedIDsPerMessage+1)
	for i := range caps {
		caps[i] = make([]byte, 32)
	}
	raw, err := bencode.Marshal(customNeed{MsgType: MsgTypeSyncNeed, TxID: 1, IDs: caps})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSyncNeed(raw); err == nil {
		t.Error("DecodeSyncNeed should reject cap overflow")
	}

	// Short ID slips past Encode (which we bypass here): id[0]
	// is only 16 bytes, decoder rejects.
	raw2, err := bencode.Marshal(customNeed{
		MsgType: MsgTypeSyncNeed,
		TxID:    2,
		IDs:     [][]byte{make([]byte, 16)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSyncNeed(raw2); err == nil {
		t.Error("DecodeSyncNeed should reject 16-byte id")
	}
}

// TestDecodeSyncRecordsRejectsBadFields — every record-field
// length check on the decode side: pk wrong, ih wrong, sig wrong.
func TestDecodeSyncRecordsRejectsBadFields(t *testing.T) {
	t.Parallel()
	type customRec struct {
		MsgType int          `bencode:"msg_type"`
		TxID    uint32       `bencode:"txid"`
		Records []SyncRecord `bencode:"records"`
	}

	cases := []struct {
		name string
		rec  SyncRecord
	}{
		{"short pk", SyncRecord{Pk: make([]byte, 16), Ih: make([]byte, 20), Sig: make([]byte, 64)}},
		{"short ih", SyncRecord{Pk: make([]byte, 32), Ih: make([]byte, 10), Sig: make([]byte, 64)}},
		{"short sig", SyncRecord{Pk: make([]byte, 32), Ih: make([]byte, 20), Sig: make([]byte, 32)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := bencode.Marshal(customRec{
				MsgType: MsgTypeSyncRecords,
				TxID:    1,
				Records: []SyncRecord{tc.rec},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeSyncRecords(raw); err == nil {
				t.Errorf("DecodeSyncRecords should reject %s", tc.name)
			}
		})
	}
}
