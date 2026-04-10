package dhtindex

import (
	"sort"
	"strings"
	"unicode"
)

// MinTokenBytes is the lower bound on token length we publish. Three
// is the same threshold aMule's Kad uses (per
// docs/03-p2p-search-protocols.md §1.4) and avoids spamming the DHT
// with one- and two-character noise tokens like "a", "an", "of".
const MinTokenBytes = 3

// MaxKeywordsPerTorrent is the default cap on how many keywords we
// publish for a single torrent. Each keyword is its own DHT put, so
// the total cost of adding one torrent is roughly K * (8 nearest-node
// puts), or ~80 UDP messages by default.
const MaxKeywordsPerTorrent = 8

// stopWords is a small intentionally-conservative list of English
// terms we never publish. The aim is to drop the words that would
// otherwise hash to the most-contended DHT keyword targets ("the",
// "of", "and"…) and would generate runaway result sets nobody wants.
//
// We keep this list short and English-only on purpose. Locale-aware
// stop-word handling lands with M5 alongside lingua-go integration;
// for now overshooting on stopwords is worse than undershooting,
// because a missed-stopword is just one extra DHT put while a
// dropped legitimate keyword is permanently un-discoverable.
var stopWords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "are": {}, "not": {},
	"with": {}, "from": {}, "this": {}, "that": {}, "have": {},
	"was": {}, "you": {}, "all": {}, "any": {}, "but": {},
	"can": {}, "had": {}, "has": {}, "his": {}, "her": {},
	"its": {}, "our": {}, "their": {}, "they": {}, "them": {},
	"who": {}, "what": {}, "when": {}, "where": {}, "why": {},
	"how": {},
}

// extensionTokens is a deny list of common file-extension noise that
// shows up inside torrent names but provides zero search signal.
// Lifting these out of the keyword set keeps the DHT free of "mp4"
// and "iso" hot-spots.
var extensionTokens = map[string]struct{}{
	"mp3": {}, "mp4": {}, "mkv": {}, "iso": {},
	"avi": {}, "wmv": {}, "flac": {}, "wav": {},
	"jpg": {}, "jpeg": {}, "png": {}, "gif": {},
	"zip": {}, "rar": {}, "tar": {}, "gz": {},
	"epub": {}, "pdf": {}, "txt": {},
}

// Tokenize splits a torrent name into the set of keywords this node
// will publish for it. The result is:
//
//   - lowercased
//   - unicode-letter-or-digit only (everything else is a separator)
//   - deduplicated, with the first occurrence's order preserved
//   - filtered against MinTokenBytes, stopWords, and extensionTokens
//   - capped at MaxKeywordsPerTorrent (top by appearance order)
//
// It is intentionally permissive: if you call Tokenize on the empty
// string you get an empty slice (not an error), and very long names
// just produce more tokens that get capped.
func Tokenize(name string) []string {
	if name == "" {
		return nil
	}
	runes := []rune(name)

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

	for _, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			// Lowercase letters; digits pass through unchanged.
			buf.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()

	if len(out) > MaxKeywordsPerTorrent {
		out = out[:MaxKeywordsPerTorrent]
	}
	return out
}

// TokenizeAll returns the FULL keyword list before applying the
// MaxKeywordsPerTorrent cap. Useful for tests that want to verify
// the underlying tokenisation logic without worrying about the cap.
func TokenizeAll(name string) []string {
	if name == "" {
		return nil
	}
	saved := MaxKeywordsPerTorrent
	defer func() { /* immutable; just return whatever Tokenize gives */ _ = saved }()
	// Re-implement without the cap rather than mutating the package
	// constant (which would be a data race in tests).
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

// SortedTokenize is Tokenize but with a stable lexicographic sort
// applied at the end. Useful for tests that want order-independent
// equality checks.
func SortedTokenize(name string) []string {
	out := Tokenize(name)
	sort.Strings(out)
	return out
}
