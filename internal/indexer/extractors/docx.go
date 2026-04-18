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
// We do not parse styles, comments, footnotes, or revision
// history. Just the plain reading-order text from the body
// document. Tables, headers, and footers are out of scope for M6b
// (they live in separate XML parts and are easy to add later if
// real corpora demand them).
//
// Like the EPUB extractor, this is pure stdlib + a recover()
// guard around the XML parser.
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

	text, err := extractDocumentText(rc)
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
// (<w:tab>, <w:br>) are turned into single spaces.
//
// Implementation note: we use a token-based decoder rather than
// xml.Unmarshal so a malformed run inside a giant document does
// not cause us to allocate a full DOM tree.
func extractDocumentText(r io.Reader) (string, error) {
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
				// <w:t> element wraps a text run. We accept text
				// from any namespace just in case some
				// alternative-namespace documents end up here.
				inTextRun = true
			case "tab", "br":
				// Whitespace runs from <w:tab>, <w:br> become
				// single spaces. <w:cr> handled too.
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

// init registers the DOCX extractor for the canonical Office Open
// XML MIME type.
func init() {
	Register(NewDOCXExtractor(), func(mime string, c Candidate) bool {
		switch mime {
		case "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			"application/msword":
			return true
		}
		return false
	})
}
