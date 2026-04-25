package engine

import "testing"

// TestSortHandlesByQueueOrderAscending — the sort puts the
// lowest queueOrder first. Used by the queue-promotion path
// to advance torrents in FIFO add-order. Existing
// QueueMoveToFront tests cover the call site indirectly with
// 1-2 handles; this one runs a 5-handle shuffle so the
// nested-loop swap branch fires multiple times.
func TestSortHandlesByQueueOrderAscending(t *testing.T) {
	t.Parallel()
	mk := func(qo int64) *Handle {
		return &Handle{queueOrder: qo}
	}
	hs := []*Handle{mk(50), mk(10), mk(40), mk(20), mk(30)}
	sortHandlesByQueueOrder(hs)
	want := []int64{10, 20, 30, 40, 50}
	for i, h := range hs {
		if h.queueOrder != want[i] {
			t.Errorf("position %d: got queueOrder=%d, want %d", i, h.queueOrder, want[i])
		}
	}
}

// TestSortHandlesByQueueOrderEmpty — no panics on degenerate
// input. Defence-in-depth for future callers.
func TestSortHandlesByQueueOrderEmpty(t *testing.T) {
	t.Parallel()
	sortHandlesByQueueOrder(nil)
	sortHandlesByQueueOrder([]*Handle{})
}

// TestSortHandlesByQueueOrderEqualKeysStable — equal keys
// must not flip order. Bubble-sort with a strict > comparison
// preserves input order on ties; lock that contract.
func TestSortHandlesByQueueOrderEqualKeysStable(t *testing.T) {
	t.Parallel()
	a := &Handle{queueOrder: 1}
	b := &Handle{queueOrder: 1}
	c := &Handle{queueOrder: 1}
	hs := []*Handle{a, b, c}
	sortHandlesByQueueOrder(hs)
	if hs[0] != a || hs[1] != b || hs[2] != c {
		t.Error("equal-key sort is not stable")
	}
}
