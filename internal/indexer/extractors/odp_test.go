package extractors

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestODPExtractorBasic(t *testing.T) {
	t.Parallel()

	// Build a minimal .odp: zip with content.xml containing two
	// draw:page elements, each with a <text:p> paragraph.
	content := `<?xml version="1.0" encoding="UTF-8"?>
<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
                        xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"
                        xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0">
  <office:body>
    <office:presentation>
      <draw:page draw:name="page1">
        <draw:frame><draw:text-box>
          <text:p>First slide title</text:p>
          <text:p>Slide one body text</text:p>
        </draw:text-box></draw:frame>
      </draw:page>
      <draw:page draw:name="page2">
        <draw:frame><draw:text-box>
          <text:p>Second slide content</text:p>
        </draw:text-box></draw:frame>
      </draw:page>
    </office:presentation>
  </office:body>
</office:document-content>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("content.xml")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	chunks, err := NewODPExtractor().Extract(bytes.NewReader(buf.Bytes()), 0)
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
	for _, want := range []string{"First slide title", "Slide one body text", "Second slide content"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
}

func TestODPExtractorDispatchByExtension(t *testing.T) {
	t.Parallel()
	e, _ := Dispatch(Candidate{Path: "deck.odp", Size: 1024})
	if e == nil || e.Name() != "odp" {
		t.Errorf("got extractor=%v, want odp", e)
	}
}
