# SwartzNet Rebuild — Decision Log

> The running decision log the rebuild playbook requires: every assumption made on the
> author's behalf, every question still open, and where the full rationale lives. This file
> consolidates; the detailed records are authoritative:
>
> - `SPEC.md` §7 — all 81 open questions, with `file:line` evidence anchors
> - `ARCHITECTURE.md` § "Assumptions & deferred questions" — the answered defaults in context
> - `docs/rebuild/phase3-design-record.md` — the three candidate architectures, judge scoring, critic fixes
> - `docs/rebuild/phase1-verification-ledger.md` — per-claim adversarial verification of SPEC §6 (135 confirmed / 13 refuted)

## Phase state

Phases 0–3 complete (artifacts committed 2026-07-09). **The Phase-3/4 gate CLEARED on
2026-07-17**: the author confirmed all five SPEC §0 checklist items unamended and approved
`ARCHITECTURE.md` + `PLAN.md`. Phase 4 is in progress, starting at Slice 0.

## Process decisions

| # | Decision | Rationale |
|---|---|---|
| P1 | Legacy preserved on the **`legacy-snapshot`** branch (pushed), not a `legacy/` directory | Keeps full history and diffability; the working branch stays a buildable Go module with stable import paths |
| P2 | The playbook's `DESIGN.md` is delivered as **`ARCHITECTURE.md`** (design) + **`PLAN.md`** (14-slice build order) | Two audiences, one contract; where they disagree the architecture wins and the plan is corrected |
| P3 | Every SPEC §6 defect claim was adversarially verified before being trusted; **refuted claims must NOT be "fixed"** in the rebuild | Several plausible-looking "bugs" (e.g. PoW=0 minting, seeded heavy-tail rule) are intentional, documented design — ledger in `docs/rebuild/phase1-verification-ledger.md` |
| P4 | Separation of new vs old code is **by branch, not by directory**: the rebuild replaces the tree on the rebuild branch while `legacy-snapshot` preserves the reference | Wire/disk compatibility is the deliverable; parallel trees would fork the module path, `dist/` workflow, and CI for no isolation gain |
| P5 | Same stack retained: Go 1.24.1, anacrolix/torrent v1.61.0, Bleve v2, Fyne v2 | SPEC §5.4's quirk catalog and the wire-compat matrix are anacrolix-specific and interop-tested; a stack change would invalidate the behavioral spec's anchors |
| P6 | Legacy tests that pin *buggy* behavior (permissive admission, static services mask, sequential search, unattributed ingest) are **re-derived, not ported** | Porting them would freeze the §6 defects the rebuild exists to remove |

## Assumed defaults (recorded on the author's behalf — override any of these)

Each maps a SPEC §7 question to the default the architecture adopts. Full context in
`ARCHITECTURE.md` § "Assumptions & deferred questions".

| §7 ref | Decision | Rationale |
|---|---|---|
| A5 | Services mask always-OR of `DefaultServices()` is a **bug**; `capability.Announced()` is the sole, live producer | Downgrades must reach the wire or ShareLocal=0 nodes advertise capabilities they reject |
| A3 | Unknown/future service bits are **ignored, never rejected** | Doc 06's masking wording is self-contradictory; ignore-unknown is the only forward-compatible reading |
| A1/A2 | Keep the code's **reject code 2** on scope mismatch (and its reuse for sync refusal) for wire compat; flag the BEP draft for a future codepoint | Interop with existing peers beats doc fidelity; the spec is fixable, shipped bytes are not |
| H46 | One unsafe gate: **`SWARTZNET_UNSAFE=1`** (+ `testing.Testing()` for testlab), documented | The two-name undocumented sequence broke the project's own testbed |
| B8/B9 | Admission is **deny-by-default**; permissive policy is test-only; a curated `seeds.json` is a **release prerequisite, not code** | The legacy placeholder admitted any unknown pubkey — inverted its own documented default |
| C12–C17 | Legacy per-keyword BEP-44 stays the shipping default; Aggregate lives behind the `RecordBackend` seam, **off by default**; retirement/PoW-ramp/salt schedule is an operator decision | ≥12-month legacy-read contract is binding; migration becomes adapter selection, not re-architecture |
| C16 | `MostDistinctive` is the **only** lookup-token chooser, now | The first-token lookup is a verified defect ("new ubuntu" queried the DHT for "new") |
| D20 | Bad-signature torrents **still add** (empty `SignedBy`, `ErrBadSignature` logged) | Blocking would fork behavior from vanilla and punish benign re-shares; UIs distinguish benign vs tamper |
| D21 | Trusted publishers **are exempt** from flag demotion | The trust package documents the exemption; the legacy just never checked it |
| D22 | Download completion does **not** auto-`RecordConfirmed` | Avoids self-reinforcing reputation; confirm stays an explicit user signal, consistent across surfaces |
| D23 | Bloom + reputation get a **bounded periodic checkpoint** (~5 min + batch flush) in addition to save-on-close | Crash loses at most one interval instead of the whole session's spam-resistance signal |
| D25 | identity.key permission gate stays **exactly 0600** (0400 rejected) | Preserves the legacy's observable contract; loosening is a one-line change if the author prefers "no group/other bits" |
| E27 | Queue promotion is **decoupled** from the Bloom gate | A nil Bloom stranding a completed torrent's slot is a verified defect, not a design |
| E28 | Per-torrent indexing-off **still publishes existence** to Layer D; only `--no-index`/`--no-dht-publish` suppress publication | Matches the code comment's stated privacy posture; flagged for the author under §7-28 |
| F36 | Index deletion becomes a reachable **"Forget"** action; downloaded files kept by default | Removed-but-searchable-forever is a verified defect; file deletion stays a separate, explicit act |
| §7-Q37 | Local + companion docs share one ID namespace, last-write-wins, **except non-empty `SignedBy` is sticky** against unsigned overwrites | Keeps the companion-attribution fix from being clobbered by a later local magnet add |
| §7-Q40 | Raw `.html` stays indexed by the **plaintext** extractor with tags left in; no standalone HTML extractor | "Improving" this silently changes the indexed content of every `.html` file |
| G43 | PeerBook state does **not** persist across restarts (v1 scope) | Matches legacy behavior; AddrMan-style persistence is deferred, recorded, and additive later |
| I51 | `add`-as-daemon stays (no `serve` subcommand in the final CLI; Slice 0's `serve` is a temporary scaffold deleted in Slice 2); bare 40-hex accepted as infohash add | Preserves the legacy UX contract; the scaffold exists only because Slice 0 has no engine to `add` |
| I53/I54 | `Options.PublisherPubKey` wired from identity; `/capabilities` mutates **only** `Sharing` with PATCH semantics | The daemon-owned Publisher bit becomes unreachable from the setter — the clobber becomes unrepresentable |
| J60 | User-facing license string: **Apache-2.0** (single build-stamped source) | `LICENSE`, README, and `THIRD_PARTY_LICENSES` all say Apache-2.0; the GUI About string was drift |
| §2.9/§5.9 | Non-loopback API bind **warns loudly and binds** (one-time "API is UNAUTHENTICATED" warning), not a hard refusal | Matches the spec's warn-and-bind; invariant #3's guarantee is *no auth concept in httpapi*, not a bind restriction |

## Resolved — the author gate (2026-07-17)

1. **`SPEC.md` §0 confirmed** — all five checklist items (mission framing, Aggregate
   endgame, scope exclusions, the four invariants, design-center user), unamended. §0 is
   now the normative statement of intent.
2. **`ARCHITECTURE.md` + `PLAN.md` approved** — implementation authorized, starting at
   Slice 0 (walking skeleton).

## Open — deliberately deferred (does not block Phase 4)

- **A1/A2** — BEP normativity of scope-reject vs downgrade; a dedicated sync reject code.
- **B8** — who curates/distributes the production `seeds.json` trust root (hard release gate; `/aggregate` admission counts make a starved node observable meanwhile).
- **C12–C17** — PPMI retirement schedule, PoW ramp mechanics, salt convergence.
- **C18** — how signed metainfo reaches BEP-9-only fetchers (`ut_signature` vs HTTPS mirror vs in-info signing); deferred while Channel-B crawl is non-load-bearing.
- **D19** — `next_pk` key rotation: reserved, unused.
- **G43** — PeerBook persistence (intentional v1 scope).
- The long tail of §7 (ops/docs/CI questions, §7-K/L/M) — resolved during Slice 13 or as encountered; every resolution gets appended here.

## Log

- **2026-07-09** — Phases 0–3 executed and committed (`8e687ab` SPEC §1–7, `96b109c` provisional §0, `ee54afb` ARCHITECTURE+PLAN, `26a897e` provenance records). Paused at the Phase-3/4 human gate.
- **2026-07-17** — State re-verified (working tree clean, `legacy-snapshot` intact, no code drift since the spec snapshot); this consolidated decision log created; gate re-presented to the author.
- **2026-07-17** — **Gate cleared.** Author confirmed SPEC §0 (all five items) and approved ARCHITECTURE.md + PLAN.md. Phase 4 begins: the legacy Go tree is removed from the rebuild branch (preserved on `legacy-snapshot`), and Slice 0 implementation starts.
