package extractors

import (
	"strings"
	"testing"
)

func TestFB2ExtractorBasic(t *testing.T) {
	t.Parallel()

	// Minimal FB2 document with a title, an author, a body
	// section with two paragraphs, and a <binary> cover art
	// element that must be skipped.
	fb2 := `<?xml version="1.0" encoding="UTF-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <description>
    <title-info>
      <book-title>Test Novel</book-title>
      <author><first-name>Jane</first-name><last-name>Doe</last-name></author>
    </title-info>
  </description>
  <body>
    <section>
      <title><p>Chapter 1</p></title>
      <p>Once upon a time in a small village.</p>
      <p>The story continues with adventure.</p>
    </section>
  </body>
  <binary id="cover" content-type="image/jpeg">ABCDEFGHIJKLMOP</binary>
</FictionBook>`

	chunks, err := NewFB2Extractor().Extract(strings.NewReader(fb2), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	all := ""
	for _, c := range chunks {
		all += c.Text + "\n"
	}

	for _, want := range []string{"Test Novel", "Jane", "Doe", "Chapter 1", "Once upon a time", "adventure"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	// Cover art bytes should NOT leak.
	if strings.Contains(all, "ABCDEF") {
		t.Error("binary cover art leaked into output")
	}
}

func TestFB2ExtractorDispatchByExtension(t *testing.T) {
	t.Parallel()
	e, _ := Dispatch(Candidate{Path: "book.fb2", Size: 1024})
	if e == nil || e.Name() != "fb2" {
		t.Errorf("got extractor=%v, want fb2", e)
	}
}
