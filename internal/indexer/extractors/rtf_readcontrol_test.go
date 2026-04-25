package extractors

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// readControlOn runs readControl against a string fed through a
// bufio.Reader and returns its (word, param, hex, err) tuple.
// Convenience helper so the test table stays readable.
func readControlOn(s string) (string, int, string, error) {
	return readControl(bufio.NewReader(strings.NewReader(s)))
}

// TestReadControlSingleCharEscapes covers the first switch in
// readControl: \\, \{, \}, \* — each returns the literal char in
// the hex slot and an empty word.
func TestReadControlSingleCharEscapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in  string
		hex string
	}{
		{"\\rest", "\\"},
		{"{rest", "{"},
		{"}rest", "}"},
		{"*rest", "*"},
	}
	for _, c := range cases {
		w, p, h, err := readControlOn(c.in)
		if err != nil {
			t.Errorf("readControl(%q) err = %v", c.in, err)
			continue
		}
		if w != "" || p != 0 || h != c.hex {
			t.Errorf("readControl(%q) = (%q,%d,%q), want (\"\",0,%q)",
				c.in, w, p, h, c.hex)
		}
	}
}

// TestReadControlHexEscape covers the \'XX path — two hex digits
// after the apostrophe are returned in the hex slot and the word
// is "'".
func TestReadControlHexEscape(t *testing.T) {
	t.Parallel()
	w, _, h, err := readControlOn("'4ftrailing")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if w != "'" || h != "4f" {
		t.Errorf("got (%q,_,%q), want (\"'\",_,\"4f\")", w, h)
	}
}

// TestReadControlHexEscapeShortInput covers the two short-input
// fall-throughs in the \'XX branch (only one digit available,
// then no digits available).
func TestReadControlHexEscapeShortInput(t *testing.T) {
	t.Parallel()
	// One byte after the apostrophe, then EOF — d1=ok, d2=err.
	w, _, h, err := readControlOn("'a")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if w != "'" || h != "a" {
		t.Errorf("one-digit: got (%q,_,%q), want (\"'\",_,\"a\")", w, h)
	}
	// Just the apostrophe, then EOF — d1=err.
	w2, _, h2, err := readControlOn("'")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if w2 != "'" || h2 != "" {
		t.Errorf("zero-digit: got (%q,_,%q), want (\"'\",_,\"\")", w2, h2)
	}
}

// TestReadControlEOFOnFirstByte covers the very first ReadByte
// error branch — an empty reader propagates EOF.
func TestReadControlEOFOnFirstByte(t *testing.T) {
	t.Parallel()
	_, _, _, err := readControlOn("")
	if err == nil {
		t.Error("expected EOF on empty input")
	}
}

// TestReadControlNonAlphaLiteral covers the `!isAlpha(first)`
// fall-through — characters like '~', '|', '#' are returned as
// empty word, no hex.
func TestReadControlNonAlphaLiteral(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"~", "|", "#"} {
		w, p, h, err := readControlOn(in)
		if err != nil {
			t.Errorf("err = %v", err)
		}
		if w != "" || p != 0 || h != "" {
			t.Errorf("readControl(%q) = (%q,%d,%q)", in, w, p, h)
		}
	}
}

// TestReadControlWordWithSpaceTerminator covers the standard RTF
// control word "rtf1 " — letters, then a digit-parameter, then a
// space terminator that should be consumed.
func TestReadControlWordWithSpaceTerminator(t *testing.T) {
	t.Parallel()
	br := bufio.NewReader(strings.NewReader("rtf1 next"))
	w, p, h, err := readControl(br)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if w != "rtf" || p != 1 || h != "" {
		t.Errorf("got (%q,%d,%q), want (\"rtf\",1,\"\")", w, p, h)
	}
	// After consumption, "next" should be readable directly.
	rest, _ := br.ReadString(0)
	if !bytes.Equal([]byte(rest), []byte("next")) {
		t.Errorf("remainder = %q, want %q", rest, "next")
	}
}

// TestReadControlWordNonSpaceTerminator covers the UnreadByte
// branch — a non-letter, non-digit terminator (e.g. backslash)
// must be pushed back so the caller sees it.
func TestReadControlWordNonSpaceTerminator(t *testing.T) {
	t.Parallel()
	br := bufio.NewReader(strings.NewReader("ansi\\next"))
	w, p, _, err := readControl(br)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if w != "ansi" || p != 0 {
		t.Errorf("word = (%q,%d), want (\"ansi\",0)", w, p)
	}
	// The backslash must still be available in the reader.
	b, err := br.ReadByte()
	if err != nil || b != '\\' {
		t.Errorf("expected unread '\\\\', got %q err=%v", b, err)
	}
}

// TestReadControlNegativeParameter covers the optional minus in
// the parameter — \uN where N is negative.
func TestReadControlNegativeParameter(t *testing.T) {
	t.Parallel()
	w, p, _, err := readControlOn("u-233 ")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if w != "u" || p != -233 {
		t.Errorf("got (%q,%d), want (\"u\",-233)", w, p)
	}
}
