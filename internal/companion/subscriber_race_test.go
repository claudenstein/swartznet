package companion

import (
	"context"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// hookFetcher runs a side effect while a Sync is mid-flight — the fetch happens
// after the Sync captures its Forget epoch but before it commits dedup state, so
// a hook that calls Forget reproduces the Unfollow-vs-Sync race deterministically.
type hookFetcher struct {
	inner CompanionFetcher
	hook  func()
}

func (f *hookFetcher) FetchCompanionTorrent(ctx context.Context, ih [20]byte) (string, error) {
	if f.hook != nil {
		f.hook()
	}
	return f.inner.FetchCompanionTorrent(ctx, ih)
}

// TestSubscriberForgetDuringSyncDoesNotResurrectDedup pins the #11 fix: when
// Forget (an Unfollow) runs while a Sync is in flight, that Sync must NOT write
// dedup state back for the forgotten publisher. Otherwise a later re-follow would
// silently dedup content it should re-import (the maps would claim the snapshot
// was already seen).
func TestSubscriberForgetDuringSyncDoesNotResurrectDedup(t *testing.T) {
	t.Parallel()
	pub := keypair(31)
	src := &fakeCorpus{torrents: []indexer.TorrentDoc{{InfoHash: strings.Repeat("a", 40), Name: "x", FilePaths: []string{"a"}}}}
	jsonPath, _ := publishToDisk(t, pub, src)

	rec := &recorder{}
	var sub *Subscriber
	fetcher := &hookFetcher{
		inner: &pathFetcher{path: jsonPath},
		hook:  func() { sub.Forget(pub) }, // Unfollow races the in-flight Sync
	}
	sub, err := NewSubscriber(&memGetter{store: storeWith(pub, [20]byte{7})}, fetcher, rec, DefaultSubscriberOptions(), discardLog())
	if err != nil {
		t.Fatal(err)
	}

	res := sub.Sync(context.Background(), pub)
	if res.Err != nil {
		t.Fatalf("sync: %v", res.Err)
	}

	sub.mu.Lock()
	_, hasImported := sub.imported[pub]
	_, hasIH := sub.lastIH[pub]
	sub.mu.Unlock()
	if hasImported || hasIH {
		t.Errorf("dedup state resurrected after Forget-during-Sync (imported=%v lastIH=%v)", hasImported, hasIH)
	}

	// Sanity: a fresh re-follow (epoch already bumped) imports again rather than
	// deduping against the ghost state.
	res2 := sub.Sync(context.Background(), pub)
	if res2.Err != nil {
		t.Fatalf("re-sync: %v", res2.Err)
	}
	if res2.Deduped {
		t.Error("re-follow deduped against resurrected state — content would be missing")
	}
}

// TestPublisherGeneratedAtMonotonicUnderClockRegression pins the #5 fix: a
// content change must advance GeneratedAt past the last published value even when
// the wall clock stepped backward. Followers reject a regressed GeneratedAt as a
// replay, so a well-behaved publisher must keep it strictly increasing.
func TestPublisherGeneratedAtMonotonicUnderClockRegression(t *testing.T) {
	t.Parallel()
	pub := keypair(41)
	src := &fakeCorpus{torrents: []indexer.TorrentDoc{{InfoHash: strings.Repeat("1", 40), Name: "a", FilePaths: []string{"a"}}}}
	p, _, _ := newTestPublisher(t, src, pub)

	p.refreshOnce(context.Background())
	p.mu.Lock()
	first := p.lastGeneratedAt
	// Simulate that the previous publish carried a far-future timestamp (the clock
	// was ahead), then corrected well below it before this refresh.
	future := first + 1_000_000
	p.lastGeneratedAt = future
	p.mu.Unlock()
	if first == 0 {
		t.Fatal("first refresh did not record a GeneratedAt")
	}

	// Change content so the snapshot is genuinely new (different fingerprint), not
	// deduped to the reused timestamp.
	src.torrents = []indexer.TorrentDoc{{InfoHash: strings.Repeat("2", 40), Name: "b", FilePaths: []string{"b"}}}
	p.refreshOnce(context.Background())

	p.mu.Lock()
	got := p.lastGeneratedAt
	p.mu.Unlock()
	if got <= future {
		t.Errorf("GeneratedAt = %d, want > %d (must not regress on a content change)", got, future)
	}
}
