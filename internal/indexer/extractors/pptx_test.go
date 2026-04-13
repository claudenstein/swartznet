package extractors

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestPPTXExtractorBasic(t *testing.T) {
	t.Parallel()

	// Build a minimal .pptx: zip with two slideN.xml files.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	slide1 := `<?xml version="1.0" encoding="UTF-8"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <p:cSld><p:spTree>
    <p:sp><p:txBody>
      <a:p><a:r><a:t>Slide one title</a:t></a:r></a:p>
      <a:p><a:r><a:t>Bullet one</a:t></a:r></a:p>
      <a:p><a:r><a:t>Bullet two</a:t></a:r></a:p>
    </p:txBody></p:sp>
  </p:spTree></p:cSld>
</p:sld>`

	slide2 := `<?xml version="1.0" encoding="UTF-8"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <p:cSld><p:spTree>
    <p:sp><p:txBody>
      <a:p><a:r><a:t>Second slide heading</a:t></a:r></a:p>
    </p:txBody></p:sp>
  </p:spTree></p:cSld>
</p:sld>`

	for name, content := range map[string]string{
		"ppt/slides/slide1.xml": slide1,
		"ppt/slides/slide2.xml": slide2,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	chunks, err := NewPPTXExtractor().Extract(bytes.NewReader(buf.Bytes()), 0)
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
	for _, want := range []string{"Slide one title", "Bullet one", "Bullet two", "Second slide heading"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
}

func TestPPTXExtractorSlideOrder(t *testing.T) {
	t.Parallel()
	// Verify that slide10 appears AFTER slide2 (numeric, not
	// lexical ordering).
	if slideOrderKey("ppt/slides/slide2.xml") >= slideOrderKey("ppt/slides/slide10.xml") {
		t.Error("slide order key must sort slide2 before slide10")
	}
}

func TestPPTXExtractorDispatch(t *testing.T) {
	t.Parallel()
	e, _ := Dispatch(Candidate{Path: "deck.pptx", Size: 1024})
	if e == nil || e.Name() != "pptx" {
		t.Errorf("got extractor=%v, want pptx", e)
	}
}
