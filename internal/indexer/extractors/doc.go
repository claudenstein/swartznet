// Package extractors holds SwartzNet's pluggable text-extraction backends.
//
// An Extractor reads a file (as an io.Reader, so callers can stream without
// buffering entire files into RAM) and returns a sequence of text chunks
// suitable for the full-text index. Which Extractor handles a given file is
// decided by Dispatch, which keys off MIME type and file extension.
//
// Every extractor is a self-contained file in this package; register.go
// holds the single ordered registration list. The pipeline in
// internal/indexer feeds completed files into Dispatch; it does not know
// or care about specific extractor implementations.
//
// Hardening invariants every extractor must keep:
//
//   - Input reads are bounded (io.LimitReader against maxBytes or a
//     per-extractor cap); decompressed streams from zip containers are
//     bounded separately (maxDocTextBytes) because DEFLATE amplifies.
//   - A genuinely empty file returns (nil, nil), never an error.
//   - Library panics are converted to errors by an in-extractor recover
//     where the underlying parser is known to panic; the pipeline keeps
//     its own recover as the outer net.
package extractors
