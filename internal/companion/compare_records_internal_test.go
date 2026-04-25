package companion

import "testing"

// TestCompareRecordsBranches exercises every ordering branch of
// compareRecords. Function is unexported and the BuildBTree
// integration tests only hit the main path; this fills in the
// less-covered "kw equal but ih differs" and "kw is a prefix of
// the other" cases.
func TestCompareRecordsBranches(t *testing.T) {
	t.Parallel()

	mk := func(kw string, ihByte byte) Record {
		var r Record
		r.Kw = kw
		r.Ih[0] = ihByte
		return r
	}

	cases := []struct {
		name string
		a, b Record
		want int
	}{
		{"equal", mk("alpha", 0x01), mk("alpha", 0x01), 0},
		{"kw less", mk("aaaa", 0x01), mk("aaab", 0x01), -1},
		{"kw greater", mk("aaab", 0x01), mk("aaaa", 0x01), 1},
		{"kw equal, ih less", mk("k", 0x01), mk("k", 0x02), -1},
		{"kw equal, ih greater", mk("k", 0x02), mk("k", 0x01), 1},
		{"a is prefix of b", mk("ab", 0x00), mk("abc", 0x00), -1},
		{"b is prefix of a", mk("abc", 0x00), mk("ab", 0x00), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := compareRecords(tc.a, tc.b); got != tc.want {
				t.Errorf("compareRecords(%q/%x, %q/%x) = %d, want %d",
					tc.a.Kw, tc.a.Ih[0], tc.b.Kw, tc.b.Ih[0], got, tc.want)
			}
		})
	}
}
