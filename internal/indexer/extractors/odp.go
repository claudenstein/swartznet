package extractors

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// ODPExtractor extracts text from OpenDocument Presentation
// (.odp) files — the LibreOffice Impress equivalent of .pptx.
// An .odp is a ZIP archive whose body lives at content.xml, same
// as .odt; the visible text still sits inside <text:p> / <text:h>
// / <text:span> elements. The per-slide structure is wrapped in
// <draw:page> elements but for indexing purposes we treat every
// paragraph the same — the reading order naturally preserves
// slide order.
//
// This reuses the exact same XML walker as the ODT extractor;
// the only difference is which MIME type / extension claims the
// file.
type ODPExtractor struct{}

// NewODPExtractor returns a ready-to-use ODP extractor.
func NewODPExtractor() *ODPExtractor { return &ODPExtractor{} }

// Name implements Extractor.
func (*ODPExtractor) Name() string { return "odp" }

const odpMaxInputBytes = 256 * 1024 * 1024

// Extract implements Extractor.
func (e *ODPExtractor) Extract(r io.Reader, maxBytes int64) (chunks []Chunk, err error) {
	if maxBytes <= 0 || maxBytes > odpMaxInputBytes {
		maxBytes = odpMaxInputBytes
	}

	defer func() {
		if rec := recover(); rec != nil {
			chunks = nil
			err = fmt.Errorf("odp: panic during extraction: %v", rec)
		}
	}()

	buf, err := io.ReadAll(io.LimitReader(r, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("odp: read input: %w", err)
	}
	if len(buf) == 0 {
		return nil, nil
	}

	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, fmt.Errorf("odp: open zip: %w", err)
	}

	content := findODTContentXML(zr)
	if content == nil {
		return nil, nil
	}
	rc, err := content.Open()
	if err != nil {
		return nil, fmt.Errorf("odp: open content.xml: %w", err)
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

func init() {
	Register(NewODPExtractor(), func(mime string, c Candidate) bool {
		if c.Size > 256*1024*1024 {
			return false
		}
		if mime == "application/vnd.oasis.opendocument.presentation" {
			return true
		}
		return strings.HasSuffix(strings.ToLower(c.Path), ".odp")
	})
}
