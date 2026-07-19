package engine

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/anacrolix/torrent"
	pp "github.com/anacrolix/torrent/peer_protocol"

	"github.com/swartznet/swartznet/contracts/ltepwire"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// maxInboundSnSearchWorkers bounds concurrent inbound sn_search handlers and
// reply writers (two independent semaphores). A full semaphore drops the frame
// rather than queueing — the read loop must never block on sn_search work.
const maxInboundSnSearchWorkers = 256

// extName is the anacrolix LTEP extension name for sn_search.
var extName = pp.ExtensionName(ltepwire.ExtensionName)

// errReplyOverloaded is returned by the gated reply writer when the reply
// semaphore is exhausted, so HandleMessage sees the send as failed, not queued.
var errReplyOverloaded = errors.New("engine: sn_search reply dropped: writer slots exhausted")

// peerTracker maps a remote address to its live anacrolix connection, so the
// sn_search sender can resolve a PeerToken to a conn without exposing any
// addr-keyed map to callers.
type peerTracker struct {
	mu    sync.RWMutex
	conns map[string]*torrent.PeerConn
}

func newPeerTracker() *peerTracker { return &peerTracker{conns: make(map[string]*torrent.PeerConn)} }

func (t *peerTracker) add(addr string, pc *torrent.PeerConn) {
	t.mu.Lock()
	t.conns[addr] = pc
	t.mu.Unlock()
}
func (t *peerTracker) remove(addr string) {
	t.mu.Lock()
	delete(t.conns, addr)
	t.mu.Unlock()
}
func (t *peerTracker) get(addr string) *torrent.PeerConn {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.conns[addr]
}

// swarmSender implements swarmsearch.Transport. It resolves a PeerToken to a
// live connection and writes the frame as a standard BEP-10 extended message.
// A token for a closed/absent conn resolves to nil and errors — no bytes go out.
type swarmSender struct{ peers *peerTracker }

func (s *swarmSender) SendExtension(peer swarmsearch.PeerToken, frame []byte) error {
	pc := s.peers.get(peer.Addr())
	if pc == nil {
		return fmt.Errorf("engine: no live sn_search conn for %s", peer.Addr())
	}
	return pc.WriteExtendedMessage(extName, frame)
}

// gatedReply builds a swarmsearch.ReplyFunc bound to one connection. It takes a
// reply-semaphore slot per accepted reply, copies the body (the protocol layer
// reuses its buffer), writes on its own goroutine, and returns
// errReplyOverloaded when the semaphore is full.
func gatedReply(sem chan struct{}, pc *torrent.PeerConn, log *slog.Logger) swarmsearch.ReplyFunc {
	return func(payload []byte) error {
		select {
		case sem <- struct{}{}:
		default:
			return errReplyOverloaded
		}
		body := append([]byte(nil), payload...)
		go func() {
			defer func() { <-sem }()
			if err := pc.WriteExtendedMessage(extName, body); err != nil {
				log.Debug("engine.swarm.reply_write_err", "err", err)
			}
		}()
		return nil
	}
}

// indexerSearcher adapts *indexer.Index to swarmsearch.LocalSearcher so the
// swarmsearch package never imports Bleve. AddedAt/Seeders/Leechers are not
// available from the index, so they stay zero — and a zero AddedAt correctly
// omits the freshness stamp on the wire (§6 defect b).
type indexerSearcher struct{ idx *indexer.Index }

func (s *indexerSearcher) SearchLocal(query string, limit int) (int, []swarmsearch.LocalHit, error) {
	resp, err := s.idx.Search(indexer.SearchRequest{Query: query, Limit: limit})
	if err != nil {
		return 0, nil, err
	}
	hits := make([]swarmsearch.LocalHit, 0, len(resp.Hits))
	for _, h := range resp.Hits {
		hits = append(hits, swarmsearch.LocalHit{
			DocType:   h.DocType,
			InfoHash:  h.InfoHash,
			Name:      h.Name,
			SizeBytes: h.SizeBytes,
			FileIndex: h.FileIndex,
			FilePath:  h.FilePath,
			Score:     h.Score,
		})
	}
	return int(resp.Total), hits, nil
}
