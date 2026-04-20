package companion_test

import (
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/companion"
)

func TestRegtestPublisherOptionsAreAccelerated(t *testing.T) {
	t.Parallel()
	def := companion.DefaultPublisherOptions()
	rt := companion.RegtestPublisherOptions()

	if !(rt.Interval > 0 && rt.Interval < def.Interval) {
		t.Errorf("regtest Interval = %v, want positive and < default %v", rt.Interval, def.Interval)
	}
	if !(rt.MinInterval > 0 && rt.MinInterval < def.MinInterval) {
		t.Errorf("regtest MinInterval = %v, want positive and < default %v",
			rt.MinInterval, def.MinInterval)
	}
	if rt.PutTimeout <= 0 {
		t.Errorf("regtest PutTimeout = %v, want > 0", rt.PutTimeout)
	}
	// Specific values from the regtest constants — these are the
	// promised accelerated timings the docs and scenario tests rely on.
	if rt.Interval != 10*time.Second {
		t.Errorf("regtest Interval = %v, want 10s", rt.Interval)
	}
	if rt.MinInterval != 100*time.Millisecond {
		t.Errorf("regtest MinInterval = %v, want 100ms", rt.MinInterval)
	}
}

func TestEncodeSizeMatchesEncode(t *testing.T) {
	t.Parallel()
	idx := companion.CompanionIndex{
		Version:   1,
		Format:    "swartznet-content-index",
		Publisher: "abcd",
		Torrents: []companion.TorrentRecord{
			{InfoHash: "1111111111111111111111111111111111111111", Name: "ubuntu", Size: 4096},
			{InfoHash: "2222222222222222222222222222222222222222", Name: "debian", Size: 8192},
		},
	}

	encoded, err := companion.Encode(idx)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := companion.EncodeSize(idx)
	if err != nil {
		t.Fatalf("EncodeSize: %v", err)
	}
	if got != len(encoded) {
		t.Errorf("EncodeSize = %d, want %d (= len(Encode()))", got, len(encoded))
	}
}

// TestSubscriberWorkerAllResultsAndTotalRunsBeforeStart verifies the
// two getters return zero/empty values on a freshly-built worker
// with no follows registered. This exercises the lock + map-snapshot
// code path without depending on the worker actually running.
func TestSubscriberWorkerAllResultsAndTotalRunsBeforeStart(t *testing.T) {
	t.Parallel()
	sub, err := companion.NewSubscriber(&fakeGetter{}, &fakeFetcher{}, &recorderIngester{},
		companion.DefaultSubscriberOptions(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	w, err := companion.NewSubscriberWorker(sub)
	if err != nil {
		t.Fatal(err)
	}

	if got := w.AllResults(); len(got) != 0 {
		t.Errorf("AllResults on fresh worker = %d entries, want 0", len(got))
	}
	if got := w.TotalRuns(); got != 0 {
		t.Errorf("TotalRuns on fresh worker = %d, want 0", got)
	}
}

// TestSubscriberWorkerAllResultsPopulatedAfterSync drives the worker
// through one sync (Follow + Start) and verifies AllResults reports
// the recorded SyncResult and TotalRuns advances past zero.
func TestSubscriberWorkerAllResultsPopulatedAfterSync(t *testing.T) {
	t.Parallel()
	path, _ := writeCompanionPayload(t)

	getter := &fakeGetter{}
	getter.SetPointer([20]byte{0xab})
	fetcher := &fakeFetcher{path: path}
	rec := &recorderIngester{}

	opts := companion.DefaultSubscriberOptions()
	opts.Interval = time.Hour // we drive via Follow trigger
	sub, err := companion.NewSubscriber(getter, fetcher, rec, opts, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	w, err := companion.NewSubscriberWorker(sub)
	if err != nil {
		t.Fatal(err)
	}
	w.Start()
	defer w.Stop()

	var pub [32]byte
	pub[0] = 5
	w.Follow(pub, "test")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.LastSync(pub).TorrentsImported > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	results := w.AllResults()
	if len(results) != 1 {
		t.Fatalf("AllResults len = %d, want 1", len(results))
	}
	if results[0].TorrentsImported != 2 {
		t.Errorf("AllResults[0].TorrentsImported = %d, want 2", results[0].TorrentsImported)
	}
	if w.TotalRuns() == 0 {
		t.Error("TotalRuns = 0 after a Follow-triggered sync, want > 0")
	}
}
