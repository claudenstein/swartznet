package extractors

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// buildMinimalMOBI synthesises a tiny but structurally-correct
// MOBI file with two EXTH records (Title + Author). Enough to
// exercise the metadata extraction path.
func buildMinimalMOBI(t *testing.T) []byte {
	t.Helper()

	title := "My Test Book"
	author := "Jane Doe"

	// EXTH block: 12-byte header + records.
	// Record format: type(4) | length(4) | data
	exthRec := func(typ uint32, data string) []byte {
		r := make([]byte, 8+len(data))
		binary.BigEndian.PutUint32(r[0:4], typ)
		binary.BigEndian.PutUint32(r[4:8], uint32(8+len(data)))
		copy(r[8:], data)
		return r
	}
	exthAuthor := exthRec(100, author)
	exthBody := append([]byte{}, exthAuthor...)

	exthHdr := make([]byte, 12)
	copy(exthHdr[0:4], "EXTH")
	binary.BigEndian.PutUint32(exthHdr[4:8], uint32(12+len(exthBody)))
	binary.BigEndian.PutUint32(exthHdr[8:12], 1) // 1 record
	exth := append(exthHdr, exthBody...)

	// MOBI header: 132 bytes + a bit for safety. Fields we set:
	//   0-3   "MOBI"
	//   4-7   header length (uint32 BE)
	//   12-15 text encoding (65001 = UTF-8)
	//   84-87 title offset within record 0
	//   88-91 title length
	//   128-131 EXTH flags (0x40 = EXTH present)
	mobiHeaderLen := uint32(132)
	mobi := make([]byte, mobiHeaderLen)
	copy(mobi[0:4], "MOBI")
	binary.BigEndian.PutUint32(mobi[4:8], mobiHeaderLen)
	binary.BigEndian.PutUint32(mobi[12:16], 65001)
	binary.BigEndian.PutUint32(mobi[128:132], 0x40)

	// PalmDOC header: 16 bytes of anything, we don't decode it.
	palmDocHdr := make([]byte, 16)

	// Record 0 layout: palmDocHdr + mobiHeader + EXTH + title bytes.
	// Title goes after EXTH. Compute offsets:
	rec0 := make([]byte, 0, len(palmDocHdr)+len(mobi)+len(exth)+len(title))
	rec0 = append(rec0, palmDocHdr...)
	rec0 = append(rec0, mobi...)
	rec0 = append(rec0, exth...)
	titleOffset := uint32(len(rec0))
	rec0 = append(rec0, title...)
	// Now set title offset+length in the MOBI header (inside rec0).
	binary.BigEndian.PutUint32(rec0[16+84:16+88], titleOffset)
	binary.BigEndian.PutUint32(rec0[16+88:16+92], uint32(len(title)))

	// PalmDB header: 78 bytes + 8-byte record index for one
	// record. Record 0 begins immediately after the index.
	palmDB := make([]byte, 78)
	copy(palmDB[0:32], []byte("mybook"))         // name
	binary.BigEndian.PutUint16(palmDB[76:78], 1) // 1 record

	// Record index: offset | attrs+uid
	recIdx := make([]byte, 8)
	rec0Offset := uint32(len(palmDB) + len(recIdx))
	binary.BigEndian.PutUint32(recIdx[0:4], rec0Offset)

	var buf bytes.Buffer
	buf.Write(palmDB)
	buf.Write(recIdx)
	buf.Write(rec0)
	return buf.Bytes()
}

func TestMOBIExtractorReadsTitleAndAuthor(t *testing.T) {
	t.Parallel()
	data := buildMinimalMOBI(t)

	chunks, err := NewMOBIExtractor().Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	text := chunks[0].Text
	for _, want := range []string{"Title: My Test Book", "Author: Jane Doe"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func TestMOBIExtractorRejectsJunk(t *testing.T) {
	t.Parallel()
	_, err := NewMOBIExtractor().Extract(strings.NewReader("not a mobi file"), 0)
	if err == nil {
		t.Error("expected error for junk input")
	}
}

func TestMOBIExtractorDispatchByExtension(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"book.mobi", "book.azw", "book.azw3"} {
		e, _ := Dispatch(Candidate{Path: path, Size: 1024})
		if e == nil || e.Name() != "mobi" {
			t.Errorf("%s: got extractor=%v, want mobi", path, e)
		}
	}
}
