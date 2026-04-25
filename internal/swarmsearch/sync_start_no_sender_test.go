package swarmsearch

import (
	"errors"
	"testing"
)

// TestSendSyncNeedNoSender — when no Sender is attached,
// SendSyncNeed must return ErrNoSender rather than panicking
// or silently no-op. Construct a session directly so we can
// drive SendSyncNeed past the sender-nil guard.
func TestSendSyncNeedNoSender(t *testing.T) {
	t.Parallel()
	p := New(nil)
	// Not calling SetSender — sender is nil.
	sess := NewSyncSession(1, RoleInitiator, nil)
	if _, err := sess.Begin(SyncFilter{}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	p.registerSyncSession("peer-no-sender", sess)

	var id [32]byte
	if err := p.SendSyncNeed("peer-no-sender", sess, [][32]byte{id}); !errors.Is(err, ErrNoSender) {
		t.Errorf("expected ErrNoSender, got %v", err)
	}
}

// TestCloseSyncNoSender — same pattern: sender absent, should
// surface ErrNoSender. Also confirms the deferred
// releaseSyncSession still runs (session must be unregistered
// even when sending failed).
func TestCloseSyncNoSender(t *testing.T) {
	t.Parallel()
	p := New(nil)
	sess := NewSyncSession(7, RoleInitiator, nil)
	if _, err := sess.Begin(SyncFilter{}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	p.registerSyncSession("peer-close-no-sender", sess)
	txid := sess.TxID()

	if err := p.CloseSync("peer-close-no-sender", sess, SyncStatusConverged); !errors.Is(err, ErrNoSender) {
		t.Errorf("expected ErrNoSender, got %v", err)
	}
	// Session is released even on send failure.
	if got := p.lookupSyncSession("peer-close-no-sender", txid); got != nil {
		t.Error("CloseSync should release the session even when send fails")
	}
}

// TestSendSyncNeedNeedFrameError — calling SendSyncNeed on a
// session whose phase doesn't allow NeedFrame returns the
// downstream error. Drive by constructing an idle session
// directly (NeedFrame requires PhaseSymbolsFlowing or
// PhaseBegun; PhaseIdle errors).
func TestSendSyncNeedNeedFrameError(t *testing.T) {
	t.Parallel()
	p := New(nil)
	snd := &recordingSender{}
	p.SetSender(snd)
	sess := NewSyncSession(1, RoleInitiator, nil) // phase = PhaseIdle
	p.registerSyncSession("peer-bad-phase", sess)

	var id [32]byte
	err := p.SendSyncNeed("peer-bad-phase", sess, [][32]byte{id})
	if err == nil {
		t.Error("SendSyncNeed should error when session phase is PhaseIdle")
	}
}
