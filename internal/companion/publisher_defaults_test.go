package companion_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
)

// TestNewPublisherFillsLogAndOptionDefaults covers the
// previously-uncovered nil-log defaulting branch in NewPublisher.
// Existing tests pass silentLogger() so the slog.Default()
// substitution branch never fires; here we pass nil explicitly
// alongside a zero PublisherOptions (only Dir set) so every
// option-default path also runs.
func TestNewPublisherFillsLogAndOptionDefaults(t *testing.T) {
	t.Parallel()
	idx := seedIndex(t)
	put := &fakePutter{}
	seed := &fakeSeeder{}
	opts := companion.PublisherOptions{Dir: t.TempDir()} // zero Interval/MinInterval/PutTimeout

	p, err := companion.NewPublisher(idx, put, seed, opts, nil)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	if p == nil {
		t.Fatal("NewPublisher returned nil")
	}
}
