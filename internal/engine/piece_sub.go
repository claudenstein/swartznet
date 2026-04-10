package engine

import (
	"log/slog"
	"sync"

	"github.com/anacrolix/torrent"
)

// pieceSubscription bridges anacrolix/torrent's pubsub.Subscription, which
// delivers events on Values(), into a long-lived goroutine that routes events
// out on an owned Go channel. The wrapper exists so that higher layers can
// attach multiple consumers (the CLI live-status reporter, the M2 indexer,
// future metrics) without fighting over a single-consumer pubsub channel.
type pieceSubscription struct {
	log *slog.Logger

	sub *torrentSubscription // see type alias below

	mu       sync.Mutex
	closed   bool
	closeCh  chan struct{}
	consumer chan torrent.PieceStateChange
}

// torrentSubscription is a narrow interface matching the real
// pubsub.Subscription[torrent.PieceStateChange] returned by
// (*torrent.Torrent).SubscribePieceStateChanges, so we do not take a direct
// dependency on the anacrolix/missinggo pubsub package from callers.
type torrentSubscription struct {
	values <-chan torrent.PieceStateChange
	closer func()
}

func startPieceSubscription(t *torrent.Torrent, log *slog.Logger) *pieceSubscription {
	real := t.SubscribePieceStateChanges()
	ts := &torrentSubscription{
		values: real.Values,
		closer: real.Close,
	}

	ps := &pieceSubscription{
		log:      log,
		sub:      ts,
		closeCh:  make(chan struct{}),
		consumer: make(chan torrent.PieceStateChange, 64),
	}

	go ps.run(t.InfoHash().HexString())
	return ps
}

// Events returns a channel that fires whenever a piece in this torrent
// transitions state (downloading, completed, failed). Readers MUST drain
// this channel or the producer goroutine will eventually block.
//
// In M1 we expose the raw anacrolix event type directly. M2 will introduce
// a higher-level "file-complete" event synthesised from a stream of piece
// events and the torrent's file layout.
func (p *pieceSubscription) Events() <-chan torrent.PieceStateChange {
	return p.consumer
}

// Close detaches from the underlying subscription and terminates the
// forwarder goroutine. Idempotent.
func (p *pieceSubscription) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.closeCh)
	p.mu.Unlock()
	// Call the upstream closer under no lock — it may block briefly while
	// pubsub unsubscribes.
	p.sub.closer()
}

// run is the forwarder goroutine. It reads from anacrolix's pubsub channel
// and pushes into our owned consumer channel. When either side closes we
// clean up.
func (p *pieceSubscription) run(ihHex string) {
	defer close(p.consumer)
	for {
		select {
		case <-p.closeCh:
			return
		case ev, ok := <-p.sub.values:
			if !ok {
				return
			}
			// Non-blocking fanout. If the consumer is backed up we drop the
			// event and log it. The indexer in M2 will replace this with a
			// proper back-pressure strategy; for M1 the point is just to
			// prove the wiring works without ever blocking the producer.
			select {
			case p.consumer <- ev:
			default:
				p.log.Debug("piece.sub.drop",
					"info_hash", ihHex,
					"piece", ev.Index,
				)
			}
		}
	}
}
