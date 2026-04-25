package extractors

import "testing"

// TestID3LabelKnownFrames covers every documented frame ID
// mapping in the id3Label switch, including the synonym pair
// (TDRC/TYER → "Year").
func TestID3LabelKnownFrames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		id, want string
	}{
		{"TIT2", "Title"},
		{"TPE1", "Artist"},
		{"TPE2", "Album artist"},
		{"TALB", "Album"},
		{"TDRC", "Year"},
		{"TYER", "Year"},
		{"TCON", "Genre"},
		{"TRCK", "Track"},
		{"TPUB", "Publisher"},
		{"COMM", "Comment"},
		{"USLT", "Lyrics"},
	}
	for _, tc := range cases {
		if got := id3Label(tc.id); got != tc.want {
			t.Errorf("id3Label(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestID3LabelUnknownReturnsEmpty(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"", "tit2", "TXYZ", "WXXX"} {
		if got := id3Label(id); got != "" {
			t.Errorf("id3Label(%q) = %q, want empty", id, got)
		}
	}
}

// TestDecodeID3TextEncodings exercises every encoding case in
// decodeID3Text/decodeID3Bytes for the simpler frame IDs (TIT2 etc.,
// which have no COMM/USLT language+descriptor prefix).
func TestDecodeID3TextEncodings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body []byte
		want string
	}{
		{
			name: "iso-8859-1",
			body: append([]byte{0}, []byte("Hello\x00")...),
			want: "Hello",
		},
		{
			// UTF-16 LE: ends with U+20AC (€) so the high byte
			// (0x20) is non-zero; otherwise decodeID3Bytes's
			// trailing-NUL trim would eat the last char's pad.
			name: "utf-16 LE BOM",
			body: []byte{1, 0xff, 0xfe, 'H', 0, 'i', 0, 0xac, 0x20},
			want: "Hi€",
		},
		{
			name: "utf-16 BE BOM",
			body: []byte{1, 0xfe, 0xff, 0, 'H', 0, 'i', 0x20, 0xac},
			want: "Hi€",
		},
		{
			name: "utf-16 BE without BOM (enc=2)",
			body: []byte{2, 0, 'O', 0, 'k', 0x20, 0xac},
			want: "Ok€",
		},
		{
			name: "utf-8 (enc=3)",
			body: append([]byte{3}, []byte("Olá")...),
			want: "Olá",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := decodeID3Text(tc.body, "TIT2"); got != tc.want {
				t.Errorf("decodeID3Text = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDecodeID3TextEmptyBody covers the len==0 short-circuit.
func TestDecodeID3TextEmptyBody(t *testing.T) {
	t.Parallel()
	if got := decodeID3Text(nil, "TIT2"); got != "" {
		t.Errorf("decodeID3Text(nil) = %q, want empty", got)
	}
}

// TestDecodeID3TextCOMMSkipsLangAndDescriptor exercises the
// COMM/USLT prefix-skip path: encoding 0 with a NUL-terminated
// descriptor.
func TestDecodeID3TextCOMMSkipsLangAndDescriptor(t *testing.T) {
	t.Parallel()
	// enc=0, lang="eng", descriptor="title\0", payload="hello world".
	body := []byte{0}
	body = append(body, 'e', 'n', 'g')              // lang
	body = append(body, 't', 'i', 't', 'l', 'e', 0) // descriptor + NUL
	body = append(body, []byte("hello world")...)
	if got := decodeID3Text(body, "COMM"); got != "hello world" {
		t.Errorf("decodeID3Text(COMM) = %q, want \"hello world\"", got)
	}
}

// TestDecodeID3TextCOMMUTF16Descriptor exercises the UTF-16
// double-NUL descriptor strip path on COMM frames.
func TestDecodeID3TextCOMMUTF16Descriptor(t *testing.T) {
	t.Parallel()
	// enc=1 (UTF-16), lang="eng", descriptor="ab" UTF-16-LE
	// terminated by an aligned NUL pair, payload="Hi€" UTF-16-LE
	// BOM (€ ensures the trailing-NUL trim doesn't eat the last
	// char's pad byte).
	body := []byte{1}
	body = append(body, 'e', 'n', 'g')                          // lang
	body = append(body, 'a', 0, 'b', 0, 0, 0)                   // descriptor + NUL16
	body = append(body, 0xff, 0xfe, 'H', 0, 'i', 0, 0xac, 0x20) // payload
	if got := decodeID3Text(body, "USLT"); got != "Hi€" {
		t.Errorf("decodeID3Text(USLT) = %q, want \"Hi€\"", got)
	}
}

// TestDecodeID3TextCOMMTooShort covers the len(text) < 3 guard
// in the COMM/USLT branch (short body has nothing past the encoding
// byte).
func TestDecodeID3TextCOMMTooShort(t *testing.T) {
	t.Parallel()
	body := []byte{0, 'e', 'n'} // just enc + 2 bytes
	if got := decodeID3Text(body, "COMM"); got != "" {
		t.Errorf("decodeID3Text(short COMM) = %q, want empty", got)
	}
}
