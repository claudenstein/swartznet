package extractors

import (
	"io"
	"strings"

	"golang.org/x/net/html"
)

// extractHTMLText reads an HTML or XHTML stream and returns just the
// visible text content, with tags stripped, scripts/styles removed,
// and consecutive whitespace collapsed to single spaces. Block-level
// elements get a newline so paragraph boundaries survive into the
// chunker downstream.
//
// Used by the EPUB extractor (which iterates the XHTML files inside
// the archive) and the ZIM extractor (per-article HTML blobs). The
// implementation walks the html.Tokenizer rather than building a
// full DOM, which keeps memory bounded for very large books.
//
// maxOut bounds both the raw bytes consumed and the tokenizer's
// internal buffer (SetMaxBuf — the default is unlimited, so without
// it one giant text node or unterminated tag buffers the whole
// stream before a single token is emitted). Values <= 0 or above
// maxDocTextBytes are clamped to maxDocTextBytes. Hitting either
// bound is graceful truncation, not an error: we return the text
// accumulated so far so one oversized chapter doesn't poison the
// rest of a book.
func extractHTMLText(r io.Reader, maxOut int64) (string, error) {
	if maxOut <= 0 || maxOut > maxDocTextBytes {
		maxOut = maxDocTextBytes
	}
	tz := html.NewTokenizer(io.LimitReader(r, maxOut))
	tz.SetMaxBuf(int(maxOut))
	var (
		out       strings.Builder
		skipDepth int  // counts nested <script>/<style> blocks we are inside
		lastChar  byte // tracks the last byte we appended for whitespace collapsing
	)

	appendText := func(s string) {
		// Collapse runs of whitespace to a single space.
		for i := 0; i < len(s); i++ {
			c := s[i]
			if c == '\n' || c == '\r' || c == '\t' {
				c = ' '
			}
			if c == ' ' {
				if lastChar == ' ' || lastChar == '\n' || lastChar == 0 {
					continue
				}
				out.WriteByte(' ')
				lastChar = ' '
				continue
			}
			out.WriteByte(c)
			lastChar = c
		}
	}

	appendBreak := func() {
		// Force a newline boundary between block-level elements so
		// the chunker can split paragraphs cleanly.
		if lastChar != '\n' && lastChar != 0 {
			out.WriteByte('\n')
			lastChar = '\n'
		}
	}

	for {
		// Output guard: a zip-bomb XHTML chapter can amplify a tiny
		// compressed input into gigabytes of visible text. Stop once
		// the accumulated text crosses the budget and return what we
		// have so far.
		if int64(out.Len()) > maxOut {
			return strings.TrimSpace(out.String()), nil
		}
		tt := tz.Next()
		switch tt {
		case html.ErrorToken:
			err := tz.Err()
			if err == io.EOF || err == html.ErrBufferExceeded {
				// EOF includes hitting the maxOut LimitReader;
				// ErrBufferExceeded means a single token outgrew
				// the budget. Both are graceful truncation.
				return strings.TrimSpace(out.String()), nil
			}
			return "", err

		case html.TextToken:
			if skipDepth == 0 {
				appendText(string(tz.Text()))
			}

		case html.StartTagToken:
			name, _ := tz.TagName()
			tag := string(name)
			if isSkipTag(tag) {
				skipDepth++
				continue
			}
			if isBlockTag(tag) {
				appendBreak()
			}

		case html.EndTagToken:
			name, _ := tz.TagName()
			tag := string(name)
			if isSkipTag(tag) {
				if skipDepth > 0 {
					skipDepth--
				}
				continue
			}
			if isBlockTag(tag) {
				appendBreak()
			}

		case html.SelfClosingTagToken:
			name, _ := tz.TagName()
			if string(name) == "br" {
				appendBreak()
			}
		}
	}
}

// isSkipTag returns true for tags whose contents should be entirely
// excluded from the extracted text. <script>, <style>, and SVG
// blocks are noise for an FTS index.
func isSkipTag(tag string) bool {
	switch tag {
	case "script", "style", "noscript", "svg", "math":
		return true
	}
	return false
}

// isBlockTag returns true for HTML elements whose start or end
// implies a paragraph boundary. Used to insert newlines so the
// chunker can split on paragraph breaks downstream.
func isBlockTag(tag string) bool {
	switch tag {
	case "p", "div", "section", "article", "header", "footer", "nav",
		"li", "tr", "br", "blockquote", "pre", "hr",
		"h1", "h2", "h3", "h4", "h5", "h6":
		return true
	}
	return false
}
