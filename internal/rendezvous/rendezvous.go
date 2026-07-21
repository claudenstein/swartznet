// Package rendezvous derives the well-known mainline-DHT infohashes SwartzNet
// nodes use to find each other.
//
// The discovery problem: a node only learns whether a peer speaks sn_search
// AFTER a BitTorrent+LTEP handshake, and that handshake only happens inside a
// torrent swarm the two nodes both joined. Two SwartzNet users who share no
// content torrent therefore never meet. A rendezvous infohash is a swarm they
// join for the sole purpose of meeting: every participant announce_peers it on
// the ordinary mainline DHT, so get_peers on it returns other SwartzNet nodes.
// To a vanilla DHT node it is indistinguishable from any other torrent's
// infohash — no new DHT verb, port, or reserved bit. Mainline-compat is
// preserved; see docs/12-rendezvous-draft.md.
//
// Three flavours, all deterministic so independent nodes derive byte-identical
// targets:
//
//   - Global   — one well-known swarm every node can join (broad discovery).
//   - Topic    — one swarm per keyword/topic, so nodes interested in the same
//     content meet (relevant discovery, and no single global list).
//   - Community — HMAC-SHA1 of a shared secret, so a closed group finds each
//     other without their membership being publicly enumerable.
package rendezvous

import (
	"crypto/hmac"
	"crypto/sha1"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
)

// saltVersion namespaces every rendezvous infohash. Bumping the trailing
// version migrates the whole network to a fresh set of swarms in lockstep; it
// is part of the wire contract, so treat it as frozen and append-only.
const saltVersion = "swartznet:rv:1"

// MaxTopicBytes bounds a normalized topic. A topic is only ever hashed, so the
// cap exists to keep config sane, not for wire safety.
const MaxTopicBytes = 128

// GlobalInfoHash is the single well-known swarm every node may join. It is a
// public directory of SwartzNet membership — see the privacy note in
// docs/12-rendezvous-draft.md before enabling it in a sensitive deployment.
func GlobalInfoHash() metainfo.Hash {
	return metainfo.HashBytes([]byte(saltVersion + ":global"))
}

// TopicInfoHash is the rendezvous swarm for a topic. The topic is normalized
// first so "Ubuntu Linux", "ubuntu  linux", and " ubuntu linux " all map to the
// same swarm. An empty (post-normalization) topic is not a valid rendezvous
// point; callers should skip it — ok reports whether the topic was usable.
func TopicInfoHash(topic string) (h metainfo.Hash, ok bool) {
	norm := NormalizeTopic(topic)
	if norm == "" {
		return metainfo.Hash{}, false
	}
	return metainfo.HashBytes([]byte(saltVersion + ":t:" + norm)), true
}

// CommunityInfoHash derives a private rendezvous swarm from a shared secret via
// HMAC-SHA1(secret, saltVersion). Only holders of the secret can compute the
// target, so the group meets on the mainline DHT without its membership being
// enumerable by outsiders. An empty secret is not usable — ok reports validity.
func CommunityInfoHash(secret string) (h metainfo.Hash, ok bool) {
	if secret == "" {
		return metainfo.Hash{}, false
	}
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte(saltVersion))
	sum := mac.Sum(nil) // exactly 20 bytes, the width of metainfo.Hash
	copy(h[:], sum)
	return h, true
}

// NormalizeTopic lowercases and collapses all whitespace runs to a single space
// (and trims the ends), so trivially-different spellings of the same topic land
// in the same swarm. It is deliberately conservative — no unicode folding or
// stemming — so the mapping stays obvious and stable across implementations.
// The result is truncated to MaxTopicBytes on a rune boundary.
func NormalizeTopic(topic string) string {
	norm := strings.Join(strings.Fields(strings.ToLower(topic)), " ")
	if len(norm) <= MaxTopicBytes {
		return norm
	}
	// Truncate on a rune boundary so we never split a multibyte character.
	cut := MaxTopicBytes
	for cut > 0 && !utf8RuneStart(norm[cut]) {
		cut--
	}
	return strings.TrimRight(norm[:cut], " ")
}

// utf8RuneStart reports whether b is the first byte of a UTF-8 rune (i.e. not a
// 0b10xxxxxx continuation byte).
func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }
