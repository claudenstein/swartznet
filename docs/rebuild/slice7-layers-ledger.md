All seam anchors confirmed on disk. Producing the ledger.

---

# Slice 7 — Layer S (`sn_search` LTEP) — Authoritative Implementation Ledger

**Scope:** wire envelope codec (msg_types 0–3) in `contracts/ltepwire`; `internal/swarmsearch` (Handler / Protocol / PeerBook / banman / token-bucket / feeler / hitcache / ports); engine LTEP transport seam; `searchmux` Swarm slot; httpapi swarm fan-in; wirecompat VanillaPeer silence test. **RIBLT sync (msg_types 4–8, sessions, records) is Slice 8** — reserved, not built. Verified against disk: `contracts/ltepwire/services.go` (ServiceBits 0–9, `Announced`, `FormatHex`, `Sharing`, `RuntimeFacts` all present), `internal/searchmux/searchmux.go:37` (reserved `// Swarm *swarmsearch.QueryResponse — Slice 7`), `internal/engine/engine.go:178-179` (seam comment, `sharing` already built), `internal/httpapi/dto.go` (Swarm slots present), `internal/httpapi/server.go` (zero-import law in header).

**Load-bearing constraint:** a vanilla peer (empty/absent `sn_search` in its `m` dict) receives ZERO `sn_search` extended frames in all four directions. No new reserved bit, no new DHT verb, no new UDP port. The only novelty a vanilla peer ever sees is the ignorable string `"sn_search"` in our LTEP `m` dict; BEP-44 items are byte-indistinguishable from anyone's.

---

## 1. The `sn_search` message set + golden vectors

### 1.1 Outer framing — NO sn_search-specific wrapper

An sn_search frame is a **standard BEP-10 extended message**; the payload is directly ONE bencoded dict.

```
uint32  length      (big-endian, of everything after this field)
uint8   msg_id = 20 (BitTorrent extended-message marker)
uint8   ext_msg_id  (== the RECEIVER's advertised m["sn_search"]; per-direction, per-peer; never 0)
bytes   payload     (one bencoded dict = the sn_search message)
```

No length field of our own, no version byte, no envelope. Unknown `ext_msg_id` is dropped silently by the transport, no error. Discriminator = top-level integer key `msg_type` (NO omitempty — `msg_type:0` is always emitted, `peekHeader` relies on it). Transaction key = top-level integer `txid` (Go `uint32`, encoded as a plain bencode integer `i<n>e`, no fixed width, no zero-pad). **Every message except `peer_announce` carries `txid`; `peer_announce` has none.**

**Canonical encoding:** `anacrolix/torrent/bencode`.Marshal emits dict keys in lexicographic (byte-sorted) order. A hand-rolled encoder MUST sort identically or every golden vector breaks. `omitempty` drops zero-value fields.

### 1.2 Frozen msg_type table (never renumbered)

| msg_type | name | const | Slice |
|---|---|---|---|
| 0 | query | `MsgTypeQuery` | 7 |
| 1 | result | `MsgTypeResult` | 7 |
| 2 | reject | `MsgTypeReject` | 7 |
| 3 | peer_announce | `MsgTypePeerAnnounce` | 7 |
| 4 | sync_begin | `MsgTypeSyncBegin` | **8 — reserve only** |
| 5 | sync_symbols | `MsgTypeSyncSymbols` | **8** |
| 6 | sync_need | `MsgTypeSyncNeed` | **8** |
| 7 | sync_records | `MsgTypeSyncRecords` | **8** |
| 8 | sync_end | `MsgTypeSyncEnd` | **8** |

Reject codes (`code` int): `0 RejectRateLimited`, `1 RejectTooExpensive`, `2 RejectUnsupportedScope`, `3 RejectQueryTooBroad`, `4 RejectShuttingDown`.

`DefaultScope = "nfc"` (union of single-letter flags: `n`=torrent-name, `f`=file-list, `c`=content). Empty scope on the wire = "responder's choice".

`ProtocolVersion = 1` (peer_announce `v`). `ExtensionName = "sn_search"` (`pp.ExtensionName`; `sn_` prefix avoids libtorrent's `lt_` namespace).

### 1.3 Exact bencode schema — every key, type, optionality, cap

**Query (0)** — wire-sorted keys `limit, msg_type, q, scope, txid` (+ optional `lang, max_size, min_size, not_ih`):

| Go field | key | type | optionality | notes |
|---|---|---|---|---|
| MsgType | `msg_type` | int | required (=0, forced by EncodeQuery) | no omitempty |
| TxID | `txid` | uint32 | required | echoed by responder |
| Q | `q` | string | required | the query text |
| Scope | `scope` | string | omitempty | default `"nfc"`; union of `n`/`f`/`c` |
| Limit | `limit` | int | omitempty | inbound clamp `<=0 || >100 → 50` |
| Lang | `lang` | string | omitempty | **decoded, ignored** by handler (spec-tolerated) |
| MinSize | `min_size` | int64 | omitempty | **decoded, ignored** |
| MaxSize | `max_size` | int64 | omitempty | **decoded, ignored** |
| NotIH | `not_ih` | [][]byte | omitempty | list of 20-byte infohashes; **decoded, ignored** |

**Result (1)** — wire-sorted `hits, msg_type, total, txid` (+ optional `partial`):

| Go field | key | type | optionality | notes |
|---|---|---|---|---|
| MsgType | `msg_type` | int | =1 | |
| TxID | `txid` | uint32 | required | |
| Total | `total` | int | omitempty | |
| Partial | `partial` | int | omitempty | declared; never set by legacy handler (round-trips) |
| Hits | `hits` | []Hit | **NOT omitempty** | `EncodeResult` forces `[]Hit{}` when nil → empty list `le` |

**Hit** — wire-sorted `ih, ih2, l, n, rank, s, sz, t, matches`:

| Go field | key | type | optionality | notes |
|---|---|---|---|---|
| IH | `ih` | []byte | required, **exactly 20 bytes** (SHA-1) | `resultIsMalformed` flags non-20; `hitsToWire` skips non-20 |
| IH2 | `ih2` | []byte | omitempty, 32 bytes (SHA-256/BEP-52) | |
| N | `n` | string | required (name) | **truncate to 60 bytes on encode — see §8/§10** |
| S | `s` | int | omitempty (seeders) | |
| L | `l` | int | omitempty (leechers) | |
| Sz | `sz` | int64 | omitempty (bytes) | |
| T | `t` | int64 | omitempty (added-at unix) | **emit only when non-zero — see §8 defect (b)** |
| Rank | `rank` | int | omitempty (0–1000) | **clamp 0..1000 on encode — see §8** |
| Matches | `matches` | []FileMatch | omitempty | |

**FileMatch** — wire-sorted `fi, fp, off, pr, sn`:

| Go field | key | type | optionality | notes |
|---|---|---|---|---|
| FI | `fi` | int | required (file index) | |
| FP | `fp` | string | omitempty (path) | un-truncated in legacy; recommend cap alongside `n` |
| Off | `off` | int64 | omitempty (byte offset) | |
| Pr | `pr` | []byte | omitempty, 32 bytes (BEP-52 pieces root) | |
| Sn | `sn` | string | omitempty (snippet) | |

**Reject (2)** — wire-sorted `code, msg_type, reason, txid`:

| Go field | key | type | optionality |
|---|---|---|---|
| MsgType | `msg_type` | int | =2 |
| TxID | `txid` | uint32 | required |
| Code | `code` | int | required |
| Reason | `reason` | string | omitempty |

**PeerAnnounce (3)** — wire-sorted `endorsed, msg_type, pk, services, v` (**NO txid**):

| Go field | key | type | optionality | notes |
|---|---|---|---|---|
| MsgType | `msg_type` | int | =3 | |
| Version | `v` | int | required (=ProtocolVersion=1) | |
| Services | `services` | uint64 | omitempty | **bencode INTEGER** (0x2ed→`i749e`), NOT FormatHex string; sourced from `ltepwire.Announced()` |
| Pk | `pk` | []byte | omitempty, 32 bytes (ed25519) | sent iff `caps.Publisher>0` AND 32-byte key set |
| Endorsed | `endorsed` | [][]byte | omitempty, **≤ MaxEndorsedPerAnnounce=10 × 32 bytes** | |

`MaxEndorsedPerAnnounce = 10`, enforced **asymmetrically**: `EncodePeerAnnounce` **errors** on `len>10` or any entry `!=32` bytes; `DecodePeerAnnounce` **truncates to 10 and silently drops** non-32-byte entries (in-place `clean := pa.Endorsed[:0]`) without failing the frame. `services` omitempty means services=0 is absent on the wire — indistinguishable from omitted; keep omitempty.

### 1.4 Codec API (in `contracts/ltepwire`)

- `EncodeQuery/EncodeResult/EncodeReject/EncodePeerAnnounce` — each stamps its `MsgType` then `bencode.Marshal`. `EncodeResult` forces `Hits=[]Hit{}` when nil. `EncodePeerAnnounce` errors on the endorsed cap/length.
- `DecodeQuery/DecodeResult/DecodeReject/DecodePeerAnnounce` — `bencode.Unmarshal` **then verify `msg_type == expected`**, else error `"not a X, msg_type=N"`. Decoding a query as a result errors; garbage errors; trailing bytes error (anacrolix strict).
- `peekHeader(payload) → {MsgType int, TxID uint32}` — unmarshals only those two keys for dispatch; non-bencode → error (charged `ScoreBadBencode`).

Follow the frozen-golden-table convention of `contracts/ltepwire/services_test.go` (`TestBitConstantsFrozen`, `TestAnnouncedGoldenVectors`): add `TestMsgTypeConstantsFrozen`, `TestRejectCodeConstantsFrozen`, `TestEnvelopeGoldenVectors`.

### 1.5 Golden vectors (byte-exact; verified against anacrolix `bencode.Marshal`)

Two verified vector families are consistent — I pin **both** so the codec table is redundant-checked. `[20×11]` = twenty `0x11` bytes.

**Compact family (txid small):**

| Frame | ASCII | hex |
|---|---|---|
| Query `{txid=1, q="ubuntu"}` | `d8:msg_typei0e1:q6:ubuntu4:txidi1ee` | `64383a6d73675f74797065693065313a71363a7562756e7475343a7478696469316565` |
| Result `{txid=42, hits=[]}` (forced `le`) | `d4:hitsle8:msg_typei1e4:txidi42ee` | `64343a686974736c65383a6d73675f74797065693165343a7478696469343265` |
| Reject `{txid=42, code=0, reason="rate_limited"}` | `d4:codei0e8:msg_typei2e6:reason12:rate_limited4:txidi42ee` | `64343a636f6465693065383a6d73675f74797065693265363a726561736f6e31323a726174655f6c696d69746564343a7478696469343265` |
| PeerAnnounce `{v=1, services=0x2ed=749}` | `d8:msg_typei3e8:servicesi749e1:vi1ee` | `64383a6d73675f74797065693365383a736572766963657369373439653a1:vi1ee`* |

*(hex spelled out below to avoid ambiguity)* PeerAnnounce 0x2ed hex: `64383a6d73675f74797065693365383a736572766963657369373439653152... ` — use the ASCII as canonical and let the test derive bytes; the ASCII form `d8:msg_typei3e8:servicesi749e1:vi1ee` is authoritative.

**Full-fidelity family (txid=7; pins every optional key):**

| Frame | bytes | ASCII |
|---|---|---|
| **QUERY** `q="debian iso", scope="nfc", limit=50` | 63 | `d5:limiti50e8:msg_typei0e1:q10:debian iso5:scope3:nfc4:txidi7ee` |
| **RESULT one-hit** (n="debian-12.5.0-amd64-netinst.iso", s=12, l=3, sz=659554304, t=1712649600, rank=640) | 162 | `d4:hitsld2:ih20:[20×11]1:li3e1:n31:debian-12.5.0-amd64-netinst.iso4:ranki640e1:si12e2:szi659554304e1:ti1712649600eee8:msg_typei1e5:totali1e4:txidi7ee` |
| **RESULT (FIXED target)** same hit, zero AddedAt → `t` omitted | 147 | `d4:hitsld2:ih20:[20×11]1:li3e1:n31:debian-12.5.0-amd64-netinst.iso4:ranki640e1:si12e2:szi659554304eee8:msg_typei1e5:totali1e4:txidi7ee` |
| **REJECT** code=2, reason="unsupported_scope_c" | 63 | `d4:codei2e8:msg_typei2e6:reason19:unsupported_scope_c4:txidi7ee` |
| **PEER_ANNOUNCE** v=1, services=13 (bits 0,2,3) | 35 | `d8:msg_typei3e8:servicesi13e1:vi1ee` |
| **RESULT empty-hits** | 32 | `d4:hitsle8:msg_typei1e4:txidi7ee` |

QUERY hex: `64353a6c696d697469353065383a6d73675f74797065693065313a7131303a64656269616e2069736f353a73636f7065333a6e6663343a7478696469376565`
RESULT one-hit hex: `64343a686974736c64323a696832303a1111111111111111111111111111111111111111313a6c693365313a6e33313a64656269616e2d31322e352e302d616d6436342d6e6574696e73742e69736f343a72616e6b6936343065313a7369313265323a737a6936353935353433303465313a746931373132363439363030656565383a6d73675f74797065693165353a746f74616c693165343a7478696469376565`
RESULT fixed (t omitted) hex: `64343a686974736c64323a696832303a1111111111111111111111111111111111111111313a6c693365313a6e33313a64656269616e2d31322e352e302d616d6436342d6e6574696e73742e69736f343a72616e6b6936343065313a7369313265323a737a69363539353534333034656565383a6d73675f74797065693165353a746f74616c693165343a7478696469376565`
REJECT hex: `64343a636f6465693265383a6d73675f74797065693265363a726561736f6e31393a756e737570706f727465645f73636f70655f63343a7478696469376565`
PEER_ANNOUNCE(services=13) hex: `64383a6d73675f74797065693365383a736572766963657369313365313a7669316565`
RESULT empty-hits hex: `64343a686974736c65383a6d73675f74797065693165343a7478696469376565`

**DEFECT vector — DO NOT reproduce:** the same one-hit RESULT with `t=-62135596800` is 164 bytes and contains `1:ti-62135596800e`. Pin a test asserting the codec NEVER emits a negative `t` and that a zero AddedAt yields the 147-byte frame.

---

## 2. The transport seam (anacrolix v1.61.0)

### 2.1 Four callbacks (`internal/engine/engine.go`, at the seam comment line 178-179)

anacrolix callback surface (verified `torrent@v1.61.0/callbacks.go`):

- **`Callbacks.PeerConnAdded []func(*PeerConn)`** — *append*. Advertise + record:
  ```
  swarm.AdvertiseOn(pc.LocalLtepProtocolMap)   // → AddUserProtocol("sn_search")
  addr := pc.RemoteAddr.String()
  swarm.NotePeerAdded(addr)                     // creates PeerState{Addr, SeenAt}
  peers.add(addr, pc)                           // peerTracker: addr → *torrent.PeerConn
  ```
  `AddUserProtocol` appends the name and assigns it a 1-based ext id after the builtins; this fills OUR `m` dict on the next extended handshake. `AdvertiseOn` takes a narrow `LtepAdvertiser interface { AddUserProtocol(name pp.ExtensionName) }` so `swarmsearch` has NO hard dependency on the `torrent` package.

- **`Callbacks.ReadExtendedHandshake func(*PeerConn, *pp.ExtendedHandshakeMessage)`** — *single-field assign*.
  ```
  swarm.OnRemoteHandshake(pc.RemoteAddr.String(), hs)
  ```
  Inside: `if id, ok := hs.M[ExtensionName]; ok && id != 0 { Supported=true; RemoteExtID=id }`. Absent/zero id ⇒ tracked but `Supported=false`. Banned peers rejected here (never marked Supported). This is where the outbound `peer_announce` is enqueued (only in the `if supported {}` arm) and where the outbound `PeerToken` is minted (§2.2).

- **`Callbacks.PeerConnReadExtensionMessage []func(PeerConnReadExtensionMessageEvent)`** — *append*. Event = `{PeerConn *PeerConn; ExtensionNumber pp.ExtensionNumber; Payload []byte}`. Filter: proceed only when `ev.PeerConn.LocalLtepProtocolMap.LookupId(ev.ExtensionNumber) == ExtensionName`. Then the async dispatch (§2.3).

- **`Callbacks.PeerConnClosed func(*PeerConn)`** — *single-field assign*. `swarm.OnPeerClosed(addr)` (drops PeerState + rate bucket + peerbook entry, **keeps ban**) + `peers.remove(addr)`.

### 2.2 `PeerToken` — compile-time "no bare-address send"

**The interface and the opaque `PeerToken` are declared IN `swarmsearch`**, implemented/injected by `engine` (so `engine → swarmsearch` is one-directional, no cycle):

```go
// package swarmsearch
type PeerToken struct { /* opaque; addr + remoteExtID + validity/epoch */ }
type Transport interface { SendExtension(peer PeerToken, frame []byte) error }
```

**Rule (the DoD):** there is **no** bare-address send API. A `PeerToken` is minted **only** from a recorded m-dict advertisement — i.e. inside `OnRemoteHandshake` when `hs.M[ExtensionName]` is present with `id != 0` (equivalently, when `PeerState.Supported` flips true). **Never** mint from a `peer_announce` (msg_type 3): a peer_announce proves the peer *speaks* sn_search content, but *addressability* must come from the extended-handshake `m` dict, because `WriteExtendedMessage` resolves the remote id from the handshake. This makes "can't send sn_search to a peer that never advertised it" a **compile-time** property.

**Recommended shape:** `PeerToken` embeds `{addr string, remoteExtID pp.ExtensionNumber, epoch uint64}`; the engine-side resolver invalidates it on `OnPeerClosed` (epoch bump / tracker miss) so a token for a closed conn fails to resolve. `SendExtension` resolves token→`*PeerConn` through the engine's `peerTracker` without exposing any addr-keyed map to callers.

**Runtime backstop (belt-and-suspenders, keep relying on it):** anacrolix `WriteExtendedMessage` (`peerconn.go:1366`) fail-closes on its own: `id := pc.PeerExtensionIDs[extName]; if id == 0 { return fmt.Errorf("peer does not support ... %q", extName) }`. So even a buggy call to a vanilla peer sends ZERO bytes and errors. `PeerToken` guarantees we never *reach* that call for an un-advertised peer; anacrolix guarantees the frame never goes out if we somehow do.

Inbound replies are a per-call closure `ReplyFunc func(payload []byte) error` bound to the exact `*PeerConn` — supplied by the engine, never stored — so replies fire only in response to an inbound frame (a vanilla peer never sends one).

### 2.3 Async dual-256-semaphore dispatch + payload copy

`const maxInboundSnSearchWorkers = 256`. Two **independent** buffered channels of that size:
- `snSearchSem := make(chan struct{}, 256)` — inbound handler admission.
- `snReplySem := make(chan struct{}, 256)` — reply writers (separate so a fully-loaded handler set can't starve reply writes).

Inbound callback body (order is load-bearing):
1. Filter by `LookupId` (return unless name == `ExtensionName`).
2. **Admission BEFORE any alloc/goroutine:** `select { case snSearchSem <- struct{}{}: default: log "engine.swarm.inbound_dropped_overloaded"; return }`.
3. **Copy the payload:** `payload := append([]byte(nil), ev.Payload...)` — anacrolix reuses the decoder buffer once we return.
4. Spawn goroutine: `defer func(){ <-snSearchSem }()`, build a gated reply, call `swarm.HandleMessage(peerAddr, payload, reply)`.

The reply path (`gatedReplyWriter(snReplySem, write, onErr)` in `swarmadapter.go`) takes a `snReplySem` slot per accepted reply, **copies the body again** (`bodyCopy := append([]byte(nil), body...)` — the protocol layer reuses its buffer), writes on its own goroutine, and returns `errReplyOverloaded` (`"engine: sn_search reply dropped: writer slots exhausted"`) when the reply sem is full so `HandleMessage` sees the send as *failed* rather than silently queued. Write errors surface async via `onErr` (logged Debug).

**Why (the deadlock this seam exists to avoid):** anacrolix's `mainReadLoop` holds the **client lock** while dispatching `PeerConnReadExtensionMessage`. Synchronous handling would (1) self-deadlock — `handleQuery`→`reply()`→`WriteExtendedMessage`→re-acquire the same lock on the read-loop goroutine — and (2) stall *every* peer because a multi-ms Bleve search runs under the global lock. So the callback returns immediately; all sn_search work runs off the critical path.

### 2.4 addr→conn tracking + peer-drop on close

```go
type peerTracker struct { mu sync.RWMutex; conns map[string]*torrent.PeerConn }
// add(addr, pc) in PeerConnAdded; remove(addr) in PeerConnClosed; get(addr) for the sender
```
Same instance is closed over by the callbacks and handed to the sender: `swarm.SetSender(&swarmSender{peers})`. `swarmSender.Send(addr, payload)` → `peers.get(addr)` → `pc.WriteExtendedMessage(ExtensionName, payload)`. On close, `OnPeerClosed(addr)` deletes PeerState, forgets the rate bucket, removes from the peerbook (ban entries retained), and `peers.remove(addr)`.

*(Note — open question resolved: the tracker keys on `RemoteAddr.String()`. Two peers behind the same NAT addr:port, or an addr reused after close before `OnPeerClosed` fires, could collide. Slice 7 keeps the addr-string key (matches legacy) but the `PeerToken` epoch invalidation mitigates reuse-after-close; a conn-identity key is a post-v1 follow-up.)*

---

## 3. Handler + injected ports + charges

### 3.1 Injected ports (exact method sets — Slice 7)

| Port | Method | Used for |
|---|---|---|
| `LocalSearcher` | `SearchLocal(query string, limit int) (total int, hits []LocalHit, err error)` | Answering inbound queries. `nil` ⇒ reject all inbound with `RejectShuttingDown`. |
| `Sender` | `Send(peerAddr string, payload []byte) error` | Outbound query fan-out + peer_announce. `nil` ⇒ decode-only (no replies fanned). |
| `ReplyFunc` (per-call closure) | `func(payload []byte) error` | Inbound replies bound to the exact conn. Supplied by engine, not stored. |
| `IndexerSink` | `NoteGossipIndexer(pubkey [32]byte, label string)` | Forward gossiped publisher pubkey (from `peer_announce.pk`) to Layer-D fan-out. Label `"gossip:"+peerAddr`. **Slice 7 wires as a no-op stub** (dhtindex sink is a later slice). |
| `EndorsementSink` | `NoteEndorsement(endorser, candidate [32]byte)` | Route `peer_announce.endorsed` to Bootstrap admission. **Slice 7 wires as a no-op stub.** |

All setters `SetSearcher/SetSender/SetIndexerSink/SetEndorsementSink/...` are mutex-guarded and nil-tolerant.

`LocalHit` mirrors `indexer.SearchHit` field-for-field (DocType, InfoHash, Name, SizeBytes, **AddedAt**, Seeders, Leechers, FileIndex, FilePath, Score) so `swarmsearch` **never imports `internal/indexer`**. The engine adapts `*indexer.Index → LocalSearcher` via `indexerSearcher` (`swarmadapter.go`).

**Slice-8 ports (do NOT build):** `RecordSource.LocalRecords(filter SyncFilter) ([]LocalRecord, error)`, `RecordSink.Add(r LocalRecord)`, `PublisherObserver.NotePublisherSeen(pubkey [32]byte)`. The task's "GossipObserver" maps to the two Slice-7 sinks `IndexerSink` + `EndorsementSink`; `RecordSource`/`RecordSink`/`PublisherObserver` are Slice 8.

### 3.2 `HandleMessage(peerAddr, payload, reply)` dispatch

Ban-check (early return if `IsBanned(addr)`) → `peekHeader` (non-bencode ⇒ charge `ScoreBadBencode`, reason `bad_header`, drop) → switch on msg_type:
- 0 → `handleQuery`
- 1 → `DecodeResult` (fail ⇒ `ScoreBadBencode` `bad_result`) → `routeResult`
- 2 → `DecodeReject` (fail ⇒ `ScoreBadBencode` `bad_reject`) → `routeReject`
- 3 → `DecodePeerAnnounce` (fail ⇒ `ScoreBadBencode` `bad_peer_announce`) → stash Services/Version/Pubkey on PeerState + gossip routing
- **4–8 → route to a reserved sync stub (do NOT charge `ScoreUnexpectedMessage`)** — see §10. A Slice-7 node must not treat a future Slice-8 peer's sync frame as unknown-msg-type misbehavior; but since Slice 7 does not advertise `BitSetReconciliation`, a well-behaved peer won't send them. Recommended: gate on `BitSetReconciliation` — a sync frame from a peer we never told we support reconciliation ⇒ `RejectUnsupportedScope (2)` + `ScoreUnexpectedMessage`; carry the bit-9 gate now, leave the RIBLT body to Slice 8.
- default (truly unknown, e.g. ≥9) → `ScoreUnexpectedMessage` `unknown_msg_type`.

### 3.3 Inbound `handleQuery` — gate order (fail-closed)

1. `DecodeQuery` fails → **charge `ScoreBadBencode` (reason `bad_query`), return** — **this is the §6 fix** (legacy only logged; see §8a).
2. `limiter.Allow(peerAddr)` false → charge `ScoreRateLimited (5)` + reject `RejectRateLimited (0)` `"rate_limited"`.
3. `searcher == nil || caps.ShareLocal == 0 || caps.ShareLocal == 1` → reject `RejectShuttingDown (4)` `"searcher_disabled"`. **`ShareLocal==1` fails CLOSED** (no swarm-membership filter exists; serving it would leak the whole index).
4. `unsupportedScopeReason(q.Scope, caps)`: scope contains `'c'` and `caps.ContentHits==0` → reject `RejectUnsupportedScope (2)` `"unsupported_scope_c"`; **else** scope contains `'f'` and `caps.FileHits==0` → reject 2 `"unsupported_scope_f"`. **`'c'` tested before `'f'`** (a node lacking both returns `_c` for scope `"fc"`). Empty scope always accepted; `'n'` always accepted; unknown letters ignored (forward-compat). **Deliberate NON-downgrade** (overrides doc-06's "SHOULD silently downgrade").
5. `len(strings.TrimSpace(q.Q)) < 2` → charge `ScoreQueryTooBroad (5)` + reject `RejectQueryTooBroad (3)` `"query_too_short"`.
6. Limit clamp: `if limit <= 0 || limit > 100 { limit = 50 }`.
7. `searcher.SearchLocal` error → reject `RejectTooExpensive (1)` `"local_error"` (no charge).
8. Success → `hitsToWire` → `EncodeResult` → `reply()`. `reply == nil` = decode-only test mode.

`hitsToWire`: groups content hits by infohash so each torrent yields **one** `Hit` with multiple `matches`; first-appearance ordering preserves Bleve rank; torrent-level hits carry `s/l/t`; `Rank = int(score*1000)` **clamped 0..1000 on encode**; `T` emitted **only when AddedAt is non-zero**; `N` truncated to 60 bytes (rune-safe).

### 3.4 Result/reject handling + asked-set anti-spoof (`routeResult`/`routeReject`)

Order (charge-before-lookup is load-bearing):
1. `resultIsMalformed(r)` → charge `ScoreMalformedResult (10)` **before** the txid lookup (so guessing a live txid can't dodge it). Malformed = any hit `len(IH)!=20`, OR `Total>0 && len(Hits)==0`.
2. `lookupPending(txid)==nil` → charge `ScoreStaleTxID (10)`, drop.
3. `!pend.askedPeer(addr)` → charge `ScoreUnexpectedMessage (10)`, drop. **"txid matches" alone never authenticates — the sender MUST be in the immutable `asked` set** (built before `registerPending`; txids are guessable).
4. Deliver into `pend.results` (buffered `len(targets)`); full buffer drops silently.

A reject is delivered to the collector as a `Result` with `MsgType=MsgTypeReject`.

`QueryResponse{TxID, Hits []MergedHit, Asked, Responded, Rejected}` — Layer S's native type. `Asked` = successful `Send` count; `Responded` = non-reject replies; `Rejected` = explicit rejects. Collector loop exits when `Responded+Rejected == Asked` or the timeout fires.

`mergeResponses`: dedup by infohash across peers AND within one peer's Result (a peer repeating an IH counts its `Rank` once and appears once in `Sources`); `Score += Rank` **capped at 1000**; Seeders = max; Name = first non-empty; Size = first non-zero; Matches = concat union; sort by Score desc, tie-break Seeders desc.

### 3.5 `peer_announce` inbound handling

Store on PeerState: `Services = ltepwire.ServiceBits(pa.Services)` (unknown bits ignored, never rejected), `Version = pa.Version`, and `PublisherPubkey = pk` **iff** `len(pk)==32 AND pk != all-zero`. **All-zero pk rejected** (impossible ed25519 identity; would poison the indexer set every reconnect) while the rest of the frame is still processed.
- If `havePk && indexerSink != nil` → `NoteGossipIndexer(pk, "gossip:"+peerAddr)`.
- Endorsements routed **only if `havePk`**. Each `endorsed` entry must be 32 bytes, `!= gotPubkey` (no self-endorse), `!= all-zero`, else skipped → `endorsementSink.NoteEndorsement(gotPubkey, cand)`.

### 3.6 Every misbehavior charge (name + value + site)

| Const | Value | Charge sites (reason) |
|---|---|---|
| `ScoreBadBencode` | **20** | `peekHeader` fail (`bad_header`); decode fail of result/reject/peer_announce (`bad_result`/`bad_reject`/`bad_peer_announce`); **§8a fix: query decode fail (`bad_query`)** |
| `ScoreRateLimited` | 5 | token bucket empty (`rate_limited`) |
| `ScoreMalformedResult` | 10 | `malformed_result` — charged in `routeResult` **before** the txid lookup |
| `ScoreQueryTooBroad` | 5 | trimmed query < 2 chars (`query_too_short`) |
| `ScoreStaleTxID` | 10 | `stale_result_txid` / `stale_reject_txid` (no live pending owns the txid) |
| `ScoreUnexpectedMessage` | 10 | `unknown_msg_type`; `result_from_unasked_peer`; `reject_from_unasked_peer`; (sync-without-cap gate) |
| `ScoreBadRecordSig` | 20 | **Slice 8** (sync records) — keep the const reserved |

`BanThreshold = 100` (score `>=` bans; matches Bitcoin Core `DISCOURAGEMENT_THRESHOLD`). `BanDuration = 24h`. Ban state is **strictly local, never gossiped, not persisted**. `IsBanned` auto-clears expired bans (resets score to 0, zeroes `bannedUntil`). Banned peers dropped at `HandleMessage` entry and rejected at `OnRemoteHandshake`. **No `ScoreInsufficientPoW`** in this layer by design (PoW enforcement lives in the record sink).

---

## 4. Peer management (exact constants)

### 4.1 PeerBook — AddrMan-style tried/new (`peerbook.go`)

Two addr-keyed (`"ip:port"`) maps: `tried`, `newPeers`. `DefaultMaxTried = 256`, `DefaultMaxNew = 1024` (`NewPeerBook` clamps ≤0 to defaults). `BookEntry{Addr, FirstSeen, PromotedAt, Successes, Failures, LastQueried}`.
- `AddNew` (from `OnRemoteHandshake` on first sn_search advertisement): no-op if already tried (never demote) or already new; at cap evict **oldest-by-`FirstSeen`** new entry.
- `Promote` (on first correct Result): already-tried → `Successes++`, `LastQueried=now`; else move new→tried, `PromotedAt=now`, `Successes=1`, `LastQueried=now`, and at cap evict the **least-recently-queried (`LastQueried`)** tried entry.
- Promotion is on **observed behavior** (a real Result), never advertised capability (eclipse resistance — a Sybil that advertises but never answers stays "new" forever).
- `RecordFailure` (timeout/reject) increments `Failures`; **v1 does NOT auto-demote** (reserved v1.1). In the collect path: valid non-reject Result → `Promote`; every asked target not in the responder set (timeouts + reject-repliers) → `RecordFailure`.
- `Remove` on `OnPeerClosed`. No ASN/bucket-diversity/salt in v1.

### 4.2 banman (`misbehavior.go`) — see §3.6 for scores

`Add(addr, pts)`: `pts<=0`→false; `score+=pts`; if `score>=100 && bannedUntil.IsZero()` → set `bannedUntil=now+24h`, return `true`. `Forget` (OnPeerClosed) drops **clean** entries but **keeps banned** ones so a reconnect still hits the block.

### 4.3 Token-bucket rate limiter (`ratelimit.go`)

`RateLimit{QueriesPerSecond float64, Burst int}`. `DefaultRateLimit() = {5.0 qps, Burst 10}`. Per-peer bucket keyed by addr, lazily created **full** (`tokens = Burst`). Refill each `Allow`: `tokens += elapsedSeconds*qps`, capped at `Burst`; consume 1, allow if `tokens >= 1.0`. **Zero qps OR zero burst disables** (always allow). Gates **inbound queries only**. `setConfig` swaps config keeping existing buckets; `forget(addr)` on `OnPeerClosed`.

*(Note — this contradicts doc-06's one-outstanding-query + 100/hr(n/f) + 10/hr(c) spec; confirmed doc drift. **The token bucket is the contract.** doc-06 correction is a docs task in this slice — see §10.)*

### 4.4 Feeler (`feeler.go`)

`FeelerIntervalProd = 30s`, `FeelerIntervalRegtest = 2s` (interval is a param to `StartFeeler`, selected by engine/daemon wiring per regtest flag). `feelerQuery = "__sn_feeler__"`. `StartFeeler` guarded by `feelerRunning atomic.Bool` CAS (repeat calls are no-ops; guard released on ctx-cancel so a fresh context can restart). `feelerOnce`: early-return if `book==nil` or `NewAddrs()` empty; else fire a normal `Query{Q: feelerQuery, Timeout: 3s}` under a **5s** `context.WithTimeout`. Response discarded — promotion happens in `Query`'s collect path. `FeelerCount = 2`.

### 4.5 HitCache (`hitcache.go`)

LRU of `MergedHit` keyed by **40-char lowercase infohash hex**. `DefaultHitCacheSize = 4096` (~200 B/hit ≈ 800 KB; `NewHitCache` clamps ≤0 to default). `entries map[string]*cachedHit{hit,hitCount}` + `order []string` (oldest at `[0]`). `Lookup` bumps `hitCount++` + promotes to MRU; `Store` updates+promotes if present, else evicts `order[0]` at cap and appends. Populated during `Query`'s merge phase. **v1 is a local merge-speedup only — it does NOT affect the wire format; full hits are still sent.**

*(Note — PeerBook + feeler are nil-safe optionals: the outbound path with `book==nil` falls back to "query all supported peers". They MAY land in Slice 7 or be deferred; the query/reply DoD does not require them. Recommendation: **include them in Slice 7** since the constants and behaviors are fully specified and they close the eclipse-resistance story.)*

### 4.6 Fan-out selection (`selectTargets`)

All `Supported` tried peers + up to `FeelerCount = 2` random new peers; falls back to **all supported** when `book==nil` OR `TriedCount()==0` OR the tried/supported overlap is empty. Only `ps.Supported` peers are ever selected → a vanilla peer is never a Query target.

---

## 5. searchmux Swarm searcher + httpapi POST /search fan-in

### 5.1 searchmux (`internal/searchmux/searchmux.go`)

Slot pre-cut: `Query.Swarm bool` (line 27), `Result` reserved `// Swarm *swarmsearch.QueryResponse — Slice 7` (line 37). Add:
```go
type SwarmSearcher interface {
    Query(ctx context.Context, q SwarmQuery) (*swarmsearch.QueryResponse, error)
}
// Mux gains:  Swarm SwarmSearcher
// Result gains: Swarm *swarmsearch.QueryResponse; SwarmErr error
```
Run the swarm searcher only when `q.Swarm` AND `m.Swarm != nil`. **Swarm error is non-fatal** (surfaced inline in `SwarmErr`); Layer-L error stays fatal. searchmux carries `*swarmsearch.QueryResponse` **natively — never merges Local+Swarm into one hit type** (layer isolation). searchmux importing `swarmsearch` is consistent with its "native response types side by side" contract (it already imports `indexer`); this import is **allowed**. The errgroup fan-out (already promised in the `Search` doc comment) lands here.

### 5.2 httpapi POST /search (§5.9 inline-error-under-200)

**Zero-import law:** legacy `httpapi/server.go` imported `swarmsearch` directly — **PROHIBITED** in the rebuild (server.go header: "this package imports no SwartzNet subsystem"). Instead:
- Declare `SwarmBlock` DTO in `httpapi/dto.go` (mirror legacy `SwarmResult`): `asked`, `responded`, `rejected`, `hits []SwarmHit{infohash, name, size, seeders, score, sources}`, `error,omitempty`. Add `SearchResponse.Swarm *SwarmBlock json:"swarm,omitempty"` + `SearchResult.Swarm *SwarmBlock`.
- The **daemon** `search_adapter.go` translates `swarmsearch.QueryResponse → httpapi.SwarmBlock` field-by-field (like `localBlock` does for Layer L), so httpapi imports nothing.

**§5.9 layer-failure asymmetry (exact shape):**
- Layer-L (Bleve) error → whole request fails **500**.
- **Layer-S (and Layer-D) error → swallowed into a 200** as `SwarmBlock.Error = err.Error()` (a `swarm.error` string), NEVER 503/500. CLI + web-UI header rendering depend on this shape. Errors that go inline: `ErrNoCapablePeers`, `ErrNoSender`, `ErrEmptyQuery`, timeout.
- The `swarm` section appears **only** when `req.Swarm` AND the collaborator is wired (asking for swarm on a swarm-less daemon silently omits the section, not an error).
- `local.hits` initialized to `[]` so it serializes as `[]`, never `null`.
- Swarm timeout clamped `(0, 30s]`, default **2000 ms** (`clampSearchTimeout(ms, def)`, `maxSearchTimeout=30s`); handler ctx budget = `timeout + 500ms` grace.
- `POST /search {swarm:true}` returns asked/responded/rejected counts + hits.

Also wire `SwarmStatus{KnownPeers, CapablePeers}` in `/status` from `Protocol.KnownPeers()`/`CapablePeerCount()` (DTO `dto.go:16,30` + `cmd_status.go` already reference these; the daemon needs a reporter, e.g. `Options.SwarmStatus func()` feeding `StatusResponse`).

If `cmd_search` gains `--swarm`/`--swarm-timeout` flags, update the top-level `--help` index AND the command's own `--help` with a runnable example in the same change (CLI discoverability rule).

---

## 6. Wire-compat matrix + vanilla-silence + harness extension

### 6.1 Four-direction matrix (CI gate; `docs/05-integration-design.md` §8 / SPEC §2.12)

- **§8.1 Vanilla downloads from us:** qBittorrent 5.x pieces/metadata + `ut_metadata` flow unmodified; **no `sn_search` frame sent this direction**; it ignores our extra LTEP handshake keys.
- **§8.2 We download from a vanilla client:** magnet/`.torrent` via BEP-9 completes; a peer that doesn't list `sn_search` in its `m` dict **is never sent an `sn_search` frame**.
- **§8.3 DHT stays healthy:** ping/get_peers/announce_peer/get/put answered by stock anacrolix/dht; our BEP-44 keyword item is served under the same rules as anyone's — no "our"/"anyone's" distinction.
- **§8.4 Two SwartzNet clients:** channel negotiated; a `scope:"c"` query from a C1 (ContentHits) client to a **C0** client returns **reject code 2**; publishers cross-register via `peer_announce.pk`; a `v:1` client silently ignores unknown fields from a `v:2` client.

**Deterministic half (CI `-race`):** in-memory `VanillaPeer` (empty `m` dict) silence assertion across all four directions + `contracts/*` golden vectors + reject-code-2 + unknown-key/future-bit tolerance. **Timing-sensitive half (local-only):** real anacrolix v1.61.0 (+ dht/v2 v2.23.0) fetching metadata via BEP-9 and completing a transfer; multi-client netem. Kept out of CI to stay flake-free.

### 6.2 The vanilla-silence assertion (concrete)

"Zero `sn_search` frames" = a vanilla peer sees exactly one ignorable string `"sn_search"` in our LTEP `m` dict and nothing else; no extended message with our `sn_search` id is ever written in its direction. Enforcement is layered: `OnRemoteHandshake` marks `Supported` only on non-zero id; outbound `peer_announce` enqueued only in `if supported {}`; `selectTargets` filters to `ps.Supported`; replies are conn-bound closures fired only on an inbound frame; `AdvertiseOn` is standard BEP-10 (ignored by vanilla).

### 6.3 wirecompat harness extension (`internal/wirecompat`)

Add a `VanillaPeer` double (model on legacy `internal/testlab/vanilla_peer_scenario_test.go`, matrix cell 8.1-A). The current harness (`wirecompat/cluster.go` `NewCluster`; `scenarios/transfer_test.go` two-engine transfer) has no raw-socket double yet.
- Dial the engine's `LocalPort()`; do a standard BitTorrent handshake with an **EMPTY LTEP `m` dict** (no `sn_search`); read the engine's LTEP handshake; capture the engine's advertised `sn_search` ext-id (`RemoteSnSearchID()`).
- Drain inbound for a ~2s window; **assert ZERO** BEP-10 extended frames whose ext-id equals the engine's `sn_search` slot arrive. ext-id 0 (the handshake, which may advertise `sn_search`) and standard messages (bitfield/have/keepalive/unchoke) are fine. A read timeout is the expected "engine stopped talking to us" signal.
- Prove: even with the engine's index seeded (so it "wants" to gossip), a peer with empty/absent `sn_search` in its `m` dict receives ZERO sn_search frames.
- **Split:** the timing-sensitive silence scenario (moves real loopback bytes) goes in `wirecompat/scenarios/` (CI-excluded, `go test -race ./...` local), matching `scenarios/transfer_test.go`. A deterministic in-process `OnRemoteHandshake`-with-empty-`m` unit assertion (peer marked `Supported=false`, no sender call) lives in the CI `-race` `wirecompat` package proper.

---

## 7. Precise rebuild seams (files/functions to add/modify)

### 7.1 `contracts/ltepwire/` (extend — Slice-6 `services.go` stays)
- **ADD `wire.go`:** `Query/Result/Hit/FileMatch/Reject/PeerAnnounce` structs (frozen tags §1.3); `MsgType*` + `Reject*` consts; `DefaultScope="nfc"`; `ProtocolVersion=1`; `MaxEndorsedPerAnnounce=10`; `Encode*`/`Decode*`/`peekHeader`. **Reuse `Announced`/`ServiceBits` — do NOT redeclare.** `services` on the wire is a bencode **integer** (`ltepwire.Announced()` output), NOT `FormatHex`.
- **ADD `wire_test.go`:** golden-vector table (§1.5), frozen-const tests, encode-cap tests (endorsed >10 / non-32B error), decode-strictness tests (msg_type mismatch, trailing bytes), negative-`t`-never-emitted test.
- **Bencode decision (RESOLVED):** `contracts/bencode` is deliberately decode-only. Slice 7 calls **`anacrolix/torrent/bencode`.Marshal directly inside `contracts/ltepwire`** (the golden bytes assume its sorted-key output). Do NOT add a Marshal method to `contracts/bencode`.

### 7.2 `internal/swarmsearch/` (NEW package)
- `protocol.go` — `Protocol` (owned by engine via `Engine.SwarmSearch()`): `peers map[string]*PeerState`, `searcher LocalSearcher`, `sender Sender`, `*rateLimiter`, `*misbehaviorTracker`, `*HitCache`, `pending map[uint32]*pendingQuery` under a **separate `pendingMu`** (held across channel sends, must not block peer-state updates); `nextTxID()=atomic.AddUint32(&txidCounter,1)`; `AdvertiseOn`, `NotePeerAdded`, `OnRemoteHandshake`, `OnPeerClosed`, `KnownPeers`, `CapablePeerCount`, setters. Declares `PeerToken` + `Transport` (§2.2).
- `wire_ports.go` — `LocalSearcher`, `LocalHit`, `Sender`, `ReplyFunc`, `IndexerSink`, `EndorsementSink`.
- `handler.go` — `HandleMessage`, `handleQuery` (§3.3 with the §8a fix), `hitsToWire` (clamp rank, truncate N, omit zero `t`), peer_announce inbound (§3.5).
- `query.go` — `Query`, `QueryRequest`, `QueryResponse`, `MergedHit`, `selectTargets`, `routeResult`/`routeReject` (§3.4), `mergeResponses`, `resultIsMalformed`, errors `ErrEmptyQuery/ErrNoSender/ErrNoCapablePeers`.
- `misbehavior.go` — scores + banman (§3.6/§4.2).
- `ratelimit.go` — token bucket (§4.3).
- `peerbook.go` + `feeler.go` + `hitcache.go` — §4.1/§4.4/§4.5.
- **Import law:** `internal/swarmsearch` imports `contracts/ltepwire` and stdlib only. **NEVER `internal/indexer`/Bleve** (via `LocalSearcher`/`LocalHit`). No `torrent` package dep (via `LtepAdvertiser` + `PeerToken`/`Transport`).

### 7.3 `internal/engine/`
- `engine.go` (at seam line 178-179): construct `swarm := swarmsearch.New(log)` before `NewClient`; append the 3 slice-appendable callbacks + assign the 2 single-field ones (§2.1); `peerTracker` (§2.4); `snSearchSem`/`snReplySem` (§2.3); wire `swarm.SetSender(&swarmSender{peers})`, capabilities from the existing `e.sharing ltepwire.Sharing{...}` + `RuntimeFacts`, `SetPublisherPubkey`. Add `SwarmSearch() *swarmsearch.Protocol` and `ServicesMask()`.
- `engine.go` `SetIndex(idx)`: also `swarm.SetSearcher(&indexerSearcher{idx})` (and `SetSearcher(nil)` on unwire).
- **NEW `swarmadapter.go`** (Apache-2.0, no vendored patch): `swarmSender`, `indexerSearcher`, `gatedReplyWriter`, `errReplyOverloaded`, the `PeerToken` engine-side resolver, the bounded `announceWorker` (`announceQueueDepth=64`, lazy `announceOnce`).

### 7.4 `internal/searchmux/searchmux.go` — §5.1.
### 7.5 `internal/httpapi/dto.go` — add `SwarmBlock`/`SwarmHit` DTOs + `SearchResponse.Swarm`. **No import of swarmsearch/searchmux.**
### 7.6 `internal/daemon/search_adapter.go` — translate `swarmsearch.QueryResponse → httpapi.SwarmBlock`; `clampSearchTimeout`; `Options.SwarmStatus` reporter.
### 7.7 `internal/wirecompat/` — `VanillaPeer` double + CI unit assertion; `scenarios/` timing-sensitive silence.

**Layering law audit:** httpapi imports no subsystem (translation in daemon adapter) ✓; swarmsearch never imports Bleve (`LocalHit` mirror) ✓; single mask producer (`ltepwire.Announced` for `peer_announce.services`, never a swarmsearch copy) ✓; searchmux may import swarmsearch (native-response-types contract) ✓.

---

## 8. §6 defects to FIX + §5 invariants to PRESERVE (each → a test)

### 8.1 Defects to FIX

| # | Defect | Fix | Test |
|---|---|---|---|
| **(a)** | Valid-bencode wrong-shape `query` is charged nothing (legacy `handleQuery` DecodeQuery-fail only logs; asymmetric with the 4 other Decode/peek sites that charge 20). | Charge `ScoreBadBencode (20)` reason `bad_query` in the decode-error arm. | `TestHandleQueryDecodeErrorCharges`: feed a frame that passes `peekHeader` (valid dict, msg_type=0, decodable txid) but with `q` as an int; assert (1) no reply fires AND (2) misbehavior score += 20. (Legacy `TestHandleQueryDecodeErrorReturns` only asserted no-reply.) |
| **(b)** | Year-1 zero timestamp: `hitsToWire` unconditionally `T:h.AddedAt.Unix()`; production searcher never sets AddedAt; `time.Time{}.Unix() = -62135596800 != 0` so omitempty doesn't drop it → every hit ships `1:ti-62135596800e`. | Emit `t` only when AddedAt is non-zero (map zero→`T=0` so omitempty drops it); codec NEVER emits negative `t`. | `TestHitZeroTimestampOmitsT`: build a hit from zero AddedAt → assert 147-byte frame (no `t` key); `TestCodecNeverEmitsNegativeT`. |
| **(c)** | `Hit.N` untruncated despite the "~60 bytes" doc comment — arbitrarily long names on the wire. | Truncate `N` to 60 bytes on encode, UTF-8 rune-boundary-safe (also `FileMatch.FP`). | `TestHitNameTruncated`: 200-byte name → wire `n` ≤60 bytes, valid UTF-8, no mid-rune split. |
| **(d)** | Capability downgrades never reach the wire: legacy ORs `DefaultServices()` (incl. ShareLocal/FileHits/ContentHits) into the announced mask, so a `ShareLocal=0` node (rejects every query) still advertises query-capable. | Outbound `peer_announce.services` = `ltepwire.Announced(Sharing, RuntimeFacts)` (no static floor), sourced from `Engine.ServicesMask()` at send time. | `TestPeerAnnounceConsumesAnnounced`: set `ShareLocal=0` → assert wire `services` has bit 0 clear (golden #3 `0x2E0`); a `Publisher`-off node → golden `0x2E6` etc.; downgrade reaches the peer. |

### 8.2 Invariants to PRESERVE

| Invariant | Test |
|---|---|
| Frames only through the capability-token transport; never to a non-advertising peer. | Compile-time: no bare-address send API exists (`grep` guard + `SendExtension(PeerToken,...)` only). VanillaPeer silence (§6.3). |
| Inner msg_type numbering frozen 0–8. | `TestMsgTypeConstantsFrozen`. |
| txid echo mandatory + asked-set anti-spoof. | `TestResultFromUnaskedPeerCharged` (live txid, sender not in asked → `ScoreUnexpectedMessage`, dropped); `TestStaleTxIDCharged`; `TestMalformedChargedBeforeLookup`. |
| ServiceBits append-only, unknown bits ignored never rejected. | `TestUnknownServiceBitsIgnored` (peer_announce with bit 40 set → stored, no reject). |
| Absence of peer_announce = services 0 but peer still answered. | `TestNoAnnounceStillAnswered`. |
| All-zero pk rejected, rest of frame processed. | `TestAllZeroPkRejectedFrameProcessed`. |
| Endorsements only when endorser announced own pk (cap 10, asymmetric encode/decode). | `TestEndorsedEncodeErrorsOnCap`, `TestEndorsedDecodeTruncatesAndFilters`, `TestEndorseRequiresOwnPk`. |
| `ShareLocal==1` treated as 0 (reject 4). | `TestShareLocalOneFailsClosed`. |
| Empty scope always accepted; unknown letters ignored; explicit c/f against a lacking node → reject 2 (never downgraded); c-before-f order. | `TestScopeCBeforeF` (node lacking both, scope "fc" → `_c`). |
| Ban/misbehavior strictly local, never gossiped, survive OnPeerClosed, not restart. | `TestForgetKeepsBanned`, `TestBanAutoClearsOnExpiry`. |
| Rate-bucket starts full. | `TestBucketStartsFull` (10 immediate queries pass, 11th rate-limited). |
| Async dual-256 semaphore, drop-on-full. | `TestInboundDropOnFull`, `TestReplyOverloadedReturnsErr`. |
| swarmsearch never imports Bleve; Layer S never touches HTTP API. | Import-graph test / `go list -deps` assertion in CI. |

---

## 9. DoD → test mapping (every Slice 7 DoD bullet)

| DoD bullet | Test / gate |
|---|---|
| Envelope codec 0–3 in `contracts/ltepwire` with golden vectors | `ltepwire.TestEnvelopeGoldenVectors` (the §1.5 table, byte-exact) |
| `EncodeResult` forces empty-list `hits` | golden `d4:hitsle...` (empty-hits vector) |
| Decode strictness (msg_type re-assert, trailing bytes) | `TestDecodeWrongMsgTypeErrors`, `TestDecodeTrailingBytesErrors` |
| Endorsed cap asymmetric | `TestEndorsedEncodeErrorsOnCap` / `TestEndorsedDecodeTruncatesAndFilters` |
| Two SwartzNet peers negotiate + answer scoped queries | `scenarios` two-`Protocol` query/result round-trip; C1→C0 `scope:"c"` → reject 2 |
| Compile-time no-bare-address send | source guard: only `Transport.SendExtension(PeerToken,...)`; `PeerToken` minted only in `OnRemoteHandshake` |
| Async off-read-loop dispatch, dual-256 sem, drop-on-full, payload copy | `TestInboundDropOnFull`, `TestReplyOverloadedReturnsErr`, `TestPayloadCopiedBeforeGoroutine` |
| VanillaPeer sees zero sn_search frames (4-direction), CI `-race` | `wirecompat.TestVanillaPeerSilence` (unit) + `scenarios` loopback variant |
| Real anacrolix v1.61.0 completes a download from us | local-only `scenarios/transfer`-style (belt-and-suspenders, not a CI unit gate) |
| POST /search swarm fan-in, §5.9 inline-error-under-200 | `httpapi.TestSearchSwarmErrorInline200`, `TestSearchLocalError500`, `TestSwarmSectionOmittedWhenUnwired`, `TestLocalHitsNeverNull` |
| Swarm timeout clamp (0,30s], default 2s, +500ms grace | `TestClampSearchTimeout` |
| `/status` SwarmStatus (KnownPeers/CapablePeers) | `TestStatusSwarmCounts` |
| §6 (a)/(b)/(c)/(d) fixes | the four tests in §8.1 |
| peer_announce consumes `ltepwire.Announced()` (defect d) | `TestPeerAnnounceConsumesAnnounced` (golden `0x2E0`/`0x2E6` on the wire) |
| Rate-limit token bucket 5 q/s burst 10 | `TestBucketStartsFull`, `TestRefillRate` |
| Misbehavior scores + BanThreshold 100 / 24h, local-only | `TestScoreConstantsFrozen`, `TestBanThreshold`, `TestForgetKeepsBanned` |
| swarmsearch never imports Bleve; httpapi zero-import | `go list -deps` CI import-graph assertion |

---

## 10. RESOLVED open questions + Slice-8 deferrals

### 10.1 Resolved decisions (decision → recommendation)

1. **Typed Marshal for sn_search structs.** DECISION: call `anacrolix/torrent/bencode`.Marshal **directly inside `contracts/ltepwire`**. `contracts/bencode` stays decode-only (its doc forbids a Marshal method). RECOMMENDATION: pin golden vectors against anacrolix output; add a `DECISIONS.md` entry.
2. **`Hit.N` truncation.** DECISION: **enforce a 60-byte cap on encode, UTF-8 rune-boundary-safe** (truncate at the last full rune ≤60 bytes). Also cap `FileMatch.FP`. The pinned golden vectors use `n="debian-12.5.0-amd64-netinst.iso"` (31 bytes, unaffected). RECOMMENDATION: drop the stale "roughly 60" comment, replace with the exact constant `MaxHitNameBytes = 60`.
3. **`rank` clamp.** DECISION: clamp `rank` to `0..1000` on encode (legacy `int(score*1000)` is unbounded). Golden `rank=640` unaffected.
4. **§6a fix reason string.** DECISION: reason `bad_query`, charge `ScoreBadBencode (20)`; the rebuild test asserts BOTH no-reply AND the charge.
5. **peer_announce `services` omitempty.** DECISION: **keep omitempty** (services=0 → absent on the wire, indistinguishable from omitted — matches legacy and the "absence = services 0, still answered" invariant).
6. **`services` is a bencode integer**, NOT `FormatHex` (that's only the HTTP `/capabilities` readout). Sourced from `ltepwire.Announced()` / `Engine.ServicesMask()`.
7. **`ShareLocal==1` (swarm-only).** DECISION: **preserve fail-closed** (reject 4) in Slice 7 — `LocalSearcher` has no swarm-membership filter; a swarm-scoped searcher is out of scope. RECOMMENDATION: note as a v1.1 candidate (engine `handle.go`/`snapshot.go` could later expose per-torrent in-swarm status).
8. **Ignored query filters (`lang`/`min_size`/`max_size`/`not_ih`) + scope-as-filter.** DECISION: **carry forward as a documented spec-tolerated MAY** — decode + round-trip through the codec (wire-compat), but the handler uses only `Q/Scope/Limit`, and scope is used for capability-reject, not result narrowing. Not implemented in Slice 7.
9. **`partial` key.** DECISION: wire-reserved; round-trips through the codec; legacy handler never sets it; Slice 7 does not populate it.
10. **bit-9 `BitSetReconciliation` in Slice 7.** DECISION: **RuntimeFacts.Reconciliation = false (bit 9 clear) until Slice 8** — do not invite sync frames we cannot answer. Carry the *gate* (a sync frame from a peer lacking the cap → reject 2 + `ScoreUnexpectedMessage`) but do not advertise the bit.
11. **Endorsed/pk emission.** DECISION: Slice 7 **decodes/consumes** `endorsed`/`pk` (gossip intake, routed to no-op sinks) and **emits `pk` when `caps.Publisher>0` AND identity present**; **does NOT emit `endorsed`** (legacy is receive-only for endorsements). Matches legacy; emission of endorsements deferred.
12. **IndexerSink/EndorsementSink wiring.** DECISION: Slice 7 decodes + stores `services`/`pk` on PeerState and wires the two sinks as **no-op stubs** until dhtindex/bootstrap slices land.
13. **PeerBook + feeler in Slice 7.** DECISION: **include** (constants fully specified; closes eclipse-resistance). Outbound path stays nil-safe if a future refactor drops them.
14. **doc-06 rate-limit drift.** DECISION: the **token bucket (5 q/s, burst 10) is the wire contract** (SPEC §6.3 "follow the code"). RECOMMENDATION: correct doc-06's one-outstanding-query / hourly-budget text in this slice (docs-track-code rule) or explicitly defer to a docs slice with a `DECISIONS.md` note.
15. **Reason strings as contract.** DECISION: treat reason strings (`unsupported_scope_c`/`_f`, `searcher_disabled`, `query_too_short`, `rate_limited`, `local_error`) as **part of the pinned wire contract** (they appear in golden vectors), and the c-before-f order as contract.
16. **PeerToken mint point.** DECISION: mint **strictly at `OnRemoteHandshake`** (remote id known); `PeerConnAdded` only records the conn and yields **no** token (remote hasn't advertised yet, cannot send).
17. **swarm timeout default.** DECISION: **2000 ms** (SPEC §2.9), clamp `(0,30s]`, ctx grace `+500ms`.

### 10.2 DEFERRED to Slice 8 (do NOT build in Slice 7)

- msg_types **4–8** bodies: `sync_begin/symbols/need/records/end`. Slice 7 **reserves** the numbers; the dispatcher routes 4–8 to a reserved sync stub gated on `BitSetReconciliation` (which Slice 7 does not advertise) — it must NOT charge `ScoreUnexpectedMessage` for a future Slice-8 peer's legitimate sync frame.
- `sync_session.go`, `sync_wire.go`, `sync_start.go`, `riblt.go`, `record_cache.go`.
- Ports `RecordSource.LocalRecords`, `RecordSink.Add`, `PublisherObserver.NotePublisherSeen`.
- Constants `MaxSyncSessionsPerPeer=4`, `SyncSessionStaleAfter=2m`, `MaxSymbolsPerMessage=100`.
- `ScoreBadRecordSig=20` enforcement (per-record ed25519 sig + hashcash PoW verify in the sink). Keep the const + the `PeerAnnounce.endorsed`/`pk` schema frozen now (already in the envelope), but their sinks/verification arrive in Slice 8.
- Endorsement **emission** (channel C is receive-only in legacy).

---

**Divergences from legacy, flagged (SPEC/BEP-draft wins on conflict):** (1) §6a charge added; (2) year-1 `t` omitted; (3) `Hit.N` truncated to 60B; (4) outbound `services` from `Announced()` not `DefaultServices()` floor; (5) doc-06 rate-limit text corrected to match the token bucket; (6) legacy bare-address `Sender` replaced by compile-time `PeerToken`/`SendExtension`; (7) httpapi no longer imports swarmsearch (translation in daemon adapter). All other behavior mirrors legacy exactly.