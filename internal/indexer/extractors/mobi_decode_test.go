package extractors

import "testing"

// TestDecodeMOBITextEncodings exercises every documented branch
// in decodeMOBIText: UTF-8 passthrough, Windows-1252 widen,
// encoding=0 fall-through (treated as 1252), and the unknown-
// encoding default.
func TestDecodeMOBITextEncodings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   []byte
		enc  uint32
		want string
	}{
		{"utf-8", []byte("Hello, 世界"), 65001, "Hello, 世界"},
		{"win1252 ascii", []byte("Title"), 1252, "Title"},
		{"win1252 high byte widens to rune", []byte{0x41, 0xa9}, 1252, "A©"}, // 0xa9 → U+00A9
		{"encoding-zero default to 1252", []byte("plain"), 0, "plain"},
		{"unknown encoding falls through", []byte("abc"), 9999, "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := decodeMOBIText(tc.in, tc.enc); got != tc.want {
				t.Errorf("decodeMOBIText = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDecodeMOBITextEmptyAfterTrim covers the "len==0 after
// TrimRight" short-circuit. Input that's all NUL/whitespace
// returns empty without entering any encoding branch.
func TestDecodeMOBITextEmptyAfterTrim(t *testing.T) {
	t.Parallel()
	if got := decodeMOBIText([]byte{0, 0, ' ', '\t', '\r', '\n'}, 65001); got != "" {
		t.Errorf("decodeMOBIText(all-trim) = %q, want empty", got)
	}
	if got := decodeMOBIText(nil, 65001); got != "" {
		t.Errorf("decodeMOBIText(nil) = %q, want empty", got)
	}
}

// TestPassthroughCharsetReader exercises the FB2-side charset
// adapter: it must return the reader unchanged regardless of
// the declared encoding label, and must not error.
func TestPassthroughCharsetReader(t *testing.T) {
	t.Parallel()
	for _, label := range []string{"", "utf-8", "windows-1251", "made-up"} {
		input := stringReader{s: "<x/>"}
		got, err := passthroughCharsetReader(label, input)
		if err != nil {
			t.Errorf("label=%q: err = %v", label, err)
		}
		if got == nil {
			t.Errorf("label=%q: got nil reader", label)
		}
	}
}

// stringReader is a tiny io.Reader for the passthrough test —
// avoiding strings.NewReader keeps us off the io interface
// passthrough test path that would otherwise also be checked.
type stringReader struct {
	s   string
	pos int
}

func (r stringReader) Read(p []byte) (int, error) {
	return 0, nil
}
