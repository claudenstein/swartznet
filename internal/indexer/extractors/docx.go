package extractors

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// DOCXExtractor extracts text from Microsoft Word .docx files
// (ECMA-376 / Office Open XML). A .docx is a ZIP archive whose
// main payload lives at word/document.xml; the body text is
// inside <w:t> elements grouped by <w:p> paragraphs.
//
// Styles, comments, footnotes, and revision history are not parsed —
// just the plain reading-order text from the body document. Tables,
// headers, and footers live in separate XML parts and are out of
// scope.
type DOCXExtractor struct{}

// NewDOCXExtractor returns a ready-to-use DOCXExtractor.
func NewDOCXExtractor() *DOCXExtractor { return &DOCXExtractor{} }

// Name implements Extractor.
func (*DOCXExtractor) Name() string { return "docx" }

// docxMaxInputBytes caps how much of an input file we buffer into
// memory. 256 MiB matches the EPUB extractor and is enough for
// even very long documents.
const docxMaxInputBytes = 256 * 1024 * 1024

// Extract implements Extractor.
func (e *DOCXExtractor) Extract(r io.Reader, maxBytes int64) (chunks []Chunk, err error) {
	if maxBytes <= 0 || maxBytes > docxMaxInputBytes {
		maxBytes = docxMaxInputBytes
	}

	defer func() {
		if rec := recover(); rec != nil {
			chunks = nil
			err = fmt.Errorf("docx: panic during extraction: %v", rec)
		}
	}()

	buf, err := io.ReadAll(io.LimitReader(r, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("docx: read input: %w", err)
	}
	if len(buf) == 0 {
		return nil, nil
	}

	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, fmt.Errorf("docx: open zip: %w", err)
	}

	doc := findDocumentXML(zr)
	if doc == nil {
		// Not a Word document — possibly an Excel xlsx or
		// PowerPoint pptx that shares the .zip envelope. Return
		// nil rather than erroring; the dispatcher will pick a
		// different extractor next time it sees this file.
		return nil, nil
	}
	rc, err := doc.Open()
	if err != nil {
		return nil, fmt.Errorf("docx: open document.xml: %w", err)
	}
	defer rc.Close()

	// Bound the DECOMPRESSED entry stream before it reaches the XML
	// decoder: the input cap above only limits the compressed bytes,
	// and a deflate bomb amplifies ~1032:1. See maxDocTextBytes.
	text, err := extractDocumentText(io.LimitReader(rc, maxDocTextBytes), maxBytes)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	return chunkText(text, DefaultChunkTargetBytes), nil
}

// findDocumentXML returns the zip entry for word/document.xml,
// or nil if the archive does not contain one.
func findDocumentXML(zr *zip.Reader) *zip.File {
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			return f
		}
	}
	return nil
}

// extractDocumentText walks the WordprocessingML XML stream and
// returns the visible body text. Paragraphs (<w:p>) become blank-
// line-separated paragraphs in the output; tab and break runs
// (<w:tab>, <w:br>, <w:cr>) are turned into single spaces.
//
// A token-based decoder is used rather than xml.Unmarshal so a
// malformed run inside a giant document does not cause a full DOM
// tree allocation. maxOut bounds the accumulated output text;
// <= 0 falls back to defaultTextOutputCap.
func extractDocumentText(r io.Reader, maxOut int64) (string, error) {
	if maxOut <= 0 {
		maxOut = defaultTextOutputCap
	}
	dec := xml.NewDecoder(r)
	dec.Strict = false
	// Permit unknown character entities; some Word docs include
	// custom entities that the strict decoder rejects.
	dec.Entity = xml.HTMLEntity

	var (
		out         strings.Builder
		inTextRun   bool
		inParagraph bool
	)

	for {
		// Output guard: a zip-bomb DOCX can amplify a tiny compressed
		// input into gigabytes of <w:t> text. Stop accumulating once
		// we cross the budget and return the partial result.
		if int64(out.Len()) > maxOut {
			break
		}
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("docx: xml decode: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			local := t.Name.Local
			switch local {
			case "p":
				// Start of a paragraph: insert a blank line
				// between paragraphs but only after the first.
				if inParagraph && out.Len() > 0 {
					out.WriteString("\n\n")
				}
				inParagraph = true
			case "t":
				// <w:t> wraps a text run. Text is accepted from any
				// namespace (matching is on Name.Local only).
				inTextRun = true
			case "tab", "br":
				out.WriteByte(' ')
			case "cr":
				out.WriteByte(' ')
			}

		case xml.CharData:
			if inTextRun {
				out.Write(t)
			}

		case xml.EndElement:
			if t.Name.Local == "t" {
				inTextRun = false
			}
		}
	}

	return strings.TrimSpace(out.String()), nil
}

// claimsDOCX claims the canonical Office Open XML MIME type plus the
// legacy application/msword alias.
func claimsDOCX(mime string, c Candidate) bool {
	switch mime {
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/msword":
		return true
	}
	return false
}
