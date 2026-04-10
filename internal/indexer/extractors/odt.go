package extractors

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// ODTExtractor extracts text from OpenDocument Text (.odt) files
// — the LibreOffice / OpenOffice equivalent of .docx. ODT is a
// ZIP archive whose body lives at content.xml; visible text sits
// inside <text:p> paragraphs as <text:span>/<text:s>/character
// data.
//
// Same architecture as the DOCX extractor: token-based XML decode
// (no full DOM tree), recover() guard, paragraph boundaries
// preserved as blank lines so the chunker has clean split points.
type ODTExtractor struct{}

// NewODTExtractor returns a ready-to-use ODTExtractor.
func NewODTExtractor() *ODTExtractor { return &ODTExtractor{} }

// Name implements Extractor.
func (*ODTExtractor) Name() string { return "odt" }

// odtMaxInputBytes caps how much of an input file we buffer.
const odtMaxInputBytes = 256 * 1024 * 1024

// Extract implements Extractor.
func (e *ODTExtractor) Extract(r io.Reader, maxBytes int64) (chunks []Chunk, err error) {
	if maxBytes <= 0 || maxBytes > odtMaxInputBytes {
		maxBytes = odtMaxInputBytes
	}

	defer func() {
		if rec := recover(); rec != nil {
			chunks = nil
			err = fmt.Errorf("odt: panic during extraction: %v", rec)
		}
	}()

	buf, err := io.ReadAll(io.LimitReader(r, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("odt: read input: %w", err)
	}
	if len(buf) == 0 {
		return nil, nil
	}

	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, fmt.Errorf("odt: open zip: %w", err)
	}

	content := findODTContentXML(zr)
	if content == nil {
		return nil, nil
	}
	rc, err := content.Open()
	if err != nil {
		return nil, fmt.Errorf("odt: open content.xml: %w", err)
	}
	defer rc.Close()

	text, err := extractODTText(rc)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	return chunkText(text, DefaultChunkTargetBytes), nil
}

// findODTContentXML returns the zip entry for content.xml or nil
// if the archive does not contain one.
func findODTContentXML(zr *zip.Reader) *zip.File {
	for _, f := range zr.File {
		if f.Name == "content.xml" {
			return f
		}
	}
	return nil
}

// extractODTText walks the OpenDocument body XML and returns
// just the visible character data. The interesting elements are:
//
//   - <text:p>      — paragraph boundary
//   - <text:h>      — heading boundary (treat as paragraph)
//   - <text:span>   — inline span (no boundary, just contents)
//   - <text:s>      — single space placeholder
//   - <text:tab>    — tab character (we emit a single space)
//   - <text:line-break> — soft line break (we emit a single space)
//
// Style/font/automatic-style elements are skipped because they
// can contain noise like font names embedded as character data.
func extractODTText(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.Entity = xml.HTMLEntity

	var (
		out         strings.Builder
		skipDepth   int
		inParagraph bool
	)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("odt: xml decode: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			local := t.Name.Local
			switch local {
			case "p", "h":
				if inParagraph && out.Len() > 0 {
					out.WriteString("\n\n")
				}
				inParagraph = true
			case "s", "tab", "line-break":
				out.WriteByte(' ')
			case "automatic-styles", "styles", "font-face-decls", "scripts":
				// These sections contain style metadata, not body
				// text. Skip the entire subtree.
				skipDepth++
			}

		case xml.CharData:
			if skipDepth == 0 {
				out.Write(t)
			}

		case xml.EndElement:
			local := t.Name.Local
			switch local {
			case "automatic-styles", "styles", "font-face-decls", "scripts":
				if skipDepth > 0 {
					skipDepth--
				}
			}
		}
	}

	return strings.TrimSpace(out.String()), nil
}

// init registers the ODT extractor for the canonical OpenDocument
// Text MIME type.
func init() {
	Register(NewODTExtractor(), func(mime string, c Candidate) bool {
		switch mime {
		case "application/vnd.oasis.opendocument.text":
			return true
		}
		return false
	})
}
