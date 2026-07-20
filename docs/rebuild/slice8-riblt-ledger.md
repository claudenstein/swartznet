All seams verified. Here is the synthesized, implementation-ready ledger.

---

# SLICE 8 LEDGER — RIBLT Set-Reconciliation + Signed-Record Substrate

Reconciled from 5 extractions. **SPEC wins over legacy on conflict** (charge-on-limit, `kw≤64`, ID de-dup single-source). "Frozen" = a second implementation must produce byte-identical output. Current-tree seams verified against `HEAD` on `overnight/test-harness-2026-04-20`.

---

## 0. Package map (frozen homes)

| Concern | New/edited home | Import law |
|---|---|---|
| RIBLT symbol + `Key()` + `contributes()` + Encoder/Decoder/peel | **NEW `contracts/riblt/`** | stdlib-only, **no crypto** (no SHA-256 here) |
| `Record` + `ElementID` + `SigMessage` + `Verify` + `SignAndMine` + `MinePoW/VerifyPoW` + `MaxKeywordBytes=64` | **NEW `contracts/record/`** | `crypto/sha256`, `crypto/ed25519`, `encoding/binary`, `errors` only |
| Sync frame codec (msg 4–8 bencode) | **extend `contracts/ltepwire/wire.go`** | may use `anacrolix/torrent/bencode` (same pre-existing exception as msg 0–3) |
| `SyncSession` (I/O-free), `RecordCache`, handler `onSync*`, `StartSync`/`SendSyncNeed`/`CloseSync`/`WaitSyncConverged`, ports | `internal/swarmsearch` (replace S7 stub) | imports `contracts/{riblt,record,ltepwire,token}`; **NEVER** `companion`/`indexer`/`torrent`/`bleve`; sends via `Transport.SendExtension(PeerToken,…)` (no bare `Sender`) |
| Mint-on-GotInfo, cache wiring, prune goroutine, `SetSigner`, bit-9 flip | `internal/engine` | imports `contracts/record`, `contracts/token`, `swarmsearch` |
| `/aggregate` `cache_size`+`reconciliation` | `internal/httpapi` DTO + `daemon` adapter + `cmd_status` | httpapi imports **no** subsystem; daemon adapter bridges `eng.RecordCache().Len()` |

**Layering resolution (open-Q):** `contracts/record` is a stdlib-only leaf, so `swarmsearch` importing it does **not** violate the "no companion/indexer" law (already imports `contracts/ltepwire`+`contracts/token`). This collapses the three-way byte duplication: **`swarmsearch.LocalRecord = record.Record` (type alias)**, `localRecordID`/`cacheRecordID`/`verifyLocalRecordSig` all become thin calls to `record.ElementID` / `record.Verify`. `companion.Record` (later slice) also aliases `record.Record`. No cycle — `contracts/record` is a leaf.

---

## 1. `contracts/riblt` — frozen math

```go
type RIBLTElement [32]byte
type RIBLTSymbol struct {
    Count   int32    // SIGNED — diff = sender.Count - local.Count can go negative
    KeyXOR  uint64
    DataXOR [32]byte
}
```

**Element key — FNV-1a-64 over all 32 bytes** (the type doc-comment "first 8 bytes LE" is STALE; do **not** port it — linearity would let the decoder hallucinate pure symbols):
```go
func (e RIBLTElement) Key() uint64 {
    var h uint64 = 0xCBF29CE484222325 // FNV offset basis
    for _, b := range e { h ^= uint64(b); h *= 0x100000001B3 } // FNV prime
    return h
}
```

**`contributes` — membership (byte-exact):** SplitMix64 finalizer of `key + idx*golden`, then `% 2^(1+idx%12) == 0` (12-step geometric cycle, moduli 2,4,8,…,4096; avg rate ≈ 0.083):
```go
func contributes(key, symbolIdx uint64) bool {
    z := key + symbolIdx*0x9E3779B97F4A7C15
    z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) * 0x94D049BB133111EB
    z = z ^ (z >> 31)
    level := 1 + (symbolIdx % 12)   // 1..12
    return z%(uint64(1)<<level) == 0
}
```

**Encoder** `RIBLTEncoder{elems, nextIdx}`: `AddElement` appends (no dedup — caller dedups). `NextSymbol()` scans **every** element, includes iff `contributes(e.Key(), nextIdx)`: `Count++; KeyXOR ^= e.Key(); xorInto(&DataXOR, e)`; then `nextIdx++`. `NextSymbolIndex()` returns the index the next call emits → fills `sync_symbols.index`. Fully deterministic (two encoders over the same set produce identical streams).

**Decoder / peeler** `RIBLTDecoder{local, diffSymbols, decoded map[RIBLTElement]int, syntheticAdded, syntheticRemoved}`:
- `AddLocalElement(e)` seeds receiver-side elements.
- `AddRemoteSymbol(s)`: implicit `idx = len(diffSymbols)`; `ls := effectiveLocalSymbol(idx)`; append `diff{Count: s.Count-ls.Count, KeyXOR: s.KeyXOR^ls.KeyXOR, DataXOR: s.DataXOR^ls.DataXOR}`; then `peel()`.
- `effectiveLocalSymbol(idx)` recomputes over `local ∪ syntheticAdded \ syntheticRemoved` (added += Count, removed -= Count, both XOR key/data) so already-decoded elements zero against future frames.
- `peel()` loops until no change: find a symbol with `Count == +1 || Count == -1`; `copy(e[:], s.DataXOR[:])` (**for a pure symbol DataXOR IS the 32-byte element ID**); **self-consistency gate `e.Key() == s.KeyXOR`** (rejects ID collisions); skip if already decoded; `direction := int(s.Count)`; `decoded[e]=direction`; append to `syntheticAdded`(dir>0)/`syntheticRemoved`(dir<0); subtract `e` from **every** symbol `j` where `contributes(e.Key(), j)` (`Count -= int32(direction)`, XOR key+data out); `changed=true; break` (rescan). Worst case O(N²).
- `Converged()` iff **every** residual symbol is fully zero (`Count==0 && KeyXOR==0 && DataXOR all-zero`). `Added()`=dir>0 (**peer has, I lack** → `sync_need`), `Removed()`=dir<0 (**I have, peer lacks** → push). Map iteration → **unordered** (§9 canonicalizes).

**Sign convention:** `diff = sender − local`, so `+1` = element in sender not local = Added = NeedIDs; `−1` = in local not sender = Removed.

### Golden vectors (pin coded-symbol bytes + contributes cycle)
Set `{a,b,c}`, `ID = SHA-256(label)`:

| label | ID (hex) | `Key()` | `contributes` true at idx 0..15 |
|---|---|---|---|
| a | `ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb` | `0x28E80605A79C8642` | {12} |
| b | `3e23e8160039594a33894f6564e1b1348bbd7a0088d42c4acb73eeaed59c009d` | `0x01BB5C8E53136742` | {12,14} |
| c | `2e7d2c03a9507ae265ecf5b5356885a53393a2029d241394997265a1a25aefc6` | `0xA27ABAE3AB0736C8` | {0,1} |

Encoder stream over `{a,b,c}`, nonzero symbols in idx 0..23 (all others = zero symbol `{0,0,32×00}`):

| i | Count | KeyXOR | DataXOR | meaning |
|---|---|---|---|---|
| 0 | 1 | `0xA27ABAE3AB0736C8` | ID(c) | pure c |
| 1 | 1 | `0xA27ABAE3AB0736C8` | ID(c) | pure c |
| 12 | 3 | `0x8B29E0685F88D7C8` | `dac9450763729e62aca78b63cbaae8dc1fa837fa018c71aceb81fc8ad828a7e0` (a⊕b⊕c) | degree-3 |
| 14 | 1 | `0x01BB5C8E53136742` | ID(b) | pure b |

(`0x28E80605A79C8642 ^ 0x01BB5C8E53136742 ^ 0xA27ABAE3AB0736C8 = 0x8B29E0685F88D7C8`.)

- **Decode golden:** receiver `{a,b}`, sender `{a,b,c}` → idx-0 diff `{1, 0xA27ABAE3AB0736C8, ID(c)}` pure+self-consistent → `c` **Added**. Mirror (receiver has extra `x`) → `{-1,…}` → `x` **Removed**.
- **SplitMix64 pin (no real IDs needed):** `contributes(0x12345678DEADBEEF, idx)` true **only** at idx {1,2,12} across 0..15.
- **Determinism set `{one,two,three,four}`:** keys `0x3EEAC52C9E14CB37 / 0x72CD28D9A94DDC01 / 0x3B17E7C45CD4103A / 0x94012A14685C6324`. i=0 `{Count 4, KeyXOR 0xE331202503D16428, DataXOR c6e23deb8f7f47544bd120fd8d871188be9dde1c4f11065fe99d304791a11941}`; i=1 `{Count 2, KeyXOR 0xAF16CDD03488731E, DataXOR 8fb432b8ce678c36b70ab4b690ee2c65550201fcb9dcbffd792b2d4b48d0ca5f}`; i=2..11 zero.

---

## 2. `contracts/record` — signed-record substrate

```go
const MaxKeywordBytes = 64
type Record struct { // == swarmsearch.LocalRecord (alias)
    Pk  [32]byte
    Kw  string   // lowercased UTF-8, 1..64 bytes (enforced — SPEC over legacy)
    Ih  [20]byte
    T   int64
    Pow uint64
    Sig [64]byte
}
```

**`ElementID` (RIBLT id) — EXCLUDES pow+sig** so a re-signed record dedupes (SINGLE source of truth; kills the `localRecordID`/`cacheRecordID` dup):
```
ElementID = SHA-256( pk[32] || kw || ih[20] || LE64(T) )   // T little-endian, 8 bytes
```

**`SigMessage` — INCLUDES pow** (= ElementID preimage + `uvarint(Pow)`):
```
SigMessage = pk[32] || kw || ih[20] || LE64(T) || uvarint(Pow)
Sig  = ed25519.Sign(priv, SigMessage);  Verify = ed25519.Verify(pk, SigMessage, sig)
PoW  = SHA-256(SigMessage) has >= D leading-zero BITS (MSB-first per byte). D=0 this slice.
```
`SignAndMine(priv,pub,kw,ih,t,bits)` = **mine THEN sign** (signing first invalidates every nonce but the signed one). `MinePoW` refuses `bits>40`; `bits==0` no-op. `VerifyPoW(rec,minBits)` nil iff ≥minBits (minBits==0 skips).

### Golden vectors (RecordID de-dup + sig message)
- **A** `Pk=0xAA∥31×00`, `Kw="linux"`, `Ih=0x11∥19×00`, `T=1712649600`, `Pow=0`: `LE64(T)=80f5146600000000`; **RecordID=`64bcf23a1e274246f1f82872d0ee10c0ddd6024dd396370b65d26277cd3f6ba0`**; sigMsg(66B)=`aa…00 6c696e7578 11…00 80f5146600000000 00`; `ElementID.Key()=0x840cc27f49744559`, `contributes` idx0..13 true at {0,12}.
- **B** = A but `Pow=300`: **SAME RecordID** (de-dup property under test); sigMsg(67B) tail `…80f5146600000000 ac02`.
- **C** zero pk/ih, `Kw="a"`, `T=0`, `Pow=0`: **RecordID=`193aa0b8735340fedf7f0aa52873c3c5f0b77a67b2b9b2f60c25d3e616d2cf41`**; sigMsg(62B)=`00×32 61 00×20 00×8 00`; `Key()=0xa635c0cb0d412495`, `contributes` idx0..13 true at {0,2}.

---

## 3. `SyncSession` state machine (I/O-free, `internal/swarmsearch`)

Roles `RoleInitiator=1 RoleResponder=2`. Phases (iota): `PhaseIdle=0 → PhaseBegun=1 → PhaseSymbolsFlowing=2 → PhaseNeeded=3 → PhaseFulfilled=4 → PhaseEnded=5`. Every public method holds `mu` (Apply* on read-loop, NeedIDs/Converged on caller). `NewSyncSession(txid,role,records)` indexes records by `ElementID` into `map[[32]byte]LocalRecord`, seeds encoder (`AddElement`) **and** decoder (`AddLocalElement`); defaults `maxSymbols=2000, maxBytes=1<<20`. **The local set is snapshotted at construction — an in-flight `RecordCache.Add` does NOT enter a live session** (frozen lifecycle).

| Method | Role | From→To | Guards |
|---|---|---|---|
| `Begin(filter)` | Init | Idle→Begun | emits `SyncBegin{Algo:"riblt-v1", ElementSize:32, LocalCount:enc.Len(), MaxSymbols, MaxBytes}` |
| `ApplyBegin(m)` | Resp | Idle→Begun | txid match; `m.ElementSize==32`; **downward negotiate**: `if m.MaxSymbols>0 && m.MaxSymbols<s.maxSymbols {s.maxSymbols=m.MaxSymbols}` (same MaxBytes) — strictly min(peer,own), lower only |
| `ProduceSymbols(count)` | sender | Begun/Flowing→Flowing | clamp to `MaxSymbolsPerMessage`(100); clamp to `maxSymbols-symbolsOut`, ≤0 → `ErrSymbolBudgetExceeded`; `baseIdx=enc.NextSymbolIndex()`; `symbolsOut+=len` |
| `ApplySymbols(m)` | recv | Begun/Flowing→Flowing | txid; **HARD ABORT `if uint32(s.symbolsIn)!=m.Index → "desync"`**; `symbolsIn+len>maxSymbols → ErrSymbolBudgetExceeded`; feed each `dec.AddRemoteSymbol`; `symbolsIn+=len`. **`m.Done` now read (see §5 multi-batch)** |
| `NeedFrame(ids)` | recv | Flowing/Begun→Needed | `len>1000` err; empty ids legal ("done decoding") |
| `ApplyNeed(m)` | resp | no guard, no phase change | txid; ≤1000; each id 32B; → (found records, missing ids) from indexed map |
| `BuildRecordsFrame(recs,missing)` | resp | →Fulfilled | `len(recs)>500` err; `bytesOut += syncRecordWireSize(r)` per record, `+=32` per missing |
| `ApplyRecords(m)` | recv | **Needed/Fulfilled only**→Fulfilled | txid; per-record `len(pk)==32 && len(ih)==20 && len(sig)==64`; **`if s.bytesIn+frameBytes > s.maxBytes → ErrSyncBytesBudgetExceeded`**; records treated OPAQUE (sig verified by sink) |
| `Finish(status)` | either | any→Ended | empty→"converged"; emits `SyncEnd{Decoded:recordsIn, Sent:symbolsOut, BytesIn, BytesOut}` — **never sets AbortCode** |
| `ApplyEnd(m)` | either | any→Ended | txid |

**Byte accounting = SEMANTIC size, never bencoded:** `syncRecordWireSize(r) = 32 + len(r.Kw) + 20 + 8 + 8 + 64` (=132+len(kw)); each `missing` id = 32.

---

## 4. Sync wire (`contracts/ltepwire`, msg 4–8) + golden vectors

Discriminators (already reserved in `wire.go:47-51`): `SyncBegin=4 SyncSymbols=5 SyncNeed=6 SyncRecords=7 SyncEnd=8`. Status: `"converged" "limit_exceeded" "aborted"`. Codec via `bencode.Marshal` → **canonical lexicographically-sorted dict keys** (frozen for byte-exactness). Every encoder force-sets `MsgType`; every decoder re-checks it (`"not a sync_X, msg_type=%d"`), enforces the same size invariants (defence in depth). `DecodeSyncBegin` rejects `element_size!=32`. **`kw≤64` enforced at wire decode (per record) — added this slice (SPEC over legacy, which did not cap it).**

| Frame | Keys (bencode) | Rules |
|---|---|---|
| **SyncBegin(4)** | `algo, element_size, filter, local_count, max_bytes?, max_symbols?, msg_type, txid` | `filter` NO omitempty (empty → `6:filterde`); algo default `"riblt-v1"`; element_size default+require 32 |
| **SyncFilter** | `pubkeys?, since?, prefix?` | pubkeys each 32B; nil = every publisher |
| **SyncSymbol** | `b, c, h` | `b`=DataXOR **exactly 32B**; `c`=Count int32 signed; `h`=KeyXOR **uint64 unsigned** (decimal may exceed 2^63) |
| **SyncSymbols(5)** | `done?, index, msg_type, symbols, txid` | `1 ≤ len(symbols) ≤ 100`; empty = encode+decode error; `index` = position of first symbol |
| **SyncNeed(6)** | `ids, msg_type, txid` | ≤1000; each 32B; nil→`[]`; zero-length = "done decoding" |
| **SyncRecord** | `ih, kw, pk, pow, sig, t` | `len(pk)==32, len(ih)==20, len(sig)==64`; **`len(kw)≤64`** |
| **SyncRecords(7)** | `missing?, msg_type, records, txid` | records ≤500; `missing` = ids sender also lacks |
| **SyncEnd(8)** | `abort_code?, bytes_in?, bytes_out?, decoded?, msg_type, sent?, status, txid` | status default "converged"; **`abort_code` never populated this slice** |

Caps: `MaxSymbolsPerMessage=100 MaxRecordsPerMessage=500 MaxNeedIDsPerMessage=1000`. Budgets: `DefaultSyncMaxSymbols=2000 DefaultSyncMaxBytes=1<<20`.

### Golden vectors (byte-verified vs anacrolix bencode v1.61.0)
- **sync_begin** (txid=7, empty filter, local_count=3, max_symbols=2000, max_bytes=1048576), 126 bytes:
  `d4:algo8:riblt-v112:element_sizei32e6:filterde11:local_counti3e9:max_bytesi1048576e11:max_symbolsi2000e8:msg_typei4e4:txidi7ee`
  hex `64343a616c676f383a7269626c742d763131323a656c656d656e745f73697a6569333265363a66696c746572646531313a6c6f63616c5f636f756e74693365393a6d61785f627974657369313034383537366531313a6d61785f73796d626f6c73693230303065383a6d73675f74797065693465343a7478696469376565`
- **sync_symbols** (txid=7, index=0, one symbol c=1 h=`0x0102030405060708`=72623859790382856 b=`00..1f`), 113 bytes:
  `d5:indexi0e8:msg_typei5e7:symbolsld1:b32:<00..1f>1:ci1e1:hi72623859790382856eee4:txidi7ee`
  hex `64353a696e646578693065383a6d73675f74797065693565373a73796d626f6c736c64313a6233323a000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f313a63693165313a68693732363233383539373930333832383536656565343a7478696469376565`

---

## 5. Initiator + multi-batch streaming (`sync_start.go`)

**Transport divergence (must honor):** the rebuild has **no bare-address `Sender`** — only token-gated `Transport.SendExtension(peer PeerToken, frame []byte)` (`ports.go:59`). `PeerToken` is minted in `OnRemoteHandshake` and stored as `PeerState.token`. `StartSync`/`SendSyncNeed`/`CloseSync` **resolve the stored `PeerToken` and send via `Transport`**, never `Sender.Send(addr,…)`. Inbound responder frames arrive through `HandleMessage(..., reply ReplyFunc)`; the session is keyed `(peerAddr, txid)` regardless of role, so the initiator's inbound `sync_symbols`/`sync_records`/`sync_end` route to its registered session.

- **`StartSync(peerAddr, filter, localRecords)`**: order — `ErrSyncPeerUnknown` (peer absent) → `ErrSyncCapabilityMissing` (`!ps.Services.Has(BitSetReconciliation)`) → `ErrNoSender`/no-token → `txid=nextTxID()` → `NewSyncSession(RoleInitiator)` → `Begin(filter)` → **`registerSyncSession` BEFORE encode/send** (a fast local-bus reply must not lose the registration race) → encode+`Transport.SendExtension`; on encode/send error `releaseSyncSession` then return. Returns `*SyncSession`. `nextTxID()=atomic.AddUint32(&txidCounter,1)` → first txid is **1** (0 never used).
- **`SendSyncNeed(peer, sess, ids [][32]byte)`**: `sess.NeedFrame(ids)` → encode → `Transport.SendExtension`.
- **`CloseSync(peer, sess, status)`**: `sess.Finish(status)`, `defer releaseSyncSession(peer, txid)`, encode+send `sync_end`. Idempotent.
- **`WaitSyncConverged(sess, timeout)`**: polls `sess.Converged()` every 10ms until timeout.

**Multi-batch mechanism (Slice-8 charter — EXCEEDS legacy one-batch responder). DECISION:**
1. Responder `onSyncBegin` registers the session, then a **bounded per-session pump** (bound to `bgCtx`, capped by negotiated `maxSymbols` + the 2-min reaper — deterministic code, LLM not in loop) emits successive `sync_symbols` batches (each ≤100, `index = enc.NextSymbolIndex()` captured pre-produce, incrementing) with light pacing.
2. The pump **stops** when (a) the session advances to `PhaseNeeded` (initiator sent `sync_need` = "I converged", the rateless stop signal — there is no "send more" frame in the vocabulary), or (b) `symbolsOut` reaches negotiated `maxSymbols` → final batch `done=1`.
3. Initiator applies each batch (strict `index` check), polls `dec.Converged()`; once converged issues `sync_need(sortedAddedIDs)` for what it lacks, and **proactively pushes** `sync_records` for what the peer lacks (`Removed()`), giving bidirectional (symmetric-difference) convergence.
4. Responder answers `sync_need` with `sync_records{records, missing}`. Both sinks ingest. RIBLT overhead ≈1.35×d; a 250-record symmetric diff needs ≈340 symbols ≈4 batches, well under the 2000 budget → the 12-step cycle (mod up to 4096) yields enough pure symbols. `done=1` is now load-bearing (legacy ignored it).

---

## 6. Handler dispatch (replace S7 stub) + engine + bit-9 flip

**Replace `internal/swarmsearch/handler.go:55-62`** (the `case MsgTypeSyncBegin..SyncEnd:` arm that does `p.ban.Add(peerAddr, ScoreUnexpectedMessage)` + `sendReject(RejectUnsupportedScope,"reconciliation_unsupported")`) with `p.handleSyncFrame(peerAddr, payload, reply)`.

`handleSyncFrame`: (1) **capability gate** `known && ps.Services.Has(BitSetReconciliation)`; miss → `sendReject(RejectUnsupportedScope=2,"sync_not_supported")` + `chargeMisbehavior(ScoreUnexpectedMessage=10,"sync_without_cap")`. (2) **lazy `reapStaleSyncSessions`** on every inbound frame (`SyncSessionStaleAfter=2*time.Minute`; each reaped session emits `sync_end "aborted"`; no background goroutine). (3) decode (error → `chargeMisbehavior(ScoreBadBencode=20,"bad_sync_*")` + drop) → `onSyncX`.

- **onSyncBegin:** `syncSessionCount(peer, excl=txid) >= MaxSyncSessionsPerPeer(4)` → `sendReject(RejectTooExpensive=1,"too_many_sessions")` + charge 10. Pull `src.LocalRecords(m.Filter)` (nil/err → no records). `NewSyncSession(Responder)` + `ApplyBegin` + register. **Zero records → `sync_end "converged"` + release.** Else start the §5 pump.
- **onSyncSymbols:** unknown session → silent drop. `ApplySymbols` err → status = `limit_exceeded` iff `errors.Is(ErrSymbolBudgetExceeded)` else `aborted`; `sendSyncEnd`; release.
- **onSyncNeed:** `ApplyNeed`→`BuildRecordsFrame`→`sync_records` (errors silent-drop, no charge). This also flips the pump's stop flag.
- **onSyncRecords:** `ApplyRecords` err → `limit_exceeded` iff `ErrSyncBytesBudgetExceeded` else `aborted`; `sendSyncEnd`+release. Success → `ingestSyncRecords`.
- **onSyncEnd:** `ApplyEnd` + release (idempotent).
- **ingestSyncRecords:** per record re-check sizes + **`record.Verify`**; bad sig → `chargeMisbehavior(ScoreBadRecordSig=20,"bad_record_sig")` + drop **that record only**; good → `sink.Add`; once per distinct pubkey → `PublisherObserver.NotePublisherSeen(pk)`.

**⚠ RESOLVED DIVERGENCE (SPEC over legacy):** legacy charges `ScoreUnexpectedMessage` on **every** apply error including `limit_exceeded`. SPEC: "symbol-budget overrun → limit_exceeded; **other** violations → aborted + misbehavior charge." → **Charge ONLY when `status == aborted`.** A peer that merely hits the negotiated budget (`limit_exceeded`) is **not** penalized; only phase/txid/index-desync/size violations (aborted) carry the charge.

**New ports (`ports.go`), mutex-guarded, nil-tolerant:**
```go
type RecordSource interface { LocalRecords(filter SyncFilter) ([]LocalRecord, error) }
type RecordSink   interface { Add(r LocalRecord) }
type PublisherObserver interface { NotePublisherSeen(pubkey [32]byte) }
type SyncFilter struct { Pubkeys [][]byte; Since int64; Prefix string } // conjunctive; zero = all
```
Setters `SetRecordSource/SetRecordSink/SetPublisherObserver`. Session registry `syncSessions map[string]map[uint32]*SyncSession` keyed `(peerAddr,txid)`, serving both roles, with `register/lookup/syncSessionCount/releaseSyncSession`.

**`RecordCache`** (both source+sink): `map[[32]byte]LocalRecord` keyed by `record.ElementID` + `order [][32]byte` FIFO + `max int` (0=unlimited). `Add` idempotent (same pk/kw/ih/t → overwrite, no new order entry), evicts FIFO head (skipping stale) before write at cap. `PruneOlderThan(cutoff)` drops `T < cutoff` (**strict `<`**). `LocalRecords(filter)`: `matchFilter` conjuncts — pubkey-set membership; `Since>0 → T >= Since` (**`>=` inclusive**); `Prefix!="" → strings.HasPrefix(Kw,Prefix)`. Lowering cap does **not** proactively evict (next `Add` does). Also `Remove/RemoveByRecord/Len/Get/Snapshot/MaxRecords/SetMaxRecords`.

**Engine:** constants `DefaultRecordCacheMax=100_000`, `DefaultRecordCacheMaxAge=30*24h`, `DefaultRecordCachePruneInterval=1h` (regtest 200ms/500ms). In `engine.New` right after `swarm := swarmsearch.New(log)`:
```go
recCache := swarmsearch.NewRecordCache(); recCache.SetMaxRecords(DefaultRecordCacheMax)
swarm.SetRecordSource(recCache); swarm.SetRecordSink(recCache)
```
Store on Engine; `RecordCache() *swarmsearch.RecordCache` (never nil); prune goroutine on `bgCtx` ticking `pruneInterval` → `recCache.PruneOlderThan(now-maxAge)`. **Mint** `MintAggregateRecords(ih [20]byte, name string)`: no-op if `signer==nil || recCache==nil`; `tokens := token.TokenizeAll(name)` (**`contracts/token`**, byte-frozen; NOT `dhtindex`); per token `record.SignAndMine(priv,pub,kw,ih,now,bits=0)` → `LocalRecord` → `recCache.Add`. **PoW bits = 0 this slice.** Call site: `engine.autoIndex` (the `index.go` GotInfo waiter), after `indexerDocFromTorrent(h)`, **gated on signer/cache — NOT on `h.isIndexing()`** (must fire even when Layer-L off).

**Identity seam — DECISION (log in `DECISIONS.md`):** the daemon owns identity (`d.Identity`). Add **`Engine.SetSigner(signer, pub [32]byte)`** called by the daemon **before `RestoreSession()`** (so restored torrents mint), rather than `engine.New` loading `cfg.IdentityPath` (legacy). This preserves the rebuild's daemon-owns-identity architecture; update the legacy-style `mint_records_test.go` to call `SetSigner` instead of setting `cfg.IdentityPath`.

**Bit-9 re-enable (`internal/engine/capability.go:42`):** `Reconciliation: false → true`; drop the "FALSE until Slice 8" doc language (lines 33-38). Masks: default `0x0FD → 0x2FD`, `--no-index` `0x0ED → 0x2ED` (bit 9 = 0x200). In `capability_test.go` update `0x0FD→0x2FD` (line 41), both `0x0ED→0x2ED` (lines 59,70), and the downgrade case `0x0F0` (bit 9 survives a downgrade that clears only bits 0..3 — so it becomes `0x2F0`), and **delete/invert `TestReconciliationBitNotAdvertised`** (lines 107+) which asserts bit 9 is clear. `DefaultServices()` already ORs `BitSetReconciliation=1<<9`. This makes the capability gate valid on both sides.

**`/aggregate` readout — DECISION:** add `cache_size int json:"cache_size"` and `reconciliation bool json:"reconciliation"` to `AggregateStatusResponse`; daemon `controllerAdapter.aggregate()` fills `cache_size = eng.RecordCache().Len()`, `reconciliation` mirrors `RuntimeFacts.Reconciliation`; keep `services` on the single `ServicesReporter` render path. `cmd_status.go` prints `records cached: N / reconciliation: on` in the Layer-S block. Web UI field render **deferred** (optional).

---

## 7. Rebuild seams (layering laws honored)

- `contracts/riblt`: builtins only (no crypto — SHA-256 lives in `contracts/record`). Golden-test style mirrors `contracts/sign`/`contracts/ltepwire` (package doc "a second implementation must produce byte-identical output").
- `contracts/record`: `crypto/sha256`, `crypto/ed25519`, `encoding/binary`, `errors` only.
- `contracts/ltepwire`: sync codec uses `anacrolix/torrent/bencode` (pre-existing exception, same as msg 0–3).
- `swarmsearch`: imports `contracts/{riblt,record,ltepwire,token}`; **no** `companion`/`indexer`/`torrent`/`bleve`; sends via `Transport.SendExtension(PeerToken,…)`.
- `httpapi`: no subsystem import — the daemon adapter bridges `RecordCache().Len()` and the reconciliation flag; `services` stays on the single `ServicesReporter` path.
- Three-layer isolation preserved: Layer-S (`swarmsearch`) carries `swarmsearch.QueryResponse`/session types; sync records reconcile at the `httpapi`/daemon seam, not via a shared cross-layer type. Layer-D DHT is Slice 9.

---

## 8. DoD → test mapping

| DoD | Test(s) |
|---|---|
| Golden vectors pin coded-symbol bytes + contributes cycle | `contracts/riblt/riblt_test.go`: `TestKeyFNVGolden`, `TestContributesSplitMix64Pin` (`0x12345678DEADBEEF`→{1,2,12}), `TestEncodeStreamGolden` ({a,b,c} idx 0..23), `TestDeterminismFourSet` ({one..four} i=0/i=1), `TestConvergedRejectsNonZeroDataXOR` |
| Two peers, 250-record symmetric difference converge **multi-batch** | `internal/wirecompat/scenarios/reconcile_test.go` (excluded from CI; local `go test -race`): both caches converge to the union; assert **>1** `sync_symbols` batch and monotonically-incrementing `index`; assert `done=1` terminates |
| Two valid signings of one record → same RecordID | `contracts/record/record_test.go`: `TestElementIDExcludesPowSig` (A vs B same id, diff pow/sig), `TestElementID/SigMessageGolden` (A/B/C), `TestSignVerifyRoundTrip`, `TestCacheIDMatchesSessionID` (alias equality) |
| StartSync reachable | `sync_start_test.go`: `TestStartSyncUnknownPeer`(ErrSyncPeerUnknown), `TestStartSyncCapabilityMissing`, `TestStartSyncRegistersBeforeSend`, `TestStartSyncViaTransportToken`; plus a non-test daemon caller path |
| Budgets downward | `sync_session_test.go`: `TestApplyBeginNegotiatesDownward` (peer 500 lowers own 2000; peer 3000 leaves 2000) |
| limit_exceeded vs aborted **+ charge policy** | `handler_test.go`: `TestOnSyncSymbolsBudget_LimitExceeded_NoCharge`, `TestOnSyncSymbolsDesync_Aborted_Charges`, `TestOnSyncRecordsBytes_LimitExceeded_NoCharge`, `TestOnSyncRecordsPhase_Aborted_Charges` (SPEC resolution) |
| Semantic-size accounting | `TestSyncRecordWireSize` (132+len(kw), missing=32), `TestApplyRecordsBytesBudget` |
| Index-mismatch hard abort | `TestApplySymbolsIndexMismatchAborts` (`uint32(symbolsIn)!=Index` → error, no peel) |
| Capability gate / bit-9 | `capability_test.go` masks `0x2FD`/`0x2ED`; `swarmsearch` gate test: sync frame from peer without bit 9 → `RejectUnsupportedScope` + charge |

---

## 9. Resolved open questions

1. **Charge-on-limit:** SPEC wins — `limit_exceeded` is **penalty-free**; only `aborted` charges `ScoreUnexpectedMessage`.
2. **`kw≤64`:** enforce at **mint** (`record` rejects >64/empty), **wire decode** (`DecodeSyncRecords` per record), and implicitly at ingest. Primary = wire decode + mint.
3. **Duplicated ElementID:** collapse to **one** `contracts/record.ElementID`; `localRecordID`/`cacheRecordID`/`verifyLocalRecordSig` become calls to it / `record.Verify`. `LocalRecord = record.Record` alias.
4. **`abort_code`:** keep the field (`omitempty`), **never populate** this slice; numeric vocabulary deferred (Slice 9+).
5. **NeedIDs ordering:** map order is nondeterministic → **sort `Added()` ids ascending** before building `sync_need` (canonical wire, deterministic tests).
6. **`done`/multi-batch:** `done` becomes load-bearing; responder pump streams batches, stops on `sync_need` (converged) or budget (final `done=1`). See §5.
7. **Session snapshot lifecycle:** `LocalRecords` called **once** per `sync_begin`, frozen for the session; live `Add` does not retro-enter.
8. **Filter comparator asymmetry:** intentional, **frozen** — `matchFilter` uses `T >= Since` (inclusive floor); `PruneOlderThan` uses `T < cutoff` (strict drop).
9. **`PruneOldestTo`:** not present in legacy — **drop the stale doc reference**; lazy FIFO eviction on `Add` is the only drain (no proactive helper this slice).
10. **Peel O(N²):** the 2000-symbol budget is the sole guard; ≤4M ops worst case synchronously under the session mutex — acceptable, no separate iteration bound.
11. **Identity wiring:** `Engine.SetSigner` called by daemon (option b); update mint test. Log in `DECISIONS.md`.
12. **SPEC §-refs in ported comments:** repoint `sync_wire.go`/`companion` `§2.x`/`§1.x` anchors at the rebuild `SPEC.md` sections when porting.
13. **`/aggregate` field names:** `cache_size` + `reconciliation` (booleans/ints as above).

---

## 10. Deferred to Slice 9+

- **Layer-D DHT publish/fetch (BEP-44 mutable keyword index)** — Slice 8 is the record substrate + sync ONLY.
- Autonomous per-peer reconciliation **scheduling cadence** (Slice 8 makes `StartSync` reachable + a manual/daemon trigger; the periodic feeler-driven loop lands with Layer-D).
- **`MinPoWBits > 0`** schema bump (this slice mints with `bits=0`, dual-read migration window).
- `abort_code` numeric vocabulary.
- `RecordCache.PruneOldestTo` proactive drain.
- Web UI (`httpapi/web`) rendering of the new `/aggregate` fields.
- `companion` package record-substrate adoption (aliases `contracts/record.Record` when it lands).