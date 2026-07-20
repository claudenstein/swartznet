package indexer

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
)

// ErrBadQuery marks a request whose free-form Query is not valid Bleve
// query-string syntax (unbalanced parentheses, a dangling field operator, …).
// It is a CLIENT error: callers crossing an API boundary should map it to a
// 400, never a 500 — the index is healthy, the query is not.
var ErrBadQuery = errors.New("indexer: malformed query")

// MaxSearchLimit is a defensive upper bound on SearchRequest.Limit —
// an order of magnitude above the largest legitimate caller (the httpapi
// /search cap is 500, sn_search's is 100), so no realistic user hits it,
// but a misbehaving internal caller cannot pin the index mutex while
// Bleve materialises a multi-million-hit result.
const MaxSearchLimit = 10_000

// SearchRequest describes a query. A request with neither Query nor
// SignedBy is rejected; either alone is valid.
type SearchRequest struct {
	Query string // free-form Bleve QueryString; matches name/files/text
	Limit int    // max hits to return; defaults to 20 if zero
	// Highlight, when true, asks Bleve to return matched text fragments
	// on each hit (SearchHit.Fragments), wrapped <mark>...</mark> by the
	// HTML highlighter.
	Highlight bool
	// SignedBy, when non-empty, restricts results to torrents whose
	// .torrent file was signed with this 64-char hex ed25519 pubkey.
	// Combine with Query, or leave Query empty to fetch every signed
	// torrent from this publisher.
	SignedBy string
}

// SearchHit is a single result row. Fields marked "torrent" populate for
// torrent-level documents, "content" fields for content-level hits; one
// Search can return both kinds interleaved — check DocType.
type SearchHit struct {
	DocType  string  // "torrent" or "content"
	InfoHash string  // 40-char lowercase hex
	Score    float64 // raw Bleve relevance, unscaled (Layer S scales later)

	// Torrent-level fields.
	Name      string   // torrent name
	SizeBytes int64    // total torrent bytes
	FileCount int      // cached file count
	Trackers  []string // tracker URLs (may be empty)
	SignedBy  string   // 64-char hex signer pubkey, empty for unsigned

	// Content-level fields.
	FileIndex int    // position in torrent's file list
	FilePath  string // user-visible file path
	FileSize  int64  // file bytes on disk
	Mime      string // MIME type
	Extractor string // producer extractor name

	// Fragments maps a Bleve field name to matched text fragments,
	// pre-wrapped by Bleve's HTML highlighter so matching terms appear
	// as <mark>term</mark>. Populated only when SearchRequest.Highlight
	// is true; nil otherwise. Callers rendering to plain text should
	// strip the <mark> wrappers. The useful keys are "text" (content
	// hits) and "name"/"files" (torrent hits).
	Fragments map[string][]string
}

// SearchResponse is the result envelope for a Search call. Hits is always
// non-nil (empty slice when nothing matched) so JSON callers render
// "hits":[] rather than null.
type SearchResponse struct {
	Total uint64      // total hit count across the whole index
	Hits  []SearchHit // hits, up to Limit, ordered by descending Score
	Took  time.Duration
}

// Search runs a query against the index.
//
// The query is a Bleve QueryString supporting syntax end-users type
// directly into the search box:
//
//   - `word1 word2` — any document containing any term
//     (Bleve's default is SHOULD, not MUST).
//   - `+required` — prefix with `+` to make a term required.
//   - `-excluded` — prefix with `-` to exclude docs matching it.
//   - `"exact phrase"` — double quotes for phrase match.
//   - `name:ubuntu` — restrict a term to a specific field.
//     Text-analyzed fields (`name`, `files`, `text`) take any tokenized
//     term. Keyword-analyzed fields (`infohash`, `trackers`,
//     `file_path`, `mime`, `extractor`) match the exact stored value.
//   - `word~1` — fuzzy match with edit distance 1.
//   - `word^2` — boost a term.
//
// Locked down by TestSearchQueryOperators. Layers S and D pass the raw
// query string through this same path, so the grammar is identical across
// all three layers.
//
// SignedBy filtering never goes through the QueryString grammar: it is an
// exact-match TermQuery on the lowercased pubkey, so a hostile value
// cannot inject query syntax.
func (i *Index) Search(req SearchRequest) (*SearchResponse, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return nil, errors.New("indexer: closed")
	}
	// A SignedBy-only request (empty Query) is valid — "fetch every
	// signed torrent from this publisher". Only a request with neither
	// is rejected.
	if req.Query == "" && req.SignedBy == "" {
		return nil, errors.New("indexer: empty query")
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}
	if req.Limit > MaxSearchLimit {
		req.Limit = MaxSearchLimit
	}

	var q query.Query
	if req.Query != "" {
		qs := bleve.NewQueryStringQuery(req.Query)
		// Parse the query string up front so a syntax error (unbalanced parens,
		// dangling field op) is a typed client error rather than an opaque 500
		// surfacing from deep inside bleve.Search below.
		if _, perr := qs.Parse(); perr != nil {
			return nil, fmt.Errorf("%w: %v", ErrBadQuery, perr)
		}
		q = qs
	}
	if req.SignedBy != "" {
		signedQ := bleve.NewTermQuery(strings.ToLower(req.SignedBy))
		signedQ.SetField(fieldSignedBy)
		if q == nil {
			q = signedQ
		} else {
			q = bleve.NewConjunctionQuery(q, signedQ)
		}
	}
	sr := bleve.NewSearchRequestOptions(q, req.Limit, 0, false)
	sr.Fields = []string{
		fieldType,
		fieldInfoHash,
		// torrent fields
		fieldName, fieldSizeBytes, fieldFileCount, fieldTrackers, fieldSignedBy,
		// content fields
		fieldFileIndex, fieldFilePath, fieldFileSize, fieldMime, fieldExtractor,
	}
	if req.Highlight {
		// The html highlighter wraps matches with <mark>...</mark>.
		// Scoped to the fields useful for the UI, not every stored
		// field, so the payload stays small.
		sr.Highlight = bleve.NewHighlightWithStyle("html")
		sr.Highlight.Fields = []string{fieldName, fieldFilePaths, fieldText}
	}

	res, err := i.bleve.Search(sr)
	if err != nil {
		return nil, fmt.Errorf("indexer: search: %w", err)
	}

	out := &SearchResponse{
		Total: res.Total,
		Hits:  make([]SearchHit, 0, len(res.Hits)),
		Took:  res.Took,
	}
	for _, h := range res.Hits {
		hit := SearchHit{Score: h.Score}
		if v, ok := h.Fields[fieldType].(string); ok {
			hit.DocType = v
		}
		if v, ok := h.Fields[fieldInfoHash].(string); ok {
			hit.InfoHash = v
		}
		// Torrent-level fields.
		if v, ok := h.Fields[fieldName].(string); ok {
			hit.Name = v
		}
		if v, ok := h.Fields[fieldSizeBytes].(float64); ok {
			hit.SizeBytes = int64(v)
		}
		if v, ok := h.Fields[fieldFileCount].(float64); ok {
			hit.FileCount = int(v)
		}
		switch v := h.Fields[fieldTrackers].(type) {
		case string:
			hit.Trackers = []string{v}
		case []any:
			for _, t := range v {
				if s, ok := t.(string); ok {
					hit.Trackers = append(hit.Trackers, s)
				}
			}
		}
		if v, ok := h.Fields[fieldSignedBy].(string); ok {
			hit.SignedBy = v
		}
		// Content-level fields.
		if v, ok := h.Fields[fieldFileIndex].(float64); ok {
			hit.FileIndex = int(v)
		}
		if v, ok := h.Fields[fieldFilePath].(string); ok {
			hit.FilePath = v
		}
		if v, ok := h.Fields[fieldFileSize].(float64); ok {
			hit.FileSize = int64(v)
		}
		if v, ok := h.Fields[fieldMime].(string); ok {
			hit.Mime = v
		}
		if v, ok := h.Fields[fieldExtractor].(string); ok {
			hit.Extractor = v
		}
		// Deep-copy Bleve's fragment map; nil when Highlight was false
		// or nothing matched a highlighted field.
		if len(h.Fragments) > 0 {
			hit.Fragments = make(map[string][]string, len(h.Fragments))
			for k, v := range h.Fragments {
				hit.Fragments[k] = append([]string(nil), v...)
			}
		}
		out.Hits = append(out.Hits, hit)
	}
	return out, nil
}
