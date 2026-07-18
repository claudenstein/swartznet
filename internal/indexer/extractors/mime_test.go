package extractors

import "testing"

// TestMimeFromPathOverridesStdlib pins the documented behaviour
// that the project-local extTypes map takes precedence over the
// stdlib mime.TypeByExtension table — the .ts case is the
// motivating example (stdlib thinks .ts is MPEG-TS video).
func TestMimeFromPathOverridesStdlib(t *testing.T) {
	t.Parallel()
	if got := mimeFromPath("foo.ts"); got != "text/x-typescript" {
		t.Errorf("mimeFromPath(.ts) = %q, want text/x-typescript", got)
	}
}

// TestMimeFromPathFallsBackToStdlib covers the branch where
// extTypes does NOT have the extension but the stdlib does.
// .html / .json / .xml are reliably present in the stdlib table
// across Go versions and not in our override map.
func TestMimeFromPathFallsBackToStdlib(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		// We only assert prefix because stdlib values vary across
		// Go versions (some carry "; charset=utf-8").
		wantPrefix string
	}{
		{"foo.html", "text/html"},
		{"foo.json", "application/json"},
		{"foo.xml", "text/xml"},
	}
	for _, tc := range cases {
		got := mimeFromPath(tc.path)
		if got == "" {
			t.Errorf("mimeFromPath(%q) = empty, expected stdlib fallback", tc.path)
			continue
		}
		// The function strips the "; charset=..." suffix; the
		// remainder must start with the expected type/subtype.
		// Also assert no stray ";" leaked through.
		if got[0] == ';' {
			t.Errorf("mimeFromPath(%q) = %q, leading semicolon", tc.path, got)
		}
		// Must not contain a charset suffix (the function strips it).
		for i := 0; i < len(got); i++ {
			if got[i] == ';' {
				t.Errorf("mimeFromPath(%q) = %q, charset suffix not stripped", tc.path, got)
				break
			}
		}
		// Lenient prefix check (some go versions return application/xml for .xml).
		if !startsWithAny(got, []string{tc.wantPrefix, "application/xml"}) {
			t.Errorf("mimeFromPath(%q) = %q, want prefix %q", tc.path, got, tc.wantPrefix)
		}
	}
}

func TestMimeFromPathEmptyExtension(t *testing.T) {
	t.Parallel()
	if got := mimeFromPath("README"); got != "" {
		t.Errorf("mimeFromPath(no ext) = %q, want empty", got)
	}
	if got := mimeFromPath(""); got != "" {
		t.Errorf("mimeFromPath('') = %q, want empty", got)
	}
}

func TestMimeFromPathUnknownExtension(t *testing.T) {
	t.Parallel()
	// .nosuch-extension-foobar is not in extTypes and not in the
	// stdlib table — both lookups must fall through to "".
	if got := mimeFromPath("file.nosuch-extension-foobar"); got != "" {
		t.Errorf("mimeFromPath(unknown) = %q, want empty", got)
	}
}

// TestDispatchUsesProvidedMIMEWithoutSniffing covers the branch
// in Dispatch where c.MIME is non-empty, so the mimeFromPath
// fallback is skipped. We can't directly observe "skipped" without
// a sniff hook, but we can prove the returned mime equals what we
// passed in (so the caller's value won round-trip).
func TestDispatchUsesProvidedMIMEWithoutSniffing(t *testing.T) {
	t.Parallel()
	_, mime := Dispatch(Candidate{Path: "foo.ts", MIME: "text/plain", Size: 10})
	if mime != "text/plain" {
		t.Errorf("Dispatch returned mime = %q, want \"text/plain\" (caller's value)", mime)
	}
}

func startsWithAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if len(s) >= len(p) && s[:len(p)] == p {
			return true
		}
	}
	return false
}
