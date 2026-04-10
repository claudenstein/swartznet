// Package engine wraps anacrolix/torrent and is the single point of contact
// between SwartzNet's higher layers (search, indexer, CLI, REST API) and the
// underlying BitTorrent protocol.
//
// The goals of this wrapper, in priority order:
//
//  1. Minimise the surface of anacrolix/torrent that the rest of SwartzNet
//     touches, so that we can swap or patch the upstream library without
//     rippling changes through the whole codebase.
//
//  2. Provide the extension hooks documented in docs/05-integration-design.md
//     §6: a piece-complete subscription (to feed the local indexer in M2) and
//     a PeerConnAdded hook (to register the sn_search BEP-10 extension in M3).
//
//  3. Enforce configuration invariants so that higher layers can trust the
//     Engine was brought up in a sensible state.
//
// The Engine is safe for concurrent use. It owns a single *torrent.Client for
// its lifetime; Close releases all Client resources and is idempotent.
package engine
