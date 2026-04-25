package daemon

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
)

// fakeHTTPSClient returns a fixed response body or an error.
type fakeHTTPSClient struct {
	body []byte
	err  error
}

func (f fakeHTTPSClient) Get(_ context.Context, _ string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.body, nil
}

func TestFallbackToHTTPSAddsNewAnchors(t *testing.T) {
	lookup := newTestLookup()
	opts := DefaultBootstrapOptions()
	b, _ := NewBootstrap(lookup, nil, nil, nil, opts, nil)

	newPub := pubkeyBytes("fallback-anchor-1")
	body := []byte(fmt.Sprintf(`{"version":1,"anchors":["%s"]}`,
		hex.EncodeToString(newPub[:])))

	added, err := b.FallbackToHTTPS(context.Background(), "https://example/v1/anchors",
		fakeHTTPSClient{body: body})
	if err != nil {
		t.Fatalf("FallbackToHTTPS: %v", err)
	}
	if added != 1 {
		t.Errorf("added = %d, want 1", added)
	}
	got := b.AnchorKeys()
	if len(got) != 1 || got[0] != newPub {
		t.Errorf("AnchorKeys = %v, want [%x]", got, newPub)
	}
}

func TestFallbackToHTTPSDedupesExisting(t *testing.T) {
	existing := pubkeyBytes("already-configured")
	opts := DefaultBootstrapOptions()
	opts.AnchorHexes = []string{hex.EncodeToString(existing[:])}
	lookup := newTestLookup()
	b, _ := NewBootstrap(lookup, nil, nil, nil, opts, nil)

	body := []byte(fmt.Sprintf(`{"version":1,"anchors":["%s"]}`,
		hex.EncodeToString(existing[:])))

	added, err := b.FallbackToHTTPS(context.Background(), "x", fakeHTTPSClient{body: body})
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Errorf("added = %d, want 0 (duplicate)", added)
	}
}

func TestFallbackToHTTPSSkipsMalformedEntries(t *testing.T) {
	lookup := newTestLookup()
	b, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)

	good := pubkeyBytes("good")
	body := []byte(fmt.Sprintf(
		`{"version":1,"anchors":["not-hex","deadbeef","%s","",""]}`,
		hex.EncodeToString(good[:])))

	added, err := b.FallbackToHTTPS(context.Background(), "x", fakeHTTPSClient{body: body})
	if err != nil {
		t.Fatalf("FallbackToHTTPS: %v", err)
	}
	if added != 1 {
		t.Errorf("added = %d, want 1 (only the valid one)", added)
	}
}

func TestFallbackToHTTPSRejectsEmptyURL(t *testing.T) {
	lookup := newTestLookup()
	b, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)

	if _, err := b.FallbackToHTTPS(context.Background(), "", nil); err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestFallbackToHTTPSPropagatesGetError(t *testing.T) {
	lookup := newTestLookup()
	b, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)

	_, err := b.FallbackToHTTPS(context.Background(), "x",
		fakeHTTPSClient{err: errors.New("transport down")})
	if err == nil {
		t.Fatal("expected error from failing client")
	}
}

func TestFallbackToHTTPSRejectsBadJSON(t *testing.T) {
	lookup := newTestLookup()
	b, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)

	_, err := b.FallbackToHTTPS(context.Background(), "x",
		fakeHTTPSClient{body: []byte("not json")})
	if err == nil {
		t.Fatal("expected decode error")
	}
}

func TestFallbackToHTTPSRejectsOversizedResponse(t *testing.T) {
	lookup := newTestLookup()
	b, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)

	huge := make([]byte, MaxAnchorBootstrapBytes+10)
	for i := range huge {
		huge[i] = 'x'
	}
	_, err := b.FallbackToHTTPS(context.Background(), "x", fakeHTTPSClient{body: huge})
	if err == nil {
		t.Fatal("expected oversize-response error")
	}
}

// After FallbackToHTTPS adds anchors, RunAnchors can pick them up.
// We don't actually wire a PPMI getter here — the test just
// confirms the anchor list was updated.
func TestFallbackThenRunAnchors(t *testing.T) {
	lookup := newTestLookup()
	b, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)

	newPub := pubkeyBytes("anchor-after-fallback")
	body := []byte(fmt.Sprintf(`{"version":1,"anchors":["%s"]}`,
		hex.EncodeToString(newPub[:])))

	if _, err := b.FallbackToHTTPS(context.Background(), "x", fakeHTTPSClient{body: body}); err != nil {
		t.Fatal(err)
	}

	// Without a PPMI getter, RunAnchors errors out but the
	// anchor list is still populated.
	_, errs := b.RunAnchors(context.Background())
	if len(errs) == 0 {
		t.Fatal("expected no-getter error")
	}
	if k := b.AnchorKeys(); len(k) != 1 || k[0] != newPub {
		t.Errorf("anchor list lost after RunAnchors: %v", k)
	}
}
