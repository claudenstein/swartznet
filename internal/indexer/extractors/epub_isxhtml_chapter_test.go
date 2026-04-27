package extractors

import "testing"

// TestIsXHTMLChapterEmptyName covers the
// `if name == "" || strings.HasSuffix(name, "/") { return false }`
// guard. Empty name returns false directly; a trailing-slash
// (directory) entry returns false too.
func TestIsXHTMLChapterEmptyName(t *testing.T) {
	t.Parallel()
	if isXHTMLChapter("") {
		t.Error("isXHTMLChapter(\"\") should return false")
	}
	if isXHTMLChapter("dir/") {
		t.Error("isXHTMLChapter(\"dir/\") should return false")
	}
}
