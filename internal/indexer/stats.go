package indexer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blevesearch/bleve/v2"
)

// Stats is the per-index snapshot returned by Stats(). The JSON tags are
// API surface: the HTTP /index/stats DTO mirrors them one-for-one.
type Stats struct {
	// DirBytes is the total on-disk size of the Bleve directory (the sum
	// of every regular file under Index.path).
	DirBytes int64 `json:"dir_bytes"`
	// DocCount is the total number of Bleve documents (torrent +
	// content). The schema sentinel is internal metadata, never counted.
	DocCount uint64 `json:"doc_count"`
	// TorrentCount is the number of torrent-level documents.
	TorrentCount uint64 `json:"torrent_count"`
	// ContentCount is the number of content-level documents (one per
	// file-chunk extraction).
	ContentCount uint64 `json:"content_count"`
	// CorpusTextBytes is the sum of every ContentDoc.Text field in the
	// index — the raw text fed to Bleve. Zero when there are no content
	// docs.
	CorpusTextBytes int64 `json:"corpus_text_bytes"`
	// InflationRatio is DirBytes / CorpusTextBytes when the corpus is
	// non-empty, zero otherwise.
	InflationRatio float64 `json:"inflation_ratio"`
}

// Stats returns a Stats snapshot. The corpus-bytes sum scans EVERY
// content doc via a paginated query while holding the global mutex, so
// callers must poll at human cadence (seconds), never in tight loops.
func (i *Index) Stats() (Stats, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return Stats{}, errors.New("indexer: closed")
	}

	var out Stats

	total, err := i.bleve.DocCount()
	if err != nil {
		return Stats{}, fmt.Errorf("indexer: Stats doc count: %w", err)
	}
	out.DocCount = total

	// Per-type counts via two count-only queries (Size=0, read Total off
	// the envelope) — the cheapest "how many docs match" call.
	for _, tt := range []struct {
		ty  string
		dst *uint64
	}{
		{typeTorrent, &out.TorrentCount},
		{typeContent, &out.ContentCount},
	} {
		q := bleve.NewQueryStringQuery("+" + fieldType + ":" + tt.ty)
		sr := bleve.NewSearchRequestOptions(q, 0, 0, false)
		res, err := i.bleve.Search(sr)
		if err != nil {
			return Stats{}, fmt.Errorf("indexer: Stats %s count: %w", tt.ty, err)
		}
		*tt.dst = res.Total
	}

	// Corpus text bytes: walk every content doc in batches, projecting
	// only the text field. The loop terminates on a short page; a
	// defensive ceiling scaled off the known content count bounds a
	// pathological index that never returns one — hitting it logs the
	// truncation and never errors.
	q := bleve.NewQueryStringQuery("+" + fieldType + ":" + typeContent)
	const batch = 1000
	var (
		from    = 0
		textSum int64
	)
	maxPages := int(out.ContentCount/batch) + 2
	for page := 0; ; page++ {
		if page >= maxPages {
			i.log.Warn("indexer.stats_text_scan_truncated",
				"pages", page, "scanned", from, "content_count", out.ContentCount)
			break
		}
		sr := bleve.NewSearchRequestOptions(q, batch, from, false)
		sr.Fields = []string{fieldText}
		res, err := i.bleve.Search(sr)
		if err != nil {
			return Stats{}, fmt.Errorf("indexer: Stats text scan: %w", err)
		}
		if len(res.Hits) == 0 {
			break
		}
		for _, h := range res.Hits {
			if v, ok := h.Fields[fieldText].(string); ok {
				textSum += int64(len(v))
			}
		}
		if len(res.Hits) < batch {
			break
		}
		from += batch
	}
	out.CorpusTextBytes = textSum

	// On-disk size degrades to 0 for a missing or unreadable root.
	if size, err := dirBytes(i.path); err == nil {
		out.DirBytes = size
	}

	if out.CorpusTextBytes > 0 {
		out.InflationRatio = float64(out.DirBytes) / float64(out.CorpusTextBytes)
	}
	return out, nil
}

// dirBytes sums the size of every regular file under root. Symlinked
// directories are not descended into (os.DirEntry.IsDir is false for
// them, which also avoids symlink loops); subdirectory errors are skipped
// so one unreadable subdir cannot abort the walk.
func dirBytes(root string) (int64, error) {
	var total int64
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		if e.IsDir() {
			sub, _ := dirBytes(filepath.Join(root, e.Name()))
			total += sub
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return total, nil
}
