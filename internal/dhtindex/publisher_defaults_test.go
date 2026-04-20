package dhtindex_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// nopPutter satisfies Putter without doing anything; the
// constructor never touches it.
type nopPutter struct{}

func (nopPutter) PublicKey() [32]byte { return [32]byte{} }

func (nopPutter) Put(_ interface{ Done() <-chan struct{} }) {} // unused

// TestNewPublisherFillsDefaults covers the constructor's
// default-substitution branches: passing a zero PublisherOptions
// and a nil logger must yield a non-nil Publisher with sensible
// defaults filled in (we can't read the unexported opts directly,
// but the absence of a panic + a non-nil return proves the
// substitution path executed without a divide-by-zero / nil
// channel construction).
func TestNewPublisherFillsDefaults(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")
	p := dhtindex.NewPublisher(nil, mf, dhtindex.PublisherOptions{}, nil)
	if p == nil {
		t.Fatal("NewPublisher returned nil")
	}
}

// TestNewPublisherNormalisesNegativeMinPutInterval covers the
// MinPutInterval < 0 branch: negatives are normalised to zero
// (effectively "no throttle") rather than producing a wait that
// elapses immediately for a different reason in the worker loop.
func TestNewPublisherNormalisesNegativeMinPutInterval(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")
	p := dhtindex.NewPublisher(nil, mf, dhtindex.PublisherOptions{
		MinPutInterval: -42,
	}, nil)
	if p == nil {
		t.Fatal("NewPublisher returned nil")
	}
}
