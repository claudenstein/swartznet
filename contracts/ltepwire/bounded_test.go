package ltepwire

import (
	"runtime"
	"strings"
	"testing"
)

// TestDecodeBoundedRejectsAllocAmplification pins the HIGH round-5 fix: an
// inbound frame that declares a huge inner string (here txid as a ~120 MiB
// bencoded string with 1 real byte) must NOT drive a ~128 MiB allocation before
// erroring. Unbounded, anacrolix does make([]byte, declaredLen) before the read
// fails; bounded to len(payload), parseStringLength rejects the impossible
// length immediately. We assert both the error AND that the transient allocation
// is tiny — the amplification, not just the outcome, is what closes the DoS.
func TestDecodeBoundedRejectsAllocAmplification(t *testing.T) {
	// d{msg_type:0}{txid:<string len 125829120 = 120 MiB, content "X">}
	frame := []byte("d8:msg_typei0e4:txid125829120:Xe")

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := PeekMsgType(frame)
	runtime.ReadMemStats(&after)

	if err == nil {
		t.Fatal("malicious amplification frame decoded without error")
	}
	const cap = 32 << 20 // 32 MiB — far above a bounded decode, far below 120 MiB
	if grew := after.TotalAlloc - before.TotalAlloc; grew > cap {
		t.Fatalf("PeekMsgType allocated %d bytes for a %d-byte frame — amplification not bounded", grew, len(frame))
	}
}

// TestDecodeBoundedRejectsHugeQuery covers the DecodeQuery path the same way: a
// tiny frame declaring `q` as a huge string must not over-allocate.
func TestDecodeBoundedRejectsHugeQuery(t *testing.T) {
	frame := []byte("d8:msg_typei0e1:q125829120:Xe")
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := DecodeQuery(frame)
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("huge-q frame decoded without error")
	}
	const cap = 32 << 20
	if grew := after.TotalAlloc - before.TotalAlloc; grew > cap {
		t.Fatalf("DecodeQuery allocated %d bytes for a %d-byte frame — amplification not bounded", grew, len(frame))
	}
}

// TestDecodeBoundedStillDecodesValidFrames guards against over-tightening: a
// normal, well-formed frame with legitimately-sized strings must still decode.
func TestDecodeBoundedStillDecodesValidFrames(t *testing.T) {
	raw, err := EncodeQuery(Query{TxID: 42, Q: strings.Repeat("ubuntu ", 20), Scope: "nfc", Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	q, err := DecodeQuery(raw)
	if err != nil {
		t.Fatalf("valid frame rejected by bounded decode: %v", err)
	}
	if q.TxID != 42 || !strings.HasPrefix(q.Q, "ubuntu") || q.Limit != 25 {
		t.Fatalf("round-trip corrupted: %+v", q)
	}
	mt, err := PeekMsgType(raw)
	if err != nil || mt != MsgTypeQuery {
		t.Fatalf("PeekMsgType(valid) = %d, %v", mt, err)
	}
}
