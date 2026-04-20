package swarmsearch

import (
	"errors"
	"io"
	"log/slog"
	"testing"
)

// TestChargeMisbehaviorTriggersBanLog covers the previously-
// uncovered "score crossed ban threshold" warn branch in
// chargeMisbehavior. Stacking enough points in one push
// crosses BanThreshold (100), which causes Add() to return true
// and the function to log the ban.
func TestChargeMisbehaviorTriggersBanLog(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Single charge of 200 points must cross the threshold.
	p.chargeMisbehavior("1.2.3.4:6881", 200, "test")
	if !p.IsBanned("1.2.3.4:6881") {
		t.Error("IsBanned should be true after a 200-point charge")
	}
}

// TestSendRejectNilReplyShortCircuits covers the `reply == nil`
// guard. Passing a nil ReplyFunc should not panic and should
// return immediately without trying to encode.
func TestSendRejectNilReplyShortCircuits(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Must not panic.
	p.sendReject(nil, "1.2.3.4:6881", 7, 1, "test")
}

// TestSendRejectFireSendError exercises the success path's
// reply-error log branch: the reply closure runs but returns
// an error, which sendReject swallows after a debug log.
func TestSendRejectFireSendError(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))

	called := false
	reply := func(_ []byte) error {
		called = true
		return errors.New("simulated send failure")
	}
	p.sendReject(reply, "1.2.3.4:6881", 7, 1, "test")
	if !called {
		t.Error("sendReject did not invoke the reply closure")
	}
}

// TestSendRejectFireSuccess exercises the happy path: reply
// runs, returns nil, no log. The closure must observe a
// non-empty payload (the encoded reject body).
func TestSendRejectFireSuccess(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))

	var got []byte
	reply := func(body []byte) error {
		got = body
		return nil
	}
	p.sendReject(reply, "1.2.3.4:6881", 7, 1, "test")
	if len(got) == 0 {
		t.Error("sendReject did not pass an encoded payload to the reply closure")
	}
}
