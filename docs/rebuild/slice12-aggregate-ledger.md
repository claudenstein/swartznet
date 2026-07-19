# Slice 12 — Aggregate/PPMI/SNAGG B-tree — extraction ledger

_Byte-exact extraction from legacy-snapshot. Slice 12 = the Aggregate backend behind the RecordBackend seam, opt-in (ship default stays LayerDMode=legacy). The frozen contract is contracts/snagg + contracts/dhtschema.PPMIValue._

I have everything I need, including confirmation of the two cross-file constants (`--pow-bits > 40` refused; `os.WriteFile(..., 0644)`) and the anacrolix bencode struct-field sort behavior that fixes the canonical dict key order. Here is the section.

## snagg-format

Byte-exact reverse-engineering of the legacy "Aggregate" signed B-tree companion index, from `internal/companion/btree.go` (page/record/trailer codec), `internal/companion/build_btree.go` (deterministic builder), and `internal/companion/read_btree.go` (walker/verifier), on branch `legacy-snapshot`. Everything below is what `contracts/snagg` must reproduce byte-for-byte.

Provenance note: in the legacy tree this lives in package `companion` and every error string is prefixed `companion:`. The normative comments cite `docs/research/SPEC.md §1`. Section references below (`§1.2`, `§1.6`, `§1.7`) are those SPEC anchors as quoted in-code.

---

### 1. Frozen constants (verify + freeze exactly)

| Name | Value | Where | Notes |
|---|---|---|---|
| `BTreeMagic` | `{'S','N','A','G','G',0x00}` = `53 4E 41 47 47 00` | btree.go | **6-byte magic**, leading bytes of *every* page (leaf, interior, root, trailer). Cross-impl wire contract. |
| `BTreeVersion` | `0x01` (`uint8`) | btree.go | Page-header version byte. Reader **refuses** any other value. |
| `PageKindRoot` | `0x00` | btree.go | Page kind discriminator (header byte 7). |
| `PageKindInterior` | `0x01` | btree.go | |
| `PageKindLeaf` | `0x02` | btree.go | |
| `PageKindTrailer` | `0xFF` | btree.go | |
| `PageHeaderSize` | `16` | btree.go | Fixed header length, every page. |
| `MinPoWBitsDefault` | `20` (`uint8`) | btree.go | Hashcash difficulty the writer emits / reader enforces by default. **Not** the 40 cap. |
| `MaxKeywordBytes` | `64` | btree.go | Cap on `kw` byte length. |
| `MaxRecordBytes` | `256` | btree.go | Hard ceiling on one bencoded `Record`. |
| `TrailerPayloadSize` | `1+32+8+8+4+4+8+1+32+64` = **`162`** | btree.go | The "162-byte trailer" (payload only; page also carries the 16-byte header → 178 occupied bytes). |
| `MinPieceSize` | `16384` | build_btree.go | Smallest accepted piece/page. **Default `--piece-size`.** |
| `MaxPieceSize` | `4*1024*1024` = `4194304` | build_btree.go | Largest accepted piece/page. |
| Trailer version literal | `0x01` | btree.go / build_btree.go | Separate from `BTreeVersion`; both happen to be `0x01`. Encoder rejects any other. |
| `--pow-bits` cap | refused if `> 40` | `cmd/swartznet/cmd_aggregate_build.go:68` | error `"aggregate build: --pow-bits above 40 refused (cost prohibitive)"`. The cap is enforced **only in the CLI**, not in `BuildBTree`. |
| output file mode | `0644` | `cmd_aggregate_build.go:97` | `os.WriteFile(outPath, built.Bytes, 0644)`. |
| CLI `--piece-size` default | `companion.MinPieceSize` (16384) | `cmd_aggregate_build.go:57` | help: `"piece size in bytes; MUST match the .torrent's metainfo when wrapped"`. |
| CLI `--pow-bits` default | `0` | `cmd_aggregate_build.go:59` | 0 = do not mine (sign only). |

Endianness: **all multi-byte integer fields are little-endian** (`binary.LittleEndian`), *except* varint length prefixes which are `binary.PutUvarint`/`Uvarint` (LEB128 unsigned). This includes `payload_length` (u16 LE), interior `num_children` (u16 LE), leaf `num_records` (u16 LE), child index (u32 LE), and every trailer integer.

---

### 2. Page header — 16 bytes, every page

`encodeHeader` / `decodeHeader` (btree.go). Page-absolute offsets:

| Offset | Width | Field | Encoding | Value / rule |
|---|---|---|---|---|
| 0 | 6 | magic | raw | `53 4E 41 47 47 00` |
| 6 | 1 | version | u8 | `0x01`; decode rejects mismatch |
| 7 | 1 | kind | u8 | `PageKind` (0x00/0x01/0x02/0xFF) |
| 8 | 1 | level | u8 | 0 = leaf; increases toward root. **Informational** — the reader navigates by `kind`, never `level`. |
| 9 | 1 | flags | u8 | always 0 written; not interpreted |
| 10 | 2 | payload_length | u16 LE | bytes of payload following the header |
| 12 | 4 | reserved | raw | zero-filled by `make()`; **decode does NOT enforce zero** (intentional forward-compat slack) |

The whole page buffer is `make([]byte, pageSize)` and the header+payload are `copy`-ed in; **all remaining bytes to `pageSize` stay zero** (zero-padding is part of the on-disk contract — pages are exactly one piece wide). `decodeHeader` errors: `"companion: page %d bytes, need %d for header"` (short buffer), `"companion: bad page magic %q, want %q"`, `"companion: page version %d unsupported (this build reads %d)"`.

---

### 3. Record wire format (bencode) — the leaf payload unit

`Record` struct in-memory: `Pk [32]byte`, `Kw string` (≤64 lowercased UTF-8), `Ih [20]byte` (BEP-3 SHA-1 infohash), `T int64`, `Pow uint64`, `Sig [64]byte`. On the wire it is a **bencoded dict** (`recordWire`), keys: `pk`([]byte), `kw`(string), `ih`([]byte), `t`(int64), `pow`(uint64), `sig`([]byte).

**Canonical key ordering is a cross-implementation contract.** `bencode.Marshal` is `anacrolix/torrent@v1.61.0/bencode`, whose `makeEncodeFields` sorts struct fields by tag (`encodeFieldsSortType.Less: ef[i].tag < ef[j].tag`, `sort.Sort` at encode.go:291). So dict keys are emitted in **lexicographic tag order**:

```
ih  <  kw  <  pk  <  pow  <  sig  <  t
```

Golden example — record with `kw="ubuntu"`, `ih=00 01 02 … 13` (20 bytes), `pk=` 32 bytes, `pow=` some N, `t=` some T, `sig=` 64 bytes, bencodes to (no whitespace; `‹…›` = raw bytes):

```
d 2:ih 20:‹20 ihbytes› 2:kw 6:ubuntu 2:pk 32:‹32 pkbytes› 3:pow iNe 3:sig 64:‹64 sigbytes› 1:t iTe e
```

`EncodeRecord` errors: `"companion: record keyword is empty"`, `"companion: record keyword %d bytes exceeds cap %d"`, `"companion: marshal record: %w"`, `"companion: encoded record %d bytes exceeds cap %d"` (`> MaxRecordBytes`=256). `DecodeRecord` validates lengths and errors: `"companion: unmarshal record: %w"`, `"companion: record pk %d bytes, want 32"`, `"companion: record ih %d bytes, want 20"`, `"companion: record sig %d bytes, want 64"`, `"companion: record keyword %d bytes exceeds cap %d"`.

`DecodeRecord` does **not** verify signature or PoW — transport form only.

---

### 4. Sort key, signature preimage, and PoW preimage (three distinct byte strings — do not conflate)

**`RecordKey(r)` — leaf sort order & separator source:**
```
kw_bytes || 0x00 || ih[20]
```
The `0x00` separator makes lex order group all records for a keyword contiguously, tie-broken by infohash. Length = `len(kw) + 1 + 20`. Records in a leaf are sorted by this key; interior separators are drawn from it (§6).

`compareRecords(a,b)` = byte-compare of `RecordKey(a)` vs `RecordKey(b)`; on a shared prefix, the shorter key sorts first (`-1/0/+1`).

**`RecordSigMessage(r)` — the ed25519 sign preimage (NOT the bencode form):**
```
Pk[32] || kw_bytes || Ih[20] || LE64(uint64(T)) || uvarint(Pow)
```
Note: `T` is written as an 8-byte **little-endian** unsigned (`binary.LittleEndian.PutUint64(ts, uint64(r.T))`); `Pow` as an LEB128 uvarint. `Sig` is signed *over this*, so the signature commits to the PoW nonce but not to itself. `VerifyRecordSig` → `ed25519.Verify(Pk, RecordSigMessage(r), Sig)`; failure = `"companion: record signature failed to verify"`.

**PoW preimage = the same `RecordSigMessage(r)` bytes:** `VerifyRecordPoW` computes `SHA256(RecordSigMessage(rec))` and requires ≥ `minBits` leading zero bits (`leadingZeroBits`, MSB-first). Because the preimage contains `Pow`, mining perturbs the hash; the record is signed **after** a satisfying nonce is found. `minBits==0` → check disabled (returns nil). Error: `"companion: record PoW %d bits < required %d"`.

---

### 5. Leaf page payload

`EncodeLeaf(level, records, pageSize)` (btree.go). Payload (page-absolute offset 16):

| Payload offset | Width | Field |
|---|---|---|
| 0 | 2 | num_records, u16 LE (`len(records)`, ≤ 65535) |
| 2 | var | repeated: `uvarint(len(enc))` then `enc` = `EncodeRecord(r)` bytes |

`payload_length` in the header = total payload byte count. Overflow (`PageHeaderSize + payload > pageSize`) returns sentinel `ErrPageOverflow` (`"companion: page overflow"`) — the builder's split signal. Other errors: `"companion: leaf page needs ≥1 record"`, `"companion: too many records for one leaf page"` (>65535), `"companion: leaf payload exceeds uint16"`.

`DecodeLeaf`: rejects wrong kind (`"companion: expected leaf, got kind 0x%02x"`), `payload_length` past page (`"companion: payload length exceeds page"`), short payload (`"companion: leaf payload too short"`), bad varint (`"companion: bad record length varint"`), truncated record (`"companion: short record bytes"`). **Decoded record byte-slices alias the input page buffer** — callers persisting them past the next `Piece()` call must copy (the walker does this for separators; see §8).

---

### 6. Interior / root page payload + MIN-KEY separator scheme

`EncodeInterior(kind, level, children, pageSize)` — `kind` must be `PageKindInterior` (0x01) or `PageKindRoot` (0x00). `InteriorChild{Separator []byte, ChildIndex uint32}`. Payload (offset 16):

| Payload offset | Width | Field |
|---|---|---|
| 0 | 2 | num_children, u16 LE (≥1, ≤65535) |
| 2 | var | repeated: `uvarint(len(sep))` then `sep` bytes then `LE32(ChildIndex)` |

**Separator scheme (load-bearing, unusual).** The stored `Separator` is **the min key of that child's own subtree** (`RecordKey` of the subtree's smallest record) — *not* a between-siblings separator. `EncodeInterior` **forces the first child's separator empty** (`if i==0 { sep = nil }`) regardless of what the caller passed, so child[0]'s lower bound is always −∞. Consequently the reader derives ranges as: child *i*'s lower bound = `child[i].Separator` (nil/empty ⇒ −∞ for the first), child *i*'s upper bound = `child[i+1].Separator` (or +∞ for the last). For an honest left-to-right packed tree, child *i* covers `[minKey_i, minKey_{i+1})`.

Errors: `"companion: EncodeInterior wrong kind %d"`, `"companion: interior page needs ≥1 child"`, `"companion: too many children for one page"` (>65535), `"companion: interior payload exceeds uint16"`, `ErrPageOverflow`. `DecodeInterior`: `"companion: expected interior/root, got kind 0x%02x"`, `"companion: payload length exceeds page"`, `"companion: interior payload too short"`, `"companion: bad separator varint"`, `"companion: short separator or child index"`. Like leaves, returned separator slices alias the page buffer.

---

### 7. Trailer page — the 162-byte signed footer (last piece)

`Trailer` struct + `encodeTrailerFields` + `EncodeTrailer` (btree.go). The trailer is page kind `0xFF`, header `level`=0, `payload_length`=162. Payload layout, given both payload-relative and page-absolute offsets (page = header@0 + payload@16):

| Payload off | Page off | Width | Field | Encoding |
|---|---|---|---|---|
| 0 | 16 | 1 | trailer_version | u8 = `0x01` (encoder & decoder reject other) |
| 1 | 17 | 32 | pubkey | raw ed25519 public key |
| 33 | 49 | 8 | seq | u64 LE (matches PPMI seq at publish) |
| 41 | 57 | 8 | created_ts | u64 LE (unix seconds) |
| 49 | 65 | 4 | root_piece_index | u32 LE — **invariant = 0** (§1.2) |
| 53 | 69 | 4 | num_pages | u32 LE (includes the trailer page) |
| 57 | 73 | 8 | num_records | u64 LE |
| 65 | 81 | 1 | min_pow_bits | u8 |
| 66 | 82 | 32 | tree_fingerprint | raw SHA-256 (§8) |
| 98 | 114 | 64 | publisher_sig | raw ed25519 signature (§9) |
| — | 178 | — | (zero pad to pieceSize) | |

`EncodeTrailer` requires `pageSize ≥ PageHeaderSize + TrailerPayloadSize` (16+162=178) else `"companion: page %d bytes too small for trailer (needs %d)"`; rejects `trailer_version != 0x01` (`"companion: unsupported trailer version %d"`); self-checks payload==162 (`"companion: trailer payload %d bytes, expected %d"`). `DecodeTrailer`: `"companion: expected trailer, got kind 0x%02x"`, `"companion: trailer payload length %d, expected %d"`, `"companion: unsupported trailer version %d"`. `DecodeTrailer` does **not** verify the signature — the caller must call `VerifyTrailerSig` (`OpenBTree` does).

---

### 8. Fingerprint — the PPMI commit value

Computed in `BuildBTree` step 5 and re-derived in `VerifyFingerprint`:

```
tree_fingerprint = SHA256( EncodeRecord(r_0) || EncodeRecord(r_1) || … )   for r in RecordKey-sorted order
```

Critical properties:
- The hash is over **the full canonical bencoded record** — `EncodeRecord`, which **includes `pow` and `sig`**. It is *not* over a pow/sig-excluding record identity.
- Independent of `pieceSize`, page layout, `seq`, and `created_ts`. Two publishers with an identical record set (identical bytes, including identical signatures & nonces) produce an identical fingerprint → this is what lets independent publishers agree on the PPMI commit.
- Build hashes globally-sorted order. `VerifyFingerprint` iterates pieces `0 .. NumPieces-2`, skips non-leaf kinds, and hashes leaf records in stored order; because the builder lays leaves out contiguously at the highest data-piece indices in sorted order (BFS top-down, leaves last — §9), the reconstructed stream equals the sorted stream. `VerifyFingerprint` also enforces `num_records`: bails early with `"companion: more than %d records, trailer claim exceeded"` and finally `"companion: read %d records, trailer claims %d"` / `"companion: reconstructed fingerprint mismatches trailer"`.

---

### 9. Trailer signing (ed25519) + deterministic build control flow

**Sign preimage:** `TrailerSigMessage(t) = encodeTrailerFields(t)` = the trailer payload **minus** the 64-byte `publisher_sig` = the first **98 bytes** (162−64) in the exact field order of §7. `publisher_sig = ed25519.Sign(privKey, TrailerSigMessage)`; verified by `VerifyTrailerSig` → `ed25519.Verify(PubKey, TrailerSigMessage(t), PublisherSig)`; failure = `"companion: trailer signature failed to verify"`. Record identity for signing thus **excludes pow+sig** at the *trailer* level (sig is not self-referential) — consistent with the Slice-8 `contracts/record` rule — but the *fingerprint* inside that signed region is over full records (§8). The rebuild must keep both facts simultaneously.

**`BuildBTree` order** (build_btree.go), matching the required `pack_leaves → interior levels → trailer`:

1. Reject `PieceSize < 16384 || > 4194304` → `"companion: PieceSize %d outside [%d, %d]"`; reject empty records → `"companion: BuildBTree needs ≥1 record"`.
2. Defensive-copy records, sort by `compareRecords` (RecordKey order). Caller slice order preserved.
3. `packLeaves`: greedy — append records to the current leaf until `EncodeLeaf` returns `ErrPageOverflow`, then start a new leaf. A single record that overflows an empty leaf → `"companion: record of %d bytes too large for page %d"`. Per-record validation: `"companion: empty keyword in records"`, `"companion: keyword %q exceeds cap %d"`.
4. Build interior levels bottom-up via `packInteriorLevel` (greedy-pack `{separator=childMinKey, childIndex=0}` entries; first child of each page forced empty separator). Repeat until a level has one page. **Single-leaf special case:** a one-leaf tree still gets a synthetic one-child root at `level 1` (root kind 0x00 is distinct from leaf kind 0x02), so *every* tree has shape root → … → leaves → trailer and `NumPieces ≥ 3`. Error: `"companion: packInteriorLevel empty children"`, `"companion: child separator too large for interior page %d"`.
5. BFS **top-down** piece assignment: reverse levels so root is emitted first (piece 0), then each lower level as a contiguous slab, leaves last (highest data-piece indices), trailer final. `pieceIndex` assigned in that top-down sweep.
6. Rewrite each interior/root page's `ChildIndex` fields to the now-known piece indices of its consecutive children (running cursor). Sanity: `"companion: layout mismatch at level %d: consumed %d children, have %d"`.
7. Fingerprint over sorted records (§8).
8. Emit each data page zero-padded to `pieceSize` into `fileBytes` (`numPages = totalDataPages + 1`); encode error wrapped `"companion: encode page piece=%d: %w"`.
9. Trailer: `TrailerVersion=0x01`, `RootPieceIndex=0`, `NumPages`, `NumRecords`, `MinPoWBits=in.MinPoWBits` (**passed through verbatim; 0 stays 0 — no auto-default in `BuildBTree`**), `CreatedTs = in.CreatedTs` or `time.Now().Unix()` if zero, fingerprint; sign iff `PrivKey != nil` (nil → all-zero sig, fails verify — test/diagnostic only); `EncodeTrailer` into the last piece.

Output `Bytes` length = `numPages * pieceSize`.

**Determinism scope:** `crypto/ed25519.Sign` is deterministic (RFC 8032), so given a fixed `(sorted records, pubkey, privkey, seq, createdTs, minPoWBits, pieceSize)` the whole file is byte-reproducible. The **only** nondeterminism is `CreatedTs` defaulting to `time.Now()` — the in-code claim "identical input Records + identical identity produce identical bytes" holds for the *full file* **only if `CreatedTs` (and `Seq`, `PieceSize`) are pinned**. The `TreeFingerprint`/PPMI-commit is reproducible regardless. The rebuild's `aggregate build` must require an explicit `created_ts` (or freeze it) to get byte-stable golden vectors.

---

### 10. Decode / validation path (subscriber)

`OpenBTree(src PageSource)` (read_btree.go). `PageSource` = `Piece(index) ([]byte, error)` + `NumPieces() int`; `BytesPageSource{Data, PieceSize}` is the in-memory impl (`NumPieces = len(Data)/PieceSize`; `Piece` bounds-checks with `"companion: piece %d out of range [0, %d)"`).

`OpenBTree` steps: require `NumPieces ≥ 3` (`"companion: tree has %d pages, need ≥3 (root+leaf+trailer)"`); fetch last piece → `DecodeTrailer` → `VerifyTrailerSig` (`"companion: trailer signature invalid: %w"`); check `trailer.NumPages == NumPieces` (`"companion: trailer claims %d pages, source has %d"`); check `trailer.RootPieceIndex == 0` (`"companion: trailer root piece = %d, want 0"`). Only after this returns cleanly may the tree be trusted.

`Find(prefix)`:
- `pLo = []byte(prefix)`, `pHi = nextPrefix(pLo)`. `nextPrefix` = smallest slice strictly greater than all slices starting with `p`: increments the last byte `< 0xFF`, truncating trailing `0xFF`s ("ubu"→"ubv", "ub\xFF"→"uc", all-`0xFF`→`nil` meaning +∞).
- Fetch piece 0, require `kind == root` (`"companion: piece 0 kind = 0x%02x, want root"`).
- `walkToLeaves(0, pLo, pHi, visited)` — DFS collecting leaf piece indices whose `[lower,upper)` overlaps `[pLo,pHi)` via `rangeOverlapsPrefix` (`false` if `upper ≤ pLo` or `lower ≥ pHi`; nil = ±∞). Separators are deep-copied out of the aliased page buffer before recursion.
- Post-walk `checkLeafIndices`: reject `len(leaves) ≥ NumPieces` (`"companion: walk returned %d leaves for a %d-piece tree"`) or a duplicate (`"companion: leaf piece %d returned twice by walk"`).
- For each leaf: `DecodeLeaf`, then per record: keep only if `HasPrefix(rec.Kw, prefix)`, `VerifyRecordSig` passes, and (if `trailer.MinPoWBits > 0`) `VerifyRecordPoW` passes. **Records failing sig/PoW are silently dropped**, not fatal.

**Hostile-tree guards** (interior structure is *not* covered by any signature — only the leaf-record stream is, via the fingerprint, and even that only if `VerifyFingerprint` is run): (1) a **shared `visited` set** across the whole walk fails closed the moment any piece is reached twice (`"companion: piece %d reached twice (cycle or fan-in in interior pages)"`) — this bounds the walk to `NumPieces` fetches and defeats Fibonacci-style DAG fan-in that per-page checks miss; (2) each child index must be strictly `> pieceIdx`, strictly increasing within the page, and `< NumPieces-1` (below the trailer) else `"companion: piece %d child %d index %d out of range (must be in (%d, %d))"`.

`VerifyFingerprint` is the optional full-integrity pass (§8) a cautious subscriber runs after downloading the whole file; everyday prefix queries skip it and rely on per-record signatures.

---

### 11. Complete error-string catalog (freeze verbatim if the rebuild keeps parity)

All prefixed `companion:`. Codec: `record keyword is empty` · `record keyword %d bytes exceeds cap %d` · `marshal record: %w` · `encoded record %d bytes exceeds cap %d` · `unmarshal record: %w` · `record pk %d bytes, want 32` · `record ih %d bytes, want 20` · `record sig %d bytes, want 64` · `record signature failed to verify` · `page %d bytes, need %d for header` · `bad page magic %q, want %q` · `page version %d unsupported (this build reads %d)` · `EncodeInterior wrong kind %d` · `interior page needs ≥1 child` · `too many children for one page` · `interior payload exceeds uint16` · `expected interior/root, got kind 0x%02x` · `payload length exceeds page` · `interior payload too short` · `bad separator varint` · `short separator or child index` · `leaf page needs ≥1 record` · `too many records for one leaf page` · `leaf payload exceeds uint16` · `expected leaf, got kind 0x%02x` · `leaf payload too short` · `bad record length varint` · `short record bytes` · `page %d bytes too small for trailer (needs %d)` · `unsupported trailer version %d` · `trailer payload %d bytes, expected %d` · `expected trailer, got kind 0x%02x` · `trailer payload length %d, expected %d` · `trailer signature failed to verify` · `page overflow` (sentinel `ErrPageOverflow`). Builder: `PieceSize %d outside [%d, %d]` · `BuildBTree needs ≥1 record` · `empty keyword in records` · `keyword %q exceeds cap %d` · `record of %d bytes too large for page %d` · `child separator too large for interior page %d` · `packInteriorLevel empty children` · `layout mismatch at level %d: consumed %d children, have %d` · `encode page piece=%d: %w`. Reader: `piece %d out of range [0, %d)` · `tree has %d pages, need ≥3 (root+leaf+trailer)` · `fetch trailer: %w` · `decode trailer: %w` · `trailer signature invalid: %w` · `trailer claims %d pages, source has %d` · `trailer root piece = %d, want 0` · `fetch root: %w` · `root header: %w` · `piece 0 kind = 0x%02x, want root` · `walk returned %d leaves for a %d-piece tree` · `leaf piece %d returned twice by walk` · `piece %d reached twice (cycle or fan-in in interior pages)` · `fetch piece %d: %w` · `piece %d header: %w` · `piece %d unexpected kind 0x%02x` · `piece %d child %d index %d out of range (must be in (%d, %d))` · `fetch leaf %d: %w` · `decode leaf %d: %w` · `more than %d records, trailer claim exceeded` · `read %d records, trailer claims %d` · `reconstructed fingerprint mismatches trailer` · `record PoW %d bits < required %d`.

---

### 12. Invariants the rebuild MUST preserve

1. Piece 0 is the root (kind `0x00`); the last piece is the trailer (kind `0xFF`); `RootPieceIndex == 0`; `NumPages == NumPieces` (includes trailer); every tree has ≥3 pieces.
2. Every page is exactly `pieceSize` bytes, zero-padded after `header+payload`, and `pieceSize` MUST equal the wrapping torrent's metainfo piece length (default 16384; range [16384, 4194304]).
3. Leaves are contiguous, in RecordKey-sorted order, at the highest data-piece indices; interior separator = child subtree min key; first child of every interior/root page has an empty (−∞) separator.
4. `tree_fingerprint = SHA256(concat EncodeRecord over sorted records)` — full bencoded record, pow+sig included; dict keys in `ih<kw<pk<pow<sig<t` order. This is the PPMI commit and the cross-impl anchor.
5. Trailer signature = ed25519 over the first 98 trailer-payload bytes (all fields except `publisher_sig`).
6. Record sign/PoW preimage = `Pk||kw||Ih||LE64(T)||uvarint(Pow)` (NOT the bencode form). PoW = leading-zero bits of `SHA256(preimage)`.
7. Reader trusts nothing until `VerifyTrailerSig` passes; drops (never trusts) records failing per-record sig; the visited-set + strictly-increasing-downward child guards bound any hostile walk.

---

### 13. Ambiguities and §6 defects the rebuild must NOT reproduce

- **Permissive PoW admission (primary §6 defect).** `MinPoWBits` is publisher-chosen and lives in the trailer; a malicious publisher simply sets it to `0`, and both `BuildBTree` (no auto-default; passes 0 through) and `Find` (skips `VerifyRecordPoW` when `trailer.MinPoWBits == 0`) then admit *every* record with zero hashcash cost. The signed trailer authenticates the value but does not constrain it. The rebuild's admission policy should enforce a **subscriber-side floor** (e.g. reject/penalize trees advertising `MinPoWBits` below a configured minimum) rather than trusting the publisher's self-declared difficulty. (The CLI's `--pow-bits > 40` cap only bounds the *writer's* upper cost; it does nothing for the zero-floor problem.)
- **Interior structure is unauthenticated → prefix queries are not complete.** Only leaf records (via signatures) and the leaf-record stream (via the fingerprint, and only when `VerifyFingerprint` runs) are authenticated. Interior separators and child pointers are attacker-mutable; a hostile tree can steer a prefix query *away* from legitimate leaves so a query silently misses records. `Find` guarantees *authenticity* of what it returns, never *completeness*. The rebuild should document this explicitly and consider signing a structural digest, or make callers run the full-file fingerprint check when completeness matters.
- **`created_ts` default = `time.Now()` breaks byte-determinism.** The "deterministic build" claim only holds for the fingerprint unless `CreatedTs`/`Seq`/`PieceSize` are pinned. The rebuild's `aggregate build` should require an explicit `created_ts` (fail closed on 0) so golden vectors are byte-stable.
- **Reserved header bytes 12–15 not enforced zero on decode.** Two byte-different pages can decode identically, so a naive "canonical bytes" equality check over decoded-then-reencoded pages is safe but a raw-byte fingerprint of *pages* (as opposed to records) would not be canonical. The rebuild must fingerprint records, not pages (as legacy does) — or normalize reserved bytes.
- **`nil PrivKey` produces an all-zero-signature tree** that silently fails `VerifyTrailerSig`. Harmless (readers reject it) but the rebuild should make unsigned builds an explicit, named mode rather than a `nil`-pointer side effect.
- **Record-identity mismatch to watch at integration.** Slice-8 `contracts/record` defines identity **excluding** pow+sig; the SNAGG fingerprint hashes the **full** record **including** pow+sig. These are deliberately different preimages. The rebuild must NOT "unify" them — computing the SNAGG fingerprint over the pow/sig-excluding identity would break both cross-impl fingerprint agreement and the ≥12-month legacy-read back-compat (`VerifyFingerprint` on legacy trees would fail).
- **Naming.** Legacy symbols/errors say `companion:`/`BTree`; the frozen contract is `contracts/snagg`. Keep the on-wire magic `SNAGG\x00` and all byte layouts identical; only Go identifiers/prefixes may change. If the rebuild keeps error-string parity for tooling, freeze the strings in §11 verbatim.

**Out of scope for these three reader files (documented elsewhere, flagged for the Slice-12 wiring):** the BEP-44 PPMI pointer value/schema and the `SHA256("snet.index")` double-hash target salt live in `contracts/dhtschema`/`dhtindex`, not here; the `--pow-bits ≤ 40` cap and `0644` write mode live in `cmd/swartznet/cmd_aggregate_build.go` (verified: lines 68, 97). `BuildBTree` returns raw `Bytes`; wrapping those bytes into a `.torrent` (piece length MUST equal `PieceSize`) is the caller's job.

---

I now have everything needed. Returning the exhaustive byte-exact section.

## ppmi-pow

Scope: the **PPMI** (Publisher-Pointer Mutable Item) BEP-44 primitive and the **hashcash PoW** on Aggregate records. Legacy sources read from `legacy-snapshot`: `internal/dhtindex/ppmi.go`, `internal/dhtindex/ppmi_dht.go`, `internal/companion/pow.go`. Cross-checked against `internal/companion/btree.go` (Record + `RecordSigMessage`), `internal/dhtindex/dht.go` (`nextSeq`/`checkPutStats`), `internal/dhtindex/schema.go` (`MaxValueBytes`), `cmd/swartznet/cmd_aggregate_build.go` (CLI cap + 0644 write), and the already-rebuilt Slice-8 `contracts/record/record.go` + Slice-9 `contracts/dhtschema/dhtschema.go` in the working tree.

Note up front: **PoW lives on the B-tree leaf `Record`, NOT on the PPMI item.** The PPMI carries no hashcash; its anti-spam is the BEP-44 signature + monotonic seq + per-IP DHT rate limits. These two subsystems only meet at the pointer→tree→record verification chain (`PPMIValue.Commit == Trailer.TreeFingerprint`, then per-record sig+PoW). Slice 12 must keep them as two independent contracts.

---

### 1. `PPMISalt` — the DHT salt (CROSS-IMPL WIRE CONTRACT, double-hash) — VERIFIED

```go
const PPMISaltSeed = "snet.index"                 // exact ASCII seed, 10 bytes, no NUL, no newline
var PPMISalt = func() []byte {                     // 32 bytes, computed once at package init
    sum := sha256.Sum256([]byte(PPMISaltSeed))
    return sum[:]
}()
```

VERIFIED: the salt is **`SHA256("snet.index")`** — the plaintext string is NOT used as the salt; its SHA-256 digest is. This is what the source calls the "double-hash": (a) SHA-256 hashes the seed string into the 32-byte salt, then (b) the DHT target hashes again with SHA-1 (see §2). Every SwartzNet publisher uses this *same* salt, so a passive DHT observer cannot fingerprint SwartzNet publishers by salt — discrimination is at the pubkey, not the salt.

**Golden salt bytes** (freeze these in `contracts/dhtschema`):
```
PPMISalt (hex) = 89fe1aae185c70dbf9d4ccaa44f9996a3d0d00e55eb2821ad9b143401fbc965d
             = {0x89,0xfe,0x1a,0xae,0x18,0x5c,0x70,0xdb, 0xf9,0xd4,0xcc,0xaa,0x44,0xf9,0x99,0x6a,
                0x3d,0x0d,0x00,0xe5,0x5e,0xb2,0x82,0x1a, 0xd9,0xb1,0x43,0x40,0x1f,0xbc,0x96,0x5d}
```
(Reproduced independently: `printf 'snet.index' | sha256sum` → `89fe1aae…965d`.)

Ambiguity to resolve in the freeze: the seed is `"snet.index"` — the docstring alludes to "SPEC §1 of track A/C". The seed string, not the digest, is the human-auditable constant; keep `PPMISaltSeed` as a named constant in the rebuild so auditors reproduce the derivation without hexdumping binaries.

### 2. DHT target = SHA1(pubkey || PPMISalt) (CROSS-IMPL WIRE CONTRACT)

Production uses `bep44.MakeMutableTarget(pubkey, PPMISalt)`; the memory mock computes it inline. Both are byte-identical. From `anacrolix/dht/v2@v2.23.0/bep44/target.go`:

```go
type Target = [sha1.Size]byte              // [20]byte
func MakeMutableTarget(pubKey [32]byte, salt []byte) Target {
    return sha1.Sum(append(pubKey[:], salt...))   // SHA1( 32-byte pubkey || 32-byte salt )
}
```
Memory mock (`ppmi_dht.go`): `target := sha1.Sum(append(m.pub[:], PPMISalt...))`.

So: **`target = SHA1( publisher_ed25519_pubkey[32] ‖ SHA256("snet.index")[32] )`**, a 20-byte value. The preimage is exactly 64 bytes. This is the frozen addressing rule for Slice 12.

### 3. `PPMIValue` struct + bencode tags + widths (CROSS-IMPL WIRE CONTRACT)

```go
type PPMIValue struct {
    IH     []byte `bencode:"ih"`                 // REQUIRED, exactly 20 bytes (BEP-3 infohash of companion torrent)
    Commit []byte `bencode:"commit,omitempty"`   // 0 or 32 bytes == Trailer.TreeFingerprint (SHA256 of canonical leaf stream)
    Topics []byte `bencode:"topics,omitempty"`   // 0 or 32 bytes; optional cuckoo-filter digest of covered prefixes
    Ts     int64  `bencode:"ts"`                 // REQUIRED, unix seconds; encoder stamps time.Now().Unix() if zero
    NextPk []byte `bencode:"next_pk,omitempty"`  // 0 or 32 bytes; reserved for key rotation, empty in v1
}
```

Field-width invariants (enforced on BOTH encode and decode):
- `ih`: **must be exactly 20**. (0 is rejected — unlike the optional fields, IH has no zero-length exemption.)
- `commit`: `len == 0 || len == 32`.
- `topics`: `len == 0 || len == 32`.
- `next_pk`: `len == 0 || len == 32`.
- `ts`: `int64`; zero is auto-filled at encode.

**Bencode ordering is alphabetical by key** (anacrolix marshaller sorts struct keys — VERIFIED by round-trip): `commit`, `ih`, `next_pk`, `topics`, `ts`. `omitempty` fields vanish when zero-length, so a minimal item is just `ih` + `ts`.

**Golden minimal vector** (all-zero 20-byte IH, `Ts=1700000000`, all optionals empty) — freeze as `contracts/dhtschema.PPMIValue` fixture:
```
string : d2:ih20:\x00×20 2:tsi1700000000ee
hex    : 64323a696832303a0000000000000000000000000000000000000000323a747369313730303030303030306565
length : 45 bytes
```
With a 32-byte all-`0xAB` `commit` added: `d6:commit32:\xAB×32 2:ih20:\x00×20 2:tsi1700000000ee`, length 88 bytes. Real items are ~100 bytes.

### 4. Size cap — `MaxPPMIValueBytes = MaxValueBytes = 1000`

```go
const MaxPPMIValueBytes = MaxValueBytes   // ppmi.go
const MaxValueBytes = 1000                // schema.go (BEP-44 mutable `v` cap; == dhtschema.MaxValueBytes, Slice 9)
```
The 1000-byte BEP-44 cap is shared with the legacy keyword item. PPMI values are far under it; the ceiling is defence-in-depth. The comparison is **inclusive** — exactly 1000 bytes is accepted; `len(out) > 1000` is rejected (matching the Slice-9 `dhtschema` semantics).

### 5. `EncodePPMI` control flow + exact error strings

```
1. len(IH)     != 20               → "dhtindex: PPMI ih %d bytes, want 20"
2. len(Commit) ∉ {0,32}            → "dhtindex: PPMI commit %d bytes, want 0 or 32"
3. len(Topics) ∉ {0,32}            → "dhtindex: PPMI topics %d bytes, want 0 or 32"
4. len(NextPk) ∉ {0,32}            → "dhtindex: PPMI next_pk %d bytes, want 0 or 32"
5. if Ts == 0 → Ts = time.Now().Unix()     // mutates a local copy (v is by value)
6. out, err := bencode.Marshal(v)  → on err: "dhtindex: encode PPMI: %w"
7. len(out) > MaxPPMIValueBytes    → "dhtindex: encoded PPMI %d bytes exceeds BEP-44 cap %d"
8. return out, nil
```
Non-determinism note: step 5 stamps wall-clock `Ts`, so `EncodePPMI` output is **not** reproducible across calls when `Ts==0`. For golden vectors the fixture must pin `Ts` to a fixed value (the rebuild should let callers pass an explicit clock/timestamp, as `dhtschema.EncodeValue` does).

### 6. `DecodePPMI` control flow + exact error strings

```
1. len(payload) == 0               → "dhtindex: empty PPMI value"
2. len(payload) > MaxPPMIValueBytes → "dhtindex: PPMI value %d bytes exceeds BEP-44 cap %d"   // BEFORE unmarshal
3. bencode.Unmarshal(payload,&v)   → on err: "dhtindex: decode PPMI: %w"
4. len(IH)     != 20               → "dhtindex: decoded PPMI ih %d bytes, want 20"
5. len(Commit) ∉ {0,32}            → "dhtindex: decoded PPMI commit %d bytes, want 0 or 32"
6. len(Topics) ∉ {0,32}            → "dhtindex: decoded PPMI topics %d bytes, want 0 or 32"
7. len(NextPk) ∉ {0,32}            → "dhtindex: decoded PPMI next_pk %d bytes, want 0 or 32"
8. return v, nil
```
The over-cap check runs **before** `Unmarshal` (step 2) — a hostile node can return up to the ~64 KiB UDP datagram limit, well past the 1000-byte storage cap, so decode fails closed before driving any allocation. This mirrors the Slice-9 `dhtschema.DecodeValue` reject-before-unmarshal ordering — keep it identical in Slice 12.

`EstimatePPMISize(v)` returns `len(bencode.Marshal(v))`, or `MaxPPMIValueBytes+1 (=1001)` on marshal error (fail-fast helper; does not stamp `Ts`, so it under-counts a zero-`Ts` value versus the live encode — see the Slice-9 `EstimateValueSize` fix that stamps a representative `Ts`; the rebuild should apply the same fix here).

### 7. `PutPPMI` / `GetPPMI` control flow (shares `checkPutStats` + seq)

`AnacrolixPutter{ server *dht.Server; private ed25519.PrivateKey; public [32]byte }`. `PutPPMI`:
```
1. if value.Ts == 0 → value.Ts = time.Now().Unix()   // second Ts stamp (also inside EncodePPMI)
2. encoded, err := EncodePPMI(value)                  // propagates §5 errors
3. bencode.Unmarshal(encoded, &v interface{})         // RE-DECODE to interface{}
      → on err: "dhtindex: re-decode PPMI: %w"
4. target := bep44.MakeMutableTarget(a.public, PPMISalt)
5. seqToPut(seq) closure builds bep44.Put{ V:v, K:&pubArr, Salt:PPMISalt, Seq:nextSeq(seq) }; put.Sign(a.private)
6. stats, err := getput.Put(ctx, target, a.server, PPMISalt, seqToPut)
      → on err: "dhtindex: put PPMI: %w"
7. return checkPutStats(stats, "PPMI put")
```
Key subtlety (matches the `dhtschema` doc note): the value is **re-decoded into `interface{}` and re-marshalled by anacrolix inside `Put.Sign`**, so `EncodePPMI` MUST produce anacrolix-`bencode`-identical bytes or the signature would cover different bytes than were stored. The rebuild's `contracts/dhtschema` must use `anacrolix/torrent/bencode` (not `contracts/bencode`) for the PPMI encode path, exactly as the keyword path already does.

`nextSeq` (`dht.go`): `seq+1`, clamped at `math.MaxInt64`. Anacrolix's `getput.Put` reads the current seq and the closure bumps it by 1 — callers keep **no** seq state. BEP-44 requires strictly increasing seq; the clamp is a defensive no-op guard (unreachable: 2^63 puts).

`checkPutStats` (shared by keyword put, BEP-46 pointer put, and PPMI put — the guard "cannot drift between them"):
```go
func checkPutStats(stats *traversal.Stats, what string) error {
    if stats == nil || stats.NumResponses == 0 {
        return fmt.Errorf("dhtindex: %s reached zero DHT nodes", what)   // PPMI: "dhtindex: PPMI put reached zero DHT nodes"
    }
    return nil
}
```
This is the fail-closed guard: `getput.Put` returns nil error even when the traversal reached zero nodes (cold routing table / partition / all peers rejected), so the item never lands. Without this the caller would record success for an unfetchable item. **This is production-correct deterministic control — preserve it in Slice 12.**

`AnacrolixGetter{ server *dht.Server }`. `GetPPMI(ctx, pubkey [32]byte)`:
```
1. target := bep44.MakeMutableTarget(pubkey, PPMISalt)
2. res, _, err := getput.Get(ctx, target, a.server, nil, PPMISalt)
      → on err: "dhtindex: get PPMI %x: %w"   // %x = 20-byte target hex
3. return DecodePPMI([]byte(res.V))            // BEP-44 sig verification happens inside anacrolix get
```

`MemoryPutterGetter` (tests only; `store` for keyword items + `ppmiStore map[[20]byte]ppmiStoredItem` coexist without collision since salts differ):
- `PutPPMI`: encodes (validation only), then `target := sha1.Sum(append(m.pub[:], PPMISalt...))`, stores `ppmiStoredItem{value, stored: time.Now()}` under lock. Does NOT bump seq or verify sigs.
- `GetPPMI(pubkey)`: `target := sha1.Sum(append(pubkey[:], PPMISalt...))`; miss → `"dhtindex: no PPMI stored for pubkey"`. Returns the stored struct value (not a re-decode).

### 8. Hashcash PoW on records (CROSS-IMPL HASH CONTRACT)

The PoW preimage is **`RecordSigMessage(r)`** (the signed message), NOT `EncodeRecord`. `pow.go`'s unexported `recordPreimage` is byte-identical to `btree.go`'s exported `RecordSigMessage` — both produce:

```
preimage = Pk[32] ‖ Kw(raw UTF-8, ≤64) ‖ Ih[20] ‖ LE64(T) ‖ uvarint(Pow)
```
Concretely (`RecordSigMessage`, `btree.go:173`):
```go
buf := append(buf, r.Pk[:]...)                       // 32 bytes
buf = append(buf, r.Kw...)                           // len(Kw) bytes, no separator
buf = append(buf, r.Ih[:]...)                        // 20 bytes
binary.LittleEndian.PutUint64(ts[:], uint64(r.T))    // 8 bytes, LITTLE-ENDIAN
buf = append(buf, ts[:]...)
n := binary.PutUvarint(nonce[:], r.Pow)              // 1..10 bytes, unsigned LEB128 varint
buf = append(buf, nonce[:n]...)
```
PoW target: `SHA256(preimage)` must have **≥ D leading zero bits**, MSB-first, where `D = Trailer.MinPoWBits`.

`leadingZeroBits` (two byte-identical copies: `pow.go:leadingZeroBitsOfByteSlice` and `read_btree.go:leadingZeroBits` — the source flags the duplication as intentional-for-now; the rebuild should have ONE copy): iterate bytes; each `0x00` adds 8; on the first non-zero byte, scan `mask` from `0x80` down, counting until a set bit. `contracts/record` already collapses this to `math/bits.LeadingZeros8`.

`MineRecordPoW(r, bits uint8, maxIterations uint64) (Record, error)`:
```
if bits == 0            → return r, nil            (no-op)
if bits > 40            → "companion: MineRecordPoW refuses bits=%d (cost prohibitive)"
for iter := 0; maxIterations==0 || iter<maxIterations; iter++:
    r.Pow = iter
    if leadingZeroBits(SHA256(RecordSigMessage(r))) >= bits → return r, nil
return r, ErrPoWExhausted
```
- Nonce search starts at **0**, increments by 1, and `r.Pow` is set to the *iteration counter itself* (so the found nonce is the smallest passing value).
- `maxIterations == 0` means **unbounded** (never returns `ErrPoWExhausted`). Expected cost ≈ 2^bits.
- `var ErrPoWExhausted = errors.New("companion: PoW iteration budget exhausted")` — only returned when caller sets a cap.

`SignAndMineRecord(priv, pub, kw, ih, ts, bits)`:
```
if len(pub) != 32 → "companion: SignAndMineRecord pub %d bytes, want 32"
build r{Pk=pub, Kw=kw, Ih=ih, T=ts}
mined, err := MineRecordPoW(r, bits, 0)    // unbounded; on err: "companion: mine record: %w"
sig := ed25519.Sign(priv, RecordSigMessage(mined)); copy(mined.Sig[:], sig)
```
**Ordering is load-bearing: mine THEN sign.** Signing first would invalidate the signature for every nonce but the signed one (the signature covers `Pow`).

`VerifyRecordPoW(rec, minBits uint8)` (`read_btree.go:389`): `minBits==0` → nil (skip); else `SHA256(RecordSigMessage(rec))` leading-zero-bits `< minBits` → `"companion: record PoW %d bits < required %d"`. On the read path (`BTreeReader.Find`) a record failing sig OR PoW is **silently dropped** (not a whole-query failure) when `Trailer.MinPoWBits > 0`.

### 9. The `maxPoWBits = 40` cap — enforced in THREE places (all consistent)

1. `MineRecordPoW`: `bits > 40` → error `"companion: MineRecordPoW refuses bits=%d (cost prohibitive)"`.
2. CLI `cmd_aggregate_build.go:68`: `if powBits > 40 { … "aggregate build: --pow-bits above 40 refused (cost prohibitive)"; return exitUsage }` (test asserts `--pow-bits 50` → exitUsage). Flag: `fs.UintVar(&powBits, "pow-bits", 0, "hashcash difficulty (0 = no mining, 20 = production default)")`.
3. Slice-8 `contracts/record`: `const maxPoWBits = 40`; `MinePoW` returns `ErrPoWTarget` ("record: PoW target too high") when `bits > maxPoWBits`.

`MinPoWBitsDefault uint8 = 20` (`btree.go:48`) is the production difficulty the writer emits and the reader enforces; the trailer stores the *actual* `MinPoWBits` (offset 65 within the 162-byte trailer payload).

### 10. Record identity EXCLUDES pow+sig — cross-check with `contracts/record` (Slice 8)

Legacy `companion.Record` (`btree.go`):
```go
type Record struct {
    Pk[32]; Kw string; Ih[20]; T int64; Pow uint64; Sig[64]  // Sig is ed25519 over Pk||Kw||Ih||T||Pow
}
```
- **Signature covers `Pow`, not `Sig`** (`RecordSigMessage` ends with `uvarint(Pow)` and does not include `Sig`) — so PoW cannot be stripped/replaced without breaking the sig, but the sig is not self-referential.
- Legacy has **no** explicit content-identity hash. Its ordering/dedupe key is `RecordKey = Kw ‖ 0x00 ‖ Ih` (NUL separator) — this omits `Pk` and `T` and is a *sort/separator* key, not an identity.

Slice-8 `contracts/record` introduces `ElementID` (the RIBLT reconciliation key) as the identity:
```go
idPreimage = Pk[32] ‖ Kw ‖ Ih[20] ‖ LE64(T)                 // NO Pow, NO Sig
ElementID  = SHA256(idPreimage)                              // EXCLUDES Pow AND Sig → two signings of one semantic record dedupe
SigMessage = idPreimage ‖ uvarint(Pow)                       // Pow INSIDE the signature
```
**VERIFIED byte-match:** `contracts/record.SigMessage()` is byte-identical to legacy `RecordSigMessage` (same `Pk‖Kw‖Ih‖LE64(T)‖uvarint(Pow)`, same little-endian T, same uvarint Pow). So the PoW/sig preimage is a preserved cross-implementation contract. The *new* piece is `ElementID` (SHA256 over the preimage minus the Pow varint) — it has no legacy equivalent and must not be conflated with legacy `RecordKey`. Slice 12 records reconcile on `ElementID` (excludes pow+sig); the PPMI's `Commit` still binds to `Trailer.TreeFingerprint` (SHA256 of the canonical *sorted-leaf* stream, a separate hash).

### 11. Invariants to freeze for Slice 12

- Salt: `SHA256("snet.index")` = `89fe1aae…965d` (32 bytes), identical for every publisher.
- Target: `SHA1(pubkey[32] ‖ salt[32])` (20 bytes).
- PPMI dict keys (alphabetical): `commit, ih, next_pk, topics, ts`; `ih` always 20 bytes and mandatory; `ts` always present; `commit/topics/next_pk` are 0-or-32 and `omitempty`.
- PPMI value cap 1000 bytes, inclusive, checked before unmarshal on decode.
- PoW preimage = signed message = `Pk‖Kw‖Ih‖LE64(T)‖uvarint(Pow)`; target = ≥D leading zero bits of `SHA256(preimage)`; D from `Trailer.MinPoWBits`; default 20; hard cap 40 in three places.
- Mine-then-sign ordering; nonce search from 0 ascending; `maxIterations==0` = unbounded.
- `checkPutStats` fail-closed on zero DHT nodes for every put path.
- Offline `aggregate build` writes output mode **`0644`** (`os.WriteFile(outPath, built.Bytes, 0644)`); `--piece-size` default `companion.MinPieceSize = 16384`.

### 12. Flagged ambiguities / legacy defects the rebuild must NOT reproduce

1. **Piece-size mismatch (real defect).** CLI `--piece-size` defaults to `companion.MinPieceSize = 16384` and its help says it "MUST match the .torrent's metainfo when wrapped" — but the actual companion-torrent wrapper `torrent.go` hardcodes `CompanionPieceLength = 256 * 1024 (262144)`. The offline default (16384) does **not** equal the wrap-time piece length (262144). The task's "default piece-size 16384 must match the wrapping torrent's metainfo piece length" is therefore an *unmet* invariant in legacy. Slice 12 must reconcile these to a single frozen piece length (and the golden SNAGG page/trailer bytes depend on it).

2. **Permissive `DecodePPMI` admission.** Decode validates only field widths — it does NOT verify the BEP-44 signature (delegated to anacrolix's get path) NOR that the referenced companion torrent exists NOR that `Commit == Trailer.TreeFingerprint`. The docstring says the caller "is still responsible" for those. In `MemoryPutterGetter` there is no sig check at all. The rebuild's composite/aggregatePPMI read path must make the Commit-binding verification mandatory and explicit (fail closed), not a caller responsibility — otherwise a valid-but-lying PPMI (correct sig, wrong/`Commit`) is admitted.

3. **Double `Ts` stamping / non-determinism.** `Ts` is stamped from the wall clock in both `PutPPMI` and `EncodePPMI`; combined with `EstimatePPMISize` not stamping it, this is the same footgun Slice 9 already fixed for `dhtschema.EstimateValueSize`. Make `Ts` an explicit input for reproducible golden vectors.

4. **Empty `Commit` allowed.** `commit` is `omitempty` and 0-length is legal ("empty only during dual-write migration, PROPOSAL §6 phase 1"). A 0-length commit means the pointer is unbound to any record set — an unverifiable pointer. The rebuild's `composite` dual-write mode should require a non-empty `Commit` in `aggregatePPMI`/`composite` writes (permit empty only for legacy-read back-compat), not carry the permissive phase-1 admission forward.

5. **Duplicated `leadingZeroBits` helper** (`pow.go` vs `read_btree.go`) — the source itself flags this as tech debt; collapse to one (as `contracts/record` did with `bits.LeadingZeros8`).

6. **`getput.Get` seq/highest-value trust.** `GetPPMI` returns `res.V` from anacrolix without the caller asserting `res.Seq` monotonicity or that a higher-seq item wasn't withheld — standard BEP-44 rollback exposure. Note it; not new to PPMI but relevant to the ≥12-month legacy-read back-compat contract.

Source line anchors: salt/struct/encode/decode = `internal/dhtindex/ppmi.go`; put/get + memory mock = `internal/dhtindex/ppmi_dht.go`; `nextSeq`/`checkPutStats`/struct fields = `internal/dhtindex/dht.go:30,48,75,299,329`; `MaxValueBytes` = `internal/dhtindex/schema.go:23`; PoW mine/sign = `internal/companion/pow.go`; `RecordSigMessage`/Record = `internal/companion/btree.go:173,142`; `VerifyRecordPoW`/`Find` = `internal/companion/read_btree.go:389,115`; CLI cap+0644+piece-size = `cmd/swartznet/cmd_aggregate_build.go:57,68,97`; `MinPieceSize=16384` = `internal/companion/build_btree.go:44`; `CompanionPieceLength=262144` = `internal/companion/torrent.go:19`; rebuilt identity contract = `contracts/record/record.go` (working tree).

---

I have everything needed and have verified the golden byte vectors against the real Go bencode marshaller. Here is the extraction.

## aggregate-backend-admission

Scope: how the legacy Aggregate/PPMI read path plugs into `Lookup`, and the cold-start admission engine that decides which publishers ever enter the fan-out set. Source files on `legacy-snapshot`: `internal/dhtindex/lookup.go`, `internal/dhtindex/dht.go`, `internal/dhtindex/ppmi.go`, `internal/dhtindex/ppmi_dht.go`, `internal/daemon/bootstrap.go`, `internal/daemon/bootstrap_https.go`, `internal/daemon/daemon.go`, `internal/httpapi/aggregate.go`, `internal/reputation/reputation.go`, `internal/config/config.go`. NOTE: there is **no `internal/admission/` package** on the legacy branch — the admission engine lives in `internal/daemon/bootstrap.go` as type `Bootstrap`. The rebuild's proposed `AdmissionEngine`/`DefaultPolicy` are new names for a **deny-by-default** replacement of this permissive `Bootstrap` (see the §6 DEFECT block).

---

### 1. `dhtschema.PPMIValue` — the frozen BEP-44 pointer wire contract

Defined in `internal/dhtindex/ppmi.go`. This is a **cross-implementation wire/byte contract** (the value bencoded into a BEP-44 mutable item; a second impl must reproduce it byte-for-byte). Go struct + tags:

```go
type PPMIValue struct {
    IH     []byte `bencode:"ih"`               // 20-byte BEP-3 infohash of companion index torrent
    Commit []byte `bencode:"commit,omitempty"` // 0 or 32 bytes = companion.Trailer.TreeFingerprint = SHA256(canonical record stream)
    Topics []byte `bencode:"topics,omitempty"` // 0 or 32 bytes = cuckoo-filter digest of covered keyword prefixes
    Ts     int64  `bencode:"ts"`               // unix seconds; informational (BEP-44 seq is canonical ordering)
    NextPk []byte `bencode:"next_pk,omitempty"`// 0 or 32 bytes; reserved for key rotation, empty in v1
}
```

**CRITICAL byte-order fact:** the anacrolix bencode marshaller (`anacrolix/torrent@v1.61.0/bencode/encode.go`, `makeEncodeFields` → `sort.Sort` with `Less: ef[i].tag < ef[j].tag`) emits struct fields **lexicographically by bencode key, NOT declaration order**. So the on-wire key order is: `commit`, `ih`, `next_pk`, `topics`, `ts` (declaration order `ih,commit,topics,ts,next_pk` is irrelevant to the bytes). A reimplementation that emits keys in struct-declaration order produces a *different* signed value and will fail signature verification. Freeze the sorted order in `contracts/dhtschema`.

**GOLDEN VECTOR — minimal PPMIValue** (`IH` = 20×`0x00`, `Ts` = 1700000000; omitempty drops commit/topics/next_pk). Verified against real Go `bencode.Marshal`, length **45 bytes**:
```
hex:   64323a696832303a0000000000000000000000000000000000000000323a747369313730303030303030306565
ascii: d2:ih20:<20×00>2:tsi1700000000ee
```

**GOLDEN VECTOR — full PPMIValue** (`IH`=20×`0x01`, `Commit`=32×`0xaa`, `Topics`=32×`0xbb`, `NextPk`=32×`0xcc`, `Ts`=1700000000). Verified, length **175 bytes**, key order `commit,ih,next_pk,topics,ts`:
```
hex: 64363a636f6d6d697433323aaaaa…aa323a696832303a0101…01373a6e6578745f706b33323acccc…cc363a746f7069637333323abbbb…bb323a747369313730303030303030306565
```

Size caps: `MaxPPMIValueBytes = MaxValueBytes = 1000` (`schema.go`: `MaxValueBytes = 1000`, `MaxSaltBytes = 64`). PPMIs are ~100 bytes in practice; the 1000-byte cap is the BEP-44 storage ceiling enforced on both encode and decode.

**`EncodePPMI` control flow** (fail-closed order matters — reproduce exactly): (1) `len(IH) != 20` → `"dhtindex: PPMI ih %d bytes, want 20"`; (2) `len(Commit) != 0 && != 32` → `"dhtindex: PPMI commit %d bytes, want 0 or 32"`; (3) same for `Topics` (`"…PPMI topics %d bytes, want 0 or 32"`), (4) `NextPk` (`"…PPMI next_pk %d bytes, want 0 or 32"`); (5) **if `Ts == 0` set `Ts = time.Now().Unix()`** (every written item gets a fresh ts — this makes raw `EncodePPMI` output non-deterministic unless the caller pre-sets `Ts`; the golden vectors above pin `Ts` explicitly); (6) `bencode.Marshal` → wrap error `"dhtindex: encode PPMI: %w"`; (7) `len(out) > 1000` → `"dhtindex: encoded PPMI %d bytes exceeds BEP-44 cap %d"`.

**`DecodePPMI` control flow:** (1) `len(payload)==0` → `"dhtindex: empty PPMI value"`; (2) `len > 1000` → `"dhtindex: PPMI value %d bytes exceeds BEP-44 cap %d"` (bound BEFORE unmarshal — a hostile node can return up to the ~64 KiB UDP limit); (3) `bencode.Unmarshal` → `"dhtindex: decode PPMI: %w"`; (4) re-validate all four size fields with the `"decoded PPMI …"` variants of the strings above. `EstimatePPMISize` returns `MaxPPMIValueBytes+1` on marshal error (fail-fast sentinel).

---

### 2. PPMI DHT target — the double-hash salt (wire contract)

From `ppmi.go` + `ppmi_dht.go` + anacrolix `bep44.MakeMutableTarget` (`= sha1.Sum(pubKey[:] || salt)`):

```
PPMISaltSeed = "snet.index"                       // exact plaintext constant
PPMISalt     = SHA256("snet.index")               // 32 bytes, computed at package init
target       = SHA1( pubkey[32] || PPMISalt[32] ) // 20-byte BEP-44 target
```

This is the "double-hash salt": the **salt itself is a SHA-256 digest**, and BEP-44 then applies SHA-1 over `pubkey||salt`. **GOLDEN (wire contract):**
```
PPMISalt = 89fe1aae185c70dbf9d4ccaa44f9996a3d0d00e55eb2821ad9b143401fbc965d   (32 bytes)
target for all-zero pubkey = 7d3839ece4d7251d6c443fe9fbb3b6e7852a78c8         (20 bytes)
```
Privacy rationale (from the source comment): **every** SwartzNet publisher uses the *same* salt, so a passive DHT observer cannot fingerprint SwartzNet traffic by salt — discrimination happens only at the per-publisher pubkey. The rebuild MUST keep the single shared salt; a per-publisher salt would leak the network.

Interfaces (`ppmi_dht.go`): `PPMIPutter{ PutPPMI(ctx, PPMIValue) error }`, `PPMIGetter{ GetPPMI(ctx, pubkey [32]byte) (PPMIValue, error) }`. Production `AnacrolixPutter.PutPPMI`/`AnacrolixGetter.GetPPMI` use `getput.Put`/`getput.Get` at `bep44.MakeMutableTarget(pub, PPMISalt)`, sign with `nextSeq(seq)` (seq clamps at `math.MaxInt64`), and **fail closed via `checkPutStats(stats, "PPMI put")`** → `"dhtindex: %s reached zero DHT nodes"` when `stats==nil || stats.NumResponses==0` (a nil-error put that reached no nodes must not be recorded as success). Getter errors: `"dhtindex: get PPMI %x: %w"`. In-memory fake keeps a separate `ppmiStore map[[20]byte]ppmiStoredItem` keyed by `SHA1(pub||PPMISalt)`; miss → `"dhtindex: no PPMI stored for pubkey"`. The memory Get path does **not** verify signatures (test-only).

---

### 3. The Lookup seam: `PPMIGetter` / `SetPPMIGetter` / `resolvePPMIs`

`internal/dhtindex/lookup.go`. `Lookup` holds `ppmiGetter PPMIGetter` (nil by default) guarded by `l.mu`. Seam methods:
- `SetPPMIGetter(g PPMIGetter)` — attach/detach; nil restores exact pre-Aggregate behavior.
- `PPMIGetter() PPMIGetter` — read back (used by `/aggregate` to report `ppmi_enabled`).

**`Query` control flow (byte-precise ordering the rebuild must preserve):**
1. `Tokenize(query)`; empty → `"dhtindex: query produces no tokens"`. `keyword = tokens[0]` (longest non-stopword in input order). `salt, err = SaltForKeyword(keyword)` — the legacy per-keyword salt is the **raw lowercased UTF-8 keyword bytes** (`SaltForKeyword`: `salt=[]byte(keyword)`, cap `MaxSaltBytes=64`; note: `Query` does NOT lowercase — that is `Tokenize`'s job). This is deliberately a *different* salt scheme from PPMI's fixed hashed salt; do not let PPMI's salt leak into the legacy path.
2. Snapshot `indexers`, `tracker`, `bloom`, `sources`, `minScore`, `ppmiGetter` under RLock. Empty indexer set → return `&LookupResponse{}` (zero).
3. Reputation cutoff: if `tracker!=nil && minScore>0`, keep only `tracker.Threshold(pubkey, minScore)` (filters the same backing array via `indexers[:0]`).
4. **Aggregate fan-out:** if `ppmiGetter!=nil`, `ppmis, ppmiMissing = resolvePPMIs(ctx, ppmiGetter, indexers)`; build `resolvedMask[pubkey]=true` for each resolved publisher.
5. **Fallback set:** `fallback := indexers[:0:len(indexers)]`; append only publishers **not** in `resolvedMask`. Resolved-via-PPMI publishers are *removed* from the legacy per-keyword fan-out — "we trust the Aggregate pointer and don't double-query."
6. `resp = l.legacyQuery(ctx, salt, fallback, tracker, bloom, sources)`; then `resp.IndexersAsked = len(indexers)` (PPMI + legacy counted together), `resp.PPMIsResolved = ppmis`, `resp.PPMIMissing = ppmiMissing`.

**`resolvePPMIs(ctx, getter, indexers)`** fans out one goroutine per indexer calling `getter.GetPPMI(ctx, info.PubKey)` into a buffered channel, `wg.Wait()`, then drains: each `err!=nil` increments `missing`; each success appends `ResolvedPPMI{PubKey, Label, Value}`. Returns `([]ResolvedPPMI, missingCount)`. It does NOT download the companion torrent — it only resolves the pointer; the caller feeds `Value.IH` to the engine, then walks the companion B-tree to produce hits.

**Invariant / seam contract for Slice 12:** `resolvePPMIs` marks a publisher "resolved" purely on a successful PPMI *get*, independent of the legacy path. `Query` still runs the legacy per-keyword path for un-migrated publishers (empty `resolvedMask` entry). This is the composite/dual-read behavior the rebuild's `composite` mode must reproduce: legacy-then-aggregate, with resolved publishers dropped from the legacy fan-out to avoid double-counting.

---

### 4. `LookupResponse` PPMI fields (OMITTED in Slice 9, ADD in Slice 12)

```go
type LookupResponse struct {
    IndexersAsked     int          // PPMI + legacy fan-out combined = len(indexers) post-cutoff
    IndexersResponded int          // legacy responders only (incremented in legacyQuery)
    Hits              []LookupHit  // from the legacy per-keyword path
    PPMIsResolved     []ResolvedPPMI // successful PPMI pointers (empty when no PPMIGetter)
    PPMIMissing       int            // publishers whose PPMI get failed → fell back to legacy
}
type ResolvedPPMI struct { PubKey [32]byte; Label string; Value PPMIValue }
```
`IndexersResponded` counts only legacy responders — a PPMI-only resolution does NOT bump it (subtle: a fully-migrated network reports `IndexersResponded==0` with a full `PPMIsResolved`). Slice 9's `legacyKeyword` port dropped `PPMIsResolved`/`PPMIMissing`/`ResolvedPPMI`; Slice 12 re-adds them behind the backend seam. `legacyQuery` merge/scoring (dedupe by 40-hex infohash, source-count + Bayesian-reputation + Bloom scoring, sort by `BloomHit` desc → `Score` desc → source-count desc → `Name` asc) is unchanged and belongs to the legacy path, not the aggregate path.

---

### 5. The admission engine — `daemon.Bootstrap` (three-channel cold start)

`internal/daemon/bootstrap.go`. Deny/admit gate that decides which publishers `Lookup.AddIndexer` ever sees. It is the object `/aggregate` reports as `bootstrap`.

**Constants / defaults:**
- `DefaultAnchorPubkeys = []string{}` — **empty in dev/v0.5.x builds** (comment: "MUST be populated with real operator keys before a production release"; target is 5).
- `DefaultBootstrapOptions()`: `AnchorHexes = copy(DefaultAnchorPubkeys)`, `MaxTrackedPublishers = 100`, `EndorsementThreshold = 3`.
- `NewBootstrap` clamps non-positive `MaxTrackedPublishers`→100, `EndorsementThreshold`→3. Requires a non-nil `Lookup` (`"daemon: bootstrap needs a Lookup"`). Anchor hex parse: non-hex → `"daemon: anchor %q not hex: %w"`; `len != 32` → `"daemon: anchor %q has %d bytes, want 32"`.

**`admit(pub, label, source) bool`** — the single mutation point:
- already in `admitted` → return `true` (idempotent).
- `source != "anchor" && len(admitted) - anchorsAdmitted >= MaxTrackedPublishers` → **refuse**, return `false`, and log `Warn("daemon.aggregate_bootstrap.admit_capped", "pubkey", hex(pub[:8]), "source", source, "cap", MaxTrackedPublishers)`. **Anchors are exempt from the cap** (tracked separately in `anchorsAdmitted`, so anchors neither consume candidate slots nor get refused).
- else record in `admitted`; if `source=="anchor"` bump `anchorsAdmitted`; then `lookup.AddIndexer(pub, label)`; and **only for anchors** call `tracker.MarkSeeded(reputation.PubKey(pub), label)`.

`"admit_capped"` is the cold-start starvation state the task references — it is a **log line, not a returned status**; `/aggregate` shows only aggregate counts, so a capped node is only visible via logs. The rebuild should surface capped-refusals in status/telemetry, not just slog.

**Channel A — anchors** (`RunAnchors`): fan out `ppmi.GetPPMI` for every anchor; success → `admit(pub, "anchor-"+hex(pub[:4]), "anchor")`. Nil PPMIGetter → `[]error{"daemon: bootstrap has no PPMIGetter"}`; per-anchor failure → `"anchor %x: %w"`. Driver `runAnchorLoop` runs once at startup if anchors exist, then re-runs on each `anchorsAdded` signal (so a build with empty `DefaultAnchorPubkeys` still fetches anchors delivered later by the HTTPS fallback). Log: `Info("daemon.aggregate_bootstrap.anchors", "succeeded", …, "errors", …)`.

**Channel B — crawl** (`CandidateFromCrawl(cand, sigValid)`): `sigValid==false` → return `false` (SPEC §3.2 requires a valid `snet.sig` before admission). Else record in `observed` (so `IsPending` is true), then `bloomPolicy(cand)` → admit `("crawled","bep51")` or stay pending.

**Channel C — endorsement** (`IngestEndorsement(endorser, cand)`): if already admitted, record endorser and return `true`. Else add endorser to `endorsements[cand]`; if `countStrongEndorsers(cand) >= EndorsementThreshold` → admit `("endorsed","endorsement")`; else if `bloomPolicy(cand)` → admit `("endorsed-bloom","endorsement")`; else `false`.

**`bloomPolicy(cand)`:** `bloom==nil` → `false` (deny crawl candidates on a cold cache); `tracker!=nil && tracker.Threshold(cand, 0.3)` → `true`; else `false`. **`countStrongEndorsers(cand)`:** with no tracker returns `len(endorsers)` (all count); with a tracker counts endorsers where `tracker.Threshold(e, 0.5)`.

**`MarkSeeded` reputation effect** (`reputation.go`): sets `SeededAt=now`, `SeedLabel=label`; `scoreOf` adds `SeedBonus * 2^(-age/SeedHalfLife)` on top of the organic Bayesian score. Constants: `defaultUnknownScore=0.5`, `smoothingPriorWeight=5.0`, `SeedBonus=0.45`, `SeedHalfLife=90*24h`. A fresh anchor thus starts near `0.5+0.45≈0.95` and decays to organic over ~6 months. `Tracker.Threshold` returns `true` when `cutoff<=0 || NaN`, else `Score(pk) >= cutoff` (**`>=`, inclusive** — load-bearing for the defect below).

**Counts / introspection methods:** `AnchorCount()=len(anchorKeys)`, `AdmittedCount()=len(admitted)`, `PendingCount()` = distinct pubkeys in `endorsements ∪ observed` minus any already admitted. `IsAdmitted`/`IsPending` expose per-pubkey state.

---

### 6. `seeds.json` shape + reputation seed loader

`internal/config/config.go`: `SeedListPath` default `~/.local/share/swartznet/seeds.json` (`defaultSeedListPath` = `filepath.Join(swartznetShareRoot(),"seeds.json")`). File format (from `reputation.go`):
```json
{"version":1,"seeds":[{"pubkey":"<64-char hex>","label":"<name>"}]}
```
`LoadSeedList(path)`: missing file → `(0,nil)` (not an error — cold start allowed); read err → `"reputation: read seed list: %w"`; bad JSON → `"reputation: parse seed list: %w"`; `Version != 1` → `"reputation: unsupported seed list version %d (want 1)"`; per-entry bad/`len!=32` hex → skip with `"reputation: seed entry %d: bad pubkey %q"`. Valid entries are normalized to **lowercase-hex** derived from decoded bytes (upper/mixed-case in the file would otherwise never match the lowercase lookup path) and imported via `MarkSeeded`.

---

### 7. HTTPS last-ditch anchor channel

`internal/daemon/bootstrap_https.go`: `DefaultBootstrapURL = ""` (empty in dev; releases set the project host). `MaxAnchorBootstrapBytes = 64*1024`. Response JSON `{"version":int,"anchors":["<64-hex>",…],"comment":"…"}`. `FallbackToHTTPS` fails closed on any non-`https` URL (`requireHTTPS(url,false)` → `"daemon: bootstrap URL must use https scheme, got %q"`; loopback-`http` exemption is test-only via `httpGetClient.allowInsecureLoopback`). Errors: `"daemon: FallbackToHTTPS requires a URL"`, `"daemon: HTTPS bootstrap get: %w"`, `"daemon: bootstrap response %d bytes exceeds cap %d"`, `"daemon: decode bootstrap response: %w"`, non-200 → `"daemon: bootstrap endpoint %d %s"`. Parses hex anchors, dedupes against existing, appends; on `added>0` signals the buffered `anchorsAdded` channel to wake `runAnchorLoop`. Body read is `io.LimitReader(body, MaxAnchorBootstrapBytes+1)`.

---

### 8. `/aggregate` endpoint (counts anchors/admitted/pending)

`internal/httpapi/aggregate.go`, `GET /aggregate`, `AggregateStatusResponse` JSON:
```json
{
  "ppmi_enabled": false,
  "known_indexers": 0,
  "indexers": [{"pk":"<64hex>","label":"…"}],
  "record_source_kind": "cache|custom|",
  "record_cache_size": 0,
  "record_cache_max": 0,
  "services": "<16 hex>",
  "bootstrap": {"anchors":0,"admitted":0,"pending":0}
}
```
`ppmi_enabled = (s.lookup.PPMIGetter() != nil)`. `bootstrap` block via the `BootstrapProbe` interface (`AnchorCount/AdmittedCount/PendingCount`) — an import-cycle-avoiding seam `daemon.Bootstrap` satisfies natively; nil when DHT is off. `services` is `formatServicesHex(DefaultServices())` big-endian 8-byte hex; clients check bit 9 `BitSetReconciliation = 0x200` for sync-protocol capability.

---

### 9. §6 PERMISSIVE-ADMISSION DEFECT — the rebuild MUST NOT reproduce (deny-by-default only)

The legacy `bloomPolicy`/`countStrongEndorsers` are **fail-open** because `defaultUnknownScore=0.5` combined with inclusive-`>=` thresholds below 0.5 lets *unknown* pubkeys clear every gate. Two concrete live holes:

1. **"any unknown 0.5 pubkey."** `bloomPolicy` never inspects Bloom *membership*. Its actual admit condition is `tracker != nil && Threshold(cand, 0.3)`. For an unknown pubkey `Score=0.5 >= 0.3` → admit. In production `daemon.go` wires **both** a real `KnownGoodBloom()` and `ReputationTracker()` into `NewBootstrap`, so **every sig-valid channel-B crawl candidate is admitted regardless of Bloom content.** The docstring ("at least two of the candidate's hits appear in the known-good Bloom filter") describes behavior the code does not implement. The legacy test `TestBootstrapBloomPolicyTrackerKnowsPublisher` *asserts this as correct* ("should admit … defaultUnknownScore=0.5 ≥ 0.3") — do not port that test.

2. **"3 fresh Sybils clearing endorsement."** `countStrongEndorsers` counts endorsers with `Threshold(e, 0.5)`. Unknown endorser `Score=0.5 >= 0.5` (inclusive) → counts as "strong." Three brand-new Sybil endorser keys with zero history each score 0.5, sum to 3, hit `EndorsementThreshold=3`, and admit the candidate. With **no** tracker it is worse — `countStrongEndorsers` returns `len(endorsers)`, so any 3 distinct endorser identities admit.

Root cause: the neutral prior (0.5) is treated as sufficient trust, and gate cutoffs (0.3, 0.5-inclusive) sit at or below it. **Rebuild requirement:** the `AdmissionEngine`/`DefaultPolicy` must be **deny-by-default** — unknown/unseeded pubkeys never clear admission on the strength of the neutral prior alone. Bloom admission must test *actual known-good membership*; endorsement admission must require endorsers with *positive earned* reputation strictly above the prior (not `>= 0.5`), and Sybil-resistance must not collapse to "N distinct keys." Anchors (explicitly seeded via `MarkSeeded`) remain the only trust root.

---

### 10. Production reality + config note (flag for the rebuild)

- **The legacy PPMI *read* path is dark-launched.** `SetPPMIGetter` is called **only in tests** (`aggregate_e2e_test.go`, `lookup_ppmi_test.go`, `aggregate_test.go`) — never in `internal/daemon` or any non-test production path. So in the shipping legacy daemon `l.ppmiGetter` is always nil: `resolvePPMIs` never runs, `PPMIsResolved`/`PPMIMissing` are always empty/0, and `/aggregate` always reports `ppmi_enabled:false`. The `Bootstrap` *is* constructed and *does* fetch/admit anchors into `Lookup`, but the Lookup never gets a PPMIGetter attached. Slice 12 is where PPMI-read actually goes live behind the `RecordBackend` seam; the legacy code is the reference for the *format*, not for a wired-up read flow.
- **No `LayerDMode` in legacy config.** `internal/config/config.go` has no `LayerDMode`/`composite`/`aggregatePPMI` fields (grep returns nothing). Those three modes are rebuild-only; the legacy default is effectively "legacy per-keyword only, PPMI dark." The ≥12-month legacy-read back-compat contract means Slice 12's `legacyKeyword` path (salt = raw lowercased keyword bytes, `KeywordValue` schema, `SaltForKeyword` cap 64) must stay the shipping default and must not inherit PPMI's fixed hashed salt or B-tree assumptions.

### 11. Ambiguities to resolve in the rebuild
- `EncodePPMI` mutates `Ts` to `time.Now().Unix()` when zero → raw encode is non-deterministic; deterministic golden fixtures MUST pin `Ts`. Decide whether the rebuild's fingerprint-stable build sets `Ts` from the trailer/commit clock rather than wall-clock.
- `bloomPolicy` is a documented *placeholder* ("Today we don't yet know the candidate's hits… returns true when disposition is not clearly bad") — the intended semantics (≥2 Bloom hits OR ≥1 anchor-index overlap) were never implemented. The rebuild must implement the real predicate, not the placeholder.
- `IndexersResponded` excludes PPMI resolutions; on a fully migrated network it reads 0 despite a full `PPMIsResolved`. Confirm whether the rebuild's response counters should fold PPMI responders in.
- `Threshold` uses inclusive `>=`; every trust cutoff at exactly `defaultUnknownScore` admits unknowns. The rebuild's cutoffs must be strictly-greater-than the prior (or the prior must be below all cutoffs).

---

## aggregate-cli

Byte-exact extraction of the legacy SwartzNet "Aggregate index" subsystem as surfaced through the offline `swartznet aggregate build|inspect|find` CLI and the BEP-51 crawler. All paths read from git branch `legacy-snapshot`. The CLI is a thin driver over `internal/companion` (SNAGG B-tree) and `internal/dhtindex` (PPMI pointer + crawler); this section quotes the CLI plus every byte contract it depends on, because the Slice-12 rebuild must reproduce those bytes from fixtures alone.

---

### 0. Scope, dispatch, exit codes

`cmd/swartznet/cmd_aggregate.go` dispatches `aggregate <sub>` where `<sub> ∈ {inspect, find, build, help|-h|--help}`. Unknown/missing subcommand prints usage and returns `exitUsage`.

Exit-code constants (`cmd/swartznet/main.go:31-33`) — **wire-relevant for scripts/CI**:

```
exitOK      = 0
exitRuntime = 1
exitUsage   = 2
```

Top-level `aggregate` usage text (verbatim, from `printAggregateUsage`):

```
swartznet aggregate — Aggregate (PPMI + B-tree) ops tooling

Usage:
  swartznet aggregate <subcommand> [args]

Subcommands:
  build  --out=FILE [flags]         Sign + pack JSONL records into a signed B-tree index.
  inspect <index-file>              Print trailer metadata for an Aggregate index.
  find <index-file> <prefix>        List records matching a keyword prefix.
  help                              Print this message.
```

Missing subcommand emits `swartznet aggregate: missing subcommand` + usage → `exitUsage`. Unknown subcommand: `swartznet aggregate: unknown subcommand %q` + usage → `exitUsage`.

---

### 1. `aggregate build` — offline sign + pack (`cmd_aggregate_build.go`)

Fully offline: never touches DHT, daemon, or network. Reads JSONL, signs each record with the publisher ed25519 key, optionally mines hashcash PoW, packs a signed SNAGG B-tree, writes it **mode `0644`**, prints stats.

**Flags** (Go `flag` package — both `-x` and `--x` accepted):

| Flag | Type | Default | Help string (verbatim) |
|------|------|---------|------------------------|
| `-in` | string | `"-"` | `JSONL input file; '-' reads from stdin` |
| `-out` | string | `""` **(required)** | `output path for the signed B-tree payload (required)` |
| `-key` | string | `""` | `ed25519 identity file; defaults to the node's ~/.local/share/swartznet/identity.key` |
| `-seq` | uint64 | `1` | `sequence number to embed in the trailer (monotonic per publisher)` |
| `-piece-size` | int | `companion.MinPieceSize` = **16384** | `piece size in bytes; MUST match the .torrent's metainfo when wrapped` |
| `-pow-bits` | uint | `0` | `hashcash difficulty (0 = no mining, 20 = production default)` |

**Validation / control flow** (exact order):
1. `fs.Parse` failure → `exitUsage`.
2. `outPath == ""` → prints `aggregate build: --out is required` → `exitUsage`.
3. `powBits > 40` → prints `aggregate build: --pow-bits above 40 refused (cost prohibitive)` → `exitUsage`. **This is the frozen PoW cap of 40; `build` refuses `> 40` before any work.**
4. `loadPrivKey(keyPath)` — on error prints `aggregate build: load key: %v` → `exitRuntime`. Key resolution:
   - `keyPath == ""` → `identity.LoadOrCreate(config.Default().IdentityPath)` (honors `$XDG_DATA_HOME`; **auto-creates** the default identity — the only place minting is allowed).
   - Explicit `keyPath` → `loadIdentityNoCreate`: `os.Stat` first; missing file → `identity file %q: %w` (**fail-closed, never mints** — a typo must not orphan records under a fresh pubkey).
5. `readRecords(inPath)` — on error prints `aggregate build: read records: %v` → `exitRuntime`.
6. `len(recs) == 0` → `aggregate build: no records in input` → `exitUsage`.
7. `buildAndSign(...)` — on error prints `aggregate build: %v` → `exitRuntime`.
8. `os.WriteFile(outPath, built.Bytes, 0644)` — on error prints `aggregate build: write %s: %v` → `exitRuntime`. **Output file mode is `0644`.**

**JSONL input schema** (`jsonRecord`), one JSON object per line:
```
{"kw":"<keyword>", "ih":"<40-char lowercase hex SHA-1 infohash>", "t":<int64 unix, optional default 0>}
```
`readRecords` (buffer: 64 KiB init, **1 MiB** max token) — per-line validation, byte-exact errors:
- JSON parse fail → `line %d: %w`
- `len(jr.IH) != 40` → `line %d: ih %d chars, want 40 (hex sha-1)`
- `jr.Kw == ""` → `line %d: empty kw`
- empty lines skipped; `scanner.Err()` bubbled.

**`buildAndSign`** (per record `i`):
- `hex.DecodeString(jr.IH)` fail → `record %d: decode ih: %w`
- decoded length `!= 20` → `record %d: ih decoded to %d bytes`
- if `powBits > 0`: `companion.SignAndMineRecord(priv, pub, kw, ih, t, uint8(powBits))`; fail → `record %d: sign+mine: %w`
- if `powBits == 0`: set `Pk=pub`, `Kw`, `Ih`, `T`; sign `ed25519.Sign(priv, companion.RecordSigMessage(r))`; `Pow` stays 0.
- then `companion.BuildBTree(BuildBTreeInput{Records, PubKey, PrivKey, Seq, PieceSize, MinPoWBits: powBits})`.

**Success stdout** (verbatim format):
```
Built Aggregate index
  records:     %d
  pages:       %d
  bytes:       %d
  fingerprint: %s        (hex of the 32-byte TreeFingerprint)
  output:      %s
```

Runnable example: `swartznet aggregate build --in=recs.jsonl --out=idx.snagg --seq=7 --pow-bits=20`

---

### 2. `aggregate inspect` — trailer metadata (`cmd_aggregate.go`)

Flags: `-piece-size` int default **16384** (`piece size in bytes; must match the torrent's metainfo`).

Control flow: `NArg != 1` → `usage: swartznet aggregate inspect <index-file>` → `exitUsage`. `os.ReadFile` fail → `swartznet: read %s: %v` → `exitRuntime`. Wraps bytes in `companion.BytesPageSource{Data, PieceSize}`, calls `companion.OpenBTree(src)`; **any failure (bad magic, wrong version, bad trailer signature, `<3` pages, page-count mismatch, non-zero root piece) →** `swartznet: open b-tree: %v` → `exitRuntime`. So `inspect` is the fail-fast integrity gate.

**Success stdout** (verbatim; publisher pk + fingerprint are hex):
```
Aggregate index inspection
  file:           %s
  file size:      %d bytes
  piece size:     %d bytes
  pages:          %d
  records:        %d
  publisher pk:   %s
  sequence:       %d
  created:        %d (unix)
  min PoW bits:   %d
  fingerprint:    %s
```

---

### 3. `aggregate find [--verify]` — prefix query (`cmd_aggregate.go`)

Flags: `-piece-size` int default **16384**; `-verify` bool default `false` (`also run VerifyFingerprint (scans every leaf; slower)`).

Control flow: `NArg != 2` → `usage: swartznet aggregate find [--piece-size=N] [--verify] <index-file> <prefix>` → `exitUsage`. `ReadFile` fail → `swartznet: read %s: %v` → `exitRuntime`. `OpenBTree` fail → `swartznet: open b-tree: %v` → `exitRuntime`. If `--verify`: `reader.VerifyFingerprint()` fail → `swartznet: fingerprint verification failed: %v` → `exitRuntime` (re-derives the fingerprint from every leaf — see §7). `reader.Find(prefix)` fail → `swartznet: find %q: %v` → `exitRuntime`.

**Success stdout**:
```
Matches for prefix %q: %d records
  <40-hex infohash>  <keyword left-padded to 40>  t=%d
```
(`fmt` verb: `"  %s  %-40s  t=%d\n"`, ih = `hex.EncodeToString(h.Ih[:])`.)

Runnable example: `swartznet aggregate find --verify idx.snagg ubu`

---

### 4. SNAGG page format — byte contract (`internal/companion/btree.go`)

Every page is exactly `PieceSize` bytes, zero-padded. **`PageHeaderSize = 16`.**

**Magic (frozen 6-byte wire constant):** `BTreeMagic = {'S','N','A','G','G',0x00}` = **`53 4e 41 47 47 00`**.
**Version (frozen):** `BTreeVersion = 0x01`. Readers MUST refuse any other version → `companion: page version %d unsupported (this build reads %d)`.

**Page header (16 bytes, all multi-byte fields little-endian):**

| Offset | Width | Field | Notes |
|--------|-------|-------|-------|
| `[0:6]` | 6 | magic | must equal `53 4e 41 47 47 00`, else `companion: bad page magic %q, want %q` |
| `[6]` | 1 | Version | `0x01` |
| `[7]` | 1 | Kind (`PageKind`) | `0x00`=root, `0x01`=interior, `0x02`=leaf, `0xFF`=trailer |
| `[8]` | 1 | Level | uint8 |
| `[9]` | 1 | Flags | uint8 |
| `[10:12]` | 2 | PayloadLength | uint16 LE |
| `[12:16]` | 4 | reserved | zero on write; **NOT enforced on read** (forward-compat) |

`decodeHeader` also errors `companion: page %d bytes, need %d for header` if page shorter than 16.

**PageKind values (stable on wire):** `PageKindRoot=0x00`, `PageKindInterior=0x01`, `PageKindLeaf=0x02`, `PageKindTrailer=0xFF`.

**Frozen size caps:** `MaxKeywordBytes = 64`, `MaxRecordBytes = 256`, `MinPoWBitsDefault = 20`, `MinPieceSize = 16384`, `MaxPieceSize = 4*1024*1024`.

#### 4a. Record wire form (`recordWire`, bencode)

`EncodeRecord` marshals `recordWire` via `github.com/anacrolix/torrent/bencode`. Fields → bencode dict keys: `pk`(32 bytes), `kw`(string), `ih`(20 bytes), `t`(int64), `pow`(uint64), `sig`(64 bytes). **Cross-impl note:** the dict is emitted in the bencode library's canonical (lexicographically sorted) key order — `ih, kw, pk, pow, sig, t` — so a golden record dict is `d2:ih20:…2:kw<len>:…2:pk32:…3:powi<pow>e3:sig64:…1:ti<t>ee`. The rebuild must reproduce that exact key order and integer encodings.

`EncodeRecord` guards: empty keyword → `companion: record keyword is empty`; `len(Kw) > 64` → `companion: record keyword %d bytes exceeds cap %d`; encoded `> 256` → `companion: encoded record %d bytes exceeds cap %d`; marshal error → `companion: marshal record: %w`.

`DecodeRecord` length guards: pk≠32 → `companion: record pk %d bytes, want 32`; ih≠20 → `companion: record ih %d bytes, want 20`; sig≠64 → `companion: record sig %d bytes, want 64`; kw>64 → `companion: record keyword %d bytes exceeds cap %d`; unmarshal fail → `companion: unmarshal record: %w`. **DecodeRecord does NOT verify sig or PoW** — transport form only.

#### 4b. `RecordKey` — sort key + separator basis (frozen)

```
RecordKey(r) = Kw || 0x00 || Ih[20]
```
The `0x00` NUL separator groups all records for a keyword contiguously, tie-broken by infohash. **Excludes** pk, t, pow, sig. This is the ordering used for leaf packing and interior-separator (MIN-KEY) selection.

#### 4c. `RecordSigMessage` — signing/PoW preimage (**frozen cross-impl hash contract**)

```
RecordSigMessage(r) = Pk[32] || Kw(raw UTF-8) || Ih[20] || LE64(uint64(T))[8] || uvarint(Pow)
```
- `T`: 8 bytes `binary.LittleEndian.PutUint64(uint64(r.T))`.
- `Pow`: `binary.PutUvarint` (1–10 bytes).
- ed25519 signature is over these bytes; hashcash preimage is these bytes (SHA-256).
- `pow.go`'s `recordPreimage` is byte-identical (documented duplicate).

**Verified identical to the Slice-8 rebuild `contracts/record/record.go` `SigMessage()`** = `pk || kw || ih || LE64(T) || uvarint(pow)`. This is a match — Pow is INSIDE the signature (cannot be stripped), Sig itself is not self-referential. Note however that the Slice-8 `ElementID = SHA256(pk||kw||ih||LE64(T))` (excludes pow+sig) has **no legacy equivalent** — legacy has only `RecordKey` (excludes pk/t too) and the fingerprint (§7, includes pow+sig). See §9 defect.

#### 4d. Leaf page payload (`EncodeLeaf`)

```
[0:2]  uint16 LE  record count (1..65535)
repeat: uvarint(len(EncodeRecord(r)))  ||  EncodeRecord(r) bytes
```
Caller must pre-sort by `RecordKey`. `ErrPageOverflow` = `companion: page overflow` when it won't fit `pageSize`. Other guards: `companion: leaf page needs ≥1 record`, `companion: too many records for one leaf page`, `companion: leaf payload exceeds uint16`. `DecodeLeaf` guards: wrong kind → `companion: expected leaf, got kind 0x%02x`; `companion: payload length exceeds page`; `companion: leaf payload too short`; `companion: bad record length varint`; `companion: short record bytes`.

#### 4e. Interior / root page payload (`EncodeInterior`)

```
[0:2]  uint16 LE  child count (1..65535)
repeat: uvarint(len(sep)) || sep bytes || uint32 LE ChildIndex(piece index)
```
**MIN-KEY separator scheme:** `sep[i]` = smallest `RecordKey` in child `i`'s subtree (its `minKey`). The **first child's separator is forced empty (`nil`) = −∞** — `EncodeInterior` overrides any non-empty first separator to `nil`. So a child's effective range is `[sep[i], sep[i+1])`, last child = `+∞`. Guards: wrong kind → `companion: EncodeInterior wrong kind %d`; `companion: interior page needs ≥1 child`; `companion: too many children for one page`; `companion: interior payload exceeds uint16`. `DecodeInterior`: `companion: expected interior/root, got kind 0x%02x`; `companion: interior payload too short`; `companion: bad separator varint`; `companion: short separator or child index`. **Decoded separators alias the input buffer — callers holding them past buffer reuse must copy.**

#### 4f. Trailer page — **162-byte payload (frozen)** (`Trailer`)

`TrailerPayloadSize = 1+32+8+8+4+4+8+1+32+64 = 162`. Header kind `0xFF`, `PayloadLength = 162`. Payload byte layout (`encodeTrailerFields` + sig):

| Offset | Width | Field | Encoding |
|--------|-------|-------|----------|
| `[0]` | 1 | TrailerVersion | must be `0x01` |
| `[1:33]` | 32 | PubKey | raw ed25519 pubkey |
| `[33:41]` | 8 | Seq | uint64 LE |
| `[41:49]` | 8 | CreatedTs | uint64 LE |
| `[49:53]` | 4 | RootPieceIndex | uint32 LE — **invariant = 0** |
| `[53:57]` | 4 | NumPages | uint32 LE (incl. trailer) |
| `[57:65]` | 8 | NumRecords | uint64 LE |
| `[65]` | 1 | MinPoWBits | uint8 |
| `[66:98]` | 32 | TreeFingerprint | SHA-256 (§7) |
| `[98:162]` | 64 | PublisherSig | ed25519 over `[0:98]` |

`TrailerSigMessage(t) = encodeTrailerFields(t)` = the first **98 bytes** (everything except `PublisherSig`). `EncodeTrailer` requires `pageSize ≥ 16+162 = 178`, else `companion: page %d bytes too small for trailer (needs %d)`; version≠`0x01` → `companion: unsupported trailer version %d`. `DecodeTrailer`: wrong kind → `companion: expected trailer, got kind 0x%02x`; `companion: trailer payload length %d, expected %d`; version guard. `VerifyTrailerSig` fail → `companion: trailer signature failed to verify`.

---

### 5. Whole-tree build (`build_btree.go`) — deterministic file layout

`BuildBTree(BuildBTreeInput)` → `BuildBTreeOutput{Bytes, NumPages, NumRecords, TreeFingerprint}`. Guards: `companion: PieceSize %d outside [%d, %d]` (16384..4 MiB); `companion: BuildBTree needs ≥1 record`.

File layout (each page exactly `PieceSize`, zero-padded):
```
piece 0        : root page (kind 0x00)
pieces 1..N-2  : interior/leaf pages, top-down BFS order
piece  N-1     : trailer (kind 0xFF, signed)
Bytes length = NumPages * PieceSize
```
Algorithm: (1) defensive-copy records, `sort.Slice` by `compareRecords` (byte order of `RecordKey`, shorter-key-first tiebreak); (2) `packLeaves` greedy-packs sorted records into `EncodeLeaf`-sized groups; (3) `packInteriorLevel` bottom-up until one page remains (the root); a single-leaf tree still gets a synthetic 1-child root (`level:1, isRoot`) so every tree is `root → … → leaves → trailer`; (4) top-down BFS piece-index assignment; (5) rewrite interior `ChildIndex`es (mismatch → `companion: layout mismatch at level %d: consumed %d children, have %d`); (6) fingerprint (§7); (7) trailer with `RootPieceIndex:0`, `CreatedTs` defaulting to `time.Now().Unix()` when 0, `PrivKey==nil` allowed (unsigned diagnostic build → fails `VerifyTrailerSig`). `packLeaves` oversize error: `companion: record of %d bytes too large for page %d`; empty/oversize keyword: `companion: empty keyword in records`, `companion: keyword %q exceeds cap %d`.

**Determinism:** identical input `Records` + identical `PieceSize`/`Seq`/`CreatedTs` → byte-identical output. `CreatedTs` and `Seq` are inputs (the CLI passes `--seq`, and `CreatedTs` is defaulted to now unless set) — a golden fixture must pin both.

---

### 6. Reader control flow (`read_btree.go`)

`BytesPageSource.NumPieces() = len(Data)/PieceSize` (integer division; trailing partial bytes ignored — a wrong `--piece-size` silently misparses). `Piece(i)` → `Data[i*PieceSize : (i+1)*PieceSize]`, else `companion: piece %d out of range [0, %d)`.

`OpenBTree`: requires `NumPieces ≥ 3` else `companion: tree has %d pages, need ≥3 (root+leaf+trailer)`; fetches last piece, `DecodeTrailer`, `VerifyTrailerSig` (→ `companion: trailer signature invalid: %w`), checks `trailer.NumPages == NumPieces` (→ `companion: trailer claims %d pages, source has %d`) and `RootPieceIndex == 0` (→ `companion: trailer root piece = %d, want 0`).

`Find(prefix)`: `pLo = []byte(prefix)`, `pHi = nextPrefix(pLo)` (nil = +∞; `"ubu"→"ubv"`, `"ub\xFF"→"uc"`, all-`0xFF`→nil). Root must be kind `0x00` else `companion: piece 0 kind = 0x%02x, want root`. `walkToLeaves` DFS with a **shared `visited` set** (any repeat → `companion: piece %d reached twice (cycle or fan-in in interior pages)`) and per-child bound `ci ∈ (lastChild, NumPieces-1)` strictly increasing/downward (→ `companion: piece %d child %d index %d out of range (must be in (%d, %d))`). `checkLeafIndices` fails closed on dup/over-count. Per leaf record: skip unless `bytes.HasPrefix(Kw, pLo)`; drop records failing `VerifyRecordSig`; if `trailer.MinPoWBits > 0`, drop records failing `VerifyRecordPoW`. **`MinPoWBits == 0` disables PoW enforcement entirely** (see §9).

`VerifyFingerprint`: re-hashes every leaf's records in piece order, bailing if count exceeds `trailer.NumRecords` (→ `companion: more than %d records, trailer claim exceeded`); final count mismatch → `companion: read %d records, trailer claims %d`; hash mismatch → `companion: reconstructed fingerprint mismatches trailer`.

---

### 7. TreeFingerprint (`build_btree.go` step 5 / `read_btree.go`)

```
TreeFingerprint = SHA256( concat over records (sorted by RecordKey) of EncodeRecord(r) )
```
**`EncodeRecord` is the full bencode dict — INCLUDING `pk`, `pow`, and `sig`.** This is layout-independent (piece size does not affect it) but **publisher-specific and re-sign-unstable** (see §9 defect). It is copied verbatim into `Trailer.TreeFingerprint` and into `PPMIValue.Commit`.

---

### 8. Hashcash PoW (`pow.go`, `read_btree.go`)

`MineRecordPoW(r, bits, maxIterations)`: `bits==0` → no-op; `bits>40` → `companion: MineRecordPoW refuses bits=%d (cost prohibitive)` (**second enforcement of the 40 cap**, below the CLI's). Iterates `r.Pow = 0,1,2,…`, stopping at the first nonce whose `SHA256(RecordSigMessage(r))` has `≥ bits` leading zero bits (MSB-first). `maxIterations==0` = unbounded; cap-hit → `ErrPoWExhausted` = `companion: PoW iteration budget exhausted`.

`SignAndMineRecord(priv, pub, kw, ih, ts, bits)`: `len(pub)!=32` → `companion: SignAndMineRecord pub %d bytes, want 32`; sets `Pk/Kw/Ih/T`, mines (→ `companion: mine record: %w`), THEN signs `RecordSigMessage(mined)` (order matters — signing before mining would invalidate every nonce but one).

`VerifyRecordPoW(rec, minBits)`: `minBits==0` → nil; else `leadingZeroBits(SHA256(RecordSigMessage(rec))) < minBits` → `companion: record PoW %d bits < required %d`. `leadingZeroBits` counts MSB-first per byte.

---

### 9. PPMI value + DHT target salt (`internal/dhtindex/ppmi.go`, `ppmi_dht.go`)

**Target salt (frozen, double-hash):**
```
PPMISaltSeed = "snet.index"
PPMISalt     = SHA256("snet.index")   // 32 bytes
target       = SHA1( publisher_pubkey[32] || PPMISalt[32] )   // BEP-44 mutable target
```
**Golden vector:** `PPMISalt = 89fe1aae185c70dbf9d4ccaa44f9996a3d0d00e55eb2821ad9b143401fbc965d`. Production uses `bep44.MakeMutableTarget(pub, PPMISalt)`; the in-memory mock uses `sha1.Sum(append(pub[:], PPMISalt...))` — same 64-byte preimage. Every SwartzNet publisher shares this salt (privacy: passive observers can't distinguish publishers by salt).

**`PPMIValue`** (bencode, `< 1000`-byte BEP-44 cap = `MaxPPMIValueBytes = MaxValueBytes`):

| bencode key | Go field | Bytes | Rule |
|-------------|----------|-------|------|
| `ih` | IH | 20 (required) | infohash of the companion index torrent |
| `commit` (`,omitempty`) | Commit | 0 or 32 | = `Trailer.TreeFingerprint`; empty only during dual-write migration |
| `topics` (`,omitempty`) | Topics | 0 or 32 | optional cuckoo-filter digest |
| `ts` | Ts | int64 | defaulted to `time.Now().Unix()` if 0 |
| `next_pk` (`,omitempty`) | NextPk | 0 or 32 | reserved for key rotation, empty in v1 |

`EncodePPMI` guards: `ih != 20` → `dhtindex: PPMI ih %d bytes, want 20`; commit/topics/next_pk not 0-or-32 → `dhtindex: PPMI commit/topics/next_pk %d bytes, want 0 or 32`; over cap → `dhtindex: encoded PPMI %d bytes exceeds BEP-44 cap %d`. `DecodePPMI` mirrors these plus `dhtindex: empty PPMI value` and the same size cap (decode side accepts up to ~64 KiB datagram, then caps). `PutPPMI` fails closed on zero-node landing via `checkPutStats(stats, "PPMI put")`.

**Record identity note:** `PPMIValue.Commit` = the §7 fingerprint (includes pow+sig+pk), so the PPMI pointer commits to a *publisher-specific* byte stream, not a signature-independent record set.

---

### 10. Crawler (`crawler.go`, `crawler_tick.go`, `crawler_publisher.go`)

**What the rebuilt `dhtindex` already has:** `SampleInfohashes(ctx, server, addr, target)` — the BEP-51 `sample_infohashes` primitive. Returns `SampleInfohashesResult{Samples []krpc.ID, Interval int64, Num int64, Nodes []krpc.NodeInfo}` (merges `Nodes`+`Nodes6`). Errors: `dhtindex: nil dht.Server`, `dhtindex: nil dht.Addr`, `res.ToError()`, `dhtindex: sample_infohashes reply has no r dict`. **Document only what the crawler ADDS.**

**What the crawler ADDS (`crawler_tick.go`):**
- `MetainfoFetcher func(ctx, ih) ([]byte, error)` — production wires BEP-9 `ut_metadata`; a non-nil error is counted as `FetchErrs` and must NOT abort the tick.
- `PublisherSink func(pubkey [32]byte, sigValid bool)` — one call per signed sample.
- `CrawlOutcome{Samples, Interval, Num, Nodes, Forwarded, BadSigs, Unsigned, Malformed, FetchErrs}`.
- **`CrawlOnce`** = one tick: `SampleInfohashes` → for each sample honor `ctx.Err()` (returns partial outcome + ctx err), `fetch`, then classify via `PublisherFromMetainfo` and route:
  - `perr != nil` → `Malformed++`
  - `sigValid` → `Forwarded++`, `sink(pk, true)`
  - signed-but-bad-sig (`pk != zero`) → `BadSigs++`, `sink(pk, false)` (**still forwarded** so downstream reputation sees it)
  - else → `Unsigned++` (no sink call)
  - `fetch` error → `FetchErrs++`. Nil fetcher/sink → `dhtindex: nil MetainfoFetcher` / `dhtindex: nil PublisherSink`.

**`PublisherFromMetainfo(raw)`** (`crawler_publisher.go`) — classifier over `signing.VerifyBytes` (the `snet.pubkey`/`snet.sig` metainfo fields, domain `SN-TORRENT-V1|`):
```
(pk, true,  nil)      signed + signature verifies
([32]{}, false, nil)  ErrNotSigned (no snet.pubkey) — NOT an error (most DHT torrents)
(pk, false, nil)      ErrBadSignature — claimed pubkey returned, caller MUST pass sigValid=false
([32]{}, false, err)  malformed/truncated bencode — bubbles up
```

**Admission (`daemon.Bootstrap.CandidateFromCrawl(cand, sigValid)`):** `sigValid==false` → immediate `false` (SPEC §3.2 requires valid `snet.sig`); already-admitted → `true`; else record in `observed`, then `bloomPolicy(cand)` → `admit("crawled","bep51")` or stays pending for a later endorsement/Bloom round.

**Frontier / politeness — documented but NOT implemented.** `CrawlOutcome.Nodes` (frontier expansion) and `Interval` (per-node politeness: caller must wait `≥ Interval` before re-querying a node) are **returned for the caller to act on**; there is **no worker pool, scheduler, or frontier loop in the legacy tree**. `CrawlOnce` has **zero production callers** (only `crawler_tick_test.go`); `SampleInfohashes` is called only by the `cmd/swartznet/cmd_crawl_probe.go` ops probe and tests. The "production crawler layers a worker pool on top" text in the comments is aspirational. The Slice-12 rebuild must supply the deterministic worker-pool/frontier/politeness scheduler itself — and per the Production Architecture Rules, that scheduling/claiming/backoff must be deterministic code, not an LLM/prompt.

---

### 11. Ambiguities and §6 defects the rebuild must NOT reproduce

1. **Permissive PoW admission (primary §6 defect).** Reader PoW enforcement is gated on the *publisher-declared* `Trailer.MinPoWBits`; `MinPoWBits == 0` disables it entirely (`Find` and `VerifyRecordPoW` both short-circuit). A spammer simply builds with `--pow-bits=0` (which is also the CLI default) and every reader accepts unminted records. The rebuild must enforce a **network-wide minimum independent of the trailer value** — never trust a self-declared floor of 0.
2. **Default build is spam-vulnerable + inconsistent help.** `--pow-bits` defaults to `0` (no mining → `MinPoWBits=0` trees), yet the help says "20 = production default". `MinPoWBitsDefault=20` exists but `BuildBTree` does NOT auto-apply it. The rebuild should default to a real floor or refuse to publish a `MinPoWBits=0` tree.
3. **Fingerprint is publisher-specific, contradicting the convergence claim.** `TreeFingerprint`/`PPMIValue.Commit` hash `EncodeRecord` (incl. `pk`, `pow`, `sig`), so two publishers — or the same publisher after re-mining — never produce the same commit, despite `build_btree.go`'s comment "two publishers arrive at the same PPMI commit fingerprint independently." This also diverges from the Slice-8 `record.ElementID` (which correctly excludes pow+sig). If convergence/dedup is wanted, freeze the commit over a signature/nonce-independent identity (the `ElementID` preimage), not `EncodeRecord`.
4. **Unsigned interior structure.** Only the leaf-record stream is bound by the trailer signature; interior `ChildIndex`/separator bytes are attacker-controlled. Legacy mitigates with the shared `visited` set + strictly-increasing-downward child bounds + `checkLeafIndices` — **keep these guards**; they are load-bearing, not optional.
5. **Crawler forwards unverifiable pubkeys.** `CrawlOnce` calls `sink(pk, false)` for bad-signature samples, relying on the downstream `CandidateFromCrawl` to reject them. The split is defense-in-depth but fragile — the rebuild should make the "don't admit `sigValid=false`" contract explicit at every sink.
6. **Reserved header bytes `[12:16]` not validated.** `decodeHeader` ignores them; a future writer may set flags there. Golden fixtures must pin them to zero, but the reader must tolerate non-zero for forward-compat (do not tighten to a hard zero-check without a version bump).
7. **`readRecords` doesn't cap keyword length.** `MaxKeywordBytes=64` is enforced only deep in `packLeaves`/`EncodeRecord` (`companion: keyword %q exceeds cap 64`), not per-line — oversize keywords fail late with a companion-layer error instead of a `line %d` message.
8. **`--piece-size` correctness is unchecked.** Nothing in `build`/`inspect`/`find` verifies `--piece-size` equals the wrapping torrent's metainfo piece length (default 16384). `NumPieces = len/PieceSize` (integer truncation), so a mismatched value silently misparses the file rather than erroring. The rebuild should read piece length from the metainfo, not a flag, or validate it.
9. **Two encodings of `T`.** `T` is serialized as `LE64` inside `RecordSigMessage`/PoW preimage but as a bencode integer inside the record dict / PPMI. Golden vectors must pin both; a second implementation that conflates them will mismatch the signature or the fingerprint.
10. **Bencode canonical key order is a hidden contract.** Record and PPMI dicts depend on `anacrolix/torrent/bencode`'s sorted-key output (`ih,kw,pk,pow,sig,t`). Freeze this explicitly in `contracts/snagg`/`contracts/dhtschema` so a non-anacrolix encoder reproduces byte-identical dicts.

---

**Golden constants to freeze in `contracts/snagg` / `contracts/dhtschema`:**
- SNAGG magic = `53 4e 41 47 47 00` (6 bytes); version `0x01`; header 16 bytes; kinds `0x00/0x01/0x02/0xFF`.
- Trailer payload = **162 bytes**, sig over first 98; `RootPieceIndex==0`, root at piece 0, trailer at piece N-1.
- `RecordKey = kw||0x00||ih`; `RecordSigMessage = pk||kw||ih||LE64(T)||uvarint(pow)` (== Slice-8 `SigMessage`).
- PoW cap **40** (refused by both CLI `exit 2` and `MineRecordPoW`); `MinPoWBitsDefault=20`.
- `MinPieceSize=16384` (default `--piece-size`), `MaxPieceSize=4 MiB`, `MaxKeywordBytes=64`, `MaxRecordBytes=256`.
- `PPMISalt = SHA256("snet.index") = 89fe1aae185c70dbf9d4ccaa44f9996a3d0d00e55eb2821ad9b143401fbc965d`; `target = SHA1(pub||PPMISalt)`.
- Build output file mode **0644**; exit codes 0/1/2.

---
