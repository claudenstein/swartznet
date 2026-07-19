package ltepwire

import (
	"bytes"

	"github.com/anacrolix/torrent/bencode"
)

// decodeBounded unmarshals an UNTRUSTED sn_search frame with the parsed string
// length bounded to the payload size. anacrolix bencode.Unmarshal leaves
// MaxStrLen at the ~128 MiB default and allocates make([]byte, declaredLen)
// BEFORE reading the bytes, so a ~32-byte frame that declares a huge inner
// string (e.g. a txid or `q` length near 2^27) forces a ~128 MiB transient
// allocation. Every ltepwire decoder runs on untrusted inbound frames —
// PeekMsgType fires on EVERY inbound sn_search frame, dispatched on up to 256
// concurrent workers — so an unbounded decode is a remote memory-exhaustion DoS
// (millions-fold amplification: ~32 bytes → ~128 MiB per frame).
//
// A bencoded string can never legitimately be longer than the payload that
// contains it, so bounding MaxStrLen to len(payload) rejects exactly the
// impossible (hostile) declarations and never a valid frame. This is the
// ltepwire counterpart of contracts/dhtschema.decodeBounded, which closed the
// same allocation-amplification class on the BEP-44 side.
func decodeBounded(payload []byte, v any) error {
	d := bencode.NewDecoder(bytes.NewReader(payload))
	// MaxStrLen == 0 means "use the 128 MiB default", so only tighten it when the
	// payload is non-empty; an empty payload declares no string and cannot
	// amplify (Decode returns EOF before allocating anything).
	if n := int64(len(payload)); n > 0 {
		d.MaxStrLen = n
	}
	return d.Decode(v)
}
