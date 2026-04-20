package extractors

import (
	"encoding/binary"
	"strings"
	"testing"
)

// makeEXTH builds a synthetic EXTH header with the given records.
// Layout per MOBI spec:
//
//	bytes  0..3  "EXTH"
//	bytes  4..7  header length (we don't validate this)
//	bytes  8..11 record count
//	then `count` records, each:
//	  4-byte BE recType, 4-byte BE recLen (incl. these 8 bytes), payload
func makeEXTH(records []struct {
	recType uint32
	payload []byte
}) []byte {
	var body []byte
	for _, r := range records {
		recLen := uint32(8 + len(r.payload))
		var hdr [8]byte
		binary.BigEndian.PutUint32(hdr[0:4], r.recType)
		binary.BigEndian.PutUint32(hdr[4:8], recLen)
		body = append(body, hdr[:]...)
		body = append(body, r.payload...)
	}
	out := make([]byte, 12+len(body))
	copy(out[0:4], "EXTH")
	binary.BigEndian.PutUint32(out[4:8], 12+uint32(len(body))) // header length
	binary.BigEndian.PutUint32(out[8:12], uint32(len(records)))
	copy(out[12:], body)
	return out
}

// TestExtractEXTHTooShort covers the len(b) < 12 guard.
func TestExtractEXTHTooShort(t *testing.T) {
	t.Parallel()
	if got := extractEXTH([]byte("EXTH"), 65001); got != "" {
		t.Errorf("expected empty for short input, got %q", got)
	}
}

// TestExtractEXTHWrongMagic covers the magic-mismatch guard.
func TestExtractEXTHWrongMagic(t *testing.T) {
	t.Parallel()
	bad := make([]byte, 16)
	copy(bad[0:4], "XXXX")
	if got := extractEXTH(bad, 65001); got != "" {
		t.Errorf("expected empty for wrong magic, got %q", got)
	}
}

// TestExtractEXTHAllLabels exercises every documented recType
// branch: 100, 101, 103, 104, 105, 106, 503, 524 — plus a 999
// record that hits the `default: continue` fall-through (so its
// payload must NOT appear in the output).
func TestExtractEXTHAllLabels(t *testing.T) {
	t.Parallel()
	recs := []struct {
		recType uint32
		payload []byte
	}{
		{100, []byte("Jane Author")},
		{101, []byte("Acme Press")},
		{103, []byte("A short blurb")},
		{104, []byte("978-0-00-000000-0")},
		{105, []byte("Reference")},
		{106, []byte("2026-04-20")},
		{503, []byte("My Updated Title")},
		{524, []byte("en-US")},
		{999, []byte("hidden-payload")}, // unknown → default branch
	}
	got := extractEXTH(makeEXTH(recs), 65001)
	for _, want := range []string{
		"Author: Jane Author",
		"Publisher: Acme Press",
		"Description: A short blurb",
		"ISBN: 978-0-00-000000-0",
		"Subject: Reference",
		"Published: 2026-04-20",
		"Title: My Updated Title",
		"Language: en-US",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull: %q", want, got)
		}
	}
	// Default-branch sanity: the unknown-recType payload must
	// NOT have leaked into the output.
	if strings.Contains(got, "hidden-payload") {
		t.Errorf("default-branch leak: unknown-recType payload appeared in output: %q", got)
	}
}

// TestExtractEXTHTruncatedRecord covers the
// `recLen < 8 || p+recLen > len(b)` break branch — count claims
// 2 records but the buffer ends mid-second-record.
func TestExtractEXTHTruncatedRecord(t *testing.T) {
	t.Parallel()
	// Build a valid first record, then append a header that
	// claims a recLen larger than what's left.
	good := makeEXTH([]struct {
		recType uint32
		payload []byte
	}{
		{100, []byte("First Author")},
	})
	// Bump count to 2 so the loop attempts a second iteration.
	binary.BigEndian.PutUint32(good[8:12], 2)
	// Append a truncated second record header claiming recLen=999.
	var trunc [8]byte
	binary.BigEndian.PutUint32(trunc[0:4], 101)
	binary.BigEndian.PutUint32(trunc[4:8], 999)
	good = append(good, trunc[:]...)

	got := extractEXTH(good, 65001)
	if !strings.Contains(got, "Author: First Author") {
		t.Errorf("first record missing: %q", got)
	}
	// The truncated record must not produce any "Publisher" line.
	if strings.Contains(got, "Publisher") {
		t.Errorf("truncated record should not be decoded: %q", got)
	}
}
