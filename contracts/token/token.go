// Package token is the frozen cross-implementation tokenization contract.
// Keywords derived here become DHT wire targets (BEP-44 keyword index) in a
// later slice, so any second implementation must produce byte-identical
// output for the same input: the constants, both filter lists, the filter
// order, and the tie-break in MostDistinctive are all part of the contract.
// Stdlib-only; the package knows nothing of Bleve or the DHT.
package token

import (
	"strings"
	"unicode"
)

// MinTokenBytes is the minimum token length in BYTES (not runes), checked on
// the lowercased token: "üb" (3 bytes) passes, "ad" (2 bytes) does not.
// Three is the threshold aMule's Kad uses and keeps one- and two-character
// noise tokens off the DHT.
const MinTokenBytes = 3

// MaxKeywordsPerTorrent caps Tokenize output, applied AFTER the full filter
// chain. Each keyword is its own DHT put (~8 nearest-node messages), so the
// cap bounds the per-torrent publish cost.
const MaxKeywordsPerTorrent = 8

// stopWords is deliberately short and English-only: a missed stopword is one
// extra DHT put, while a dropped legitimate keyword is permanently
// un-discoverable. The 31 entries are frozen — changing them changes which
// DHT targets a name hashes to.
var stopWords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "are": {}, "not": {},
	"with": {}, "from": {}, "this": {}, "that": {}, "have": {},
	"was": {}, "you": {}, "all": {}, "any": {}, "but": {},
	"can": {}, "had": {}, "has": {}, "his": {}, "her": {},
	"its": {}, "our": {}, "their": {}, "they": {}, "them": {},
	"who": {}, "what": {}, "when": {}, "where": {}, "why": {},
	"how": {},
}

// extensionTokens drops file-extension noise ("mp4", "iso") that appears in
// torrent names but carries no search signal. The 19 entries are frozen;
// "gz" is unreachable (2 bytes, already dropped by MinTokenBytes) but stays
// verbatim for list fidelity.
var extensionTokens = map[string]struct{}{
	"mp3": {}, "mp4": {}, "mkv": {}, "iso": {},
	"avi": {}, "wmv": {}, "flac": {}, "wav": {},
	"jpg": {}, "jpeg": {}, "png": {}, "gif": {},
	"zip": {}, "rar": {}, "tar": {}, "gz": {},
	"epub": {}, "pdf": {}, "txt": {},
}

// Tokenize splits name into the capped keyword list published for it. Runs of
// unicode letters and digits form tokens (every other rune is a separator);
// letters are lowercased per-rune with unicode.ToLower, digits pass through.
// Each token then runs the filter chain in this exact order: drop if shorter
// than MinTokenBytes, drop if in stopWords, drop if in extensionTokens, drop
// if already seen (first occurrence wins; appearance order is preserved).
// The MaxKeywordsPerTorrent cap applies last, after all filtering. Empty or
// fully-filtered input yields nil, never an error.
func Tokenize(name string) []string {
	out := tokenizeUncapped(name)
	if len(out) > MaxKeywordsPerTorrent {
		out = out[:MaxKeywordsPerTorrent]
	}
	return out
}

// TokenizeAll is Tokenize without the MaxKeywordsPerTorrent cap; the full
// filter chain still applies.
func TokenizeAll(name string) []string {
	return tokenizeUncapped(name)
}

func tokenizeUncapped(name string) []string {
	if name == "" {
		return nil
	}
	var (
		out  []string
		seen = make(map[string]struct{})
		buf  strings.Builder
	)
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		tok := buf.String()
		buf.Reset()
		if len(tok) < MinTokenBytes {
			return
		}
		if _, ok := stopWords[tok]; ok {
			return
		}
		if _, ok := extensionTokens[tok]; ok {
			return
		}
		if _, ok := seen[tok]; ok {
			return
		}
		seen[tok] = struct{}{}
		out = append(out, tok)
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			buf.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	return out
}

// MostDistinctive returns the longest token by BYTE length; among equal
// lengths the earliest wins (frozen tie-break — every searcher must derive
// the same DHT target from the same query). Empty input returns "". Input is
// expected to be Tokenize output, so stopwords are not re-filtered here.
func MostDistinctive(tokens []string) string {
	best := ""
	for _, tok := range tokens {
		if len(tok) > len(best) {
			best = tok
		}
	}
	return best
}
