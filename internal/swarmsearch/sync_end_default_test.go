package swarmsearch

import "testing"

// TestEncodeSyncEndDefaultStatus — when the caller hands in
// an empty Status, EncodeSyncEnd promotes it to
// SyncStatusConverged before marshaling. This is the
// "operator forgot to set status" defence: a frame on the
// wire always carries a non-empty status string.
func TestEncodeSyncEndDefaultStatus(t *testing.T) {
	t.Parallel()
	raw, err := EncodeSyncEnd(SyncEnd{TxID: 42}) // Status intentionally empty
	if err != nil {
		t.Fatalf("EncodeSyncEnd: %v", err)
	}
	got, err := DecodeSyncEnd(raw)
	if err != nil {
		t.Fatalf("DecodeSyncEnd: %v", err)
	}
	if got.Status != SyncStatusConverged {
		t.Errorf("Status = %q, want %q (default)", got.Status, SyncStatusConverged)
	}
}
