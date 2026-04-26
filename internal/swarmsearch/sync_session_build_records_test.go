package swarmsearch

import (
	"testing"
)

// TestBuildRecordsFrameRejectsOversize covers the
// `if len(recs) > MaxRecordsPerMessage` cap in BuildRecordsFrame.
// The function is the responder's outbound chunker for
// sync_records replies; an over-cap call must error rather than
// emit a frame the peer would reject.
func TestBuildRecordsFrameRejectsOversize(t *testing.T) {
	t.Parallel()
	sess := NewSyncSession(7, RoleResponder, nil)
	recs := make([]LocalRecord, MaxRecordsPerMessage+1)
	if _, err := sess.BuildRecordsFrame(recs, nil); err == nil {
		t.Errorf("BuildRecordsFrame should reject > MaxRecordsPerMessage records")
	}
}
