package companion_test

import (
	"context"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
)

// TestSubscriberSyncIndexContentError covers the
// `if err := s.ingester.IndexContent(cd); err != nil { return ... }`
// arm in subscriber.ingest. The first IndexTorrent call (call 1)
// must succeed; the second call — the first IndexContent — must
// fail. recorderIngester's failNth makes that easy.
func TestSubscriberSyncIndexContentError(t *testing.T) {
	t.Parallel()
	path, _ := writeCompanionPayload(t)

	getter := &fakeGetter{}
	getter.SetPointer([20]byte{0xab})
	fetcher := &fakeFetcher{path: path}
	rec := &recorderIngester{failNth: 2} // 1 = IndexTorrent (ok), 2 = first IndexContent (fail)

	sub, err := companion.NewSubscriber(getter, fetcher, rec, companion.DefaultSubscriberOptions(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	res := sub.Sync(context.Background(), [32]byte{})
	if res.Err == nil {
		t.Fatal("expected ingest content error to abort Sync")
	}
	if res.TorrentsImported != 1 {
		t.Errorf("TorrentsImported = %d, want 1", res.TorrentsImported)
	}
}
