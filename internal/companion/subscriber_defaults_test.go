package companion_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
)

// TestNewSubscriberFillsDefaults covers the previously-uncovered
// option-default-fill paths in NewSubscriber. Every existing
// test passes DefaultSubscriberOptions() (so all timings are
// already non-zero) and a real logger; here we pass a zero
// SubscriberOptions and a nil log to drive the four defaulting
// branches: log → slog.Default, FetchTimeout, PointerTimeout,
// Interval.
func TestNewSubscriberFillsDefaults(t *testing.T) {
	t.Parallel()
	sub, err := companion.NewSubscriber(&fakeGetter{}, &fakeFetcher{}, &recorderIngester{},
		companion.SubscriberOptions{}, nil)
	if err != nil {
		t.Fatalf("NewSubscriber: %v", err)
	}
	if sub == nil {
		t.Fatal("NewSubscriber returned nil")
	}
}
