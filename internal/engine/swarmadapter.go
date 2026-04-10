package engine

import (
	"fmt"

	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// swarmSender implements swarmsearch.Sender by looking up the target
// *torrent.PeerConn in the engine's peerTracker and forwarding the
// payload through anacrolix's WriteExtendedMessage. M3c uses this to
// fan outbound queries out to every known search-capable peer.
type swarmSender struct {
	peers *peerTracker
}

// Send implements swarmsearch.Sender.
func (s *swarmSender) Send(peerAddr string, payload []byte) error {
	pc, ok := s.peers.get(peerAddr)
	if !ok {
		return fmt.Errorf("engine: swarmSender: no peer with addr %q", peerAddr)
	}
	return pc.WriteExtendedMessage(swarmsearch.ExtensionName, payload)
}

// indexerSearcher adapts an *indexer.Index to the
// swarmsearch.LocalSearcher interface. The adapter exists so the
// swarmsearch package stays independent of internal/indexer (which
// would otherwise introduce a dependency cycle: indexer →
// swarmsearch is fine, but we want neither to import the other at
// runtime).
type indexerSearcher struct {
	idx *indexer.Index
}

// SearchLocal implements swarmsearch.LocalSearcher by running a
// Bleve search against the index and translating each SearchHit
// into a swarmsearch.LocalHit. Only the fields that fit the wire
// schema are carried across.
func (s *indexerSearcher) SearchLocal(query string, limit int) (int, []swarmsearch.LocalHit, error) {
	res, err := s.idx.Search(indexer.SearchRequest{Query: query, Limit: limit})
	if err != nil {
		return 0, nil, err
	}
	out := make([]swarmsearch.LocalHit, 0, len(res.Hits))
	for _, h := range res.Hits {
		out = append(out, swarmsearch.LocalHit{
			DocType:   h.DocType,
			InfoHash:  h.InfoHash,
			Name:      h.Name,
			SizeBytes: h.SizeBytes,
			FileIndex: h.FileIndex,
			FilePath:  h.FilePath,
			Score:     h.Score,
		})
	}
	return int(res.Total), out, nil
}
