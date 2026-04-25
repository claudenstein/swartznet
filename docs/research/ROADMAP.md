# Aggregate — Implementation Roadmap

| Field | Value |
|---|---|
| Approved | 2026-04-24 |
| Implementation branch | `overnight/test-harness-2026-04-20` |
| Design source | `PROPOSAL.md` |
| Wire/format source | `SPEC.md` |

## Phase map (SPEC §8)

Each phase = one commit minimum. Tests live in `*_test.go`
alongside each production file. After every code change: rebuild
`dist/swartznet` and `dist/swartznet-gui-dev-linux-amd64`, run
`go test -race ./...` on the touched packages, commit, push.

| # | Phase | Deliverable | Deps | Status |
|---|---|---|---|---|
| 1 | **P1.1** | `internal/companion/btree.go` page encode/decode | — | **done** `8aa333b` |
| 2 | **P1.2** | `internal/companion/build_btree.go` builds trees | P1.1 | **done** |
| 3 | **P1.3** | `internal/companion/read_btree.go` prefix walker | P1.1, P1.2 | **done** |
| 4 | **P2.1** | `internal/dhtindex/ppmi.go` PPMI schema | — | **done** |
| 5 | **P2.2** | PPMI publisher glue | P2.1 | **done** |
| 6 | **P2.3** | PPMI reader with legacy fallback | P2.1 | **done** |
| 7 | **P3.1** | `internal/swarmsearch/riblt.go` rateless IBLT | — | **done** |
| 8 | **P3.2** | sn_search msg_types 4–8 handlers | P3.1 | **done** (LTEP dispatch integration deferred to Final phase) |
| 9 | **P4.1** | `internal/daemon/bootstrap.go` three channels | P2.3, P3.2 | **done** (BEP-51 crawler primitives landed: SampleInfohashes, PublisherFromMetainfo, CrawlOnce + crawl-probe CLI; production MetainfoFetcher blocked on snet.pubkey-vs-BEP-9 architectural gap, see MILESTONE doc) |
| 10 | **P5.1** | Hashcash + double-hashed salt + misbehavior | P2.1, P3.2 | **done** (ingest wiring pending P3.2) |
| 11 | **P5.2** | HTTPS anchor fallback | P4.1 | **done** |
| 12 | **Final** | E2E integration + mixed-migration test | all above | **done** |

## Execution rules per iteration

1. Mark task `in_progress` with TaskUpdate.
2. Read only what's needed; SPEC §1–3 is the contract.
3. Implement production file, then test file.
4. `/usr/local/go/bin/go test -race ./<package>/... -count=1`.
5. `/usr/local/go/bin/go build -o dist/swartznet ./cmd/swartznet`.
6. `./scripts/build-gui.sh dev` when GUI-visible or GUI-adjacent.
7. Commit with `<package>: <imperative summary>` style message.
8. Push to `origin/overnight/test-harness-2026-04-20`.
9. Mark task `completed`.
10. Schedule next wakeup (1200–1800 s), or pick next task in same
    iteration if time remains and work is tightly coupled.

## Non-goals for the implementation loop

- No premature optimization (SPEC §5 explicitly lists out-of-scope).
- No touching frontends (CLI/GUI/web) until Final phase — the
  subsystems must be correct first.
- No removing legacy Layer-D items — PROPOSAL §6 migration is a
  three-release dance. The implementation loop lands the new code
  *alongside* the old; retirement is a later decision.

## Post-Final hardening

After all 12 phases landed, the loop continued under the
"polish until shippable" charter. Per-iteration deliverables:

- **RecordCache durability**: bounded FIFO eviction
  (`DefaultRecordCacheMax = 100_000`), `PruneOlderThan`
  helper, periodic prune goroutine in `engine.New` (30d /
  hourly in prod, 500ms / 200ms in regtest).
- **BEP-51 crawler primitives**: `dhtindex.SampleInfohashes`,
  `PublisherFromMetainfo`, `CrawlOnce` with full unit-test
  coverage (loopback DHT + signed-metainfo fixtures + sink
  classification).
- **`swartznet crawl-probe` CLI**: ops command for
  hand-validating BEP-51 support on a target peer; text and
  JSON output paths both regression-tested.
- **Coverage tests**: package coverage now reads ≥87% on
  every load-bearing package; specific zero-coverage paths
  filled in for atomic file writers, pubkey accessors, sync
  handler unknown-session branches, and B-tree error paths.
- **Doc sync**: CHANGELOG, MILESTONE, and ROADMAP all reflect
  what shipped vs what remains deferred (notably the BEP-9
  vs top-level `snet.pubkey` gap blocking a complete
  production MetainfoFetcher).

The deferred items list in `MILESTONE-v0.5.0.md` is the
authoritative "what's left" tracker.
