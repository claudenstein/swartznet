package swarmsearch

import (
	"bytes"
	"sync"
	"testing"
	"time"

	pp "github.com/anacrolix/torrent/peer_protocol"
)

// recordingHandshakeSender captures the announce payload that
// OnRemoteHandshake's goroutine sends back. Used to assert
// that BitLayerDPublisher and the publisher pubkey are set
// when caps.Publisher > 0 + SetPublisherPubkey was called.
type recordingHandshakeSender struct {
	mu      sync.Mutex
	payload []byte
}

func (r *recordingHandshakeSender) Send(_ string, payload []byte) error {
	r.mu.Lock()
	r.payload = bytes.Clone(payload)
	r.mu.Unlock()
	return nil
}

func (r *recordingHandshakeSender) snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return bytes.Clone(r.payload)
}

// TestOnRemoteHandshakePublisherCarriesPubkey covers the two
// arms of the announce-build path:
//   - `if pubOn { services = services.With(BitLayerDPublisher) }`
//     fires when caps.Publisher > 0.
//   - `if pubOn && len(pubkey) == 32 { pa.Pubkey = ... }`
//     fires when SetPublisherPubkey set a valid 32-byte key.
func TestOnRemoteHandshakePublisherCarriesPubkey(t *testing.T) {
	p := New(nil)
	p.SetCapabilities(Capabilities{ShareLocal: 1, Publisher: 1})
	pubkey := bytes.Repeat([]byte{0xab}, 32)
	p.SetPublisherPubkey(pubkey)
	rec := &recordingHandshakeSender{}
	p.SetSender(rec)

	addr := "1.2.3.4:6881"
	p.NotePeerAdded(addr)
	p.OnRemoteHandshake(addr, &pp.ExtendedHandshakeMessage{
		M: map[pp.ExtensionName]pp.ExtensionNumber{
			ExtensionName: 11,
		},
	})

	// The announce is sent on a goroutine; poll briefly.
	deadline := time.Now().Add(time.Second)
	var raw []byte
	for time.Now().Before(deadline) {
		raw = rec.snapshot()
		if raw != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if raw == nil {
		t.Fatal("no announce sent")
	}
	pa, err := DecodePeerAnnounce(raw)
	if err != nil {
		t.Fatalf("DecodePeerAnnounce: %v", err)
	}
	if ServiceBits(pa.Services)&BitLayerDPublisher == 0 {
		t.Errorf("announce services missing BitLayerDPublisher: %x", pa.Services)
	}
	if !bytes.Equal(pa.Pubkey, pubkey) {
		t.Errorf("announce pubkey = %x, want %x", pa.Pubkey, pubkey)
	}
}
