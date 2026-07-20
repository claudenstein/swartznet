// Command dht-smoke is a live mainline-DHT smoke test for the SwartzNet
// Layer-D publisher path. It is standalone ops tooling, kept out of the main
// swartznet binary because it talks to the real public DHT.
//
// It bootstraps an anacrolix/dht/v2 server against the public routers, waits for
// the routing table to populate, then — under a fresh EPHEMERAL identity (never
// the user's real publisher key) — runs one BEP-44 mutable-item Put of a
// synthetic keyword value and Gets it back, confirming the round trip survives
// the real network. An optional stress phase issues N concurrent Puts and reports
// a latency distribution + success rate.
//
// Exit status is a scripting contract:
//
//	0  PASS — the smoke Put+Get succeeded (and, if -stress was given, at least
//	          one stress Put succeeded).
//	1  FAIL — no good DHT nodes after bootstrap, the smoke Put/Get failed, or
//	          (the fix) EVERY stress Put failed. An all-failed stress phase is a
//	          hard failure, not a logged warning: a dead DHT path must not exit 0.
//
// Usage:
//
//	dht-smoke                    # single Put/Get smoke test
//	dht-smoke -stress 20         # then 20 concurrent Puts + latency summary
//	dht-smoke -stress 50 -stress-concurrent 16 -stress-timeout 45s
package main
