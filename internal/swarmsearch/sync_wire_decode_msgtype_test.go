package swarmsearch

import (
	"testing"

	"github.com/anacrolix/torrent/bencode"
)

// TestDecodeSyncSymbolsBadDataXORLen covers DecodeSyncSymbols'
// `if len(s.DataXOR) != 32` per-symbol validation. Hand-
// marshal a frame whose first symbol carries a 5-byte
// DataXOR — bencode round-trips fine but the post-decode
// loop rejects.
func TestDecodeSyncSymbolsBadDataXORLen(t *testing.T) {
	t.Parallel()
	bad, err := bencode.Marshal(SyncSymbols{
		MsgType: MsgTypeSyncSymbols,
		TxID:    1,
		Symbols: []SyncSymbol{
			{Count: 1, KeyXOR: 0xff, DataXOR: make([]byte, 5)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSyncSymbols(bad); err == nil {
		t.Error("DecodeSyncSymbols should reject 5-byte DataXOR")
	}
}

// TestEncodeSyncRecordsNilRecordsDefault covers EncodeSyncRecords'
// `if m.Records == nil { m.Records = []SyncRecord{} }` arm.
// Pass a nil Records slice and verify the wire form is still
// produced (with the empty default substituted).
func TestEncodeSyncRecordsNilRecordsDefault(t *testing.T) {
	t.Parallel()
	raw, err := EncodeSyncRecords(SyncRecords{TxID: 1, Records: nil})
	if err != nil {
		t.Fatalf("EncodeSyncRecords nil records: %v", err)
	}
	got, err := DecodeSyncRecords(raw)
	if err != nil {
		t.Fatalf("DecodeSyncRecords round trip: %v", err)
	}
	if len(got.Records) != 0 {
		t.Errorf("got %d records, want 0", len(got.Records))
	}
}

// TestEncodeSyncNeedNilIDsDefault covers EncodeSyncNeed's
// `if m.IDs == nil { m.IDs = [][]byte{} }` arm. Pass a nil
// IDs slice — the encoder must substitute the empty slice
// before bencoding so the on-the-wire form has an explicit
// (empty) list rather than nothing.
func TestEncodeSyncNeedNilIDsDefault(t *testing.T) {
	t.Parallel()
	raw, err := EncodeSyncNeed(SyncNeed{TxID: 1, IDs: nil})
	if err != nil {
		t.Fatalf("EncodeSyncNeed nil IDs: %v", err)
	}
	if len(raw) == 0 {
		t.Error("EncodeSyncNeed produced empty payload")
	}
	// Round trip back so we know the bencoded form is valid.
	if _, err := DecodeSyncNeed(raw); err != nil {
		t.Errorf("DecodeSyncNeed of nil-IDs round trip: %v", err)
	}
}

// TestDecodeSyncRecordsTooManyRecordsCap covers DecodeSyncRecords's
// `if len(m.Records) > MaxRecordsPerMessage` cap arm. Encode a
// SyncRecords frame with MaxRecordsPerMessage+1 records — the
// payload bencodes fine but the wire-level cap rejects it.
func TestDecodeSyncRecordsTooManyRecordsCap(t *testing.T) {
	t.Parallel()
	tooMany := make([]SyncRecord, MaxRecordsPerMessage+1)
	for i := range tooMany {
		tooMany[i] = SyncRecord{
			Pk:  make([]byte, 32),
			Ih:  make([]byte, 20),
			Sig: make([]byte, 64),
		}
	}
	// EncodeSyncRecords also rejects oversize counts, so
	// bencode-marshal the struct directly to bypass the
	// publisher-side cap check.
	raw, err := bencode.Marshal(SyncRecords{
		MsgType: MsgTypeSyncRecords,
		TxID:    1,
		Records: tooMany,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSyncRecords(raw); err == nil {
		t.Error("DecodeSyncRecords should reject record count exceeding the cap")
	}
}

// TestDecodeSyncBeginWrongMsgType covers DecodeSyncBegin's
// `if m.MsgType != MsgTypeSyncBegin` arm. Bencode a payload
// that decodes successfully into a SyncBegin struct but
// carries the wrong msg_type discriminator.
func TestDecodeSyncBeginWrongMsgType(t *testing.T) {
	t.Parallel()
	bad, err := bencode.Marshal(map[string]interface{}{
		"msg_type":     int64(MsgTypeSyncEnd), // 8 — not Begin (4)
		"tx_id":        int64(1),
		"element_size": int64(32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSyncBegin(bad); err == nil {
		t.Error("DecodeSyncBegin should reject wrong msg_type")
	}
}
