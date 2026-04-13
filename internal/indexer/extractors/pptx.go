package extractors

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
)

// PPTXExtractor extracts text from PowerPoint (.pptx) files. A
// .pptx is a ZIP archive with per-slide XML documents under
// `ppt/slides/slideN.xml`. The visible body text lives in
// `<a:t>` elements (DrawingML text runs). Slide layout metadata
// and speaker notes live in sibling files and are currently out
// of scope.
//
// We mirror the DOCX extractor's design: read the zip, iterate
// every slideN.xml in numeric order, token-stream-decode each
// slide's XML pulling <a:t> text, insert double newlines between
// slides.
type PPTXExtractor struct{}

// NewPPTXExtractor returns a ready-to-use PPTX extractor.
func NewPPTXExtractor() *PPTXExtractor { return &PPTXExtractor{} }

// Name implements Extractor.
func (*PPTXExtractor) Name() string { return "pptx" }

const pptxMaxInputBytes = 256 * 1024 * 1024

// Extract implements Extractor.
func (e *PPTXExtractor) Extract(r io.Reader, maxBytes int64) (chunks []Chunk, err error) {
	if maxBytes <= 0 || maxBytes > pptxMaxInputBytes {
		maxBytes = pptxMaxInputBytes
	}

	defer func() {
		if rec := recover(); rec != nil {
			chunks = nil
			err = fmt.Errorf("pptx: panic during extraction: %v", rec)
		}
	}()

	buf, err := io.ReadAll(io.LimitReader(r, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("pptx: read input: %w", err)
	}
	if len(buf) == 0 {
		return nil, nil
	}

	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, fmt.Errorf("pptx: open zip: %w", err)
	}

	slides := findPPTXSlides(zr)
	if len(slides) == 0 {
		// Not a PowerPoint deck.
		return nil, nil
	}

	var out strings.Builder
	for i, slide := range slides {
		if i > 0 {
			out.WriteString("\n\n")
		}
		rc, err := slide.Open()
		if err != nil {
			return nil, fmt.Errorf("pptx: open %s: %w", slide.Name, err)
		}
		text, err := extractDrawingMLText(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		out.WriteString(text)
	}

	final := strings.TrimSpace(out.String())
	if final == "" {
		return nil, nil
	}
	return chunkText(final, DefaultChunkTargetBytes), nil
}

// findPPTXSlides returns the zip entries for ppt/slides/slideN.xml,
// sorted by N ascending so the output matches the slide order in
// the deck.
func findPPTXSlides(zr *zip.Reader) []*zip.File {
	var slides []*zip.File
	for _, f := range zr.File {
		name := f.Name
		if strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml") {
			slides = append(slides, f)
		}
	}
	// Sort by numeric suffix so slide2 < slide10.
	sort.Slice(slides, func(i, j int) bool {
		return slideOrderKey(slides[i].Name) < slideOrderKey(slides[j].Name)
	})
	return slides
}

// slideOrderKey extracts the integer N from "ppt/slides/slideN.xml".
// Pads with leading zeros so lexical sort gives numeric order.
func slideOrderKey(name string) string {
	base := strings.TrimPrefix(name, "ppt/slides/slide")
	base = strings.TrimSuffix(base, ".xml")
	if len(base) == 0 {
		return ""
	}
	// Pad to 6 digits for stable sort.
	const pad = "000000"
	if len(base) >= len(pad) {
		return base
	}
	return pad[:len(pad)-len(base)] + base
}

// extractDrawingMLText walks a DrawingML-bearing XML stream and
// returns the text content of every <a:t> element. Text runs
// inside different shapes are separated by newlines so bullet
// lists and slide titles don't collapse into run-on sentences.
func extractDrawingMLText(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.Entity = xml.HTMLEntity

	var (
		out      strings.Builder
		inText   bool
		inPara   bool
		textSeen bool // text emitted in the current paragraph
	)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("pptx: xml decode: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				// <a:p> is a paragraph inside a shape's text body.
				// Emit a newline between paragraphs, but only
				// after the first one has produced output.
				if inPara && textSeen {
					out.WriteByte('\n')
				}
				inPara = true
				textSeen = false
			case "t":
				inText = true
			case "br":
				out.WriteByte('\n')
			}

		case xml.CharData:
			if inText {
				out.Write(t)
				textSeen = true
			}

		case xml.EndElement:
			if t.Name.Local == "t" {
				inText = false
			}
		}
	}

	return strings.TrimSpace(out.String()), nil
}

func init() {
	Register(NewPPTXExtractor(), func(mime string, c Candidate) bool {
		if c.Size > 256*1024*1024 {
			return false
		}
		if mime == "application/vnd.openxmlformats-officedocument.presentationml.presentation" {
			return true
		}
		return strings.HasSuffix(strings.ToLower(c.Path), ".pptx")
	})
}
