package swarmsearch

import (
	"bytes"
	"testing"
)

func TestSyncBeginRoundTrip(t *testing.T) {
	var pk [32]byte
	pk[0] = 0xAA
	msg := SyncBegin{
		TxID: 42,
		Filter: SyncFilter{
			Pubkeys: [][]byte{pk[:]},
			Since:   1712649600,
			Prefix:  "lin",
		},
		LocalCount: 1000,
		MaxSymbols: 500,
		MaxBytes:   100000,
	}
	raw, err := EncodeSyncBegin(msg)
	if err != nil {
		t.Fatalf("EncodeSyncBegin: %v", err)
	}
	got, err := DecodeSyncBegin(raw)
	if err != nil {
		t.Fatalf("DecodeSyncBegin: %v", err)
	}
	if got.TxID != msg.TxID {
		t.Errorf("TxID mismatch")
	}
	if got.Algo != "riblt-v1" {
		t.Errorf("Algo = %q, want riblt-v1", got.Algo)
	}
	if got.ElementSize != 32 {
		t.Errorf("ElementSize = %d, want 32", got.ElementSize)
	}
	if !bytes.Equal(got.Filter.Pubkeys[0], pk[:]) {
		t.Errorf("pubkey mismatch")
	}
}

func TestSyncSymbolsRoundTrip(t *testing.T) {
	symbols := make([]SyncSymbol, 3)
	for i := range symbols {
		var b [32]byte
		b[0] = byte(i)
		symbols[i] = SyncSymbol{Count: int32(i + 1), KeyXOR: uint64(i * 0x1234), DataXOR: b[:]}
	}
	msg := SyncSymbols{TxID: 7, Symbols: symbols, Index: 100}
	raw, err := EncodeSyncSymbols(msg)
	if err != nil {
		t.Fatalf("EncodeSyncSymbols: %v", err)
	}
	got, err := DecodeSyncSymbols(raw)
	if err != nil {
		t.Fatalf("DecodeSyncSymbols: %v", err)
	}
	if got.TxID != 7 || got.Index != 100 || len(got.Symbols) != 3 {
		t.Errorf("fields mismatch: %+v", got)
	}
}

func TestSyncSymbolsRejectsEmptyOrOversized(t *testing.T) {
	if _, err := EncodeSyncSymbols(SyncSymbols{TxID: 1, Symbols: nil}); err == nil {
		t.Error("expected error for empty symbols")
	}
	oversize := make([]SyncSymbol, MaxSymbolsPerMessage+1)
	for i := range oversize {
		var b [32]byte
		oversize[i] = SyncSymbol{DataXOR: b[:]}
	}
	if _, err := EncodeSyncSymbols(SyncSymbols{TxID: 1, Symbols: oversize}); err == nil {
		t.Error("expected error for oversize symbols")
	}
}

func TestSyncSymbolsRejectsBadDataXOR(t *testing.T) {
	bad := []SyncSymbol{{Count: 1, DataXOR: make([]byte, 16)}}
	if _, err := EncodeSyncSymbols(SyncSymbols{TxID: 1, Symbols: bad}); err == nil {
		t.Error("expected error for 16-byte DataXOR")
	}
}

func TestSyncNeedRoundTrip(t *testing.T) {
	ids := [][]byte{make([]byte, 32), make([]byte, 32)}
	ids[0][0] = 0xDD
	ids[1][31] = 0xEE
	msg := SyncNeed{TxID: 5, IDs: ids}
	raw, err := EncodeSyncNeed(msg)
	if err != nil {
		t.Fatalf("EncodeSyncNeed: %v", err)
	}
	got, err := DecodeSyncNeed(raw)
	if err != nil {
		t.Fatalf("DecodeSyncNeed: %v", err)
	}
	if len(got.IDs) != 2 {
		t.Fatalf("ids count mismatch: %d", len(got.IDs))
	}
}

func TestSyncNeedRejectsShortIDs(t *testing.T) {
	ids := [][]byte{make([]byte, 20)}
	if _, err := EncodeSyncNeed(SyncNeed{TxID: 1, IDs: ids}); err == nil {
		t.Error("expected error for 20-byte id")
	}
}

func TestSyncRecordsRoundTrip(t *testing.T) {
	r := SyncRecord{
		Pk:  make([]byte, 32),
		Kw:  "linux",
		Ih:  make([]byte, 20),
		T:   100,
		Pow: 1234,
		Sig: make([]byte, 64),
	}
	msg := SyncRecords{TxID: 99, Records: []SyncRecord{r}}
	raw, err := EncodeSyncRecords(msg)
	if err != nil {
		t.Fatalf("EncodeSyncRecords: %v", err)
	}
	got, err := DecodeSyncRecords(raw)
	if err != nil {
		t.Fatalf("DecodeSyncRecords: %v", err)
	}
	if len(got.Records) != 1 || got.Records[0].Kw != "linux" {
		t.Errorf("records mismatch: %+v", got.Records)
	}
}

func TestSyncRecordsRejectsBadSizes(t *testing.T) {
	r := SyncRecord{Pk: make([]byte, 16), Ih: make([]byte, 20), Sig: make([]byte, 64)}
	if _, err := EncodeSyncRecords(SyncRecords{TxID: 1, Records: []SyncRecord{r}}); err == nil {
		t.Error("expected error for 16-byte pk")
	}
}

func TestSyncEndRoundTrip(t *testing.T) {
	msg := SyncEnd{TxID: 1, Status: SyncStatusConverged, Decoded: 5, Sent: 123}
	raw, err := EncodeSyncEnd(msg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeSyncEnd(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != SyncStatusConverged || got.Decoded != 5 {
		t.Errorf("fields mismatch: %+v", got)
	}
}

// Decoders reject packets with the wrong msg_type discriminator.
func TestSyncDecodersRejectWrongMsgType(t *testing.T) {
	raw, _ := EncodeSyncBegin(SyncBegin{TxID: 1})
	if _, err := DecodeSyncSymbols(raw); err == nil {
		t.Error("DecodeSyncSymbols should reject a sync_begin payload")
	}
	if _, err := DecodeSyncNeed(raw); err == nil {
		t.Error("DecodeSyncNeed should reject a sync_begin payload")
	}
	if _, err := DecodeSyncRecords(raw); err == nil {
		t.Error("DecodeSyncRecords should reject a sync_begin payload")
	}
	if _, err := DecodeSyncEnd(raw); err == nil {
		t.Error("DecodeSyncEnd should reject a sync_begin payload")
	}
}
