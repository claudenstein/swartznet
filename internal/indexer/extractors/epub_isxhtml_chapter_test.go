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

// TestIsXHTMLChapterMetaInfExcluded pins the case-insensitive
// META-INF/ exclusion and the accepted extensions.
func TestIsXHTMLChapterMetaInfExcluded(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		{"META-INF/container.xhtml", false},
		{"meta-inf/anything.html", false},
		{"OEBPS/ch1.xhtml", true},
		{"ch1.html", true},
		{"deep/nested/ch1.htm", true},
		{"content.opf", false},
		{"toc.ncx", false},
	}
	for _, tc := range cases {
		if got := isXHTMLChapter(tc.name); got != tc.want {
			t.Errorf("isXHTMLChapter(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
