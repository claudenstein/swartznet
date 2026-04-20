package daemon

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/indexer"
)

// stubPointerPutter / stubTorrentSeeder are local fakes that
// satisfy companion.PointerPutter / TorrentSeeder. The publisher
// constructor never invokes them — we only care that the adapter
// can read .Status() from a real Publisher.
type stubPointerPutter struct{}

func (stubPointerPutter) PutInfohashPointer(_ context.Context, _ []byte, _ [20]byte) error {
	return nil
}

type stubTorrentSeeder struct{}

func (stubTorrentSeeder) AddTorrentMetaInfo(_ *metainfo.MetaInfo) (any, error) {
	return nil, nil
}

// TestCompanionAdapterPublisherStatusFromRealPublisher covers the
// non-nil branch of companionAdapter.PublisherStatus by wiring
// the adapter to a real (but unstarted) companion.Publisher and
// asserting the status round-trip.
func TestCompanionAdapterPublisherStatusFromRealPublisher(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatalf("indexer.Open: %v", err)
	}
	defer idx.Close()

	var pubKey [32]byte
	pubKey[0] = 0xab // arbitrary
	opts := companion.PublisherOptions{
		Dir:          t.TempDir(),
		PublisherKey: pubKey,
	}
	pub, err := companion.NewPublisher(idx, stubPointerPutter{}, stubTorrentSeeder{}, opts,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}

	adapter := newCompanionAdapter(pub, nil, "")
	st := adapter.PublisherStatus()
	// Pre-Start the publisher has nothing to report except the
	// pubkey hex (filled by NewPublisher from PublisherKey).
	if st.PubKeyHex == "" {
		t.Error("PubKeyHex should be populated from the publisher options")
	}
	if st.PublishedCount != 0 {
		t.Errorf("PublishedCount = %d, want 0 pre-Start", st.PublishedCount)
	}
	if st.LastInfoHash != "" {
		t.Errorf("LastInfoHash = %q, want empty pre-Start", st.LastInfoHash)
	}

	// RefreshNow on a publisher that hasn't run yet must succeed
	// (not throttled — lastRefresh is zero) and queue a trigger
	// the worker would consume if Started.
	if err := adapter.RefreshNow(); err != nil {
		t.Errorf("RefreshNow on never-refreshed publisher should succeed, got %v", err)
	}
}
