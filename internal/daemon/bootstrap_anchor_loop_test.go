package daemon

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestRunAnchorLoopPicksUpFallbackAnchors — the daemon's channel-A
// goroutine must fetch anchors added AFTER construction via
// FallbackToHTTPS. The old daemon.New wiring snapshotted the anchor
// set once (and didn't even spawn the goroutine when the set was
// empty, the dev default), so HTTPS-supplied anchors were never
// fetched without a manual RunAnchors call.
func TestRunAnchorLoopPicksUpFallbackAnchors(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	late := pubkeyBytes("late-https-anchor")

	ppmi := &mockPPMIGetter{
		items: map[[32]byte]dhtindex.PPMIValue{
			late: {IH: bytes.Repeat([]byte{0x01}, 20)},
		},
	}

	// Zero anchors at construction — the dev-default cold start.
	b, err := NewBootstrap(lookup, ppmi, nil, nil, DefaultBootstrapOptions(), nil)
	if err != nil {
		t.Fatalf("NewBootstrap: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		b.runAnchorLoop(ctx)
		close(done)
	}()

	body := []byte(fmt.Sprintf(`{"version":1,"anchors":["%s"]}`,
		hex.EncodeToString(late[:])))
	added, err := b.FallbackToHTTPS(ctx, "https://example/v1/anchors",
		fakeHTTPSClient{body: body})
	if err != nil {
		t.Fatalf("FallbackToHTTPS: %v", err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}

	// The loop must fetch + admit the late anchor on its own —
	// no further RunAnchors call from us.
	deadline := time.Now().Add(5 * time.Second)
	for !b.IsAdmitted(late) {
		if time.Now().After(deadline) {
			t.Fatal("late HTTPS anchor never admitted by runAnchorLoop")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// And the loop exits cleanly on cancellation.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runAnchorLoop did not exit on ctx cancellation")
	}
}

// TestRunAnchorLoopRunsConfiguredAnchorsAtStart — anchors present
// at construction are still fetched exactly as before (the loop's
// initial pass), preserving the pre-loop daemon.New behavior.
func TestRunAnchorLoopRunsConfiguredAnchorsAtStart(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	a := pubkeyBytes("startup-anchor")

	ppmi := &mockPPMIGetter{
		items: map[[32]byte]dhtindex.PPMIValue{
			a: {IH: bytes.Repeat([]byte{0x02}, 20)},
		},
	}
	opts := DefaultBootstrapOptions()
	opts.AnchorHexes = []string{hex.EncodeToString(a[:])}
	b, err := NewBootstrap(lookup, ppmi, nil, nil, opts, nil)
	if err != nil {
		t.Fatalf("NewBootstrap: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		b.runAnchorLoop(ctx)
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for !b.IsAdmitted(a) {
		if time.Now().After(deadline) {
			t.Fatal("configured anchor never admitted by runAnchorLoop initial pass")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}
