# SwartzNet: Integration Design for a BitTorrent Client with Built-in Text Search

## How to read this document

This is the synthesis of four research reports in this directory:

- `01-torrent-clients-comparison.md` — survey of libtorrent, transmission, anacrolix/torrent, rqbit, WebTorrent
- `02-tribler-deep-dive.md` — prior art: Tribler's search overlay on top of libtorrent
- `03-p2p-search-protocols.md` — prior art: aMule/Kad, GNUnet, Gnutella, RetroShare, YaCy, Freenet
- `04-bep-extension-points.md` — the BitTorrent protocol extension points we can legally use

This document is the bridge from those surveys to a concrete buildable plan. It resolves contested points, states the architecture, and enumerates exactly what to build and in what order.

---

## 0. Corrections to the research reports

Two claims in `01-torrent-clients-comparison.md` need correcting before we proceed, because the design below depends on them:

1. **anacrolix/torrent is licensed MPL 2.0**, not MIT. `LICENSE` in the cloned repo begins with "Mozilla Public License Version 2.0". MPL 2.0 is still permissive enough for our use (file-level copyleft, does not infect the rest of our codebase), but the distinction matters: any changes we make *inside anacrolix/torrent itself* must be published under MPL 2.0. New files we add to our own repo are unaffected. This is actually a useful discipline — it forces us to keep patches small and prefer extension-API usage over core hacks.

2. **Transmission does support BEP-10/LTEP**, contradicting the comparison report. Evidence from the cloned source: `libtransmission/peer-msgs.cc:150` has "in the LTEP handshake and will respond to when sent in an LTEP", line 630 declares `parse_ut_metadata()`, line 631 declares `parse_ut_pex()`, lines 947-957 explicitly send LTEP handshakes. Transmission remains disqualified for our use, but for the right reason: its **GPLv2/v3 license is incompatible with shipping a non-GPL product**, not because it lacks BEP-10.

Everything else in `01-torrent-clients-comparison.md` survives scrutiny, including the primary recommendation.

---

## 1. Scope: what exactly are we building?

The user's one-line brief: "a torrent client that has a built-in text search function for the content in their torrents, backwards compatible with existing torrent clients."

That one sentence hides three very different features, and the design must treat them separately:

| Feature | What it means | Prior art | Difficulty |
|---|---|---|---|
| **F1. Torrent-metadata search** | Type "ubuntu 24.04" → get a list of torrent infohashes whose *name* or *tags* match. | Tribler, aMule/Kad | Medium |
| **F2. File-list search** | Same query, but also match if the text appears in the *filename of any file inside the torrent* (not just the torrent name). | (none distributed) | Medium |
| **F3. File-content search** | Same query, but also match if the text appears in the *actual text content* of a file inside the torrent (e.g. an ebook, a PDF, a source code file). | (none distributed; centralised: Google Drive, Everything, recoll) | **Hard** |

Tribler ships F1 only, and the `02-tribler-deep-dive.md` report confirms this explicitly: *"Indexed field: Only `title` (torrent name). NOT indexed: File lists, descriptions, tags, or content within torrents."* Everyone else mentioned in the research also stops at F1.

The user asked for "content in their torrents" — which, read literally, is F3. Before committing to anything, we need to be honest about the constraints of each feature.

### 1.1 Why F3 is hard in a pure P2P world

For F3 to work, someone has to:

1. **Extract text** from files (PDF, EPUB, DOCX, plain text, source code, subtitles, etc.). This is CPU-bound but tractable per file.
2. **Build an inverted index** (word → list of `(torrent_infohash, file_index, byte_offset)` tuples). For 10 TB of text across the network, the raw index can itself be hundreds of GB.
3. **Distribute the index** such that a keyword query from any peer reaches the index shard holding that keyword. This is where Kademlia + BEP-44 hits its 1000-byte ceiling hard: you cannot fit an inverted-index postings list for "the" into a single 1000-byte DHT item.
4. **Trust the index** against publishers who lie about what's in their torrents (pure spam) or who publish index entries for torrents they don't even seed (link spam).

### 1.2 Design decision: ship F1 + F2 first, make F3 hybrid-local

Rather than trying to build a global distributed full-text index (which would be a research project, not a product), we split F3:

- **Locally indexed content is searchable locally.** Every torrent *you* download (or seed) gets its file contents text-extracted and added to a local full-text index. This is strictly better than Tribler's title-only search and gives users the "search what's in my torrents" feature immediately, with no network effect required.

- **Distributed F1+F2 give you *discoverability*.** The network-level search is torrent-name + file-list. "Ubuntu 24.04 iso" is something a global index can serve. "The exact paragraph from chapter 7 of some ebook" is served from the user's local index, after they've downloaded the torrent.

- **F3-over-the-network is a future, opt-in feature.** Once a user has a local full-text index, they can *choose* to publish a compact content index (as a companion torrent, following the BEP-46 pointer pattern from `04-bep-extension-points.md` §7.3) that others can download. Searchers interested in F3 fetch these companion indexes and query them locally. This is not in v1.

This tiering is the most important design decision in the entire project. It converts an intractable distributed-systems problem into three smaller, mostly-independent problems.

---

## 2. Layered architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│  Frontends (three, sharing the same daemon wiring)                       │
│  - CLI            cmd/swartznet  (scriptable, no CGo)                    │
│  - Web UI         go:embed HTML/CSS/JS, served on localhost:7654         │
│  - Native GUI     cmd/swartznet-gui + internal/gui (Fyne v2, CGo)        │
├──────────────────────────────────────────────────────────────────────────┤
│  internal/daemon  (shared startup: engine + indexer + companion + httpapi)│
├──────────────────────────────────────────────────────────────────────────┤
│  Local HTTP API  (localhost only, loopback bind)                         │
│  - POST /search    POST /torrent          POST /confirm    POST /flag    │
│  - GET  /torrents  POST /torrents/*/{pause,resume,indexing}              │
│  - DELETE /torrents/*                                                    │
│  - GET/POST /capabilities    GET /status  GET /index/stats   GET /healthz│
│  - GET/POST /companion {refresh,follow,unfollow}                         │
├──────────┬──────────────────────┬────────────────┬───────────────────────┤
│ Local    │ Remote (peer-wire)   │ Remote (DHT)   │ Companion-index       │
│ Index    │ sn_search over LTEP  │ BEP-44 keyword │ layer (BEP-46 style   │
│ (Bleve)  │                      │ items          │  pointer torrents)    │
├──────────┴──────────────────────┴────────────────┴───────────────────────┤
│  Ingestion Pipeline   (per-torrent opt-in via Engine.SetTorrentIndexing) │
│  - Piece verify hook  →  file reassembly  →  text extractor  →  Bleve    │
│  - Torrent add hook   →  metadata summary  →  DHT publish queue          │
├──────────────────────────────────────────────────────────────────────────┤
│  Torrent creation   (engine.CreateTorrent / CreateTorrentFile)           │
│  - Wraps metainfo.Info.BuildFromFilePath + bencode + atomic file write   │
├──────────────────────────────────────────────────────────────────────────┤
│  BitTorrent Engine: anacrolix/torrent                                    │
│  - BEP-3 peer wire, BEP-5 DHT, BEP-9 ut_metadata,                        │
│    BEP-10 LTEP, BEP-11 ut_pex, BEP-44, BEP-46, BEP-52                    │
└──────────────────────────────────────────────────────────────────────────┘
```

Everything above the "BitTorrent Engine" line is ours. Everything below is vendored as a library via `go.mod`.

The layer boundaries are strict: the ingestion pipeline does not know about the peer-wire protocol; the peer-wire `sn_search` handler does not know how Bleve encodes its index; the DHT publisher does not know about the local HTTP API. The only thing that crosses layers is a small `SearchResult` struct shared by the indexer and the query engine.

The three frontends all call `internal/daemon.New()` to obtain a fully-wired node; this is the single source of truth for startup order and resource cleanup. The CLI and the native GUI link the same packages directly; the web UI is reached through the HTTP API like any external tool, even though it ships embedded in the same binary.

### Per-torrent indexing control (v0.3.0)

Every torrent added to SwartzNet defaults to being indexed: its torrent-level document (name, file list, trackers) is written to Bleve within seconds of metadata arrival, and as each file finishes downloading the extraction pipeline (PDF / EPUB / DOCX / ODT / plaintext / subtitles) feeds the text into Bleve content documents.

Users can opt a specific torrent out via `Engine.SetTorrentIndexing(hex, false)`. The flag is checked inside `autoIndex` (torrent-level) and `ingestFileEvents` (content-level) and takes effect prospectively — already-indexed chunks remain in the index unless the caller additionally invokes `indexer.DeleteContentForTorrent`. The flag is surfaced:

  - In the GUI as a checkbox on the Add Magnet dialog and as a "Toggle Index" toolbar button in the Downloads tab.
  - In the HTTP API as `POST /torrents/{infohash}/indexing {"enabled": true|false}`.
  - In the CLI as a future `swartznet index off <infohash>` subcommand (not yet implemented).

The global `--no-index` CLI flag remains the stronger switch: it prevents Bleve from being opened at all, so no subsystem (including Layer D publishing) sees any torrent.

### Torrent creation (v0.3.0)

SwartzNet can build new `.torrent` files from local content via `Engine.CreateTorrent(CreateTorrentOptions)` and `CreateTorrentFile(opts, outPath)`. Both wrap `metainfo.Info.BuildFromFilePath` plus bencode serialization and an atomic temp-file rename for the on-disk variant. The GUI surfaces this as "Create Torrent" in the Downloads toolbar, accepting:

  - Root (file or folder), auto-detected as single-file or multi-file.
  - Piece length (Auto / 64 KiB / 256 KiB / 1 MiB / 2 MiB / 4 MiB / 8 MiB / 16 MiB) — Auto uses `metainfo.ChoosePieceLength`.
  - Trackers (one per line), webseeds (BEP-19), comment, private flag (BEP-27), output path.
  - Optional "start seeding immediately" which adds the MetaInfo to the engine and begins seeding from the same Root.

Piece hashing is synchronous I/O. The GUI runs it in a background goroutine and shows a "Hashing pieces..." modal with `ProgressBarInfinite` until completion — minutes for 100+ GiB, seconds for small folders.

---

## 3. Why anacrolix/torrent is the base (confirmed)

The comparison report's top-line recommendation is correct even after the two corrections in §0. Reviewing the criteria that actually matter for this specific project:

| Requirement from §2 | Why anacrolix/torrent satisfies it |
|---|---|
| We need a **piece-completion hook** so the ingestion pipeline can extract text as files finish. | `callbacks.go:11-40` defines a `Callbacks` struct with `PieceStateChange` / `StatusUpdated` slices. We can register a closure that gets called every time a piece verifies. |
| We need to **dynamically register a new BEP-10 extension name** (`lt_search`) without forking the library. | `ltep.go:12-71` defines `LocalLtepProtocolMap` with `AddUserProtocol(name)`. This is purpose-built for what we need. No other client has this. |
| We need to **send and receive custom LTEP messages** from userspace. | `peerconn.go:1037` dispatches inbound extended messages to `Callbacks.PeerConnReadExtensionMessage`, which is a slice of user-provided handlers. `peerconn.go:1366` provides `WriteExtendedMessage(name, payload)` for outbound. |
| We need to access the **mainline DHT for BEP-44 get/put** for keyword indexing. | `dht.go` wraps `github.com/anacrolix/dht/v2`, which already has a `PutMutable` / `GetMutable` API. We can reach past the torrent library and use the DHT library directly when we need to. |
| We need to **ship a proprietary product** (or at least retain the option to). | MPL 2.0 is file-level copyleft. Our own code files are unaffected. Patches to anacrolix files must be MPL; we minimise those by routing everything through callbacks and the ltep API. |
| We need the **engine to be embeddable in a single binary** with our search layer. | Go static binaries. `go build` on the whole project produces one executable. |

The comparison report gave libtorrent 6.5/10 and anacrolix/torrent 9.5/10. The gap is real. libtorrent's plugin API is more mature in absolute terms, but it requires C++ subclassing and recompilation, and it has **no piece-completion callback** — only a periodic tick. That alone would force us to write our own piece-verification shadowing code, doubling the ingestion complexity.

### 3.1 A note on bitmagnet

`01-torrent-clients-comparison.md` mentions that **bitmagnet** (a DHT crawler and search engine) is built on anacrolix/torrent. Bitmagnet is worth studying as a reference for doing large-scale DHT indexing in Go — particularly its `sample_infohashes` (BEP-51) scraping and its Postgres full-text indexing. We will not use bitmagnet directly because (a) it indexes by crawling BEP-51 + BEP-9, which is one-way (network → bitmagnet's central DB), not P2P bidirectional, and (b) it's AGPL. But we can borrow design patterns: the piece-verify → text-extract → index pipeline is conceptually similar to bitmagnet's ingest → extract → Postgres pipeline.

---

## 4. The three search layers

Drawing the three layers explicitly because they each have different latency, completeness, and privacy properties.

### 4.1 Layer L — Local full-text index

**What it indexes:** everything you've downloaded or added locally. Torrent name, trackers, file list, *and the text contents of files*.

**Data store:** [Bleve](https://github.com/blevesearch/bleve) — the pure-Go full-text search library. Alternatives considered:

| Engine | Pros | Cons | Verdict |
|---|---|---|---|
| **Bleve** (Go) | Single-binary, mature, BM25, stemming, prefix, Unicode. Same-language as anacrolix/torrent. | Slower than C-based Tantivy; index files are larger. | **Pick.** Zero cgo friction, one go.mod entry. |
| Tantivy (Rust) | Fastest pure-Rust, comparable to Lucene. | Requires cgo bridge or a separate process. | Only if benchmarks force it. |
| SQLite FTS5 | Already used by Tribler (`02-tribler-deep-dive.md` §2, store.py:85-88). Excellent Unicode and stemming. | Less flexible ranking; concurrent writes need care. | Strong runner-up; use if Bleve becomes a maintenance burden. |
| Meilisearch / Elasticsearch | Best UX, but are *servers*, not libraries. | Violates single-binary goal. | Out. |

The schema we want:

```
Document type: Torrent
  infohash:     keyword  (not tokenized, exact-match)
  name:         text     (full-text, porter stemming, prefix 2-5)
  tags:         text     (lowercased, split on comma)
  trackers:     keyword  (for "find by tracker")
  added_at:     datetime
  size_bytes:   numeric
  seeder_count: numeric  (updated from swarm stats, enables seeder ranking)
  files:        nested_text (see below)
  content:      nested_text (see below)

Nested type: File
  path:           text     (tokenized on path separators)
  mime:           keyword
  size:           numeric
  pieces_root:    keyword  (BEP-52 per-file merkle root, if hybrid torrent)

Nested type: Content
  file_index:  int        (index into torrent file list)
  text:        text       (actual extracted text, chunked ~10KB/doc)
  lang:        keyword    (detected)
  offset:      numeric    (byte offset of this chunk in the source file)
```

The `Content` nested type is what Tribler lacks and what gives us F3 locally. Only files whose text is extractable and whose size is below a configurable cap (default 100 MB) are indexed; everything else is represented only by its `File` record.

**Text extractors** (plugin interface, one implementation each to start):
- `.txt`, `.md`, `.csv`, `.log`, `.json`, `.xml`, source code → UTF-8 decode with chardet fallback
- `.pdf` → [unipdf](https://github.com/unidoc/unipdf) or `pdftotext` via `os/exec`
- `.epub` → [gepub](https://github.com/taylorskalyo/goreader) + XHTML parser
- `.docx`, `.odt` → unzip + XML text extraction
- `.srt`, `.vtt` → subtitle parser (often the most valuable content in a movie/TV torrent for discovery!)
- Anything else → store filename only, no content indexing

Lang detection via `github.com/pemistahl/lingua-go` so results can be language-filtered.

**Size budget.** Bleve's Scorch index is typically ~20-30% the size of the indexed text. For 100 GB of downloaded torrent content with maybe 5 GB of extractable text, expect a ~1.5 GB Bleve index. That's acceptable.

### 4.2 Layer S — Swarm (peer-wire) search

**What it indexes:** whatever the peer on the other end of a BitTorrent connection chose to publish from its own Local index. Format TBD per §5.

**Mechanism:** our `lt_search` BEP-10 extension (§5). Query goes to a connected peer over their existing TCP BitTorrent connection; response comes back over the same connection. Works only between two search-capable peers.

**Latency:** one RTT, maybe 50-500ms in practice.

**Completeness:** bounded to the peer-set you already have. If you're connected to 50 peers and each has indexed ~1000 torrents, you effectively query a 50K-torrent pool. This is small but *fast* and *stays close to the swarm you're already in*. The design assumption: the peers you're already talking to are good search sources because you share interests (you both downloaded this torrent).

**Privacy:** your query keywords are visible in plaintext to the peer you ask. No onion routing; bring your own VPN if that matters.

**Matches Tribler's approach but with a better peer selection strategy.** Tribler uses "20 random peers from the IPv8 overlay" which is uncorrelated with what you actually want to search for (`02-tribler-deep-dive.md` §3, §8). We use "peers already in this user's swarms" which is *topically clustered* by construction — if you're in an "ubuntu iso" swarm, those peers are more likely to know about related torrents than 20 random strangers.

### 4.3 Layer D — DHT keyword index

**What it indexes:** keyword → list of infohashes, stored in mainline DHT as BEP-44 mutable items, exactly as `04-bep-extension-points.md` §3 describes.

**Mechanism:** BEP-44 `get`/`put` via the existing anacrolix DHT library. No new DHT verbs. No new reserved bits. No new handshake.

**Layout:** per `04-bep-extension-points.md` §3.0, a keyword index entry is:

```
pubkey = our ed25519 key   (per-client, persistent)
salt   = UTF-8 lowercase keyword token  (e.g. "ubuntu")
target = SHA1(pubkey || salt)
v      = {
  "ts":   <unix timestamp of this snapshot>,
  "hits": [
    { "ih": <20-byte sha1 infohash>,
      "n":  <short name, up to 60 bytes>,
      "s":  <seeders last seen>,
      "f":  <file count>
    },
    ...
  ],
  "more": <0 | 1>   // 1 if there are more shards at salt = "ubuntu#1", ...
}
```

Per `04-bep-extension-points.md` §3.1: a typical entry like the above fits about 10-15 hits per 1000-byte BEP-44 value. When a single publisher indexes more than ~15 torrents for a keyword, they spill into additional shards at `salt = "ubuntu#1"`, `"ubuntu#2"`, etc., with the root at `salt = "ubuntu"` carrying `"more": 1` and a `"n_shards"` counter.

**Publication strategy.** Per `03-p2p-search-protocols.md` §7.5, we publish conservatively:

- When a user adds or creates a torrent, we tokenize the torrent name (lowercased, Unicode-aware, min 3 bytes per token, skip stopwords in user's locale).
- For each of the top ~10 tokens (frequency-capped to avoid spamming "the", "video", "mp4"), we enqueue a publish task.
- The publish task does: `get` current item at `(our pubkey, token)` → merge our new hit into the list → bump `seq` → sign → `put` to the 8 closest nodes.
- Per BEP-44, refresh every ~1 hour. We only refresh items that have had activity (a download, a seed session) in the last 24 hours; stale items are allowed to expire.

**Lookup strategy.** Since the lookup target is `SHA1(pubkey || salt)`, **the searcher needs to know which pubkeys to query**. This is the crux of BEP-44's limitation: it's content *under a publisher's namespace*, not a shared global keyword bucket. We need a bootstrap mechanism for "which publishers should I query for keyword X?" Two options:

1. **Well-known indexer pubkeys.** Ship a hardcoded list of ~20 "seed indexer" pubkeys with the client. These are run by us (the project) and by early cooperating community members. A query fans out GETs to all ~20 in parallel, unions the results. This is analogous to trackers in classic BitTorrent: centralised enough to work, decentralised enough to not be a single point of failure.

2. **Gossip-discovered indexer pubkeys.** Every client is also a small indexer for itself. When you connect to a peer (layer S), their `lt_search` handshake can include their pubkey. You add it to your "known indexers" set. Over time, your set grows from 20 seeds to hundreds or thousands of known-good indexers.

   To prevent the set from growing without bound, prune by recency and by "observed useful result rate." A pubkey that consistently answers queries with 0 hits gets demoted.

We ship both. Start queries against the well-known seeds; union with results from the top N gossip-discovered indexers by quality score.

**Spam mitigation.** Drawing on `03-p2p-search-protocols.md` §1.4 (aMule learned the spam lesson the hard way), §2.4 (GNUnet solved spam by signing), and §7.5 (the staged approach recommendation):

- **Signatures are already free.** BEP-44 mutable items are signed by the publisher's ed25519 key. An attacker can't forge a hit *under someone else's pubkey*. They can only publish junk under their own pubkey, which the searcher can then ignore.
- **Per-pubkey reputation.** For each known indexer pubkey, track (total_hits_returned, hits_that_downloaded_successfully, hits_user_marked_as_spam). The ratio is the reputation. Publishers below threshold don't get queried; publishers at threshold get their results demoted in the union.
- **Client-side Bloom filter of known-good infohashes.** Per `03-p2p-search-protocols.md` §7.4. Your client maintains a Bloom filter seeded from torrents you (or your cohort) have actually downloaded without flagging them as junk. Results that hit the filter go to the top of rankings; results that don't are still shown, just lower.
- **No "publishers I don't know" bucket.** In v1 we do not query pubkeys below a reputation threshold at all. Users can opt into "explore mode" later.

**Privacy.** The keyword goes over the wire *hashed* (as part of the SHA1 target in BEP-44 `get`). DHT nodes contacting you see only the target hash. The actual plaintext keyword is only known to the querier and (by reverse lookup) to the publisher who originally minted that target. This is roughly the GNUnet property (`03-p2p-search-protocols.md` §2.6) but cheaper: we don't need GNUnet's RSA-key-per-query because we're not trying to prevent offline keyword-recovery attacks (someone with a dictionary can always try hashing common keywords to infer what you queried; we don't care).

---

## 5. The `lt_search` BEP-10 extension wire format

This is a first-draft spec. It lives here so it can be iterated in one place and eventually turned into a draft BEP per `04-bep-extension-points.md` §6.

### 5.1 Handshake advertisement

In our LTEP handshake, we add `sn_search` to the `m` dictionary. We use the prefix `sn_` (for SwartzNet) rather than `lt_` because `lt_` is historically libtorrent's; see `04-bep-extension-points.md` §6.

```
{
  "m": {
    "ut_metadata": 2,
    "ut_pex":      1,
    "sn_search":   7    // example: our chosen per-direction extension ID
  },
  "metadata_size": 31235,
  "p":    51413,
  "v":    "SwartzNet 0.1",
  "reqq": 250,

  // new: version and capability signalling for sn_search
  "sn_search_v":   1,             // integer, protocol version
  "sn_search_cap": "L0F0C0P0"     // capability string (see below)
}
```

The `sn_search_cap` string is four 2-char fields packed: `L<level>F<level>C<level>P<level>` where:

- **L** = how much of our Local index we're willing to share for other peers' queries. `L0` = nothing (pure leecher of search). `L1` = share hits for torrents in the current swarm only. `L2` = share hits for all torrents in our Local index.
- **F** = File-list search support. `F0` = only torrent-name hits. `F1` = file-list hits.
- **C** = Content-level search support. `C0` = no content hits. `C1` = content hits (Layer-L F3).
- **P** = Publishing support. `P0` = not a DHT publisher. `P1` = DHT keyword publisher (publishes to Layer D).

This lets us evolve the feature set without version-number explosion. A client that only speaks `L1F1C0P0` can still talk to a `L2F1C1P1` client; they just intersect capabilities.

### 5.2 Message types

All `sn_search` messages are bencoded dicts inside the standard LTEP envelope from `04-bep-extension-points.md` §2. The dispatch key is `msg_type`:

- `0` — **query**
- `1` — **result**
- `2` — **reject**
- `3` — **peer_announce** (gossip of other search-capable peers, see §5.4)

#### 5.2.1 Query (msg_type 0)

```
{
  "msg_type": 0,
  "txid":     <u32>,
  "q":        "ubuntu 24.04",        // free text
  "scope":    "nfC",                 // subset of [n,f,c]: n=name, f=filelist, c=content
  "limit":    50,                    // max hits requested
  "lang":     "en",                  // optional language filter
  "min_size": 0,                     // optional byte-size filter
  "max_size": 0,                     // 0 = no max
  "not_ih": [                        // optional: infohashes the querier already has
    <20-byte blob>, <20-byte blob>, ...
  ]
}
```

`scope` lets the querier ask "don't run content search even if you support it", which matters when the querier wants cheap results fast. If the responder's `sn_search_cap` lacks a level, requested scope values for that level are silently ignored.

`not_ih` is an optional de-dup hint: the querier tells the responder "I already have these infohashes, don't bother sending them again." Keeps response size down on successive refinement queries.

#### 5.2.2 Result (msg_type 1)

```
{
  "msg_type": 1,
  "txid":     <u32>,                 // same as the query
  "total":    123,                   // total hits in responder's index (informational)
  "partial":  0,                     // 0 = complete, 1 = truncated
  "hits": [
    {
      "ih":    <20-byte sha1 infohash>,
      "ih2":   <32-byte sha256 infohash>,   // optional, BEP-52 hybrid
      "n":     "ubuntu-24.04-desktop-amd64.iso",  // torrent name
      "s":     42,                    // seeders seen by responder
      "l":     13,                    // leechers
      "sz":    6195404800,            // total bytes
      "t":     1712649600,            // unix timestamp added to responder's index
      "rank":  870,                   // responder's BM25*health score, 0-1000
      "matches": [                    // optional, only if scope >= f or c
        {
          "fi":  4,                    // file index in torrent
          "fp":  "casper/vmlinuz",     // file path, omitted if too large
          "pr":  <32-byte merkle root>,// BEP-52, optional
          "sn":  "...context snippet around match...",
          "off": 12345                 // byte offset of match in file
        }
      ]
    },
    ...
  ]
}
```

The `matches` field is what distinguishes this from Tribler: Tribler only ever returns `(infohash, name, seeders)`. We can return actual file-level matches and even content-level snippets. A peer who has torrent X in its local index with the content indexed can say "I matched 'foo' at chapter 3 of the EPUB at file index 12, byte offset 9084". Whether that's actually *useful* depends on how much text peers are willing to index and share — but the protocol permits it.

A responder that implements only F1 (torrent-name search) sends hits with `matches: []` or without the `matches` field. The querier degrades gracefully.

#### 5.2.3 Reject (msg_type 2)

```
{
  "msg_type": 2,
  "txid":     <u32>,
  "code":     <integer>,
  "reason":   "rate_limited"
}
```

Codes:
- `0` rate-limited (try again later)
- `1` too-expensive (responder can't answer queries of this scope right now)
- `2` unsupported-scope (your `scope` asked for something my cap says I don't do)
- `3` query-too-broad (query token distribution too common, e.g. just "the")
- `4` shutting-down

#### 5.2.4 peer_announce (msg_type 3)

```
{
  "msg_type": 3,
  "peers": [
    {"ip": <4|16 bytes>, "port": <u16>, "cap": "L1F1C0P0", "pk": <32-byte pubkey or absent>},
    ...
  ]
}
```

This is the gossip primitive for §4.3's "gossip-discovered indexer pubkeys." Whenever a peer connects a new `sn_search`-capable peer, it may opt to push an announcement to peers it's already talking to. Rate limit: at most one `peer_announce` per connection per 10 minutes, at most 20 entries per message. Modelled on BEP-11 PEX.

### 5.3 Ranking

We ship a simple default and leave room for overrides. The default per-hit score is:

```
rank(hit) = 0.4 * bm25(query, hit.name)
          + 0.2 * bm25(query, hit.files)  // if scope includes f
          + 0.2 * bm25(query, hit.content) // if scope includes c
          + 0.1 * log(hit.seeders + 1)
          + 0.1 * freshness(hit.added_at)
```

where `freshness(t) = exp(-(now-t)/30_days)`. The rank is multiplied by 1000 and rounded to integer so it fits in a small bencoded int.

Responders compute this locally and embed it as `rank`. Queriers are free to re-rank the union after merging from multiple peers.

### 5.4 Rate limiting and back-pressure

- Per-connection: one outstanding query at a time. A second query while the first is pending elicits a `reject` with `code: 0`.
- Per-peer, per-hour: 100 queries max (generous; intended to throttle bots, not humans).
- Per-scope: content-level searches (scope `c`) get a tighter budget because they are more expensive. Default 10 per hour.
- The responder computes these; the querier must handle `reject` gracefully by backing off.

### 5.5 What lives on the wire vs in the DHT

Explicit mapping:

| Question | Layer | Why |
|---|---|---|
| "I'm connected to peer P; what do they know about 'X'?" | S (`sn_search` over BEP-10) | One RTT, scoped to current peer set. |
| "Across the network, who has 'X'?" | D (BEP-44 gets against known indexers) | Only DHT scales beyond current peer set. |
| "Inside torrent T that I already have, where does 'X' appear?" | L (Bleve on disk) | The only layer that knows text content. |
| "For torrent T, who in my swarm has the content-indexed version?" | S with `scope: c` | Peers advertise their content-index capability in their cap string. |

If a user types "ubuntu iso" into the search box, the UI fires all three layers in parallel, merges results, dedupes by infohash, and ranks by the weighted sum. The user sees local hits first (they're instant), then swarm hits (100ms-ish), then DHT hits (1-5 seconds).

---

## 6. Ingestion pipeline

This is what fills Layer L.

```
torrent add / piece verified
        │
        ▼
 ┌───────────────────┐
 │ callbacks.go hook │  anacrolix/torrent fires Callbacks.PieceStateChange
 └─────────┬─────────┘
           ▼
 ┌───────────────────────┐
 │ file-complete tracker │  pieces → files (BEP-3 piece layout)
 └─────────┬─────────────┘
           │ (once a file is fully downloaded)
           ▼
 ┌───────────────────────┐
 │ mime sniff            │  extension + magic bytes
 └─────────┬─────────────┘
           │
           ▼
 ┌───────────────────────┐
 │ extractor dispatch    │  pdf / epub / docx / text / subtitle / source / ...
 └─────────┬─────────────┘
           │ (UTF-8 text stream)
           ▼
 ┌───────────────────────┐
 │ chunker + lang detect │  ~10KB chunks, lingua-go language detection
 └─────────┬─────────────┘
           │
           ▼
 ┌───────────────────────┐
 │ Bleve batch indexer   │  asynchronous, batched at 100 docs
 └───────────────────────┘
```

**Backpressure.** The extractor and Bleve indexer run in their own goroutines. The file-complete → extract transition is bounded by a channel with capacity 64; if the channel is full, piece-completion callbacks still return immediately, they just drop the extract request. A background scan picks up missed files on the next hour.

**Idempotency.** Extraction is keyed by `(infohash, file_index, file_content_sha256)`. If a file is re-downloaded or a torrent is re-added, we don't re-extract the same content.

**Opt-out.** Users can mark a torrent as "don't index" at add time. We also default to not indexing files whose path matches a user-editable deny glob (default includes `**/.git/**`, `**/*.bin`, etc.). Per-file override via the UI.

**Privacy.** The Local index is stored on disk unencrypted by default but supports an optional passphrase (AES-256-GCM at rest via [age](https://github.com/FiloSottile/age)) for users who want it.

### 6.1 Why we hook piece-complete, not file-complete

anacrolix/torrent will happily tell us when a file is complete, but the piece-complete callback arrives earlier and lets us preflight extractor dispatch. For small text files (fit in a single piece), we can start extraction the moment that piece verifies, rather than waiting for the whole torrent. For large files the piece-granular callback lets us track progress for the UI without polling.

Reference: `callbacks.go:11-40` in anacrolix/torrent exposes `PieceStateChange` as a slice of `func(Torrent, PieceStateChange)`. We register our ingestion pipeline's piece-tracker on it at `Client` construction time.

---

## 7. DHT publisher: keeping Layer D fresh

Responsibilities:

1. **Topic extraction.** On torrent-add and on periodic local-index scan, extract the top-K keywords per torrent. Default K=8. Stopword removal per the user's configured locales. Keyword tokenization follows the same rule as Bleve's analyzer so the tokens we publish are the same tokens someone would type to find this torrent.

2. **Shard allocator.** For each `(our_pubkey, keyword)` pair, maintain a local "shard manifest": how many shards we've published, which hits live in which shard, which shards are hot enough to keep refreshing.

3. **Publish queue.** A single worker goroutine drains a priority queue of `(keyword, reason)` pairs. Priorities:
   - High: new torrent, needs immediate publish
   - Medium: scheduled refresh (hourly per BEP-44)
   - Low: background re-verification (every 6 hours)

4. **Rate budget.** Publishes are rate-limited per-second to avoid flooding the DHT. Default: 5 puts/second across all keywords. Cap is configurable.

5. **Graceful degradation.** If the DHT put fails (no responding nodes, timeout), the item stays enqueued with exponential backoff. After 3 failures we mark the keyword "unhealthy" and drop to a daily retry schedule.

6. **BEP-42 compliance.** The anacrolix DHT library handles this automatically (it derives our node ID from our IP). We just make sure we're using its public API and not overriding the ID.

The publisher writes stats to `/var/log/swartznet/publisher.json` (or equivalent per-OS path) so we can measure publish success rate in the field. This is critical because spam from aMule's past (`03-p2p-search-protocols.md` §1.4) means we need to *prove* the DHT layer is working for our users — otherwise we'll silently fall back to peer-wire-only search without realising it.

---

## 8. Backwards compatibility — the explicit test matrix

Per `04-bep-extension-points.md` §5, there are four compatibility directions. Here's the test matrix for each. All four MUST pass for v1.

### 8.1 Vanilla client downloads from us

| Test | Expected behaviour |
|---|---|
| qBittorrent 5.x connects, requests pieces of a torrent we're seeding | Bytes flow, qBittorrent sees a normal peer. No `sn_search` message is ever sent in this direction. |
| qBittorrent requests metadata via BEP-9 | We serve ut_metadata exactly as anacrolix/torrent does already (unmodified). |
| qBittorrent sends `ut_pex` | We handle it normally. Our `sn_search_v` and `sn_search_cap` keys in the LTEP handshake dict are ignored (they live outside qBittorrent's known schema). |

### 8.2 We download from a vanilla client

| Test | Expected behaviour |
|---|---|
| We fetch a magnet link whose swarm has only Transmission peers | BEP-9 metadata fetch completes, we get the .torrent, pieces flow. `sn_search` is never advertised to them (they don't list it in `m`) and we never send `sn_search` messages. |
| We open a connection to a peer running `libtorrent 2.x` | Same; their handshake lists `ut_metadata` and `ut_pex`, not `sn_search`. We don't push `sn_search` to them. |

### 8.3 DHT stays healthy for non-participants

| Test | Expected behaviour |
|---|---|
| A qBittorrent node sends us `ping` | We reply. |
| A qBittorrent node sends us `get_peers` for an infohash we're announcing | We reply with peer list + token, exactly as anacrolix/dht already does. |
| A qBittorrent node sends us `announce_peer` | We accept it and store the announcement. |
| A qBittorrent node sends us `get` / `put` (BEP-44) for an item that happens to be our keyword index | We treat it exactly like any other BEP-44 item. It *is* a BEP-44 item. Their client doesn't know it's "ours," and that's fine — they serve it under the same rules they serve any mutable item. |

The last row is the most important: **there is no distinction between "our" DHT items and "anyone else's." We use the standard BEP-44 protocol, so our items look like everyone else's BEP-44 items. Vanilla clients neither know nor care what we put in `v`.** This is the entire reason we picked BEP-44 over a custom DHT verb.

### 8.4 Two SwartzNet clients interoperate with search

| Test | Expected behaviour |
|---|---|
| Both advertise `sn_search` in LTEP handshake | Search channel is negotiated; queries and results flow. |
| One has `C1` (content search), the other `C0` | Queries with `scope: "c"` from the C1 client to the C0 client return `reject code: 2`. Name/filelist queries work normally. |
| Both run the DHT publisher | Both add each other's pubkeys to their gossip-discovered indexer set after first `lt_search` handshake. |
| One has `sn_search_v: 1`, the other `sn_search_v: 2` (future) | The `v: 1` client ignores fields it doesn't understand in messages from the `v: 2` client (bencode's dict-based schema gives us forward compatibility for free). |

---

## 9. Threat model and what we explicitly don't protect against

The user asked for "a torrent client with text search." That's a product, not a threat model, so we have to pick one. Here's the one we're designing against:

**In scope:**
- **Result spam.** Publishers who push garbage infohashes under popular keywords. Mitigations: pubkey reputation (§4.3), Bloom filter of known-good (§4.3), client-side filtering, user-flagging.
- **Result poisoning.** Publishers who publish *real* infohashes but with misleading names (e.g. a malware torrent named "ubuntu-24.04.iso"). Mitigations: validation against torrent actually having files with plausible names/sizes/types; community ratings.
- **DHT sybil attacks.** Attackers spawning many DHT nodes near a popular keyword to control the 8-closest population for that keyword's mutable item. Mitigations: BEP-42 (attackers can't pick node IDs freely), pubkey-based publishing (attacker can't forge a pubkey that isn't theirs), querying multiple indexer pubkeys and intersecting.
- **Metadata tampering.** A man-in-the-middle between us and a BEP-44 storage node returning a forged `v`. Mitigation: BEP-44's ed25519 signature — forgery is infeasible without the publisher's private key.

**Out of scope:**
- **Anonymity / network-level privacy.** If you want your ISP not to see you're running a torrent client, use a VPN or Tor. We do not route queries through mixnets. We do not implement onion routing. (`03-p2p-search-protocols.md` §4 covers RetroShare for users who specifically want this; we consider it a separate product.)
- **End-to-end query privacy.** Your peer-wire queries are visible to the peers you ask. Your DHT queries are visible to the DHT nodes you ask. Hashing keywords into BEP-44 targets provides *some* obfuscation but not resistance to dictionary attack.
- **Legal content filtering.** The client does not enforce copyright policies or jurisdictional content restrictions. Users are responsible for what they search and download, exactly as with every existing BitTorrent client.
- **Search-result censorship resistance.** If the well-known indexer pubkeys all decide to stop listing a particular torrent, a user depending only on those pubkeys will not find it. Mitigations: gossip-discovered indexers (§4.3) and users running their own indexer pubkey.

This partitioning is the design's most controversial choice. The research shows (`03-p2p-search-protocols.md` §6) that Freenet-style systems have strong anonymity and correspondingly terrible UX and performance. Tribler sits in the middle and is (per §8 of the Tribler report) too slow for most users. We're on the "fast and useful" side of the tradeoff. Users who need anonymity should layer their own VPN/Tor and accept the performance cost.

---

## 10. What we are NOT copying from Tribler

Tribler is the closest prior art and we borrow its top-level separation (search overlay vs. BitTorrent engine). But `02-tribler-deep-dive.md` §8 identifies several specific design decisions we are deliberately *not* copying:

| Tribler choice | Why we reject it |
|---|---|
| Flood query to 20 *random* peers | Uncorrelated with topical relevance. We use peers in the current swarms instead. See §4.2. |
| IPv8 overlay on a separate UDP port | Adds a second, parallel P2P network to maintain. We stay on the BitTorrent peer wire (layer S) + mainline DHT (layer D). One network, one port. |
| Python FTS on SQLite FTS5 + Python ranker | Python ranking is reportedly 30x slower than the SQL path (per Tribler source comment cited in `02-tribler-deep-dive.md` §8). We use Bleve in-process in Go. |
| Title-only index | We go one better: torrent name + file list + (local only) file content. |
| FFA (Free-for-All) entries with no rate limit | Per the report, spam vector. We require publisher signatures on every DHT entry. |
| Channel publishing as a separate concept | We don't have "channels." A publisher is just an ed25519 pubkey that publishes keyword → infohash entries. Users can subscribe to a pubkey (see §11). |

What we *do* copy from Tribler:

- The high-level architectural separation (search is an optional add-on to a vanilla BitTorrent engine).
- The FTS5 schema shape for torrent metadata (`database/store.py:85-88`), useful as a reference even though we're using Bleve rather than SQLite FTS5.
- The health gossip idea (peers share seeder/leecher counts for torrents they've seen recently), which we'll piggyback on our `sn_search` message format rather than running a separate protocol.

---

## 11. User-facing features for v1

What the user actually sees:

- **Add torrent.** Same as any torrent client. Magnet link or .torrent file. Files start downloading.
- **Search box, one input field.** User types a query. Results from all three layers (L/S/D) stream in as they arrive, merged and ranked. Each result has a badge showing which layer it came from.
- **Result actions.** Click a result → either "open" (if it's a local file path from Layer L), or "add to downloads" (if it's an infohash from Layer S or D we don't yet have).
- **Indexer status page.** Shows: local index size, last publish, DHT publisher health, known indexer pubkeys with their reputation scores. Advanced users can import/export pubkeys.
- **Per-torrent "index this" toggle.** Default on for user-added torrents; default off for torrents added via `sn_search` result clicks (avoid feedback loops). User can flip either.
- **Opt-out of publishing.** Global setting: "participate in the keyword DHT." On by default. Off makes your client a pure search consumer.

What we explicitly leave out of v1:

- Companion index torrents (F3-over-network). Deferred to v2.
- Multi-word query operators (`AND`, `OR`, `NOT`, quoted phrases). v1 ships bag-of-words only.
- Federated / HTTP-API search bridge (connecting to centralized trackers as another search layer).
- Mobile client. Single-binary Go makes this feasible later but not in v1.
- GUI. v1 is CLI + local web UI; no native desktop shell.

---

## 12. Roadmap (ordered, no time estimates)

**M1 — "torrent client in Go that builds and seeds"**
- Fork anacrolix/torrent as a go.mod dependency. Verify we can build a minimal CLI that adds a magnet, downloads it, and seeds.
- Write the `Callbacks` integration scaffold but don't wire it to anything yet.
- Verify BEP-3 / BEP-5 / BEP-9 / BEP-10 interop against qBittorrent on localhost.

**M2 — "local index works"**
- Wire piece-complete callback to file-complete tracker.
- Build the extractor dispatch and plain-text + subtitle extractors.
- Stand up Bleve; index torrent metadata + file lists + text content.
- `POST /search {scope: "local"}` returns ranked hits. CLI search command working.

**M3 — "peer-wire search works"**
- Register `sn_search` in `LocalLtepProtocolMap.AddUserProtocol`.
- Implement the LTEP handshake capability fields.
- Implement query / result / reject / peer_announce messages.
- Integration test: two SwartzNet instances on localhost talking `sn_search`.

**M4 — "DHT publisher works"**
- Integrate with `github.com/anacrolix/dht/v2`'s BEP-44 put/get.
- Implement keyword tokenization, publish queue, shard manifest.
- Implement searcher-side multi-pubkey union lookup.
- Ship the seed indexer pubkey list.

**M5 — "spam survives the first contact with reality"**
- Per-pubkey reputation scoring.
- Client-side Bloom filter of known-good infohashes.
- User-facing flag button.
- Telemetry for publisher success rate (opt-in, per §7).

**M6 — "add PDF/EPUB/DOCX extractors"**
- Broaden the extractor plugin set.
- Measure: at ingest rate R, how big does the Bleve index get per TB of downloaded content? Tune chunk size.

**M7 — v1 release.**

**Post-v1 candidates:**
- BEP-52 hybrid torrent indexing (per-file merkle roots in search results).
- Multi-word operators and boolean queries.
- Companion index torrents (F3-over-the-network).
- Experimental `sample_infohashes` (BEP-51) integration for bootstrapping from the broader DHT population.
- A GNUnet-style encrypted-keyword mode as an opt-in high-privacy layer (`03-p2p-search-protocols.md` §2, §7.3).

---

## 13. Open questions that block v1

These are the known unknowns the design cannot answer from desk research alone. They need prototypes.

1. **How big is Bleve's on-disk index for real torrent content?** Depends on the mix of file types. We need to ingest ~100 GB of real torrents and measure. If the index is >5% of content size, we need to tune or switch to SQLite FTS5.

2. **How many concurrent BEP-44 mutable-item publishes can the anacrolix DHT library sustain before it starts getting rate-limited by other DHT nodes?** Related: **do most mainline DHT nodes actually implement BEP-44 today?** The research report (`04-bep-extension-points.md`) flags that BEP-44 is still formally Draft. Empirical measurement needed.

3. **Does the "peers you're already in a swarm with" assumption for Layer S actually yield topically-clustered search targets?** Plausible in theory, but if most swarms are "big popular movies" the peers you meet will be a uniform sample of the population, not a topical cluster. Measure on real swarm data.

4. **Reputation system cold-start.** On day one, every indexer pubkey has zero reputation. How do we bootstrap the first ~weeks of publisher trust? Manual curation of the well-known seed pubkeys? User community Pull Requests to the client repo adding pubkeys to the seed list?

5. **License friction on extractors.** Some of the best text extractors (e.g. for DRM'd EPUB or proprietary document formats) are AGPL or GPL. We need to pick extractors that are MPL-compatible or build our own. Audit the extractor dependency tree before M6.

6. **Does publishing to Layer D expose the publisher's identity?** Strictly, their IP is visible to DHT nodes they contact, and their ed25519 pubkey is persistent across sessions. A publisher who publishes torrents under their personal pubkey is building a traceable profile. Mitigation options: rotate pubkeys, shard publishing across ephemeral pubkeys, allow users to publish anonymously with a throwaway key. Decide before v1 ships.

---

## 14. TL;DR

1. **Base:** anacrolix/torrent (Go, MPL 2.0). Its extension API, piece callbacks, and clean library design are the only reason this project is tractable in 2026.
2. **Three search layers:** **L** (local Bleve index over downloaded content), **S** (peer-wire `sn_search` BEP-10 extension to peers in our current swarms), **D** (BEP-44 mutable items carrying keyword → infohash under per-publisher ed25519 keys).
3. **Backwards compatible by construction:** we ship vanilla BEP-3/5/9/10/44. All extensions are opt-in, announced in the LTEP `m` dict. Vanilla clients see and care about nothing new.
4. **Explicitly not Tribler:** no IPv8 side-channel, no flood-to-20-random-peers, no title-only search, no FFA spam vector. We keep Tribler's architectural separation and throw out its specific choices.
5. **Explicitly not anonymous:** in scope is spam resistance, not traffic analysis resistance. Users who need anonymity layer their own VPN/Tor.
6. **Content-level search is local-first:** indexing what *you* download is immediate; distributing content-level indexes across the network is v2.
7. **The DHT keyword index lives in the existing mainline DHT as standard BEP-44 items.** No new verbs, no new port, no new overlay network. This is the single most important call in the design — it inherits the ~10M-node DHT's network effect for free.
