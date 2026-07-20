package ltepwire_test

import (
	"bytes"
	"testing"

	"github.com/swartznet/swartznet/contracts/ltepwire"
)

// TestSyncGoldenVectors pins byte-exact sync-frame output.
func TestSyncGoldenVectors(t *testing.T) {
	t.Parallel()

	begin, err := ltepwire.EncodeSyncBegin(ltepwire.SyncBegin{
		TxID: 7, LocalCount: 3, MaxSymbols: 2000, MaxBytes: 1048576,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantBegin := "d4:algo8:riblt-v112:element_sizei32e6:filterde11:local_counti3e9:max_bytesi1048576e11:max_symbolsi2000e8:msg_typei4e4:txidi7ee"
	if !bytes.Equal(begin, []byte(wantBegin)) {
		t.Errorf("sync_begin:\n got  %q\n want %q", begin, wantBegin)
	}

	var b [32]byte
	for i := range b {
		b[i] = byte(i)
	}
	sym, err := ltepwire.EncodeSyncSymbols(ltepwire.SyncSymbols{
		TxID: 7, Index: 0,
		Symbols: []ltepwire.SyncSymbol{{C: 1, H: 0x0102030405060708, B: b[:]}},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantSym := "d5:indexi0e8:msg_typei5e7:symbolsld1:b32:" + string(b[:]) + "1:ci1e1:hi72623859790382856eee4:txidi7ee"
	if !bytes.Equal(sym, []byte(wantSym)) {
		t.Errorf("sync_symbols:\n got  %q\n want %q", sym, wantSym)
	}
}

// TestSyncDecodeStrictness: element_size≠32 and empty symbols reject.
func TestSyncDecodeStrictness(t *testing.T) {
	t.Parallel()
	// element_size 16 → reject.
	bad, _ := ltepwire.EncodeSyncBegin(ltepwire.SyncBegin{TxID: 1, ElementSize: 16})
	if _, err := ltepwire.DecodeSyncBegin(bad); err == nil {
		t.Error("element_size≠32 must reject")
	}
	// empty symbols → encode error.
	if _, err := ltepwire.EncodeSyncSymbols(ltepwire.SyncSymbols{TxID: 1}); err == nil {
		t.Error("empty sync_symbols must error on encode")
	}
	// cross-decode: a sync_begin decoded as sync_end errors.
	begin, _ := ltepwire.EncodeSyncBegin(ltepwire.SyncBegin{TxID: 1, LocalCount: 0})
	if _, err := ltepwire.DecodeSyncEnd(begin); err == nil {
		t.Error("decoding sync_begin as sync_end must error")
	}
}

// TestSyncRecordShapeEnforced: bad sizes / oversized kw reject.
func TestSyncRecordShapeEnforced(t *testing.T) {
	t.Parallel()
	good := ltepwire.SyncRecord{Pk: make([]byte, 32), Kw: "linux", Ih: make([]byte, 20), Sig: make([]byte, 64)}
	if _, err := ltepwire.EncodeSyncRecords(ltepwire.SyncRecords{TxID: 1, Records: []ltepwire.SyncRecord{good}}); err != nil {
		t.Fatalf("valid record should encode: %v", err)
	}
	bad := good
	bad.Pk = make([]byte, 31)
	if _, err := ltepwire.EncodeSyncRecords(ltepwire.SyncRecords{TxID: 1, Records: []ltepwire.SyncRecord{bad}}); err == nil {
		t.Error("31-byte pk must reject")
	}
	longkw := good
	longkw.Kw = string(make([]byte, 65))
	if _, err := ltepwire.EncodeSyncRecords(ltepwire.SyncRecords{TxID: 1, Records: []ltepwire.SyncRecord{longkw}}); err == nil {
		t.Error("65-byte kw must reject")
	}
}

// TestSyncEndDefaults: status defaults to converged.
func TestSyncEndDefaults(t *testing.T) {
	t.Parallel()
	out, _ := ltepwire.EncodeSyncEnd(ltepwire.SyncEnd{TxID: 5})
	dec, err := ltepwire.DecodeSyncEnd(out)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Status != ltepwire.SyncStatusConverged {
		t.Errorf("status = %q, want converged", dec.Status)
	}
}
