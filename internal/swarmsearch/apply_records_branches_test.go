package swarmsearch

import "testing"

// TestApplyRecordsRejectsTxIDMismatch — frames whose txid
// doesn't match the session's must be rejected. Defends
// against bogus or replayed wire frames.
func TestApplyRecordsRejectsTxIDMismatch(t *testing.T) {
	t.Parallel()
	s := NewSyncSession(42, RoleInitiator, nil)
	if _, err := s.ApplyRecords(SyncRecords{TxID: 99}); err == nil {
		t.Error("ApplyRecords with wrong txid should fail")
	}
}

// TestApplyRecordsRejectsBadFieldLengths — wire-level guard
// against records whose pk/ih/sig fields don't match the
// declared invariants. The dhtindex layer does the real
// signature verification; this is the cheap pre-check.
func TestApplyRecordsRejectsBadFieldLengths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		rec  SyncRecord
	}{
		{"short pk", SyncRecord{Pk: make([]byte, 16), Ih: make([]byte, 20), Sig: make([]byte, 64)}},
		{"short ih", SyncRecord{Pk: make([]byte, 32), Ih: make([]byte, 10), Sig: make([]byte, 64)}},
		{"short sig", SyncRecord{Pk: make([]byte, 32), Ih: make([]byte, 20), Sig: make([]byte, 32)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSyncSession(1, RoleInitiator, nil)
			if _, err := s.ApplyRecords(SyncRecords{TxID: 1, Records: []SyncRecord{tc.rec}}); err == nil {
				t.Errorf("ApplyRecords should reject %s", tc.name)
			}
		})
	}
}
