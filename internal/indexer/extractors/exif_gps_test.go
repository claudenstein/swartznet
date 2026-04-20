package extractors

import (
	"encoding/binary"
	"testing"
)

// TestParseTIFFGPSIFDExpansion drives the previously-uncovered
// GPS IFD branch of parseTIFF: when IFD0 contains tag 0x8825
// (GPS pointer), the function recurses into that sub-IFD and
// emits a synthetic 0x8825 entry of the form "<latRef><lat>, <lonRef><lon>".
// This also exercises the typ=2 (ASCII) and typ=5 (RATIONAL,
// out-of-line) branches of parseIFD.
func TestParseTIFFGPSIFDExpansion(t *testing.T) {
	t.Parallel()

	const (
		gpsIFDOff = 22
		latOff    = 72
		lonOff    = 96
		bufLen    = 120
	)
	bo := binary.LittleEndian
	tiff := make([]byte, bufLen)

	// TIFF header: II + magic + IFD0 offset (8).
	copy(tiff[0:2], "II")
	tiff[2] = 0x2A
	bo.PutUint32(tiff[4:8], 8)

	// IFD0 at offset 8 — exactly one entry: GPS IFD pointer.
	bo.PutUint16(tiff[8:10], 1) // count
	// entry: tag=0x8825, typ=4(LONG), cnt=1, val=gpsIFDOff
	bo.PutUint16(tiff[10:12], 0x8825)
	bo.PutUint16(tiff[12:14], 4)
	bo.PutUint32(tiff[14:18], 1)
	bo.PutUint32(tiff[18:22], gpsIFDOff)

	// GPS IFD at offset 22 — four entries: latRef, lat, lonRef, lon.
	bo.PutUint16(tiff[22:24], 4) // count
	// latRef: tag=0x0001, typ=2(ASCII), cnt=2, val="N\x00\x00\x00" inline
	bo.PutUint16(tiff[24:26], 0x0001)
	bo.PutUint16(tiff[26:28], 2)
	bo.PutUint32(tiff[28:32], 2)
	tiff[32] = 'N'
	// lat: tag=0x0002, typ=5(RATIONAL), cnt=3, val=latOff (out-of-line)
	bo.PutUint16(tiff[36:38], 0x0002)
	bo.PutUint16(tiff[38:40], 5)
	bo.PutUint32(tiff[40:44], 3)
	bo.PutUint32(tiff[44:48], latOff)
	// lonRef: tag=0x0003, typ=2(ASCII), cnt=2, val="E\x00\x00\x00" inline
	bo.PutUint16(tiff[48:50], 0x0003)
	bo.PutUint16(tiff[50:52], 2)
	bo.PutUint32(tiff[52:56], 2)
	tiff[56] = 'E'
	// lon: tag=0x0004, typ=5(RATIONAL), cnt=3, val=lonOff (out-of-line)
	bo.PutUint16(tiff[60:62], 0x0004)
	bo.PutUint16(tiff[62:64], 5)
	bo.PutUint32(tiff[64:68], 3)
	bo.PutUint32(tiff[68:72], lonOff)

	// lat data at offset 72: 37/1, 25/1, 30/1 → 37.4250
	for i, num := range []uint32{37, 25, 30} {
		bo.PutUint32(tiff[latOff+i*8:latOff+i*8+4], num)
		bo.PutUint32(tiff[latOff+i*8+4:latOff+i*8+8], 1)
	}
	// lon data at offset 96: 122/1, 25/1, 0/1 → 122.4167
	for i, num := range []uint32{122, 25, 0} {
		bo.PutUint32(tiff[lonOff+i*8:lonOff+i*8+4], num)
		bo.PutUint32(tiff[lonOff+i*8+4:lonOff+i*8+8], 1)
	}

	got, err := parseTIFF(tiff)
	if err != nil {
		t.Fatalf("parseTIFF: %v", err)
	}
	// parseTIFF only surfaces a synthetic 0x8825 entry from the
	// GPS sub-IFD; the individual lat/lon/ref tags stay in the
	// inner `gps` map and are not merged into `out`.
	want := "N37.4250, E122.4167"
	if got[0x8825] != want {
		t.Errorf("parseTIFF GPS = %q, want %q (full map: %#v)", got[0x8825], want, got)
	}
}
