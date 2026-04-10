// Package extractors holds SwartzNet's pluggable text-extraction backends.
//
// An Extractor reads a file (as an io.Reader, so we can stream without
// buffering entire movies into RAM) and returns a sequence of text chunks
// suitable for the full-text index. Which Extractor handles a given file is
// decided by the Dispatch function, which keys off MIME type and file
// extension.
//
// The package is deliberately minimal in M2.2a:
//
//   - PlaintextExtractor for text files, source code, subtitles, etc.
//   - Dispatch handles content-type detection and Extractor selection.
//
// Later milestones (M2.2b / M2.2c / M2.3) will add:
//
//   - Subtitle-aware extractor that strips timestamps from SRT/VTT.
//   - Source-code extractor that preserves symbols and keeps long lines.
//   - PDF extractor (via an external text-extraction library).
//   - EPUB extractor (via an unzip + XHTML parse).
//
// Every new extractor is a self-contained file in this package that adds
// an init() registration to the Dispatch table. The pipeline in
// internal/indexer reads FileCompleteEvent events and fans them into
// Dispatch; it does not know or care about specific extractor
// implementations.
package extractors
