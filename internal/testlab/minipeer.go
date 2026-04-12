package testlab

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

// MiniPeer is a lightweight sn_search peer implementation for
// adversarial testing. Modeled on Bitcoin Core's MiniNode
// (test/functional/test_framework/mininode.py): a pure-code
// reimplementation of enough of the wire protocol to handshake
// and exchange extension messages with a real SwartzNet engine,
// WITHOUT the full anacrolix stack.
//
// MiniPeer speaks the BitTorrent peer-wire protocol (BEP-3)
// just enough to:
//  1. Complete the BT handshake (19-byte pstrlen + protocol +
//     8 reserved bytes + 20-byte infohash + 20-byte peerid)
//  2. Send and receive BEP-10 extended messages (msg_type 20)
//  3. Send deliberately malformed frames for adversarial tests
//
// MiniPeer does NOT support piece transfer, DHT, PEX, or any
// other BitTorrent functionality. It exists solely to test
// the real SwartzNet engine's response to hostile wire input.
//
// Usage:
//
//	mp := testlab.DialMiniPeer(t, engineListenAddr, sharedInfoHash)
//	defer mp.Close()
//
//	// Send a valid sn_search query:
//	mp.SendExtended(sn_search_id, queryPayload)
//	resp := mp.RecvExtended()
//
//	// Send garbage to trigger misbehavior scoring:
//	mp.SendRaw([]byte("not valid bencode"))
type MiniPeer struct {
	conn     net.Conn
	peerID   [20]byte
	infoHash [20]byte
	// remoteExtIDs maps extension name → the ID the remote
	// chose in its LTEP handshake.
	remoteExtIDs map[string]int
	// localExtID is what WE tell the remote our sn_search ID is.
	localExtID int
}

// btProtocol is the BitTorrent protocol string (BEP 3).
var btProtocol = []byte("BitTorrent protocol")

// DialMiniPeer connects to a SwartzNet engine at addr, performs
// the BT + LTEP handshake, and returns a ready MiniPeer. The
// sharedIH must be an infohash the engine is tracking (e.g. the
// testlab cluster's shared dummy infohash) so the engine
// accepts the connection.
func DialMiniPeer(addr string, sharedIH [20]byte) (*MiniPeer, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("minipeer: dial %s: %w", addr, err)
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	mp := &MiniPeer{
		conn:         conn,
		infoHash:     sharedIH,
		localExtID:   42, // arbitrary — we choose our own ID
		remoteExtIDs: make(map[string]int),
	}
	// Generate a fake peer ID.
	copy(mp.peerID[:], []byte("-MN0001-minitest1234"))

	if err := mp.btHandshake(); err != nil {
		conn.Close()
		return nil, err
	}
	if err := mp.ltepHandshake(); err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{}) // clear deadline for subsequent ops
	return mp, nil
}

// Close closes the underlying TCP connection.
func (mp *MiniPeer) Close() error {
	return mp.conn.Close()
}

// RemoteSnSearchID returns the extension ID the remote peer
// assigned to sn_search, or 0 if it didn't advertise it.
func (mp *MiniPeer) RemoteSnSearchID() int {
	return mp.remoteExtIDs["sn_search"]
}

// SendExtended sends a BEP-10 extended message with the given
// extension ID and payload. The payload is the raw bencoded
// body (e.g. an sn_search Query or PeerAnnounce).
func (mp *MiniPeer) SendExtended(extID int, payload []byte) error {
	// Extended message: length-prefix(4) + msg_type(1, =20) +
	// ext_id(1) + payload.
	msgLen := 2 + len(payload) // msg_type + ext_id + payload
	buf := make([]byte, 4+msgLen)
	binary.BigEndian.PutUint32(buf[0:4], uint32(msgLen))
	buf[4] = 20 // BEP-10 extended
	buf[5] = byte(extID)
	copy(buf[6:], payload)
	_, err := mp.conn.Write(buf)
	return err
}

// SendRaw sends arbitrary raw bytes on the wire. Used for
// adversarial tests — deliberately malformed frames that
// should trigger the engine's misbehavior scoring.
func (mp *MiniPeer) SendRaw(data []byte) error {
	_, err := mp.conn.Write(data)
	return err
}

// RecvMessage reads one BT peer-wire message (length-prefixed).
// Returns the raw message bytes including the msg_type byte.
// Returns nil for keepalive (length=0) messages.
func (mp *MiniPeer) RecvMessage(timeout time.Duration) ([]byte, error) {
	mp.conn.SetReadDeadline(time.Now().Add(timeout))
	var lenBuf [4]byte
	if _, err := io.ReadFull(mp.conn, lenBuf[:]); err != nil {
		return nil, err
	}
	msgLen := binary.BigEndian.Uint32(lenBuf[:])
	if msgLen == 0 {
		return nil, nil // keepalive
	}
	if msgLen > 1<<20 {
		return nil, fmt.Errorf("minipeer: message too large (%d bytes)", msgLen)
	}
	msg := make([]byte, msgLen)
	if _, err := io.ReadFull(mp.conn, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

// RecvExtendedPayload reads messages until it finds a BEP-10
// extended message (type 20) and returns the extension ID +
// payload. Non-extended messages are silently dropped.
func (mp *MiniPeer) RecvExtendedPayload(timeout time.Duration) (int, []byte, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		msg, err := mp.RecvMessage(time.Until(deadline))
		if err != nil {
			return 0, nil, err
		}
		if msg == nil || len(msg) < 2 {
			continue
		}
		if msg[0] != 20 { // not extended
			continue
		}
		return int(msg[1]), msg[2:], nil
	}
	return 0, nil, fmt.Errorf("minipeer: no extended message within timeout")
}

// btHandshake sends and receives the BT peer-wire handshake.
func (mp *MiniPeer) btHandshake() error {
	// Send our handshake.
	var hs [68]byte
	hs[0] = 19 // pstrlen
	copy(hs[1:20], btProtocol)
	// Reserved bytes: set bit 20 (BEP-10 extension support).
	hs[25] = 0x10 // byte 5 of reserved, bit 20
	copy(hs[28:48], mp.infoHash[:])
	copy(hs[48:68], mp.peerID[:])
	if _, err := mp.conn.Write(hs[:]); err != nil {
		return fmt.Errorf("minipeer: send BT handshake: %w", err)
	}

	// Read the remote's handshake.
	var remote [68]byte
	if _, err := io.ReadFull(mp.conn, remote[:]); err != nil {
		return fmt.Errorf("minipeer: recv BT handshake: %w", err)
	}
	if remote[0] != 19 {
		return fmt.Errorf("minipeer: bad pstrlen %d", remote[0])
	}
	return nil
}

// ltepHandshake sends our LTEP extended handshake (ext ID 0)
// advertising sn_search, then reads the remote's and extracts
// their sn_search extension ID.
func (mp *MiniPeer) ltepHandshake() error {
	// Our handshake dict: advertise sn_search at our chosen ID.
	hsDict := map[string]any{
		"m": map[string]any{
			"sn_search": mp.localExtID,
		},
	}
	hsPayload, err := bencode.Marshal(hsDict)
	if err != nil {
		return fmt.Errorf("minipeer: encode LTEP hs: %w", err)
	}
	// Extended message with ID 0 = the LTEP handshake.
	if err := mp.SendExtended(0, hsPayload); err != nil {
		return fmt.Errorf("minipeer: send LTEP hs: %w", err)
	}

	// Read messages until we get an extended handshake back
	// (ext ID 0). The engine may send other messages first
	// (bitfield, have, etc).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msg, err := mp.RecvMessage(time.Until(deadline))
		if err != nil {
			return fmt.Errorf("minipeer: recv LTEP hs: %w", err)
		}
		if msg == nil || len(msg) < 2 || msg[0] != 20 {
			continue // not extended
		}
		if msg[1] != 0 {
			continue // not the handshake (ext ID 0)
		}
		// Parse the remote's handshake dict.
		var remote struct {
			M map[string]int `bencode:"m"`
		}
		if err := bencode.Unmarshal(msg[2:], &remote); err != nil {
			return fmt.Errorf("minipeer: decode remote LTEP hs: %w", err)
		}
		for name, id := range remote.M {
			mp.remoteExtIDs[name] = id
		}
		return nil
	}
	return fmt.Errorf("minipeer: no LTEP handshake received within 5s")
}
