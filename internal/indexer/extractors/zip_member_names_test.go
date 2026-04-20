package extractors

import "testing"

// TestZipMemberNamesCorruptHeader covers the zip.NewReader
// error branch in zipMemberNames. Bytes that look like a ZIP
// (right magic) but have a corrupt central directory cause
// zip.NewReader to fail.
func TestZipMemberNamesCorruptHeader(t *testing.T) {
	t.Parallel()
	// Just the local file header magic followed by garbage —
	// no central directory at all.
	bad := []byte{0x50, 0x4b, 0x03, 0x04, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	if _, err := zipMemberNames(bad); err == nil {
		t.Error("zipMemberNames on corrupt ZIP should error")
	}
}
