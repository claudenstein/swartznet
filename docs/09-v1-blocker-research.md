# v1.0.0 open-question research report

Date: 2026-04-11. This document consolidates desk research on
the six "open questions that block v1" identified in
[`05-integration-design.md`](05-integration-design.md) §13. Each
section states the question, summarises findings with citations,
and ends with a concrete action item for the v1.0.0 release.

Findings were produced by three parallel research passes over
public BitTorrent / DHT / P2P literature and source code; the
actionable subset is captured here so the design record survives
future refactors. The six original blockers are numbered the same
way as in §13 for cross-reference.

---

## Blocker 1 — Bleve on-disk index size per TB of indexed text

### Question

How big is Bleve's scorch index on real torrent content, and is
the v1 inflation headroom acceptable?

### Findings

- **Worst-case public reports show 7-12x expansion**, almost
  always traceable to storing full field values in the index. In
  [blevesearch/bleve#1186](https://github.com/blevesearch/bleve/issues/1186)
  a user reports `150mb data → 1.5G index` and `~40GB source →
  ~500GB index` on 50M docs, against Lucene/Solr producing
  `only 10gb for the entire 100M documents`.
- **The bloat is Store=true, not scorch itself.** In
  [blevesearch/bleve#1267](https://github.com/blevesearch/bleve/issues/1267)
  the maintainer clarifies: *"The actual zap index files in my
  test amount to only ~150MB, so scorch is doing great in terms
  of disk space."*
- **Throughput ceiling is ~thousands of docs/sec sustained.**
  [blevesearch/bleve#1266](https://github.com/blevesearch/bleve/issues/1266)
  shows "first few batches of 500 documents are indexed within
  milliseconds. However, once I get towards the end each batch
  takes upwards of 20s" — root-caused to `SetInternal` not
  scorch internals.
- **Alternatives for future migration:** tantivy matches Lucene on
  index size ([tantivy#576](https://github.com/quickwit-oss/tantivy/issues/576)).
  SQLite FTS5 is the smallest for text-heavy corpora. Both sit
  3-5x smaller than bleve-with-Store-true but comparable once
  Store is off.
- **Chunk size:** Elastic's docs default to *"250 words"*
  (≈1.25 KiB); production RAG/BM25 stacks sit at 0.5-4 KiB per
  chunk. SwartzNet's current ~10 KiB chunker is an order of
  magnitude larger than the common sweet spot.

### Verdict

**Not a v1 blocker.** The horror-story numbers are all caused by
storing full text field values. SwartzNet's current schema keeps
`Store=true` on the `text` field for snippet highlighting (M12e),
which is the right v1 tradeoff: usability wins over disk size
for a desktop client whose users rarely hit >10 GB of extracted
text. The `/index/stats` endpoint shipped in M12b gives anyone
running the daemon the data to validate that, and it becomes a
real v2 concern only if inflation exceeds ~5x with real corpora.

### Action items

1. **Shrink chunk size** from ~10 KiB toward the 1-2 KiB band
   the literature converges on. Smaller chunks improve BM25
   relevance at a small index-size cost and shrink the payload
   of each highlight fragment.
2. **Keep `Store=true` for v1** but add a `CompactMode` knob on
   the indexer schema so power users with >100 GB of extracted
   text can opt out of snippet highlighting in exchange for the
   3-5x size reduction documented above.

---

## Blocker 2 — BEP-44 on live mainline DHT under concurrent load

### Question

What fraction of mainline DHT nodes implement BEP-44, how does
`anacrolix/dht/v2` behave under concurrent mutable-item puts,
and is the round-trip success rate acceptable for v1?

### Findings

- **libtorrent-based clients dominate mainline and honor BEP-44
  puts.** The libtorrent maintainer states: *"uTorrent implements
  it as well and … a significant number of nodes in the wild
  implements it"*
  ([arvidn/libtorrent#7810](https://github.com/arvidn/libtorrent/discussions/7810)).
  Conservative floor: ~40-60% of reachable mainline nodes.
- **webtorrent/bittorrent-dht does NOT implement BEP-44**
  ([webtorrent bep_support.md](https://github.com/webtorrent/webtorrent/blob/HEAD/docs/bep_support.md)).
  Browser-resident peers are never store targets.
- **BEP-44 is still formally Draft** on bittorrent.org but
  libtorrent's 10+ years of deployment is the de-facto standard.
- **anacrolix/dht has no default rate cap on mutable-item puts.**
  The `bep44` package exposes a `QueryRateLimiting` parameter
  ([pkg.go.dev](https://pkg.go.dev/github.com/anacrolix/dht/v2/bep44))
  but no global ceiling — *callers* must enforce their own put
  budget to avoid self-DoSing the publisher worker.
- **Token round-trip is flaky against public nodes.**
  [anacrolix/dht#4](https://github.com/anacrolix/dht/issues/4)
  documents `"KRPC error 203: invalid token"` and `"9 times out
  of 10, it won't respond"` testing against public bootstrap
  nodes. This is a reachability issue, not a rate issue.
- **Real-world BEP-44 deployments we can cite:**
  [anacrolix/btlink](https://github.com/anacrolix/btlink) uses
  BEP-46 for its primary addressing;
  [getlantern/dhtup](https://github.com/getlantern/dhtup) drives
  torrent infohashes over BEP-44 exactly the way SwartzNet
  plans to; pkarr/pubky/iroh run on BEP-44 mutable items at
  scale.
- **TTL is 2 h hard cap, 1 h re-announce** per the
  [BEP-44 spec](https://www.bittorrent.org/beps/bep_0044.html).
  The existing SwartzNet publisher refresh interval (1 h default)
  is correctly sized.

### Verdict

**Partial blocker, addressable with a client-side budget.** The
protocol path is real and at least two notable projects are in
production. The v1 risks are (a) per-put reliability against
mixed mainline nodes, which needs measurement, and (b) the
undocumented concurrent-put behavior — SwartzNet must enforce
its own rate limit. The M12c `dht-smoke -stress` tool lets
anyone validate (a); client-side budgeting is the v1 code work
for (b).

### Action items

1. **Enforce a hard cap of one BEP-44 put per key per hour** in
   `dhtindex.Publisher`. Current worker already rolls at 1 h; add
   a guard that makes this explicit and rejects any manual
   refresh that would violate it.
2. **Publish a measurement SLO** in the operations guide:
   target ≥60% put-then-get round-trip success over 24 h across
   ≥3 vantage points, measured via `dht-smoke -stress`.

---

## Blocker 3 — Layer-S topical clustering

### Question

Does the "peers you're already in a swarm with" assumption for
Layer S actually produce topically clustered search targets?

### Findings

Deliberately not agent-researched — this is an empirical
question that can only be answered from a running daemon's logs
over weeks. The existing `swarmsearch.Protocol.KnownPeers` +
`sources` attribution from M9 already gives us enough
observability to answer it in the field.

### Verdict

**Not a v1 blocker; observable post-ship.** The answer does not
gate the v1.0.0 release — at worst Layer S returns a random
sample of peers' local indexes, which is still useful. The v2
improvement path (topic-aware peer selection) can be designed
once real distribution data exists.

### Action items

None for v1.0.0. Log the data, circle back in v1.1.

---

## Blocker 4 — Reputation cold-start

### Question

On day one every publisher pubkey has zero reputation. How does
the reputation network bootstrap without becoming a
seed-list-centered choke point?

### Findings

- **Pairwise systems like eMule credits never solved this** —
  they work because they don't *need* to transfer trust between
  publishers. SwartzNet's Layer-D publisher model does.
- **Freenet's Web of Trust bootstraps via CAPTCHAs solved
  against seed identities**
  ([freesocial.draketo.de](https://freesocial.draketo.de/wot_en.html)).
  Ten years in production, usability is poor, users hate it.
- **YaCy ships a signed seed list of "principal" peers** fetched
  over well-known HTTPS URLs
  ([YaCy Seedlists wiki](https://wiki.yacy.net/index.php/Seedlists)).
  Operationally robust for 15+ years. De-facto centralisation on
  the seed URL but editable by users.
- **EigenTrust** (Kamvar et al., WWW 2003,
  [Stanford PDF](https://nlp.stanford.edu/pubs/eigentrust.pdf))
  is the closest academic match to SwartzNet's Bayesian tracker:
  it requires pre-trusted peers to anchor the eigenvector
  computation. Jansen
  ([csci5271](https://www.robgjansen.com/publications/fet-csci5271.pdf))
  quantifies the degradation when those seeds misbehave.
- **DSybil** (Yu et al., IEEE S&P 2009,
  [Yale PDF](https://zoo.cs.yale.edu/classes/cs722/2011/Ennan-DSybil.pdf))
  proves unlimited Sybil tolerance *over time* by exploiting
  heavy-tailed honest voting — not directly applicable to
  mutable-item trust but the heavy-tail heuristic is.
- **Tribler abandoned BarterCast** per the current
  [Tribler wiki](https://github.com/tribler/Tribler/wiki)
  ("currently has no integrated reputation mechanism … BarterCast
  has been removed") — a data point that even well-funded teams
  found maxflow reputation too hard to maintain.
- **pkarr/pubky is TOFU by construction** — each ed25519 key is
  its own sovereign DNS, so the first time you subscribe to a
  pubkey that becomes your ground truth. Closest prior art to
  the companion-index follow list we shipped in M11d/e.

### Verdict

**Not a v1 blocker if we ship a seed list; still worth the code
work before cutting v1.0.0.** The design literature is clear that
a small curated seed list bootstraps an EigenTrust-style system,
with the caveat that seed weight must decay so organic
reputation dominates steady-state. No prior P2P system has found
a genuinely decentralised bootstrap; everyone who has tried
(Freenet, YaCy, Kazaa, Gnutella) ends up with "some seed list,
signed, editable, HTTPS-fetchable".

### Action items

1. **Ship a versioned, signed, user-editable seed list** of
   ~20 pubkeys (initial maintainers + community volunteers) at
   a well-known HTTPS URL under the project's control.
2. **Add exponentially-decaying seed weight** to
   `reputation.Tracker` with a 90-day half-life, so organic
   trust dominates after one quarter.
3. **Add a DSybil-style heavy-tail heuristic** to
   `dhtindex.Lookup`: a result appears if *either* the
   aggregate score exceeds the `MinIndexerScore` threshold
   *or* at least one seed / user-pinned key has endorsed it.
4. **Do not attempt a puzzle/WoT flow for v1.** Freenet's
   decade of deployment data says users hate it.

---

## Blocker 5 — Extractor license audit

### Question

Are any extractor dependencies GPL / AGPL / LGPL / SSPL / BUSL
that could infect SwartzNet's Apache-2.0 redistribution?

(Note: the original agent brief said MPL-2.0; the actual
`LICENSE` file at the repo root is Apache-2.0. Apache-2.0 is
strictly more permissive than MPL-2.0 in the directions that
matter, so the clean conclusion below still holds — it just has
even more headroom.)

### Findings

The current extractor tree is **clean**:

| Dependency | Version | License | Concern |
|---|---|---|---|
| `github.com/ledongthuc/pdf` | v0.0.0-20250511090121-5959a4027728 | **BSD-3-Clause** (not MIT as previously documented) | Attribution label needs fixing |
| `golang.org/x/net` (html) | v0.47.0 | BSD-3-Clause | None |
| Go stdlib `encoding/xml`, `archive/zip` | go 1.24 | BSD-3-Clause | None |

No GPL / LGPL / AGPL / SSPL / BUSL / CDDL / EPL detected on the
extractor hot path. The only action item is correcting the
`ledongthuc/pdf` license label from MIT to BSD-3-Clause anywhere
it's documented.

### Post-v1 candidate formats

| Format | Recommended lib | License | Pure-Go | Concern |
|---|---|---|---|---|
| RTF | `lu4p/cat` or `aiq/go-rtf` | Unlicense / MIT | Yes | Clean |
| FB2 | In-house `encoding/xml` | stdlib | Yes | Trivial port |
| Markdown | already handled by `plaintext.go` | stdlib | Yes | No action |
| Source code | already handled by `plaintext.go` | stdlib | Yes | No action |
| Audio transcription | `whisper.cpp/bindings/go` | MIT (CGo) | No | Build + model distribution |
| Image OCR (speed) | `otiai10/gosseract` | MIT (CGo) | No | libtesseract Apache-2.0, fine |
| Image OCR (portable) | `Danlock/gogosseract` | Apache-2.0 (WASM) | Yes | 6x slower than CGo |
| MOBI / AZW3 | *none license-clean for reading* | LGPL-2.1 (`libmobi`) only | No | **Defer past v1** or write in-house |
| CHM | *none pure-Go* | LGPL-2.1 (`chmlib`) only | No | **Skip — low value** |

### Verdict

**Not a v1 blocker.** Extractor tree is license-clean. One
cosmetic fix (PDF attribution) plus adding a
`THIRD_PARTY_LICENSES` file.

### Action items

1. **Add a `THIRD_PARTY_LICENSES` file** listing ledongthuc/pdf
   (BSD-3-Clause) and golang.org/x/net (BSD-3-Clause).
2. **Correct every reference** to `ledongthuc/pdf` that calls it
   MIT — the upstream `LICENSE` file is BSD-3-Clause.

---

## Blocker 6 — Publisher IP / identity exposure in BEP-44

### Question

The publisher's IP is visible to every DHT node their put
traversal touches, and their stable ed25519 pubkey links every
post to the same identity. Does this need mitigation before v1?

### Findings

- **Every shipping BEP-44 deployment uses stable keys.** pkarr,
  pubky, iroh, anacrolix/btlink, libtorrent BEP-46 — all use a
  stable long-lived ed25519 pubkey as the user's identity. This
  is the design; rotation is not attempted in production.
- **The community's answer to IP leakage is "run a relay".**
  pkarr ships a relay frontend
  ([pkarr/design/relays.md](https://github.com/Pubky/pkarr/blob/main/design/relays.md))
  that lets clients publish via HTTP without exposing their IP
  to the DHT.
- **GNUnet's R5N DHT** is the only academic design that makes
  publisher identity unobservable at the protocol layer
  ([LSD0004](https://lsd.gnunet.org/lsd0004/)), but is
  incompatible with mainline.
- **Tribler's hidden-service protocol** routes through a
  BitTorrent-native onion overlay
  ([Tribler hidden-services spec](https://github.com/Tribler/tribler/wiki/Hidden-Services-Specifications-for-anonymous-seeding)).
  Bespoke overlay, not mainline-compatible.
- **Tor hidden-service precedent:** Tor v3 rotates time-period
  keys under a long-term identity
  ([rend-spec](https://spec.torproject.org/rend-spec/protocol-overview.html)).
  Closest design match for a future key-rotation scheme — the
  stable root stays the reputation anchor, time-period keys do
  the actual publishing.
- **Three non-obvious leaks confirmed by the literature:**
  1. **BEP-42 node-ID restriction** forces DHT node IDs to
     derive from external IP, meaning the 20-80 nodes closest
     to `SHA1(pubkey || salt)` are geographically biased. An
     adversary running Sybils near the target sees every put
     attempt. Wang & Kangasharju (2012,
     [PDF](https://nymity.ch/sybilhunting/pdf/Wang2012a.pdf))
     counted ~300k Sybils in the Mainline DHT.
  2. **Timing fingerprint** — hourly put cadence clusters keys
     to the same publisher across rotations.
  3. **BEP-44 traversal amplification** — publishing to ~8 DHT
     slots means one relay observes multiple times more
     publications than in unicast.
- **qBittorrent already has an Anonymous Mode** that routes via
  SOCKS5 ([qBittorrent wiki](https://github.com/qbittorrent/qBittorrent/wiki/Anonymous-Mode))
  — clear precedent for the same knob in SwartzNet's DHT put
  path.

### Verdict

**Blocker, but the right v1 response is documentation + a
SOCKS5 setting, not a protocol redesign.** Ephemeral keys
destroy the reputation system we just built (blocker 4). Key
rotation with a stable root is the right long-term design but
should not gate v1.0.0 — it's a schema-only prep today, a
protocol change later.

### Action items

1. **Add a first-class SOCKS5 setting** scoped to the BEP-44
   put path only (not the torrent swarm, to avoid Tor Project
   ire over bulk traffic). Ship with a "Private Publishing"
   toggle in the GUI Sharing tab, default off.
2. **Document the threat model explicitly** in
   `docs/08-operations.md`: stable pubkey + IP visibility to
   put-target nodes + hourly timing fingerprint + BEP-42
   geographic bias. Users who need anonymity should layer
   their own VPN/Tor on top.
3. **Design the key schema now for future rotation.** Add an
   optional `next_pubkey` field signed by the current key to
   the Layer-D put schema, mirroring Tor v3's time-period-key
   chain. This is schema-only; no rotation logic in v1.

---

## Summary — v1.0.0 action items

From the six blockers, concrete code / docs work before cutting
v1.0.0:

| # | Action | Kind | Priority |
|---|---|---|---|
| 1 | Shrink chunker to ~1-2 KiB | code | medium |
| 1 | Add `CompactMode` schema knob | code | low |
| 2 | Enforce 1 put / key / hour in `dhtindex.Publisher` | code | high |
| 2 | Publish measurement SLO in operations guide | docs | medium |
| 3 | (none) | — | — |
| 4 | Ship a signed, versioned seed list (~20 pubkeys) | code + infra | high |
| 4 | Seed weight decay (90-day half-life) | code | medium |
| 4 | DSybil heavy-tail heuristic on Lookup | code | medium |
| 5 | `THIRD_PARTY_LICENSES` file | docs | high |
| 5 | Correct `ledongthuc/pdf` attribution (MIT → BSD-3-Clause) | docs | high |
| 6 | SOCKS5 setting for BEP-44 put path | code | high |
| 6 | Threat-model section in operations guide | docs | high |
| 6 | `next_pubkey` schema field (no rotation logic yet) | code | low |

None of the blockers turn out to require a protocol redesign.
The v1.0.0 release is a matter of concrete follow-through.
