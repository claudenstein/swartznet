// Command dht-smoke is a one-shot live mainline DHT smoke test for
// the SwartzNet publisher path.
//
// It is NOT part of the normal `go test ./...` suite because it
// joins the real BitTorrent mainline DHT, exposes the running
// machine's IP to public DHT nodes, and takes ~30-60 seconds. The
// regular unit tests under internal/dhtindex run against the
// MemoryPutterGetter test double instead.
//
// Use this when you want to validate that the BEP-44 put / get
// path works end-to-end against real network conditions: e.g.
// before tagging a release, after a dependency bump, or when
// debugging a publisher that misbehaves in production.
//
// Usage:
//
//	go run ./cmd/dht-smoke
//
// What it does:
//
//  1. Constructs a fresh anacrolix/dht/v2 server with default
//     bootstrap nodes.
//  2. Waits up to 30s for the routing table to populate to at
//     least 8 good nodes.
//  3. Generates an *ephemeral* ed25519 keypair (the user's real
//     publisher identity is never touched).
//  4. Builds an AnacrolixPutter and Puts a synthetic
//     KeywordValue under a unique time-stamped salt so it never
//     collides with previous test runs or anyone else's
//     publishes.
//  5. Builds an AnacrolixGetter and reads the same target back,
//     verifying the round-tripped value matches what was put.
//  6. Exits 0 on success, 1 on any failure with a log message.
//
// Last verified passing: 2026-04-10 — 25 good DHT nodes after
// bootstrap, Put completed in ~10s with one of 8 closest nodes
// timing out, Get returned the signed payload back in ~7s.
package main
