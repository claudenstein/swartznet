package swarmsearch

import (
	"io"
	"log/slog"
	"testing"
)

// TestHandleQueryDecodeErrorReturns covers handleQuery's
// `q, err := DecodeQuery(payload); if err != nil { return }`
// arm. HandleMessage's routing path would reject malformed
// bencode at peekHeader before reaching handleQuery, so call
// handleQuery directly with garbage bytes — the function must
// return without panicking and without invoking reply.
func TestHandleQueryDecodeErrorReturns(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))

	called := false
	reply := func(_ []byte) error {
		called = true
		return nil
	}
	p.handleQuery("1.2.3.4:6881", []byte("not bencode"), reply)
	if called {
		t.Error("reply should not fire when handleQuery decodes garbage")
	}
}
