package swarmsearch

import (
	"sync"
	"time"
)

// Misbehavior point values assigned to specific protocol
// violations, modelled directly on Bitcoin Core's
// src/net_processing.cpp::Misbehaving() call sites. Point
// values are intentionally coarse and additive: a peer that
// commits multiple small violations accumulates score the same
// way it would if it committed one large one.
//
// When a peer's score crosses BanThreshold the limiter marks
// the peer as banned. Banned peers are rejected at handshake
// time (OnRemoteHandshake) so they cannot re-establish a
// connection on the same address.
//
// Critical invariant from Bitcoin Core: **ban state is strictly
// local and NEVER gossiped**. A gossiped ban list would be an
// eclipse-attack vector — whoever can cause other peers to ban
// a victim could eclipse the victim cheaply. Each node decides
// its own bans independently.
const (
	// ScoreBadBencode is charged for a message whose bencode
	// decoder fails — indicates either a broken peer or an
	// attacker sending malformed frames. Severe.
	ScoreBadBencode = 20

	// ScoreRateLimited is charged every time a peer is
	// throttled by the rate limiter. Small — one rate-limit
	// hit is normal under load, but persistent violators add
	// up.
	ScoreRateLimited = 5

	// ScoreMalformedResult is charged for a Result message
	// whose wire format is syntactically valid but
	// semantically garbage (e.g. infohash of wrong length,
	// empty hits array claiming Total > 0).
	ScoreMalformedResult = 10

	// ScoreQueryTooBroad is charged for queries that are
	// abusively short or wildcard-only. Small — an occasional
	// typo is fine, persistent abuse adds up.
	ScoreQueryTooBroad = 5

	// ScoreStaleTxID is charged for inbound Result or Reject
	// messages whose txid doesn't match any pending outbound
	// query. Either a protocol bug or a replay attempt.
	ScoreStaleTxID = 10

	// ScoreUnexpectedMessage is charged for messages of an
	// unknown MsgType. Suggests protocol confusion.
	ScoreUnexpectedMessage = 10

	// ScoreBadRecordSig is charged for an Aggregate record
	// delivered via sync_records (SPEC §2.7) whose per-record
	// ed25519 signature fails to verify against its embedded
	// pubkey. Strong evidence of a malicious relay injecting
	// records; counted as Severe.
	ScoreBadRecordSig = 20

	// ScoreInsufficientPoW is charged for a record whose hashcash
	// nonce doesn't meet the publisher's declared MinPoWBits.
	// Indicates the peer is either running an older build that
	// doesn't mint PoW or is deliberately shipping unbound
	// records. Medium severity — one mistake isn't ban-worthy,
	// persistent drip is.
	ScoreInsufficientPoW = 10

	// BanThreshold is the score at which a peer is banned.
	// Matches Bitcoin Core's DISCOURAGEMENT_THRESHOLD of 100.
	// A single Severe violation (ScoreBadBencode = 20) is not
	// enough; it takes a few before a ban kicks in.
	BanThreshold = 100

	// BanDuration is how long a peer stays banned after
	// crossing the threshold. 24 hours matches Bitcoin Core's
	// default. A persistent attacker starts fresh after each
	// window, but re-ban is quick if they're still misbehaving.
	BanDuration = 24 * time.Hour
)

// peerScore is the per-peer misbehavior state held in the
// Protocol. One entry per peer address; evicted when the peer
// closes (OnPeerClosed).
type peerScore struct {
	// score is the current sum of misbehavior points.
	score int
	// bannedUntil is non-zero if the peer has crossed the
	// threshold. The banlist is a soft state — entries are
	// in memory and NOT persisted across restart. A restarted
	// daemon re-evaluates every peer from scratch.
	bannedUntil time.Time
}

// misbehaviorTracker is the Protocol-side bookkeeping for
// per-peer misbehavior scores. Safe for concurrent use.
type misbehaviorTracker struct {
	mu    sync.Mutex
	peers map[string]*peerScore
}

func newMisbehaviorTracker() *misbehaviorTracker {
	return &misbehaviorTracker{
		peers: make(map[string]*peerScore),
	}
}

// Add charges the given number of points to the peer at addr
// and returns true if this push crossed the ban threshold
// (triggering a disconnect).
func (m *misbehaviorTracker) Add(addr string, points int) (banned bool) {
	if points <= 0 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.peers[addr]
	if !ok {
		p = &peerScore{}
		m.peers[addr] = p
	}
	p.score += points
	if p.score >= BanThreshold && p.bannedUntil.IsZero() {
		p.bannedUntil = time.Now().Add(BanDuration)
		return true
	}
	return false
}

// IsBanned reports whether the peer at addr is currently in
// the ban window. Expired bans auto-clear on the next call.
func (m *misbehaviorTracker) IsBanned(addr string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.peers[addr]
	if !ok {
		return false
	}
	if p.bannedUntil.IsZero() {
		return false
	}
	if time.Now().After(p.bannedUntil) {
		// Ban expired — reset the entry so the peer gets a
		// fresh budget if it reconnects and behaves.
		p.score = 0
		p.bannedUntil = time.Time{}
		return false
	}
	return true
}

// Score returns the current misbehavior score for addr, or 0
// if the peer has no record. Primarily useful for tests and
// /status output.
func (m *misbehaviorTracker) Score(addr string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.peers[addr]
	if !ok {
		return 0
	}
	return p.score
}

// Forget drops the per-peer state for addr. Called from
// OnPeerClosed so long-running daemons don't leak entries for
// peers that disconnect voluntarily. Bans are kept — a
// reconnecting peer should still hit the ban on its next
// handshake — but the memory for clean peers is released.
func (m *misbehaviorTracker) Forget(addr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.peers[addr]
	if !ok {
		return
	}
	if !p.bannedUntil.IsZero() {
		// Keep ban entries so a reconnect hits the block.
		return
	}
	delete(m.peers, addr)
}

// peerCount returns the number of tracked peer entries.
// Used by tests to assert eviction.
func (m *misbehaviorTracker) peerCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.peers)
}
