package wirecompat

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

// MiniPeer is a raw-socket BitTorrent peer double — enough of BEP-3 + BEP-10 to
// handshake with a real SwartzNet engine and observe (or send) extended
// messages, WITHOUT the anacrolix stack. It exists to prove the mainline-compat
// invariant: a peer whose LTEP `m` dict omits sn_search receives ZERO sn_search
// frames. Modeled on Bitcoin Core's MiniNode.
type MiniPeer struct {
	conn         net.Conn
	peerID       [20]byte
	infoHash     [20]byte
	remoteExtIDs map[string]int
	localExtID   int
}

var btProtocol = []byte("BitTorrent protocol")

// DialMiniPeer handshakes advertising sn_search (a capable peer).
func DialMiniPeer(addr string, sharedIH [20]byte) (*MiniPeer, error) {
	return dialMiniPeer(addr, sharedIH, true)
}

// DialVanillaMiniPeer handshakes WITHOUT advertising sn_search — indistinguish-
// able from any mainline client that never heard of SwartzNet.
func DialVanillaMiniPeer(addr string, sharedIH [20]byte) (*MiniPeer, error) {
	return dialMiniPeer(addr, sharedIH, false)
}

func dialMiniPeer(addr string, sharedIH [20]byte, advertiseSNSearch bool) (*MiniPeer, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("minipeer: dial %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	mp := &MiniPeer{conn: conn, infoHash: sharedIH, localExtID: 42, remoteExtIDs: make(map[string]int)}
	copy(mp.peerID[:], []byte("-MN0001-minitest1234"))
	if err := mp.btHandshake(); err != nil {
		conn.Close()
		return nil, err
	}
	if err := mp.ltepHandshake(advertiseSNSearch); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return mp, nil
}

// Close closes the connection.
func (mp *MiniPeer) Close() error { return mp.conn.Close() }

// RemoteSnSearchID is the ext id the engine assigned to sn_search in its LTEP
// handshake (0 if the engine did not advertise it — which never happens for a
// real engine). Note the engine SENDS sn_search under the id WE advertised
// (localExtID), per BEP-10, not this one.
func (mp *MiniPeer) RemoteSnSearchID() int { return mp.remoteExtIDs["sn_search"] }

// SendExtended writes a BEP-10 extended message (msg_type 20).
func (mp *MiniPeer) SendExtended(extID int, payload []byte) error {
	msgLen := 2 + len(payload)
	buf := make([]byte, 4+msgLen)
	binary.BigEndian.PutUint32(buf[0:4], uint32(msgLen))
	buf[4] = 20
	buf[5] = byte(extID)
	copy(buf[6:], payload)
	_, err := mp.conn.Write(buf)
	return err
}

// recvMessage reads one length-prefixed peer-wire message (nil for keepalive).
func (mp *MiniPeer) recvMessage(timeout time.Duration) ([]byte, error) {
	_ = mp.conn.SetReadDeadline(time.Now().Add(timeout))
	var lenBuf [4]byte
	if _, err := io.ReadFull(mp.conn, lenBuf[:]); err != nil {
		return nil, err
	}
	msgLen := binary.BigEndian.Uint32(lenBuf[:])
	if msgLen == 0 {
		return nil, nil
	}
	if msgLen > 1<<20 {
		return nil, fmt.Errorf("minipeer: message too large (%d)", msgLen)
	}
	msg := make([]byte, msgLen)
	if _, err := io.ReadFull(mp.conn, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

// SawSnSearchWithin drains inbound messages for the window and reports whether
// ANY sn_search extended frame arrived. The engine sends sn_search under the id
// WE advertised for it (localExtID) — so a vanilla peer, which advertised no
// sn_search id, can never receive one. ext-id 0 (handshake) and standard
// peer-wire messages are ignored; a read timeout (the engine going quiet) ends
// the drain and is NOT a frame.
func (mp *MiniPeer) SawSnSearchWithin(window time.Duration) bool {
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		msg, err := mp.recvMessage(time.Until(deadline))
		if err != nil {
			return false // timeout / EOF: no sn_search frame
		}
		if len(msg) >= 2 && msg[0] == 20 && int(msg[1]) == mp.localExtID {
			return true
		}
	}
	return false
}

// RecvSnSearchPayload reads inbound messages until an sn_search extended frame
// (ext-id == our advertised id) arrives, returning its bencoded payload.
// Non-sn_search messages are skipped.
func (mp *MiniPeer) RecvSnSearchPayload(window time.Duration) ([]byte, error) {
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		msg, err := mp.recvMessage(time.Until(deadline))
		if err != nil {
			return nil, err
		}
		if len(msg) >= 2 && msg[0] == 20 && int(msg[1]) == mp.localExtID {
			return msg[2:], nil
		}
	}
	return nil, fmt.Errorf("minipeer: no sn_search frame within %s", window)
}

func (mp *MiniPeer) btHandshake() error {
	var hs [68]byte
	hs[0] = 19
	copy(hs[1:20], btProtocol)
	hs[25] = 0x10 // reserved bit 20 = BEP-10 extension support
	copy(hs[28:48], mp.infoHash[:])
	copy(hs[48:68], mp.peerID[:])
	if _, err := mp.conn.Write(hs[:]); err != nil {
		return fmt.Errorf("minipeer: send BT handshake: %w", err)
	}
	var remote [68]byte
	if _, err := io.ReadFull(mp.conn, remote[:]); err != nil {
		return fmt.Errorf("minipeer: recv BT handshake: %w", err)
	}
	if remote[0] != 19 {
		return fmt.Errorf("minipeer: bad pstrlen %d", remote[0])
	}
	return nil
}

func (mp *MiniPeer) ltepHandshake(advertiseSNSearch bool) error {
	m := map[string]any{}
	if advertiseSNSearch {
		m["sn_search"] = mp.localExtID
	}
	payload, err := bencode.Marshal(map[string]any{"m": m})
	if err != nil {
		return fmt.Errorf("minipeer: encode LTEP hs: %w", err)
	}
	if err := mp.SendExtended(0, payload); err != nil {
		return fmt.Errorf("minipeer: send LTEP hs: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msg, err := mp.recvMessage(time.Until(deadline))
		if err != nil {
			return fmt.Errorf("minipeer: recv LTEP hs: %w", err)
		}
		if len(msg) < 2 || msg[0] != 20 || msg[1] != 0 {
			continue
		}
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
	return fmt.Errorf("minipeer: no LTEP handshake within 5s")
}
