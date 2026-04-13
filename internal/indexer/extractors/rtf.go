package extractors

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// RTFExtractor parses Rich Text Format files (.rtf) and returns the
// visible text content. It implements the subset of RTF used by
// every mainstream generator (Word, LibreOffice, Apple TextEdit,
// Pages export) — enough to extract reliably from real-world
// documents without pulling in a full RTF specification parser.
//
// RTF structure (RFC-1462 / Microsoft spec v1.9.1) is:
//
//	{ \control-word arg ? Plain literal text }
//
// Groups nest with { }. Control words start with \ and end at the
// first non-letter. Some control words introduce special content:
//
//	\par           — paragraph break; we emit newline
//	\line / \tab   — soft break / tab; we emit newline / tab
//	\'XX           — hex-escaped byte; we decode, best-effort as UTF-8
//	\uN            — Unicode codepoint (decimal); we emit as rune
//	\*\<anything>  — "discardable" group (destination); we skip
//	\fonttbl \stylesheet \colortbl \info \pict \bin
//	               — large headers/binary sections; we skip whole group
//
// Everything outside recognised control words + escape sequences is
// treated as literal text. No attempt is made to render formatting
// (bold, font, colour) — only text content matters for indexing.
type RTFExtractor struct{}

// NewRTFExtractor returns a ready-to-use RTF extractor.
func NewRTFExtractor() *RTFExtractor { return &RTFExtractor{} }

// Name implements Extractor.
func (*RTFExtractor) Name() string { return "rtf" }

// Extract implements Extractor.
func (e *RTFExtractor) Extract(r io.Reader, maxBytes int64) ([]Chunk, error) {
	if maxBytes <= 0 {
		maxBytes = 32 * 1024 * 1024
	}

	br := bufio.NewReader(io.LimitReader(r, maxBytes))

	// Peek at the first 8 bytes; every RTF file begins with "{\rtf".
	// If we don't see that, bail out — this isn't RTF.
	head, _ := br.Peek(8)
	if !bytes.HasPrefix(head, []byte("{\\rtf")) {
		return nil, fmt.Errorf("rtf: missing {\\rtf header")
	}

	var out strings.Builder
	out.Grow(4096)

	// Destinations we always skip the entire containing group for.
	// Entering one increments skipDepth; we decrement on matching }.
	skipWords := map[string]bool{
		"fonttbl":    true,
		"stylesheet": true,
		"colortbl":   true,
		"info":       true,
		"pict":       true,
		"bin":        true,
		"header":     true,
		"footer":     true,
		"footnote":   true,
		"header*":    true,
		"footer*":    true,
		"listtable":  true,
		"rsidtbl":    true,
		"generator":  true,
	}

	var (
		depth     int // open group depth
		skipDepth int // > 0 means we're inside a skipped destination
	)

	for {
		b, err := br.ReadByte()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("rtf: read: %w", err)
		}

		switch b {
		case '{':
			depth++
			// Peek for "\*\<word>" which marks a discardable destination;
			// we always skip those.
			peek, _ := br.Peek(1)
			if len(peek) == 1 && peek[0] == '\\' {
				// Continue — the control word handler below decides.
			}
		case '}':
			if skipDepth > 0 && depth == skipDepth {
				skipDepth = 0
			}
			depth--
		case '\\':
			word, param, hex, err := readControl(br)
			if err != nil {
				return nil, err
			}
			if skipDepth > 0 {
				continue
			}
			switch word {
			case "":
				// Escape of a literal character: \\, \{, \}, \ , \*.
				// The character we wanted is in `hex` (single byte).
				switch hex {
				case "\\", "{", "}":
					out.WriteString(hex)
				case "*":
					// \* marks a discardable destination. Skip the
					// entire containing group.
					skipDepth = depth
				}
			case "par", "line", "sect", "page":
				out.WriteByte('\n')
			case "tab":
				out.WriteByte('\t')
			case "emdash":
				out.WriteString("—")
			case "endash":
				out.WriteString("–")
			case "lquote":
				out.WriteString("‘")
			case "rquote":
				out.WriteString("’")
			case "ldblquote":
				out.WriteString("“")
			case "rdblquote":
				out.WriteString("”")
			case "bullet":
				out.WriteString("•")
			case "'":
				// \'HH — hex byte escape; interpret as Windows-1252 best-
				// effort. Values >= 0x80 that look like extended ASCII
				// become U+00XX which is lossy but preserves punctuation.
				if len(hex) == 2 {
					var v int
					fmt.Sscanf(hex, "%x", &v)
					if v > 0 {
						out.WriteRune(rune(v))
					}
				}
			case "u":
				// \uN — Unicode codepoint. May be negative (signed 16-bit).
				// Often followed by a fallback character we should skip.
				if param != 0 {
					if param < 0 {
						param += 65536
					}
					if r := rune(param); utf8.ValidRune(r) {
						out.WriteRune(r)
					}
					// Skip the fallback char that follows \uN.
					_, _ = br.ReadByte()
				}
			default:
				if skipWords[word] {
					skipDepth = depth
				}
			}
		default:
			if skipDepth > 0 {
				continue
			}
			out.WriteByte(b)
		}

		if out.Len() > int(maxBytes) {
			break
		}
	}

	text := normaliseRTFText(out.String())
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	return chunkText(text, DefaultChunkTargetBytes), nil
}

// readControl reads a control word or escape sequence immediately
// after a '\\' byte. Returns the word (empty for single-char
// escapes), numeric parameter if any, and the literal escape char
// for single-char escapes (\\, \{, \}, \*).
//
// Examples:
//
//	\rtf1\ansi  → readControl gets 'r','t','f','1', returns word="rtf" param=1
//	\\          → word="" hex="\\"
//	\'4f        → word="'" hex="4f"
//	\u233       → word="u" param=233
func readControl(br *bufio.Reader) (word string, param int, hex string, err error) {
	first, err := br.ReadByte()
	if err != nil {
		return "", 0, "", err
	}

	// Single-character escape sequences.
	switch first {
	case '\\', '{', '}':
		return "", 0, string(first), nil
	case '*':
		return "", 0, "*", nil
	case '\'':
		// \'XX — two hex digits.
		d1, err := br.ReadByte()
		if err != nil {
			return "'", 0, "", nil
		}
		d2, err := br.ReadByte()
		if err != nil {
			return "'", 0, string(d1), nil
		}
		return "'", 0, string([]byte{d1, d2}), nil
	}

	// If first isn't a letter, treat as literal (rare — \~ is a non-
	// breaking space, etc.). Emit as empty control word.
	if !isAlpha(first) {
		return "", 0, "", nil
	}

	var wordBuf strings.Builder
	wordBuf.WriteByte(first)
	for {
		b, err := br.ReadByte()
		if err != nil {
			return wordBuf.String(), 0, "", err
		}
		if !isAlpha(b) {
			// Parameter may follow: optional minus, then digits.
			if b == '-' || (b >= '0' && b <= '9') {
				var paramBuf []byte
				paramBuf = append(paramBuf, b)
				for {
					b2, err := br.ReadByte()
					if err != nil {
						break
					}
					if b2 < '0' || b2 > '9' {
						// Parameter terminator: if it's a space it's
						// consumed; otherwise push it back.
						if b2 != ' ' {
							_ = br.UnreadByte()
						}
						break
					}
					paramBuf = append(paramBuf, b2)
				}
				fmt.Sscanf(string(paramBuf), "%d", &param)
				return wordBuf.String(), param, "", nil
			}
			// Terminator: space is consumed, anything else pushed back.
			if b != ' ' {
				_ = br.UnreadByte()
			}
			return wordBuf.String(), 0, "", nil
		}
		wordBuf.WriteByte(b)
	}
}

func isAlpha(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// normaliseRTFText collapses runs of blank lines and trims trailing
// whitespace on each line, giving a cleaner-looking extraction for
// the index.
func normaliseRTFText(s string) string {
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
	Register(NewRTFExtractor(), func(mime string, c Candidate) bool {
		if c.Size > 50*1024*1024 {
			return false
		}
		if mime == "application/rtf" || mime == "text/rtf" {
			return true
		}
		return strings.HasSuffix(strings.ToLower(c.Path), ".rtf")
	})
}
