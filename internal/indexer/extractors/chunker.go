package extractors

import "strings"

// DefaultChunkTargetBytes is the nominal size we try to hit for each
// content chunk. Tuned to 2 KiB as of M13e based on the v1.0.0
// blocker-1 research: Elastic's chunking docs default to ~250 words
// (≈1.25 KiB), production RAG/BM25 stacks converge on 0.5–4 KiB, and
// shorter chunks give better BM25 relevance per-hit plus tighter
// snippet fragments. The old 10 KiB target was an order of magnitude
// larger than the sweet spot; smaller chunks also produce smaller
// highlight fragments without losing recall because Bleve scores
// each chunk independently.
//
// Raising this value costs index size and relevance; lowering it
// below ~512 bytes costs Bleve document-metadata overhead.
const DefaultChunkTargetBytes = 2 * 1024

// chunkMaxOverrunRatio bounds how much bigger a chunk is allowed to be
// than DefaultChunkTargetBytes before we force a split even in the
// middle of a paragraph. 1.5× means "paragraphs up to 15 KiB stay
// whole, longer ones get re-split at line boundaries."
const chunkMaxOverrunRatio = 1.5

// smallFileFactor controls the "don't chunk tiny files" optimisation:
// a file whose extracted text is smaller than
// DefaultChunkTargetBytes * smallFileFactor is returned as a single
// chunk regardless of the target size. This avoids the pathological
// case where a 12 KiB file becomes two 6 KiB chunks.
const smallFileFactor = 1.25

// chunkText splits text into a sequence of Chunks no larger than about
// targetBytes each, preferring split points at paragraph boundaries
// (blank lines), then line boundaries, then spaces, then arbitrary
// positions as a last resort. Each returned Chunk carries the byte
// offset at which it begins in the input.
//
// Small inputs (see smallFileFactor) are returned as a single chunk.
//
// The function is pure and has no package dependencies beyond strings.
func chunkText(text string, targetBytes int) []Chunk {
	if targetBytes <= 0 {
		targetBytes = DefaultChunkTargetBytes
	}
	if text == "" {
		return nil
	}
	if len(text) <= int(float64(targetBytes)*smallFileFactor) {
		return []Chunk{{Text: text, Offset: 0}}
	}

	maxChunk := int(float64(targetBytes) * chunkMaxOverrunRatio)

	// Walk the text by paragraphs (blank-line separated). Accumulate
	// paragraphs into the current chunk until adding another would
	// push us past targetBytes. Any single paragraph larger than
	// maxChunk is handed to chunkBySmaller for recursive splitting.
	var (
		out    []Chunk
		curBuf strings.Builder
		curOff int
		pos    int
	)
	flush := func() {
		if curBuf.Len() == 0 {
			return
		}
		out = append(out, Chunk{Text: curBuf.String(), Offset: int64(curOff)})
		curBuf.Reset()
	}

	for pos < len(text) {
		para, next := nextParagraph(text, pos)
		// Starting a new chunk: record the input offset.
		if curBuf.Len() == 0 {
			curOff = pos
		}

		// A single paragraph that is too big gets split by line.
		if len(para) > maxChunk {
			flush()
			for _, sub := range chunkByLine(para, targetBytes, maxChunk) {
				out = append(out, Chunk{
					Text:   sub.Text,
					Offset: int64(pos) + sub.Offset,
				})
			}
			pos = next
			continue
		}

		// Adding this paragraph would overflow the current chunk
		// AND we already have at least one paragraph — flush first.
		if curBuf.Len() > 0 && curBuf.Len()+len(para)+2 > targetBytes {
			flush()
			curOff = pos
		}

		if curBuf.Len() > 0 {
			curBuf.WriteString("\n\n")
		}
		curBuf.WriteString(para)
		pos = next
	}
	flush()
	return out
}

// nextParagraph returns the paragraph starting at pos and the position
// immediately after the blank line that terminates it. A paragraph is
// any run of non-empty lines delimited by "\n\n" (optionally with
// trailing \r). If there is no more blank line, the entire remainder
// of the text is returned as the last paragraph.
func nextParagraph(text string, pos int) (string, int) {
	if pos >= len(text) {
		return "", pos
	}
	// Skip leading blank lines so we start on a real paragraph.
	for pos < len(text) && (text[pos] == '\n' || text[pos] == '\r') {
		pos++
	}
	start := pos
	for pos < len(text) {
		// Look for "\n\n" or "\r\n\r\n" (allow either).
		if text[pos] == '\n' {
			if pos+1 < len(text) && text[pos+1] == '\n' {
				return strings.TrimRight(text[start:pos], "\r\n"), pos + 2
			}
			if pos+2 < len(text) && text[pos+1] == '\r' && text[pos+2] == '\n' {
				return strings.TrimRight(text[start:pos], "\r\n"), pos + 3
			}
		}
		pos++
	}
	return strings.TrimRight(text[start:], "\r\n"), len(text)
}

// chunkByLine splits a paragraph that exceeds maxChunk at single-line
// boundaries, targeting targetBytes per chunk. Returned chunks carry
// offsets relative to the start of the paragraph.
func chunkByLine(text string, targetBytes, maxChunk int) []Chunk {
	var (
		out     []Chunk
		cur     strings.Builder
		curOff  int
		lineEnd int
		pos     int
	)
	for pos < len(text) {
		if cur.Len() == 0 {
			curOff = pos
		}
		// Find the next newline.
		lineEnd = strings.IndexByte(text[pos:], '\n')
		var lineEndAbs int
		if lineEnd == -1 {
			lineEndAbs = len(text)
		} else {
			lineEndAbs = pos + lineEnd + 1
		}
		line := text[pos:lineEndAbs]

		// A single line longer than maxChunk is hard-split at
		// targetBytes intervals. This only happens for pathological
		// input (one-line JSON blobs, minified HTML, etc.).
		if len(line) > maxChunk {
			if cur.Len() > 0 {
				out = append(out, Chunk{Text: cur.String(), Offset: int64(curOff)})
				cur.Reset()
			}
			for offset := 0; offset < len(line); offset += targetBytes {
				end := offset + targetBytes
				if end > len(line) {
					end = len(line)
				}
				out = append(out, Chunk{
					Text:   line[offset:end],
					Offset: int64(pos + offset),
				})
			}
			pos = lineEndAbs
			continue
		}

		// Adding this line would overflow the current chunk.
		if cur.Len() > 0 && cur.Len()+len(line) > targetBytes {
			out = append(out, Chunk{Text: cur.String(), Offset: int64(curOff)})
			cur.Reset()
			curOff = pos
		}
		cur.WriteString(line)
		pos = lineEndAbs
	}
	if cur.Len() > 0 {
		out = append(out, Chunk{Text: cur.String(), Offset: int64(curOff)})
	}
	return out
}
