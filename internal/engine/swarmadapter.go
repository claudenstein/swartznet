package engine

import (
	"errors"
	"fmt"

	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// errReplyOverloaded is returned by a gatedReplyWriter reply func
// when every writer slot is busy and the reply has been dropped.
var errReplyOverloaded = errors.New("engine: sn_search reply dropped: writer slots exhausted")

// gatedReplyWriter builds the reply func handed to
// swarmsearch.Protocol.HandleMessage for inbound sn_search frames.
// Each accepted reply copies its body (the protocol layer may reuse
// the buffer) and performs the write on its own goroutine so the
// handler never blocks on the client lock — but the goroutine must
// first take a slot on sem, so a peer provoking many replies (e.g.
// a sync stream) cannot drive unbounded goroutine growth. When sem
// is full the reply is dropped and errReplyOverloaded returned so
// the caller sees the send as failed instead of silently queueing.
// Write errors surface asynchronously through onErr.
func gatedReplyWriter(sem chan struct{}, write func([]byte) error, onErr func(error)) func([]byte) error {
	return func(body []byte) error {
		select {
		case sem <- struct{}{}:
		default:
			return errReplyOverloaded
		}
		bodyCopy := append([]byte(nil), body...)
		go func() {
			defer func() { <-sem }()
			if err := write(bodyCopy); err != nil {
				onErr(err)
			}
		}()
		return nil
	}
}

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
