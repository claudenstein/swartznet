# Lessons from Bitcoin Core for SwartzNet

Date: 2026-04-11. This document is a desk-research distillation
of Bitcoin Core's 15+ years of production P2P uptime, filtered
for patterns that translate to a distributed search network.
SwartzNet is *not* a blockchain — no native token, no global
ordering, no consensus — so a lot of Bitcoin's machinery is
inapplicable. Writing the "not applicable" list down explicitly
is as important as the adoption list, because it prevents future
contributors from importing the wrong mental model.

The scope was defined in advance: peer discovery, AddrMan,
misbehavior scoring, regtest/testnet tiers, BIP9/BIP8 soft-fork
deployment, compact block relay insights, Tor/I2P integration,
BIP324 v2 transport, and the functional-test framework. Each
section answers three questions: **what Bitcoin does**, **what
the underlying insight is**, and **does it translate**.

A short "recommended adoption list" at the end captures the
concrete follow-through for SwartzNet.

---

## 0. Proof-of-Work — why it does NOT apply

Getting this out of the way first because it's the elephant in
the room: Bitcoin's PoW exists so that a global, un-trusted,
permissionless network can agree on a single ordering of
transactions without any central authority. Every node
independently verifies the chain; the rules are objective;
whoever has the most CPU wins.

SwartzNet has none of the problems PoW solves:

- **No global state to agree on.** Each publisher independently
  publishes their own view of "keyword → infohash". Subscribers
  aggregate. If two publishers disagree, both are right —
  they're just different opinions.
- **No double-spend equivalent.** The worst a malicious
  publisher can do is publish spam or claim a torrent for
  content it doesn't contain. Spam is handled via reputation +
  flag + Bloom filter (M5 + M9 + M13c); content mismatch is
  caught on download by the user. Neither needs a consensus
  ledger.
- **No scarcity to allocate.** There's no block reward, no fee
  market. Adding PoW would burn CPU to produce exactly zero
  value.

PoW is the single biggest thing Bitcoin has that SwartzNet
must *not* copy. The same mental trap applies to mining
rewards, fee markets, and anything else that exists to ration
scarce block space.

**Reject.** Not applicable.

---

## 1. Peer discovery and bootstrap

### What Bitcoin does

A fresh Bitcoin Core node bootstraps in three stages:

1. **DNS seeds.** `chainparams.cpp` ships a hardcoded list of
   DNS names (e.g., `seed.bitcoin.sipa.be`,
   `dnsseed.bluematt.me`, `dnsseed.bitcoin.dashjr.org`,
   `seed.bitcoinstats.com`, `seed.bitcoin.jonasschnelli.ch`,
   etc). These resolve to A/AAAA records pointing at
   maintainer-run crawler servers that return ~25 random live
   node IPs per query. The client issues a standard
   `gethostbyname` and feeds the resulting IPs into its peer
   table. No trust on any single DNS seed — the node expects
   multiple answers and discards suspicious ones.
2. **Hardcoded seed nodes.** If every DNS seed fails (firewall,
   network partition), `chainparamsseeds.h` holds a
   hardcoded list of ~500 IPs that were active at the time of
   the release build. These are only used as a last resort and
   are dropped from the active set as soon as the normal
   peer-discovery path finds real peers.
3. **addr/addrv2 gossip.** Once at least one peer is
   connected, Bitcoin asks it for more peers via the `getaddr`
   message and receives up to 1000 addresses in `addr`/`addrv2`
   replies. New peers are folded into the AddrMan tables
   (section 2 below).

Source pointers:
- `src/chainparams.cpp` — DNS seed list
- `src/chainparamsseeds.h` — hardcoded IP list
- `src/net_processing.cpp` — `getaddr` / `addr` message handling
- [BIP 155](https://github.com/bitcoin/bips/blob/master/bip-0155.mediawiki)
  for addrv2

### The underlying insight

**Bootstrap is a distinct phase from steady-state peer
discovery, and it always involves *some* form of trust anchor.**
Every P2P system that claims to be "decentralised from day one"
actually has a bootstrap list: DNS seeds in Bitcoin, hardcoded
routers in IPFS, `bootstrap-routers` in mainline DHT, seed
nodes in Tor. Pretending otherwise produces stale and
adversarial caches (see Gnutella GWebCache history from the v1
blocker research).

The practical pattern is:
- **Diverse bootstrap anchors.** Multiple DNS names controlled
  by different entities, so any one compromise is recoverable.
- **Graceful degradation.** Hardcoded fallback if the anchors
  are down.
- **Fast transition to gossip.** Once the node has any peers,
  the bootstrap anchors become irrelevant.

### Does it translate?

**STRONG TRANSLATE.** SwartzNet already has two bootstrap
concepts — the mainline DHT bootstrap routers (handled by
anacrolix/dht/v2 itself) and the M13c curated seed list of
indexer pubkeys. Both need a way to update without a software
update. Today the seed list is a static JSON file on disk.

Concrete adoption:

- **DNS-seed pattern for the indexer seed list.** Define a
  well-known TXT-record DNS zone (e.g.
  `_seeds.swartznet.example`) that returns a signed URL to the
  current seed list JSON. The client resolves the TXT record,
  fetches the JSON over HTTPS with a pinned signer's ed25519
  pubkey, merges it with any user-maintained seeds. No one DNS
  record or HTTPS endpoint is trusted on its own — the client
  verifies the JSON signature against a baked-in root key.
- **Hardcoded bootstrap seeds as a last-resort fallback.** Ship
  the current maintainer pubkeys in `seeds_embedded.go` so
  `swartznet add` on a fresh install with no network can still
  run in cold-start mode.

No code change required today; add it to the docs for v1.1.

---

## 2. AddrMan — peer address management

### What Bitcoin does

`src/addrman.h` / `src/addrman.cpp` implements a two-table
address manager:

- **New table**: addresses the node has heard about but never
  connected to. 1024 buckets × 64 positions, eviction by age.
- **Tried table**: addresses the node has successfully
  connected to at least once. 256 buckets × 64 positions.
- **Bucket selection** uses a double-hash of the address +
  local "key" seeded at first startup, so each node has a
  different partitioning of the same address space. This makes
  an attacker who wants to fill a victim's AddrMan with their
  own nodes unable to predict which bucket any given IP will
  land in for that specific victim.
- **ASN-based diversity.** Bitcoin Core optionally loads
  `asmap.dat` which maps IP prefixes to autonomous systems,
  and uses the AS number as an extra bucket salt so that even
  an attacker with many IPs inside one AS cannot concentrate
  their poisoning. See
  [Asmap introduction PR](https://github.com/bitcoin/bitcoin/pull/16702).
- **Feeler connections.** Every ~2 minutes the node opens a
  short-lived "feeler" outbound connection to a random address
  from the `new` table. If the handshake succeeds, the address
  is promoted to `tried`. This is the only safe way to
  validate new addresses without polluting the real outbound
  set.
- **Eclipse-attack defences.** Documented in the paper
  ["Eclipse Attacks on Bitcoin's Peer-to-Peer Network"](https://eprint.iacr.org/2015/263.pdf)
  (Heilman et al., 2015) — Bitcoin Core adopted the paper's
  recommendations (ASN-diverse buckets, anchor connections,
  feeler probing).

### The underlying insight

**Peer addresses have two life-cycle states: "untested" and
"validated". The tested set drives real outbound behavior; the
untested set is the pool from which feelers recruit. Mixing
them invites eclipse attacks.** The second insight is that
**bucket selection should be unpredictable to an attacker**,
either via a per-node random salt or via structural partitions
(ASes) the attacker can't span cheaply.

### Does it translate?

**STRONG TRANSLATE** for Layer S (the sn_search peer book).
SwartzNet currently has no distinction between "seen this peer
once in a swarm" and "this peer actually speaks sn_search well
and reliably". Every peer in the current swarm is treated as an
equally-valid search target. That's fine for v1 but leaves
SwartzNet open to a small-cost eclipse attack: flood the
victim's swarms with Sybils that advertise sn_search but
answer every query with garbage, and the legitimate peers get
drowned out.

Concrete adoption (post-v1):

- **Add a per-peer sn_search book** in `internal/swarmsearch`
  with tried/new tables following the AddrMan design. Score
  peers by (query success rate, average response latency,
  result-quality feedback from the user's confirm/flag
  actions) and split into the two tables. Layer-S query
  fan-out should prefer "tried" peers and use a fraction of
  "new" slots for feeler-style discovery.
- **Wire the ASN-diversity constraint** into both the
  sn_search book and the dhtindex indexer selection. Ship an
  `asmap.dat` cached from MaxMind or similar, fall back to
  /16 CIDR bucketing if the asmap is unavailable.
- **Feeler connections for Layer D.** Before promoting a new
  publisher pubkey from the seed list to "trusted indexer",
  issue one test `Get` against it and require a successful
  round trip.

**Priority**: medium. Post-v1 but valuable for the global-rock-
solid network goal. This is where we make eclipse attacks
expensive without adding consensus.

---

## 3. Misbehavior scoring and banning

### What Bitcoin does

`src/net_processing.cpp` maintains a per-peer misbehavior
score. Protocol violations add points to the score:

- Invalid block header → 100 (immediate ban)
- Invalid transaction format → 100
- Excessive `getdata` spam → 20
- Sending unsolicited `reject` → 10
- Stale `headers` message → 20
- ... and dozens more at varying severities

When the score crosses 100, the peer is disconnected and
added to the banlist (`src/banman.cpp`) for 24 hours by
default. The banlist is persisted to `banlist.dat` so it
survives restart. It is NOT gossiped to other peers — each
node decides its own bans independently, because sharing bans
would give an adversary a way to poison the entire network.

A lighter-weight mechanism is "peer discouragement": a peer
who accumulates non-zero misbehavior but stays under 100 gets
pushed to the back of the outbound priority queue. Disconnected
first under pressure, dialed last on reconnect.

### The underlying insight

**Per-peer scoring must be local and independent.** Sharing bans
at the network layer seems appealing but is actually a
denial-of-service vector: whoever can cause other peers to ban
a victim gets to eclipse the victim cheaply. Keep each node's
scoring private.

A second insight: **scoring needs to be cheap and bounded.** The
misbehavior counters are bounded (clamped to 100), reset on
disconnect, and persisted only for the most-egregious cases.
Writing a score to disk for every packet is not viable at
thousands of peers.

### Does it translate?

**STRONG TRANSLATE.** SwartzNet has the M12f per-peer rate
limiter (token buckets on the inbound sn_search query path)
and the Layer-D reputation tracker (scoring indexer pubkeys on
confirm/flag feedback). Both are good primitives but there's
no unified "misbehavior score" for sn_search peers. A peer
sending malformed bencode, unsolicited rejects, or queries at
unreasonable scope should accumulate a score and eventually
get disconnected.

Concrete adoption (post-v1):

- **Add a `PeerScore` counter** to `swarmsearch.Protocol`'s
  per-peer state. Feed it from the wire decoder (bad bencode
  = +20), the rate limiter (over quota = +5), and the handler
  (query-too-broad = +10, scope-not-supported = +5).
- **Wire a threshold (100) to the banlist.** Peers who cross
  the threshold are added to a persistent file analogous to
  `banlist.dat` — `~/.local/share/swartznet/banlist.json` —
  and the swarmsearch protocol skips them on future
  handshakes.
- **NEVER gossip bans.** Keep every node's banlist local. The
  same logic as Bitcoin.

**Priority**: medium. A good defence-in-depth layer on top of
the existing rate limiter. Can wait for v1.1.

---

## 4. Regtest / testnet / signet tiers

### What Bitcoin does

Bitcoin Core ships three distinct "networks" besides mainnet,
with different tradeoffs:

- **Testnet3 / Testnet4**: a real distributed network using
  the same protocol as mainnet, but with coins that have zero
  market value and a relaxed difficulty (so blocks mine
  faster). Intended for dapp testing and wallet QA.
- **Signet**: a federated testnet where a designated signer
  signs every block with a known pubkey. Block production is
  predictable (no PoW race), so tests can depend on specific
  block times. Much closer to "deterministic testnet".
- **Regtest**: a single-node or tightly-controlled local
  network. Difficulty is trivial so blocks mine instantly on
  CPU, and the node accepts the `generatetoaddress` RPC to
  mine blocks on demand. Intended for unit tests and
  functional tests — every `test/functional/*.py` scenario
  runs on regtest.

The config flag is `-regtest` / `-testnet` / `-signet` on the
command line. Each selects a different `CChainParams` that
overrides dozens of constants: genesis block hash, default
ports, proof-of-work difficulty, base58 version bytes, BIP9
deployment schedule, etc.

`src/chainparams.cpp` and `src/kernel/chainparams.cpp` are
where the different modes are defined. The functional test
framework (`test/functional/test_framework/test_node.py`)
builds on regtest exclusively.

### The underlying insight

**A well-defined "fast mode" where all production time
constants are accelerated is the single biggest multiplier on
test velocity.** Bitcoin's regtest bakes in a dozen knobs —
block time, difficulty, key rotation, activation thresholds —
so any test that depends on "what happens after N blocks" can
run in milliseconds instead of hours.

The other half of the insight: **make the mode a first-class
CLI flag with a documented contract**, so every test
framework speaks the same dialect. Bitcoin's `-regtest` is
famously stable — new test authors can rely on the behavior
without having to read 10 files of setup code.

### Does it translate?

**STRONG TRANSLATE and already half-implemented.** The testbed
architecture I proposed has Layers A/B/C/D which map almost
1:1 onto unit / regtest / signet / testnet / mainnet. What's
missing is the **config-level "regtest mode"** with accelerated
constants.

Concrete adoption (priority: high, should land alongside the
Layer-B docker-compose work):

- **Add `cfg.Regtest bool` config field** that, when true:
  - Sets `Publisher.RefreshInterval` to 5 seconds instead of 1
    hour.
  - Sets `Publisher.MinPutInterval` to 100 ms instead of 55 min.
  - Sets `companion.Publisher.Interval` to 10 seconds.
  - Sets `reputation.SeedHalfLife` to 5 minutes (needs a
    refactor to make it runtime-configurable — today it's a
    package constant).
  - Sets `DHTBootstrapNodes` to a single local bootstrap node.
  - Logs `swartznet.regtest_mode_active` at startup so it's
    unmissable in the daemon log.
- **Add `--regtest` CLI flag** on `swartznet add` that sets
  the config field.
- **Testbed scenarios use `--regtest`.** Layer B containers
  set it in their entrypoint; testlab Cluster harness sets
  it on every spawned engine's config.

This is the single most impactful change from the Bitcoin
research. Everything in my earlier testbed proposal becomes
faster and simpler with a documented regtest mode.

---

## 5. BIP9 / BIP8 soft-fork deployment

### What Bitcoin does

When Bitcoin needs to deploy a new consensus rule (SegWit,
Taproot, etc.), it uses a "version bits" mechanism:

- Each new feature claims a bit in the block version field.
- Miners signal readiness by setting their bit on blocks they
  produce.
- A rolling window (say, 2016 blocks = ~2 weeks) counts the
  percentage of blocks signalling.
- When the percentage crosses a threshold (usually 95%), the
  feature activates on a well-defined block height.
- If the window expires without reaching the threshold, the
  deployment times out.

BIP9 defined this. BIP8 tightened the failure mode: instead of
timing out silently, BIP8 supports "mandatory activation" — if
the threshold isn't reached by the deadline, the feature
activates anyway. Used for Taproot to avoid the
"miner veto" problem that BIP9 had.

Key source: `src/deploymentinfo.cpp`, `src/versionbits.cpp`.

### The underlying insight

**Protocol evolution needs structured gating: a way to ship
new features that old clients can safely ignore, and a way to
measure adoption so you know when to deprecate the old
version.** Bitcoin's specific mechanism is tied to block
rewards and miner signaling, but the generalizable pattern is
capability negotiation with a rollout timeline.

### Does it translate?

**NO, but good framing.** SwartzNet doesn't have consensus
rules to deploy, so there's no block-height equivalent. But
the extension-capability pattern DOES translate — and
SwartzNet already has it:

- LTEP `m` dict advertises the extension name and version
- sn_search capability flags (`share_local`, `file_hits`,
  `content_hits`, `publisher`) are negotiated per-peer in the
  handshake
- The schema sentinel in the Bleve index is versioned (M2.0)
- The companion format is versioned (M11a)
- BEP-44 seq numbers order mutable item updates

The explicit mechanism BIP9 provides that we lack is an
**adoption metric** — "what fraction of peers on the network
speak the new extension version?". That would be useful for
deciding when to remove backward-compat code. But it's a
measurement tool, not a design change.

**Not applicable as code, but adopt the framing in
`docs/06-bep-sn_search-draft.md`**: every new sn_search wire
version should document its activation criteria and its
deprecation timeline, the same way BIPs do.

---

## 6. Compact block relay (BIP152) — the short-ID insight

### What Bitcoin does

Blocks are ~1-4 MB and contain thousands of transactions that
peers probably already have in their mempool. Rather than
sending the whole block every time, BIP152 sends a "compact
block" containing:

- Block header (80 bytes)
- Short transaction IDs (6 bytes each, derived from the
  transaction's txid + a per-block nonce via SipHash-2-4)
- Any transactions the sender knows the receiver doesn't have

The receiver reconstructs the block by looking up each short
ID in its mempool. If a transaction is missing, it requests it
via `getblocktxn`. Typical bandwidth savings: 95%+ on a
well-connected node.

### The underlying insight

**When syncing a snapshot, assume the receiver already has most
of the data and send deltas, not full payloads.** The specific
encoding (SipHash short IDs, mempool lookup) is Bitcoin-
specific, but the "delta sync" pattern shows up everywhere
from rsync to git packfiles to CRDTs.

### Does it translate?

**NOT APPLICABLE today, but a valid v2 optimization**:

SwartzNet's companion index torrents (F3, M11) ship the full
gzipped JSON every hour. When a publisher's index grows to
10+ MB, this is wasteful — most subscribers already have most
of the content from the previous sync.

Future optimization (post-v1, probably v2): publish **delta
companion indexes**. The publisher emits a base snapshot every
24h and hourly deltas pointing at the base, each containing
only the (added, removed, updated) torrent records since the
base. Subscribers fetch the base once and apply deltas
incrementally.

**Priority**: low. SwartzNet indexes are currently KB-scale,
not MB-scale. Revisit when publishers report bandwidth pain.

---

## 7. Tor / I2P first-class transport (BIP 155 / addrv2)

### What Bitcoin does

Pre-2019, Bitcoin's addr message format could only encode
IPv4 and IPv6 addresses. To support Tor hidden services,
Bitcoin Core added [BIP 155 addrv2](https://github.com/bitcoin/bips/blob/master/bip-0155.mediawiki):
a new address format that carries a network-id byte followed by
a variable-length address payload. Networks 1-2 are IPv4/IPv6,
3 is Tor v2 (deprecated), 4 is Tor v3 (onion), 5 is I2P, 6 is
Cjdns.

When a peer advertises its address via addr, Bitcoin Core
writes it into AddrMan under the appropriate network. Outbound
connection logic then picks peers across networks by policy —
e.g., "at least one outbound peer must be on Tor if Tor is
configured". The `-onlynet=tor` flag restricts outbound to the
Tor network entirely.

The node itself needs to be configured to reach those
networks: a SOCKS5 proxy for Tor (`-proxy=127.0.0.1:9050`), a
SAM bridge for I2P (`-i2psam=127.0.0.1:7656`).

### The underlying insight

**Anonymous transports should be first-class citizens in the
address format, not retrofitted as "special" case.** By making
addrv2 carry a network-id byte, Bitcoin avoided needing
separate message types or parallel AddrMan instances for Tor.
The same wire-level code paths handle everything.

### Does it translate?

**STRONG TRANSLATE.** SwartzNet's M13d threat model docs flag
publisher IP exposure as a v1-blocking concern. My initial fix
was "document the threat and ship --no-dht-publish". The BIP
155 pattern is a cleaner long-term answer: **make the publisher
identify as Tor/I2P at the wire level**, so DHT gets route
through the SOCKS proxy and the publisher's real IP never
reaches the put-target nodes.

anacrolix/dht/v2 already supports a pluggable network
interface. We'd need to wire a SOCKS5 UDP-associate path (or
fall back to publishing over an HTTPS relay as pkarr does) and
expose it via a config flag.

Concrete adoption (priority: medium, post-v1):

- **`cfg.TorSocksProxy string`** that routes ONLY the BEP-44
  put/get path through the proxy. BitTorrent swarm traffic
  stays on the clearnet (Tor Project discourages bulk
  torrent traffic, and SwartzNet bandwidth ≠ anonymity need).
- **Network-id byte in companion-subscriber follow list**. Let
  operators specify that publisher X should only be reached
  via Tor, so even a subscriber whose DHT is on clearnet will
  route pointer gets through Tor for that specific publisher.
- **Document the BIP 155 pattern** in
  `docs/07-bep-dht-keyword-index-draft.md` as the design we
  intend to follow for v1.1 key rotation + anonymous publish.

---

## 8. BIP 324 v2 transport — encrypted peer connections

### What Bitcoin does

Historically Bitcoin's peer connections were plaintext. An
observer on the path could read every transaction, every
block, every `getdata` request. BIP 324 adds an opt-in
encrypted transport: ChaCha20-Poly1305 with an ECDH handshake,
negotiated via a magic byte on the first message.

The encryption does NOT provide strong anonymity — the peer
you're talking to still knows your IP. What it does provide:

- **Path-level privacy**: ISPs and middleboxes can't read your
  transactions off the wire.
- **Detection resistance**: the encrypted handshake uses
  indistinguishable-random prefixes, so a simple port-80 DPI
  filter can't pick out Bitcoin traffic.
- **Integrity**: MAC-authenticated packets protect against
  mid-flight corruption.

Key source: `src/net.cpp` (v2 transport), 
[BIP 324](https://github.com/bitcoin/bips/blob/master/bip-0324.mediawiki).

### The underlying insight

**Clearnet peer traffic is a privacy leak that anonymous
overlays (Tor, I2P) can't fix.** Encryption between peers is
cheap, adds no consensus complexity, and eliminates an entire
class of middle-box attacks. The specific handshake (ECDH +
ChaCha20-Poly1305) is just the current best-practice choice.

### Does it translate?

**STRONG TRANSLATE.** SwartzNet currently inherits BitTorrent's
optional PeerConnection Encryption (BEP 8), which most
clients speak. That handles Layer L traffic. But Layer-S
(`sn_search`) piggybacks on the same peer wire, so its
messages get the same encryption as the rest of the connection
— which is *usually* encrypted but not guaranteed.

For Layer D (BEP-44), the queries and responses are plaintext
KRPC over UDP. An ISP can see every keyword you publish or
look up. This is a real privacy leak that BIP 324-style
encryption would close.

Concrete adoption (priority: low, research-heavy):

- Out of scope for v1. But **document the goal** in the threat
  model section of `docs/08-operations.md`: "Layer D
  keywords are visible on the wire; use a SOCKS proxy if this
  matters to you." BIP 324 for BEP-44 would need a BEP of its
  own, and that's a multi-year community process.

---

## 9. Functional-test framework

### What Bitcoin does

`test/functional/` in the Bitcoin Core repo holds ~200
scenario scripts. Each is a Python file that inherits from
`BitcoinTestFramework`, sets up N regtest nodes, and runs a
sequence of RPC calls + assertions.

Structure:
```
test/functional/
├── test_framework/
│   ├── test_node.py        # wraps one bitcoind process
│   ├── test_framework.py   # base class, N-node setup
│   ├── messages.py         # P2P wire format
│   ├── mininode.py         # lightweight peer impl
│   └── util.py             # assertion helpers
├── feature_segwit.py       # one feature
├── feature_taproot.py
├── wallet_balance.py
├── mempool_packages.py
└── ... ~200 more
```

A typical test:
1. `self.setup_clean_chain = True` — fresh regtest chain
2. `self.num_nodes = 3` — spin up 3 bitcoind processes
3. `self.extra_args = [["-opt=foo"], [], []]` — per-node flags
4. `self.run_test()` — the scenario logic
5. Scenario calls `self.nodes[0].generatetoaddress(...)` to
   mine blocks, `self.nodes[1].sendtoaddress(...)` to emit
   transactions, etc.
6. `self.sync_all()` to wait for gossip
7. Assertions via `assert_equal`, `assert_raises_rpc_error`

The framework also supports a "MiniNode" — a pure-Python peer
that speaks the wire protocol directly, used for testing how a
real bitcoind handles malformed or hostile messages.

### The underlying insight

**Per-scenario sub-processes with per-scenario temp dirs and
clean state is the right abstraction unit.** Bitcoin's
functional tests run real binaries, not library fakes, so they
catch the same class of bugs users see — process startup
races, RPC contract drift, lock ordering, file permissions.

The MiniNode pattern is the second insight: **a pure-code
reimplementation of the wire protocol gives you a way to fuzz
the real binary from the inside**. You can send deliberately
malformed messages, misordered sequences, bogus signatures —
things a real peer would never do — and assert the real node
handles them gracefully.

### Does it translate?

**STRONG TRANSLATE.** SwartzNet's Layer A (internal/testlab)
is the in-process equivalent of Bitcoin's functional-test
harness: spawn N engines, wire them together, run a scenario.
What Bitcoin does that we don't yet have:

1. **Scenarios are whole-file scripts**, not unit tests. Each
   scenario is one `*_scenario.go` file with one scenario
   function and a clear top-to-bottom story. Unit tests stay
   in `_test.go` for the primitives; scenarios live next to
   them but are named differently so you can pick what to run.
2. **MiniNode equivalent: a pure-Go fake peer** for Layer S.
   Today every testlab test uses a real `*engine.Engine`. A
   MiniPeer would implement the sn_search wire protocol
   directly (without anacrolix) so tests can deliberately
   send malformed bencode, violate the rate limit, replay
   old txids, etc. Good way to fuzz the real handler.
3. **Per-scenario documented contract**. Each scenario has a
   docstring explaining (a) what it verifies, (b) what would
   regress if it fails, (c) how long it takes. Makes the test
   suite legible.

Concrete adoption (priority: medium-high, incremental with
the testbed rollout):

- **Follow the scenario pattern** for M14e and beyond —
  `internal/testlab/scenarios/*_scenario.go` files with one
  scenario per file, long comments, and a consistent naming
  scheme.
- **Build a MiniPeer** after F3 companion scenarios work. One
  .go file that implements enough of the BEP-10 / LTEP wire
  format to handshake and exchange sn_search messages without
  anacrolix. Lets us test adversarial sequences cheaply.

---

## Recommended adoption list

Ordered by priority for v1.0.0 + immediate v1.1:

### HIGH priority (before v1.0.0 if time allows, else v1.1)

1. **Regtest mode** (§4). Single biggest multiplier on test
   velocity. Add `cfg.Regtest bool`, wire it through
   publisher intervals, companion intervals, and the DHT
   bootstrap. Every testbed layer benefits.
2. **DNS-seed pattern for the indexer seed list** (§1).
   Documented only, actual implementation in v1.1. The
   design constraint goes into
   `docs/07-bep-dht-keyword-index-draft.md` so future
   implementation doesn't second-guess it.
3. **Scenario-per-file pattern for testlab** (§9). The
   M14e F3 scenario should land as
   `internal/testlab/companion_scenario.go`, not
   `companion_test.go`. Every future scenario follows the
   same pattern.

### MEDIUM priority (v1.1)

4. **AddrMan-style peer book for Layer S** (§2). Tried/new
   tables, ASN-bucket diversity, feeler connections. Makes
   eclipse attacks on the sn_search layer expensive. Depends
   on Layer C chaos testing to validate.
5. **Misbehavior score + banlist for sn_search peers** (§3).
   Defence-in-depth on top of M12f rate limiter. Mirrors
   Bitcoin's banman.cpp. Local-only, never gossiped.
6. **MiniPeer pure-Go fake for sn_search** (§9). Unlocks
   adversarial scenario testing without spawning full
   engines.
7. **SOCKS proxy for BEP-44 put path** (§7). Closes the
   M13d threat-model gap. The research already flagged
   this as the honest v1 response — now it's backed by
   Bitcoin's precedent.

### LOW priority (v2)

8. **Delta companion indexes** (§6). Optimization for
   publishers whose indexes grow past 10+ MB. Not needed
   until someone reports bandwidth pain.
9. **BIP 324-equivalent encryption for BEP-44** (§8). Huge
   scope (new BEP, years of community process). Document
   the goal, defer indefinitely.

### NOT applicable

- **Proof-of-Work** (§0). Wrong problem.
- **BIP9/BIP8 soft-fork deployment** (§5). We have capability
  negotiation already; the specific mechanism is consensus-
  bound.

---

## Summary in one paragraph

Bitcoin Core's most transferable patterns to SwartzNet are its
**bootstrap architecture** (DNS seeds + gossip, §1), its
**peer-book design** (tried/new tables + ASN diversity, §2),
its **local misbehavior scoring** (§3), its **regtest mode** as
a test velocity multiplier (§4), its **first-class Tor/I2P
transport pattern** (§7), and its **functional-test framework
structure** (§9). Proof-of-Work, block validation, mempool
management, and chain reorg handling are all wrong-problem for
a distributed search network and should be explicitly
rejected. The #1 action item is adding a **regtest mode**
alongside the Layer-B docker-compose testbed work — it
instantly makes every subsequent scenario 100x faster.
