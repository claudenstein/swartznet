package companion

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
)

// blockingGetter lets a test deterministically pause inside
// Subscriber.Sync (the first thing Sync does is resolve the
// pointer) so it can interleave an Unfollow with the in-flight
// sync. The actual lookup always fails, so Sync returns a
// failure SyncResult shortly after release — which is exactly the
// result that must NOT be re-inserted for an unfollowed publisher.
type blockingGetter struct {
	entered  chan struct{}
	release  chan struct{}
	onceDone bool
}

func (g *blockingGetter) GetInfohashPointer(ctx context.Context, pubkey [32]byte, salt []byte) ([20]byte, error) {
	if !g.onceDone {
		g.onceDone = true
		close(g.entered)
		<-g.release
	}
	return [20]byte{}, errors.New("blockingGetter: no pointer")
}

type nopFetcher struct{}

func (nopFetcher) FetchCompanionTorrent(ctx context.Context, ih [20]byte) (string, error) {
	return "", errors.New("nopFetcher")
}

type nopIngester struct{}

func (nopIngester) IndexTorrent(indexer.TorrentDoc) error { return nil }
func (nopIngester) IndexContent(indexer.ContentDoc) error { return nil }

// TestRunOnceDropsResultForUnfollowedPublisher is the regression
// for the Unfollow-during-sync stale re-insert: runOnce snapshots
// the follow list, then writes lastSync[pub] back after the slow
// Sync. If Unfollow(pub) lands during that Sync, runOnce must not
// resurrect a result for the no-longer-followed publisher.
func TestRunOnceDropsResultForUnfollowedPublisher(t *testing.T) {
	g := &blockingGetter{entered: make(chan struct{}), release: make(chan struct{})}
	sub, err := NewSubscriber(g, nopFetcher{}, nopIngester{}, SubscriberOptions{},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewSubscriberWorker(sub)
	if err != nil {
		t.Fatal(err)
	}

	var pub [32]byte
	pub[0] = 0xAB
	w.Follow(pub, "victim")

	done := make(chan struct{})
	go func() {
		w.runOnce(context.Background())
		close(done)
	}()

	// Wait until Sync is mid-flight, then unfollow and let it
	// finish. The result for pub must be discarded.
	select {
	case <-g.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Sync never started")
	}
	w.Unfollow(pub)
	close(g.release)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runOnce did not finish")
	}

	if _, ok := w.lastSync[pub]; ok {
		t.Fatal("runOnce re-inserted a SyncResult for an unfollowed publisher")
	}
	if got := w.LastSync(pub); got.Publisher != "" {
		t.Fatalf("LastSync still surfaces a result for unfollowed publisher: %+v", got)
	}
}
