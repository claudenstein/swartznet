package companion

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/indexer"
)

// togglePutter fails PutInfohashPointer when fail is set, so a test can drive the
// put-failure branch deterministically.
type togglePutter struct {
	mu   sync.Mutex
	fail bool
	puts [][20]byte
}

func (p *togglePutter) PutInfohashPointer(_ context.Context, _ []byte, ih [20]byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		return errors.New("put failed")
	}
	p.puts = append(p.puts, ih)
	return nil
}

func newTogglePublisher(t *testing.T, src CorpusSource, pub [32]byte) (*Publisher, *togglePutter, *memSeeder) {
	t.Helper()
	seeder := &memSeeder{}
	putter := &togglePutter{}
	opts := DefaultPublisherOptions()
	opts.Dir = t.TempDir()
	opts.PublisherKey = pub
	opts.MinInterval = 0
	p, err := NewPublisher(src, putter, seeder, opts, discardLog())
	if err != nil {
		t.Fatal(err)
	}
	return p, putter, seeder
}

// TestPublisherDropsOrphanSeedOnPutFailure pins the round-5 MED fix: when the
// pointer put fails AFTER a content change, the freshly-seeded (new-infohash)
// companion torrent is referenced by no live pointer and must be dropped — else
// it leaks (seeded for the process lifetime). The still-advertised previous
// infohash must NOT be dropped.
func TestPublisherDropsOrphanSeedOnPutFailure(t *testing.T) {
	pub := keypair(52)
	src := &fakeCorpus{torrents: []indexer.TorrentDoc{{InfoHash: strings.Repeat("1", 40), Name: "a", FilePaths: []string{"a"}}}}
	p, putter, seeder := newTogglePublisher(t, src, pub)

	// Refresh 1: put succeeds → seeds ih_N, lastSeededIH=ih_N.
	p.refreshOnce(context.Background())
	p.mu.Lock()
	ihN := p.lastSeededIH
	p.mu.Unlock()
	if ihN == ([20]byte{}) {
		t.Fatal("refresh 1 did not seed")
	}

	// Change content, then make the next put fail.
	src.torrents = []indexer.TorrentDoc{{InfoHash: strings.Repeat("2", 40), Name: "b", FilePaths: []string{"b"}}}
	putter.mu.Lock()
	putter.fail = true
	putter.mu.Unlock()

	// Refresh 2: seeds ih_{N+1}, put fails → ih_{N+1} must be dropped, ih_N kept.
	p.refreshOnce(context.Background())

	p.mu.Lock()
	still := p.lastSeededIH
	p.mu.Unlock()
	if still != ihN {
		t.Fatalf("lastSeededIH advanced on put failure: %x -> %x", ihN, still)
	}

	seeder.mu.Lock()
	seen := append([]*metainfo.MetaInfo(nil), seeder.seen...)
	dropped := append([][20]byte(nil), seeder.dropped...)
	seeder.mu.Unlock()
	if len(seen) < 2 {
		t.Fatalf("expected 2 seeds, got %d", len(seen))
	}
	ihNew := [20]byte(seen[1].HashInfoBytes())
	if ihNew == ihN {
		t.Fatal("content change did not change the infohash — test cannot distinguish")
	}
	droppedNew, droppedOld := false, false
	for _, d := range dropped {
		if d == ihNew {
			droppedNew = true
		}
		if d == ihN {
			droppedOld = true
		}
	}
	if !droppedNew {
		t.Error("orphaned new companion seed was NOT dropped on put failure → leak")
	}
	if droppedOld {
		t.Error("dropped the still-advertised ih_N on a put failure")
	}
}

// TestPublisherKeepsAdvertisedSeedOnUnchangedPutFailure guards the other side of
// the fix: when the corpus is UNCHANGED (same infohash as the last successful
// publish) and a TTL-refresh put fails, the infohash is still advertised by the
// prior pointer and must NOT be dropped.
func TestPublisherKeepsAdvertisedSeedOnUnchangedPutFailure(t *testing.T) {
	pub := keypair(53)
	src := &fakeCorpus{torrents: []indexer.TorrentDoc{{InfoHash: strings.Repeat("3", 40), Name: "a", FilePaths: []string{"a"}}}}
	p, putter, seeder := newTogglePublisher(t, src, pub)

	p.refreshOnce(context.Background())
	p.mu.Lock()
	ihN := p.lastSeededIH
	p.mu.Unlock()

	// Same corpus, next put fails (TTL refresh).
	putter.mu.Lock()
	putter.fail = true
	putter.mu.Unlock()
	p.refreshOnce(context.Background())

	seeder.mu.Lock()
	dropped := append([][20]byte(nil), seeder.dropped...)
	seeder.mu.Unlock()
	for _, d := range dropped {
		if d == ihN {
			t.Error("dropped the still-advertised infohash on an unchanged-corpus put failure")
		}
	}
}
