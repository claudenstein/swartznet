// Package swarmsearch implements SwartzNet's peer-wire distributed text
// search protocol (Layer S in docs/05-integration-design.md §4.2).
//
// It sits on top of BEP-10 (the BitTorrent Extension Protocol / LTEP) and
// registers itself under the extension name "sn_search". Two peers that
// both advertise sn_search in their LTEP handshake can ask each other
// text queries over the same TCP connection they use for piece transfer,
// and get back infohash/name hits interleaved with content-level snippet
// hits.
//
// Responsibilities are split across milestones:
//
//   - M3a (this file): advertise sn_search to every peer we connect to,
//     observe remote handshakes to detect which peers support
//     sn_search, track per-peer state.
//   - M3b: define the bencoded wire format for query, result, reject,
//     and peer_announce messages, and handle inbound queries against
//     the local index.
//   - M3c: outbound Query() method that fans out to known
//     search-capable peers and aggregates responses.
//   - M3d: CLI `--swarm` flag that merges Layer L and Layer S results.
//
// The protocol is deliberately additive and opt-in: vanilla BitTorrent
// peers see an unknown name in our LTEP `m` dict and ignore it. They
// never see an sn_search message because we only send one if they also
// advertised sn_search back. See docs/05-integration-design.md §5.1.
package swarmsearch
