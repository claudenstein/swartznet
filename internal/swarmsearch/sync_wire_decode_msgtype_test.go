package swarmsearch

import (
	"testing"

	"github.com/anacrolix/torrent/bencode"
)

// TestDecodeSyncBeginWrongMsgType covers DecodeSyncBegin's
// `if m.MsgType != MsgTypeSyncBegin` arm. Bencode a payload
// that decodes successfully into a SyncBegin struct but
// carries the wrong msg_type discriminator.
func TestDecodeSyncBeginWrongMsgType(t *testing.T) {
	t.Parallel()
	bad, err := bencode.Marshal(map[string]interface{}{
		"msg_type":     int64(MsgTypeSyncEnd), // 8 — not Begin (4)
		"tx_id":        int64(1),
		"element_size": int64(32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSyncBegin(bad); err == nil {
		t.Error("DecodeSyncBegin should reject wrong msg_type")
	}
}
