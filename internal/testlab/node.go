package testlab

import (
	"context"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// IndexTorrent is a convenience that writes a TorrentDoc with a
// predictable infohash + name into this node's local Bleve
// index. Used by Layer-S scenarios to seed a node with content
// before other nodes run queries against it.
//
// infohashByte is the single byte that fills the entire
// 20-byte infohash — tests use 0x01, 0x02, etc. so log lines
// stay readable. name is the torrent name that Layer-S queries
// will match against. filePaths is an optional list of files
// inside the torrent; scenarios that also call IndexContent
// must list the same paths here so companion.BuildFromIndex
// (which only emits file records for paths listed in
// TorrentDoc.FilePaths) can attach the content chunks.
func (n *Node) IndexTorrent(t *testing.T, infohashByte byte, name string, filePaths ...string) {
	t.Helper()
	if err := n.Index.IndexTorrent(indexer.TorrentDoc{
		InfoHash:  string(hexOf(infohashByte)),
		Name:      name,
		FilePaths: filePaths,
	}); err != nil {
		t.Fatalf("testlab: IndexTorrent: %v", err)
	}
}

// IndexContent is the sibling for content-level documents. The
// content doc's FilePath is always "body.txt" and its chunk
// index is zero — tests that need more structure should call
// Index.IndexContent directly.
func (n *Node) IndexContent(t *testing.T, infohashByte byte, text string) {
	t.Helper()
	ih := string(hexOf(infohashByte))
	if err := n.Index.IndexContent(indexer.ContentDoc{
		InfoHash: ih,
		FilePath: "body.txt",
		Text:     text,
	}); err != nil {
		t.Fatalf("testlab: IndexContent: %v", err)
	}
}

// SwarmQuery runs a Layer-S query from this node against every
// sn_search-capable peer it knows about and returns the merged
// hit set. Uses a 2-second timeout by default; tests that need
// longer should call Eng.SwarmSearch().Query directly.
func (n *Node) SwarmQuery(t *testing.T, query string) *swarmsearch.QueryResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := n.Eng.SwarmSearch().Query(ctx, swarmsearch.QueryRequest{
		Q:            query,
		PerPeerLimit: 50,
		Timeout:      2 * time.Second,
	})
	if err != nil {
		t.Fatalf("testlab: SwarmQuery: %v", err)
	}
	return resp
}

// LocalQuery runs a Layer-L query against this node's own
// Bleve index. Useful for asserting that a node has the
// content a test expects before running Layer-S queries
// through its peers.
func (n *Node) LocalQuery(t *testing.T, query string) *indexer.SearchResponse {
	t.Helper()
	resp, err := n.Index.Search(indexer.SearchRequest{Query: query, Limit: 50})
	if err != nil {
		t.Fatalf("testlab: LocalQuery: %v", err)
	}
	return resp
}

// hexDigit is a tiny helper used by IndexTorrent to build a
// 40-char hex infohash without importing encoding/hex.
func hexDigit(b byte) byte {
	if b < 10 {
		return '0' + b
	}
	return 'a' + (b - 10)
}

// hexOf formats a single byte as a 40-char lowercase hex
// infohash (every pair equal). Used for the test convenience
// helpers above.
func hexOf(b byte) []byte {
	out := make([]byte, 40)
	hi := hexDigit(b >> 4)
	lo := hexDigit(b & 0x0f)
	for i := 0; i < 40; i += 2 {
		out[i] = hi
		out[i+1] = lo
	}
	return out
}
