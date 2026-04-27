package companion

import "testing"

// TestCompareRecordsLengthFallthrough covers compareRecords'
// `if len(ka) < len(kb) { return -1 }` and the symmetric > arm.
// To trigger them ka must be a strict prefix of kb (every byte
// equal up to len(ka)). Real Records can't construct that
// because RecordKey = Kw + 0x00 + 20-byte Ih and the null
// separator + fixed Ih length keep keys at len(Kw)+21. Internal
// tests can hand-build a Record whose Kw embeds nulls, making
// one key a true byte-prefix of the other.
func TestCompareRecordsLengthFallthrough(t *testing.T) {
	t.Parallel()
	// shortKw = "x", longKw = "x" + "\0" + 20 zeros — encodes the
	// same first 22 bytes of RecordKey.
	short := Record{Kw: "x"}
	long := Record{Kw: "x\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"}
	// All-zero Ihs ensure the trailing 20-byte Ih segment matches.

	if got := compareRecords(short, long); got != -1 {
		t.Errorf("compareRecords(short, long) = %d, want -1 (ka is prefix of kb)", got)
	}
	if got := compareRecords(long, short); got != 1 {
		t.Errorf("compareRecords(long, short) = %d, want 1 (kb is prefix of ka)", got)
	}
	// Equal keys still return 0 — sanity.
	if got := compareRecords(short, short); got != 0 {
		t.Errorf("compareRecords(short, short) = %d, want 0", got)
	}
}
