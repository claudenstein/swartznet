package extractors

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// FB2Extractor parses FictionBook 2.x (.fb2) ebook files. FB2 is
// an XML-based ebook format common in the Russian-speaking world
// and with a stable enough spec that a single encoding/xml walk
// handles every FB2 file in the wild.
//
// Document structure:
//
//	<FictionBook>
//	  <description>... title-info, author, ... </description>
//	  <body>
//	    <section>
//	      <title><p>Chapter 1</p></title>
//	      <p>Paragraph text.</p>
//	      <p>More text.</p>
//	      <section>... nested chapters ...</section>
//	    </section>
//	  </body>
//	</FictionBook>
//
// We extract:
//   - The title from description/title-info/book-title
//   - Every <p> inside every <body>
//   - Every <title>, <subtitle>, <epigraph> (as boundary markers)
//
// We skip <binary> (base64 cover art) and <stylesheet>.
type FB2Extractor struct{}

// NewFB2Extractor returns a ready-to-use FB2 extractor.
func NewFB2Extractor() *FB2Extractor { return &FB2Extractor{} }

// Name implements Extractor.
func (*FB2Extractor) Name() string { return "fb2" }

// Extract implements Extractor.
func (e *FB2Extractor) Extract(r io.Reader, maxBytes int64) ([]Chunk, error) {
	if maxBytes <= 0 {
		maxBytes = 32 * 1024 * 1024
	}

	dec := xml.NewDecoder(io.LimitReader(r, maxBytes))
	// FB2 files are typically UTF-8 but occasionally declare
	// windows-1251. Accept both by telling the decoder to pass
	// through non-UTF-8 bytes; encoding/xml's default CharsetReader
	// is nil which would refuse. Use a permissive hook instead.
	dec.CharsetReader = passthroughCharsetReader

	var out strings.Builder
	out.Grow(4096)

	var skipDepth int // > 0 while inside <binary> or <stylesheet>
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("fb2: decode: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch strings.ToLower(t.Name.Local) {
			case "binary", "stylesheet":
				skipDepth++
			case "p", "v":
				// <p> is the paragraph tag; <v> is a verse line
				// inside <poem>. Both emit a newline before.
				if skipDepth == 0 {
					out.WriteByte('\n')
				}
			case "title", "subtitle", "epigraph", "section":
				if skipDepth == 0 {
					out.WriteString("\n\n")
				}
			}
		case xml.EndElement:
			switch strings.ToLower(t.Name.Local) {
			case "binary", "stylesheet":
				if skipDepth > 0 {
					skipDepth--
				}
			}
		case xml.CharData:
			if skipDepth == 0 {
				out.Write(t)
			}
		}
	}

	text := normaliseFB2Text(out.String())
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	return chunkText(text, DefaultChunkTargetBytes), nil
}

// passthroughCharsetReader accepts any encoding label and returns
// the reader unchanged. This is lossy for non-UTF-8 inputs
// (bytes may decode as mojibake), but better than flat-out
// refusing to read the document.
func passthroughCharsetReader(_ string, input io.Reader) (io.Reader, error) {
	return input, nil
}

// normaliseFB2Text collapses consecutive blank lines and trims
// each line's trailing whitespace.
func normaliseFB2Text(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	var prevBlank bool
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
		} else {
			prevBlank = false
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return strings.TrimSpace(out.String())
}

func init() {
	Register(NewFB2Extractor(), func(mime string, c Candidate) bool {
		if c.Size > 50*1024*1024 {
			return false
		}
		if mime == "application/x-fictionbook+xml" {
			return true
		}
		return strings.HasSuffix(strings.ToLower(c.Path), ".fb2")
	})
}
