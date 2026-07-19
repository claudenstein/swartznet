# Slice 10 — Companion Index (publish/subscribe) — extraction ledger

_Behavioral extraction from `legacy-snapshot` internal/companion (+ daemon/httpapi/cmd). Slice-10 scope = the simple gzip-JSON CompanionIndex; SNAGG B-tree / PPMI / PoW are Slice 12._

## model

Scope of this section: the CompanionIndex **data model + gzip-JSON serialization**, extracted verbatim from three legacy files on `legacy-snapshot`: `internal/companion/types.go`, `internal/companion/serialize.go`, `internal/companion/doc.go`. These three files are 100% the **simple gzip-JSON companion path** — none of them contain any B-tree / PPMI / PoW code. (Those live in sibling files `btree.go`, `build_btree.go`, `read_btree.go`, `pow.go` in the same package and are **Slice 12, deferred** — do not reproduce them. See "Slice-10 vs Slice-12 boundary" at the end.) All constants, tags, bounds, and error strings below are quoted exactly.

---

### 1. Constants (types.go)

| Name | Type | Exact value | Meaning / rule |
|------|------|-------------|----------------|
| `FormatVersion` | untyped int const | `1` | On-disk schema version of the CompanionIndex JSON. Bumped on backward-incompatible schema change. Subscribers MUST refuse any file whose version they do not recognise. |
| `FormatFileName` | string const | `"swartznet-content-index-v1.json.gz"` | Legacy generic filename used inside the companion `.torrent` before per-publisher naming. Retained as the **empty-publisher fallback** (tests / zero-config code that build a CompanionIndex with no key). |
| `FormatMagic` | string const | `` `{"version":1,"format":"swartznet-content-index"` `` (raw string literal, no trailing brace, no trailing quote) | Documented as "the leading byte sequence of an UNCOMPRESSED companion JSON document." **CAVEAT — see §5: this constant is DEAD. It is never referenced by any production code.** |

`FormatMagic` note: the literal is `{"version":1,"format":"swartznet-content-index"` — i.e. it asserts a specific JSON key order (`version` before `format`) and no whitespace. That assertion is never actually tested against any bytes anywhere in the legacy tree (see §5).

#### `CompanionFileName(pubkeyHex string) string` — the naming rule

```
func CompanionFileName(pubkeyHex string) string {
    if pubkeyHex == "" {
        return FormatFileName                       // "swartznet-content-index-v1.json.gz"
    }
    prefix := pubkeyHex
    if len(prefix) > 12 {
        prefix = prefix[:12]
    }
    return "swartznet-content-index-" + prefix + "-v1.json.gz"
}
```

Exact rule a re-implementation must reproduce:
- Empty `pubkeyHex` → `"swartznet-content-index-v1.json.gz"` (the `FormatFileName` fallback).
- Non-empty → `"swartznet-content-index-" + <prefix> + "-v1.json.gz"`, where `<prefix>` is the **first 12 hex characters** of the pubkey (or the whole string if shorter than 12; no left-padding, no lowercasing — the raw substring is used).
- Example (from `types_test.go`): a 64-char pubkey `"abcdef…"` produces `"swartznet-content-index-abcdef012345-v1.json.gz"` style names; the test only asserts the result ends in `.json.gz`.
- This filename is the **single file's name inside the companion `.torrent`** (used at `torrent.go:58` via `filepath.Join(dir, CompanionFileName(idx.Publisher))`). The subscriber's fail-closed "safe filename / exactly 1 file / ≤32 MiB" check (in the out-of-scope `subscriber.go`) validates the file the publisher wrote under this name.

---

### 2. Structs and EXACT json tags (types.go)

All four structs are plain JSON documents. Tags reproduced exactly, including every `omitempty`.

```go
type CompanionIndex struct {
    Version     int             `json:"version"`
    Format      string          `json:"format"`
    Publisher   string          `json:"publisher,omitempty"`
    GeneratedAt int64           `json:"generated_at"`
    Torrents    []TorrentRecord `json:"torrents"`
}

type TorrentRecord struct {
    InfoHash string       `json:"infohash"`
    Name     string       `json:"name"`
    Size     int64        `json:"size,omitempty"`
    AddedAt  int64        `json:"added_at,omitempty"`
    Files    []FileRecord `json:"files,omitempty"`
}

type FileRecord struct {
    Index     int            `json:"index"`
    Path      string         `json:"path"`
    Size      int64          `json:"size,omitempty"`
    Mime      string         `json:"mime,omitempty"`
    Extractor string         `json:"extractor,omitempty"`
    Chunks    []ContentChunk `json:"chunks,omitempty"`
}

type ContentChunk struct {
    Text   string `json:"text"`
    Offset int64  `json:"offset,omitempty"`
}
```

Per-field semantics (exact, from the doc comments):

**`CompanionIndex`**
- `version` (`Version int`, **no** omitempty): `FormatVersion` at write time. Subscribers refuse unrecognised versions.
- `format` (`Format string`, **no** omitempty): stable schema id, always `"swartznet-content-index"`.
- `publisher` (`Publisher string`, **omitempty**): 64-char hex ed25519 public key, matching `reputation.PubKeyHex`. Empty ⇒ anonymous companion ("uncommon — subscribers should be skeptical"). **Load-bearing for Slice 10**: the subscriber MUST verify this equals the followed pubkey and stamp imported records with `SignedBy = Publisher`.
- `generated_at` (`GeneratedAt int64`, **no** omitempty): unix timestamp at serialization. Subscribers use it to **skip duplicates of an older/unchanged snapshot** they have already imported (the GeneratedAt dedup the legacy §6 failed to do — Slice 10 MUST do it).
- `torrents` (`Torrents []TorrentRecord`, **no** omitempty): the described torrents. Normalised to `[]` (never `null`) by both Encode and Decode — see §3/§4.

**`TorrentRecord`**
- `infohash` (`InfoHash string`, no omitempty): 40-char **lowercase SHA-1 hex**.
- `name` (`Name string`, no omitempty): human-readable torrent name.
- `size` (`Size int64`, omitempty): total bytes (for size filters).
- `added_at` (`AddedAt int64`, omitempty): unix ts when publisher added the torrent locally (freshness ranking).
- `files` (`Files []FileRecord`, omitempty): optional per-file detail; empty when publisher shares only torrent-level metadata.

**`FileRecord`**
- `index` (`Index int`, no omitempty): file's position in the torrent's upverted file list (matches the file tracker).
- `path` (`Path string`, no omitempty): user-visible file path.
- `size` (`Size int64`, omitempty): file byte length.
- `mime` (`Mime string`, omitempty): best-known MIME, e.g. `"text/plain"`, `"application/pdf"`.
- `extractor` (`Extractor string`, omitempty): name of the extractor that produced the chunks (e.g. `"plaintext"`); telemetry + subscriber trust signal.
- `chunks` (`Chunks []ContentChunk`, omitempty): extracted text chunks; may be empty (metadata-only sharing).

**`ContentChunk`**
- `text` (`Text string`, **no** omitempty): the extracted text. Mirrors `extractors.Chunk` (producer) and `indexer.ContentDoc.Text` (consumer). Note: `text` is emitted even when empty (no omitempty).
- `offset` (`Offset int64`, omitempty): byte offset of the chunk in the source file; `0` for whole-file extractions.

---

### 3. Encode path (serialize.go) — JSON → gzip

```go
func Encode(idx CompanionIndex) ([]byte, error)
```

Exact control flow a re-implementation must reproduce:

1. **Force schema fields** (caller values are overwritten, by design):
   - `idx.Format = "swartznet-content-index"`
   - `idx.Version = FormatVersion` (`1`)
2. **Normalise Torrents**: `if idx.Torrents == nil { idx.Torrents = []TorrentRecord{} }` → guarantees `"torrents":[]` in JSON, never `"torrents":null`. (Test `TestEncodeNilTorrentsDefaultsToEmptySlice` pins this; comment: "Subscribers rely on this.")
3. **Serialize**: `gzip.NewWriter(&buf)` wrapping a `bytes.Buffer`, then `json.NewEncoder(gz).Encode(idx)`.
   - Because it uses `json.Encoder.Encode` (not `json.Marshal`), the JSON body has a **trailing newline** `\n` before gzip framing. Any reader relying on exact byte layout must account for this. (The dead `FormatMagic` prefix would still match the start regardless.)
4. `gz.Close()` to flush the gzip trailer.
5. Return `buf.Bytes()`.

**Encode error messages** (exact):
- json encode failure → `fmt.Errorf("companion: encode json: %w", err)` → `"companion: encode json: ..."`
- gzip close failure → `fmt.Errorf("companion: close gzip: %w", err)` → `"companion: close gzip: ..."`

**Output framing invariant (test-pinned):** first two bytes are the gzip magic `0x1f 0x8b` (`serialize_test.go` asserts `encoded[0]==0x1f && encoded[1]==0x8b`). Encoded payload is non-empty.

**Helper `EncodeSize`:**
```go
func EncodeSize(idx CompanionIndex) (int, error)
```
Calls `Encode` and returns `len(buf)` (or the Encode error). "Useful for publisher-side budgeting before committing to a put." (i.e. lets the publisher check the compressed size against the ≤32 MiB fetch bound before wrapping in a torrent.)

---

### 4. Decode path (serialize.go) — gunzip → size-cap → JSON → format/version refuse

```go
func Decode(r io.Reader) (CompanionIndex, error)
```

Exact ordered control flow (subscribers MUST call `Decode`, not `json.Unmarshal` directly, "so the version check is enforced consistently"):

1. `gz, err := gzip.NewReader(r)` — on error: `fmt.Errorf("companion: open gzip: %w", err)` → `"companion: open gzip: ..."`. `defer gz.Close()`.
2. **Decompression safety cap**: `const maxDecompressed = 1 << 30` (**1 GiB**). Read via `io.ReadAll(io.LimitReader(gz, maxDecompressed+1))`.
   - Read error → `fmt.Errorf("companion: read gzip: %w", err)` → `"companion: read gzip: ..."`.
   - **Over-cap guard**: `if int64(len(body)) > maxDecompressed` → `errors.New("companion: decompressed payload exceeds 1 GiB safety cap")`. This is a **decompression-bomb defense** (LimitReader reads cap+1 so exceeding is detectable). Test `TestDecodeRejectsOversizeDecompressed` gzips `(1<<30)+1` bytes of `"ab"` and asserts the error text contains `"1 GiB"`.
3. `json.Unmarshal(body, &out)` → on error: `fmt.Errorf("companion: parse json: %w", err)` → `"companion: parse json: ..."`.
4. **Format refuse** (this is the *real* "magic" check — a field comparison, not a byte prefix): `if out.Format != "swartznet-content-index"` → `fmt.Errorf("companion: bad format %q, want 'swartznet-content-index'", out.Format)`.
5. **Version refuse**: `if out.Version != FormatVersion` → `fmt.Errorf("companion: unsupported version %d, this build understands %d", out.Version, FormatVersion)`. With `FormatVersion == 1` a version-2 file yields `"companion: unsupported version 2, this build understands 1"`.
6. **Torrents normalise**: `if out.Torrents == nil { out.Torrents = []TorrentRecord{} }` (symmetric with Encode).
7. Return `out`.

Ordering invariant to reproduce exactly: **gzip open → size-cap read → unmarshal → format check → version check → torrents-normalise**. Format is checked *before* version. Both checks happen *after* a successful unmarshal (so malformed JSON never reaches the version check).

---

### 5. FLAGGED — `FormatMagic` is dead / the "magic-prefix check" does not exist

The Slice-10 task brief describes the paths as "JSON → gzip, magic-prefix check" (Encode) and "gunzip → **magic check** → version refuse → unmarshal" (Decode). **The legacy code does not do a byte-level magic-prefix check.** Confirmed by `git grep` across `internal/companion/` on `legacy-snapshot`: `FormatMagic` appears **only at its definition** (`types.go:41`) and is referenced by **zero** production or test files.

What actually stands in for the "magic check" is step 4 of Decode: a comparison of the **already-unmarshaled** `out.Format` field against the string literal `"swartznet-content-index"`. There is no validation of raw decompressed bytes against `FormatMagic`, and the JSON key order asserted by that literal (`version` then `format`) is never enforced.

Recommendation for the rebuild: **do not carry `FormatMagic` forward** unless you also wire a real fast-fail prefix check. The field-based `Format`/`Version` refusal in Decode is the behavior to reproduce; it is sufficient and is the one that is test-covered. If you keep a magic constant, either use it (compare `bytes.HasPrefix(body, []byte(FormatMagic))` before unmarshal) or drop it — a defined-but-unused constant is exactly the kind of drift the rebuild is meant to shed.

---

### 6. Two distinct size caps — do not conflate

- **1 GiB (`1 << 30`)** — `maxDecompressed` in `Decode`: an **anti-zip-bomb decompression cap** on the *inflated* JSON. Lives in the `model` layer (this section).
- **≤32 MiB** — the **subscriber fail-closed fetch bound** on the *compressed companion `.torrent`* (exactly 1 file, ≤32 MiB, safe filename). That bound lives in the out-of-scope `subscriber.go`, **not** here. A re-implementation should keep both: 32 MiB gates what the subscriber downloads; 1 GiB gates what `Decode` will inflate in RAM. They are independent bounds at different layers.

---

### 7. `GeneratedAt` and `Publisher` — meaning to a subscriber (from doc comments)

- **`GeneratedAt`**: set by the publisher to the unix timestamp at serialization. To a subscriber it is the **dedup key** — "skip duplicates of an older snapshot they have already imported." Slice 10 MUST compare the decoded `GeneratedAt` against the last imported value for that publisher and **not re-import an unchanged snapshot** (the legacy §6 "re-ingesting every hour" defect was the absence of this check at the subscriber; the field itself was always present in the model).
- **`Publisher`**: set by the publisher to its own 64-char ed25519 pubkey hex. To a subscriber it is the **attribution + verification key** — subscribers "attribute imported records and maintain reputation against the publisher." Slice 10 MUST verify `decoded.Publisher == the followed pubkey` (fail closed if not) and stamp every imported Bleve record with `SignedBy = decoded.Publisher`. Note the model does **not** enforce this — `Publisher` is `omitempty` and may legally be empty ("anonymous"); enforcement is the subscriber's job.

Neither field is signed or authenticated inside the JSON — authenticity comes entirely from the BEP-46 pointer path (the pointer is published under the publisher's identity at salt `"_sn_content_index"`) plus the subscriber's `Publisher == followed` check. The JSON body is a plain, untrusted, un-signed document; treat every field as attacker-controlled up to those two external checks.

---

### 8. Package framing (doc.go)

`doc.go` states the feature is "companion content index" (F3 distributed-content-search, docs/05 §1.2 and §11) and describes the intended file split within the package:
- `types.go` — the JSON schema (this focus).
- `serialize.go` — Encode/Decode with gzip framing (this focus).
- `build.go` — (labelled M11b) `BuildFromIndex` walking an `indexer.Index` → CompanionIndex.
- `torrent.go` — (M11b) wrap a serialised companion file in a **v1 `.torrent`** and return the metainfo.
- `import.go` — (M11d) Decode + import into a target Bleve index, **namespacing each record so the subscriber knows which publisher contributed which document** (this is the `SignedBy` stamping seam).

The end-to-end story per doc.go: publisher serialises once per refresh interval → wraps in a **regular BitTorrent v1 `.torrent`** → adds it to its own engine to seed → publishes a **BEP-46-style pointer** to the new infohash via the existing `dhtindex` Layer-D path. Peers fetch the pointer, download the companion torrent, decode, and import — records then searchable via Layer L / Layer S / Layer D. **This confirms the model layer is transport-agnostic**: `types.go`/`serialize.go` know nothing about DHT, torrents, or Bleve; those are consumed at the (out-of-scope) `build.go`/`torrent.go`/`import.go`/`publisher.go`/`subscriber.go` seams. The rebuilt companion should consume the existing Slice-9 `dhtindex` BEP-46 primitives (`PutInfohashPointer` / `GetInfohashPointer` / `GetInfohashPointerInfo`, salt `"_sn_content_index"`) rather than re-implement pointer logic.

---

### 9. Slice-10 vs Slice-12 boundary (flagged)

- The three files in this focus (`types.go`, `serialize.go`, `doc.go`) are **entirely the simple gzip-JSON path** — no B-tree, no PPMI/Aggregate, no PoW. Safe to port wholesale for Slice 10.
- However, the **legacy `internal/companion` package physically co-locates** the deferred Slice-12 code: `btree.go`, `build_btree.go`, `read_btree.go`, `pow.go` (+ their many tests: `btree_*`, `decode_leaf_*`, `decode_interior_*`, `pack_*`, `walk_to_leaves_*`, `find_pow_test.go`, `pow_test.go`, `verify_fingerprint_*`). doc.go's own file-split list does **not** mention any of these — they were bolted on after M11 and are **out of Slice 10 scope**. When the rebuild reaches `build.go`/`torrent.go`/`import.go`/`publisher.go`/`subscriber.go`, extract only the CompanionIndex (gzip-JSON) code paths from those files and leave every B-tree/PPMI/PoW branch for Slice 12.
- `FormatVersion == 1` corresponds to the gzip-JSON schema documented here. If Slice 12's B-tree format ever ships, it would need its own version/format discriminator — the current `Format == "swartznet-content-index"` + `Version == 1` pair identifies **this** simple document and nothing else.

---

### Exhaustive constant/string reference (for exact reproduction)

| Where | Exact literal |
|-------|---------------|
| Format id (Encode sets, Decode checks) | `swartznet-content-index` |
| FormatVersion | `1` |
| Generic filename / empty-publisher fallback | `swartznet-content-index-v1.json.gz` |
| Per-publisher filename template | `swartznet-content-index-<first12hex>-v1.json.gz` |
| Pubkey prefix length in filename | `12` chars |
| Dead magic constant (unused) | `{"version":1,"format":"swartznet-content-index"` |
| Decompression cap | `1 << 30` = 1 GiB |
| Encode err (json) | `companion: encode json: %w` |
| Encode err (gzip close) | `companion: close gzip: %w` |
| Decode err (gzip open) | `companion: open gzip: %w` |
| Decode err (read) | `companion: read gzip: %w` |
| Decode err (over cap) | `companion: decompressed payload exceeds 1 GiB safety cap` |
| Decode err (parse) | `companion: parse json: %w` |
| Decode err (bad format) | `companion: bad format %q, want 'swartznet-content-index'` |
| Decode err (bad version) | `companion: unsupported version %d, this build understands %d` |
| Torrents normalisation (both dirs) | `nil` → `[]TorrentRecord{}` (JSON `"torrents":[]`, never `null`) |

Legacy source paths (all on branch `legacy-snapshot`): `/home/kartofel/Claude/swartznet` → `internal/companion/types.go`, `internal/companion/serialize.go`, `internal/companion/doc.go` (read via `git show legacy-snapshot:<path>`).

---

Read complete. All collaborators that `publisher.go` invokes (`BuildFromIndex`, `WriteCompanionFiles`, `Encode`, the `CompanionIndex` types) are on the simple gzip-JSON path — `publisher.go` never touches `btree.go`/`build_btree.go`/`read_btree.go`/`pow.go`. Here is the extraction.

## publisher

Legacy source: `internal/companion/publisher.go` (branch `legacy-snapshot`). Collaborators it calls live in `build.go` (`BuildFromIndex`), `torrent.go` (`WriteCompanionFiles`), `serialize.go` (`Encode`), and `types.go` (`CompanionIndex`). **Slice-10 boundary confirmed: `publisher.go` references ONLY the simple gzip-JSON path. It imports nothing from `btree.go`, `build_btree.go`, `read_btree.go`, `pow.go`, or any PPMI/Aggregate code.** The B-tree/PoW machinery is reached through a different builder (`BuildBTree*`), never from this file. `publisher.go` is 100% Slice-10 clean and can be re-implemented without pulling in any Slice-12 code.

### Package-level constant (the well-known salt)

```go
const SaltContentIndex = "_sn_content_index"
```

Exact string: `"_sn_content_index"`. This is the BEP-44 salt under which every publisher publishes their companion-index pointer. The doc comment states the subscriber-side derivation: `target = SHA1(pubkey || SaltContentIndex)`. **In the rebuild this salt already exists in `internal/dhtindex` (Slice 9); the companion Publisher must consume that primitive, not redefine the salt.** The legacy comment marks it as a "stable constant; never change without bumping FormatVersion." Note the publisher passes the *raw salt bytes* to the putter (`[]byte(SaltContentIndex)`) — the `SHA1(pubkey||salt)` target computation happens *inside* the dhtindex putter, not here.

### Ports (narrow interfaces the Publisher depends on)

Two dependencies are narrow local interfaces (adapter pattern, no hard import); one dependency is a concrete type (see the ambiguity flag at the end).

```go
type PointerPutter interface {
	PutInfohashPointer(ctx context.Context, salt []byte, infohash [20]byte) error
}
```
Satisfied by `dhtindex.AnacrolixPutter`. `infohash` is a raw `[20]byte`. `salt` is raw bytes (the publisher passes `[]byte(SaltContentIndex)`).

```go
type TorrentSeeder interface {
	AddTorrentMetaInfo(mi *metainfo.MetaInfo) (any, error)
}
```
Satisfied by the engine's `AddTorrentMetaInfo`. Return handle is discarded (`any`) — the publisher does not keep a typed torrent handle.

The third dependency, the index, is **concrete**: `idx *indexer.Index` (from `internal/indexer`). It is not behind an interface. `BuildFromIndex` calls its methods `AllTorrentDocs()` and `ContentDocsForInfoHash(hex)`. This is the one concrete cross-package import in the publisher path (flagged below).

### Options struct

```go
type PublisherOptions struct {
	Dir          string        // on-disk dir for JSON.gz payload + .torrent; REQUIRED
	PublisherKey [32]byte      // ed25519 pubkey; BEP-44 namespace + "publisher" JSON field
	Interval     time.Duration // rebuild+republish cadence; default 1h
	MinInterval  time.Duration // throttle for manual RefreshNow; default 1m
	PutTimeout   time.Duration // bounds a single BEP-44 put traversal; default 30s
	Build        BuildOptions  // what BuildFromIndex includes; default DefaultBuildOptions()
}
```

Production defaults (`DefaultPublisherOptions()`):
- `Interval = 1 * time.Hour`
- `MinInterval = 1 * time.Minute`
- `PutTimeout = 30 * time.Second`
- `Build = DefaultBuildOptions()`

Regtest defaults (`RegtestPublisherOptions()` — comment: "NEVER use this in production"):
- `Interval = 10 * time.Second`
- `MinInterval = 100 * time.Millisecond`
- `PutTimeout = 5 * time.Second`
- `Build = DefaultBuildOptions()`

Key numeric invariant from the `Interval` doc comment: **the BEP-44 pointer expires after 2h, so `Interval` MUST be ≤ 1h to keep the pointer alive.** The default 1h is the ceiling. `MaxChunksPerFile`/`MaxFilesPerTorrent`/`IncludeContent`/`IncludeTorrentNames` come from `BuildOptions` (defaults: content on, no chunk/file caps, names on).

### Publisher struct (internal state)

```go
type Publisher struct {
	idx       *indexer.Index
	putter    PointerPutter
	seeder    TorrentSeeder
	pubkeyHex string          // 64-char lowercase hex of opts.PublisherKey
	opts      PublisherOptions
	log       *slog.Logger

	mu             sync.Mutex
	lastRefresh    time.Time   // time of last SUCCESSFUL publish (== "lastPublished")
	lastAttempt    time.Time   // time of last attempt of ANY outcome
	lastInfoHash   string      // hex infohash of last successful publish
	lastError      string      // last failure message (cleared on success)
	publishedCount int         // count of successful publishes

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	trigger   chan struct{}    // buffered, cap 1
	wg        sync.WaitGroup
}
```

**State-advance invariant (the load-bearing one for Slice 10):**
- `lastRefresh` (the "last successful publish" / "lastPublished" timestamp) advances **ONLY** in `recordSuccess`. It is NEVER touched by `recordFailure`. It answers "is my pointer still alive (<2h)?".
- `lastAttempt` advances on **every** attempt — both `recordSuccess` and `recordFailure`.
- `lastInfoHash` and `publishedCount` advance only on success; `lastError` is set on failure and cleared (`""`) on success.

There is no separate `lastPublished` field — `lastRefresh` + `lastInfoHash` + `publishedCount` collectively play that role.

### Construction — `NewPublisher(idx, putter, seeder, opts, log) (*Publisher, error)`

Validation, in order, each returning a distinct error (exact strings):
1. `idx == nil` → `errors.New("companion: nil index")`
2. `putter == nil` → `errors.New("companion: nil putter")`
3. `seeder == nil` → `errors.New("companion: nil seeder")`
4. `opts.Dir == ""` → `errors.New("companion: empty dir")`

Then defaulting (mutates a local copy of `opts`):
- `log == nil` → `log = slog.Default()`
- `opts.Interval <= 0` → `1 * time.Hour`
- `opts.MinInterval <= 0` → `1 * time.Minute`
- `opts.PutTimeout <= 0` → `30 * time.Second`

`pubkeyHex = hexEncode(opts.PublisherKey[:])` — a local hex helper (avoids importing `encoding/hex`); 32 bytes → exactly 64 lowercase hex chars using digit table `"0123456789abcdef"`. `stopCh` and `trigger` (cap 1) channels are allocated. Note: `opts.PublisherKey` and other `Build` fields are **not** validated here — the comment says "everything else is validated lazily on the first refresh."

### Lifecycle — Start / Stop

`Start()`: guarded by `startOnce`; `wg.Add(1)` then `go p.run()`. Idempotent; subsequent calls are no-ops.

`Stop()`: guarded by `stopOnce`; `close(p.stopCh)` then `p.wg.Wait()`. Idempotent. Closing `stopCh` both breaks the select loop AND cancels the run-context (see `run`), so an in-flight DHT put is cancelled immediately rather than blocking Stop for up to `PutTimeout`.

### Manual trigger — `RefreshNow() error`

```go
var ErrTooSoon = errors.New("companion: refresh throttled (too soon since last refresh)")
```

Control flow:
1. Lock `mu`. If `!lastAttempt.IsZero() && time.Since(lastAttempt) < opts.MinInterval` → unlock, return `ErrTooSoon`.
2. Otherwise non-blocking send on `trigger`: `select { case trigger <- struct{}{}: return nil; default: return nil }`. If a trigger is already queued (channel full), it returns `nil` ("that's good enough").

**Throttle uses `lastAttempt`, NOT `lastRefresh`** — deliberate per the comment: a manual retry right after a failure (e.g. the empty-index case on a fresh node) still respects `MinInterval`; a failed attempt is not allowed to masquerade as a recent successful publish.

### Worker goroutine — `run()`

1. `defer wg.Done()`.
2. Create `ctx, cancel := context.WithCancel(context.Background())`, `defer cancel()`.
3. Spawn a goroutine `go func(){ <-p.stopCh; cancel() }()` — ties the run-context to Stop, so a slow put traversal is cancelled the moment Stop is called.
4. **Run an initial `refreshOnce(ctx)` immediately** (before the ticker) — comment: "so the GUI does not have to wait an hour to see anything."
5. `tick := time.NewTicker(opts.Interval)`, `defer tick.Stop()`.
6. Loop `select`:
   - `<-stopCh` → `return`
   - `<-tick.C` → `refreshOnce(ctx)`
   - `<-trigger` → `refreshOnce(ctx)`

### The publish cycle — `refreshOnce(parent context.Context)`

Runs the full pipeline once. Every step failure is recorded + logged but **never escalated/panicked** — the worker keeps running. Steps in exact order:

1. **Build.** `idx, err := BuildFromIndex(p.idx, p.pubkeyHex, p.opts.Build)`. On error → `recordFailure(fmt.Errorf("build: %w", err))`, return. (`BuildFromIndex` sets `CompanionIndex.Publisher = pubkeyHex`, `GeneratedAt = time.Now().Unix()`, and walks `idx.AllTorrentDocs()`; with `IncludeContent` it also pulls `idx.ContentDocsForInfoHash`.)

2. **Non-empty check (EMPTY = FAILURE).** `if len(idx.Torrents) == 0` → `recordFailure(errors.New("nothing to publish (empty local index)"))`, return. Exact message: `"nothing to publish (empty local index)"`. This is the mandated invariant: an empty index is a failure, `lastRefresh` does not advance, and the next tick may find something.

3. **Write files.** `_, mi, err := WriteCompanionFiles(p.opts.Dir, idx)`. On error → `recordFailure(fmt.Errorf("write: %w", err))`, return. `WriteCompanionFiles` (in `torrent.go`):
   - `os.MkdirAll(dir, 0o700)`.
   - `payload, _ := Encode(idx)` → gzip(JSON). `Encode` force-sets `Format = "swartznet-content-index"` and `Version = FormatVersion (=1)`, nil `Torrents` → `[]`.
   - Writes payload atomically (tempfile `path+".tmp"`, mode `0o600`, then `os.Rename`; tmp removed on any error) to `<dir>/<CompanionFileName(idx.Publisher)>`.
   - Filename: `CompanionFileName(pubkeyHex)` → `"swartznet-content-index-" + first12hex + "-v1.json.gz"`; empty publisher → generic `"swartznet-content-index-v1.json.gz"` (`FormatFileName`).
   - Builds a single-file v1 metainfo in memory (`buildMetaInfoForFile`): `Info{ Name: base(jsonPath), Length: len(payload), PieceLength: CompanionPieceLength }`, where `CompanionPieceLength = 256 * 1024` (256 KiB). Pieces generated from the in-memory payload via `metainfo.GeneratePieces` (no second disk read). `InfoBytes = bencode.Marshal(info)`. **Empty `AnnounceList`** — companion torrents are discovered via the BEP-46 pointer, not trackers.
   - Writes `bencode.Marshal(mi)` atomically to `<dir>/companion.torrent`.
   - Returns `(jsonPath, mi, nil)`.

4. **Seed.** `if _, err := p.seeder.AddTorrentMetaInfo(mi); err != nil { p.log.Debug("companion.publisher.seed_warn", "err", err) }`. **Seed errors are swallowed (logged at Debug only), NOT treated as failure** — comment: re-adding the same metainfo is benign since anacrolix dedupes by infohash. The cycle continues to the put step regardless.

5. **Publish pointer.**
   - `infoHash := mi.HashInfoBytes()` (a `metainfo.Hash`, i.e. `[20]byte`).
   - `ctx, cancel := context.WithTimeout(parent, p.opts.PutTimeout)`, `defer cancel()`.
   - `if err := p.putter.PutInfohashPointer(ctx, []byte(SaltContentIndex), infoHash); err != nil` → `recordFailure(fmt.Errorf("put pointer: %w", err))`, return.

6. **Success.** `p.recordSuccess(infoHash.HexString())`, then `p.log.Info("companion.publisher.refreshed", "infohash", infoHash.HexString(), "torrents", len(idx.Torrents))`.

Because `GeneratedAt` is `time.Now().Unix()` on every build, the payload bytes change each run, so the infohash normally changes between refreshes (comment confirms this). This is why re-publishing at each interval yields a fresh pointer value.

### State transitions

`recordSuccess(infoHashHex string)`: under `mu`, `now := time.Now()`; sets `lastRefresh = now`, `lastAttempt = now`, `lastInfoHash = infoHashHex`, `lastError = ""`, `publishedCount++`.

`recordFailure(err error)`: `p.log.Warn("companion.publisher.refresh_failed", "err", err)`; then under `mu`, sets `lastAttempt = time.Now()`, `lastError = err.Error()`. **`lastRefresh`, `lastInfoHash`, `publishedCount` are untouched.**

### Status reporting

```go
type PublisherStatus struct {
	LastRefresh    time.Time // last SUCCESSFUL publish (zero until first success)
	LastAttempt    time.Time // last attempt of any outcome
	LastInfoHash   string
	LastError      string
	PublishedCount int
	PubKeyHex      string
}
```

`Status() PublisherStatus`: takes `mu`, returns a snapshot of `lastRefresh`, `lastAttempt`, `lastInfoHash`, `lastError`, `publishedCount`, `pubkeyHex`. Consumed by `/status` output and the GUI.

### Complete error-message inventory (exact strings)

Constructor: `"companion: nil index"`, `"companion: nil putter"`, `"companion: nil seeder"`, `"companion: empty dir"`.
RefreshNow: `ErrTooSoon` = `"companion: refresh throttled (too soon since last refresh)"`.
refreshOnce failures (all via `recordFailure`, wrapped): `"build: %w"`, `"nothing to publish (empty local index)"`, `"write: %w"`, `"put pointer: %w"`.
Seed warning (swallowed, Debug): logged under key `"companion.publisher.seed_warn"`.
Log event keys: `"companion.publisher.refreshed"` (Info), `"companion.publisher.refresh_failed"` (Warn), `"companion.publisher.seed_warn"` (Debug).

### Full invariant list a re-implementation must reproduce

1. Well-known salt is the literal `"_sn_content_index"`; target = `SHA1(pubkey || salt)` computed inside the dhtindex putter — consume the existing Slice-9 primitive, pass raw salt bytes.
2. `Interval` ≤ 1h (pointer TTL is 2h); default exactly 1h. `MinInterval` default 1m, `PutTimeout` default 30s.
3. An empty local index (`len(Torrents)==0`) is a **failure**, not a no-op success — record it as `"nothing to publish (empty local index)"`.
4. `lastRefresh` (last-successful-publish) advances **only** on success; `lastAttempt` advances on every attempt; the manual-refresh throttle keys off `lastAttempt` so a failure still counts against `MinInterval`.
5. `lastInfoHash`/`publishedCount` advance only on success; `lastError` set on failure, cleared on success.
6. Seed errors are swallowed (Debug log), never a failure — anacrolix dedupes by infohash.
7. Pipeline order is fixed: build → non-empty check → write files → seed → put pointer → record success. A failure at any step short-circuits and records failure without advancing `lastRefresh`.
8. Files: payload named `swartznet-content-index-<first12hex>-v1.json.gz` (generic fallback when publisher key empty), metainfo at `companion.torrent`; both written atomically (tmp+rename, payload mode `0o600`, dir `0o700`). Single-file torrent, `PieceLength = 256 KiB`, empty `AnnounceList`.
9. Payload is gzip(JSON) with `Format="swartznet-content-index"`, `Version=1` force-set by `Encode`; `GeneratedAt = now.Unix()` set per build (so infohash rotates each cycle).
10. `Start`/`Stop`/RefreshNow are concurrent-safe and idempotent; Stop cancels an in-flight put via the run-context so it never blocks up to `PutTimeout`. An initial refresh fires immediately on Start, before the first tick.
11. The Publisher never blocks query latency: `BuildFromIndex` holds no locks and makes no network calls.

### Ambiguities / boundaries flagged

- **Concrete `*indexer.Index` dependency vs the "narrow ports" invariant.** `putter` and `seeder` are narrow interfaces, but the index is a concrete `*indexer.Index`, and `BuildFromIndex` calls `idx.AllTorrentDocs()` / `idx.ContentDocsForInfoHash()`. The Slice-10 "companion takes only narrow ports (no concrete indexer import beyond seams)" goal is **not** met by the legacy publisher — the rebuild should introduce an index-reader port (e.g. an interface exposing just those two methods) rather than importing `indexer` concretely. This is the one seam to redesign.
- **Slice-10 vs Slice-12:** `publisher.go` is clean — no reference to `btree.go`/`build_btree.go`/`read_btree.go`/`pow.go`/PPMI. The `Build BuildOptions` field only steers the simple `BuildFromIndex` (gzip-JSON) path. The B-tree/PoW builder is a separate entrypoint the publisher never calls, so the Slice-10 publisher can be reproduced verbatim without touching any deferred code.
- **`Build` sub-option validation** happens nowhere in the publisher (comment: "validated lazily on the first refresh"); in practice `BuildOptions` has no validation, so bad values (e.g. negative caps) are simply ignored by `collectFileRecords` (which only acts when `> 0`).
- **`PublisherKey` is never checked for zero.** A zero key yields a 64-char all-zero `pubkeyHex`, a real (but meaningless) pointer namespace, and the generic filename is NOT used (empty-publisher fallback only triggers when the *hex string* is empty, which it never is here). The rebuild may want to reject an all-zero identity explicitly.

---

I have everything needed for the READER:build scope. Here is the section.

## build

Scope of this section: `internal/companion/build.go` and `internal/companion/torrent.go` on `legacy-snapshot`, i.e. **building a `CompanionIndex` from the local corpus and wrapping the gz-JSON bytes as a single-file `.torrent`**. Supporting types (`types.go`, `serialize.go`) and the source-port methods in `internal/indexer` are quoted where load-bearing. B-tree / PPMI / PoW files (`btree.go`, `build_btree.go`, `read_btree.go`, `pow.go`) are **out of Slice-10 scope** and are not covered — `build.go`/`torrent.go` contain none of that logic; they are the pure gzip-JSON path. The `Publisher` orchestration in `publisher.go` is a *seam* (Slice-10 has its own publisher slice) but three of its behaviors are quoted below because they define the build's contract (empty-index-is-failure, infohash-for-seeding, narrow ports).

---

### 1. The source port the builder reads from (the "CorpusExport")

The legacy builder does **not** use an interface. `build.go` imports `internal/indexer` **concretely** and takes a `*indexer.Index`:

```go
func BuildFromIndex(idx *indexer.Index, publisherHex string, opts BuildOptions) (CompanionIndex, error)
```

It reads the corpus through exactly **two** concrete methods on `*indexer.Index`:

| Method | Signature | Returns |
|---|---|---|
| `AllTorrentDocs` | `func (i *Index) AllTorrentDocs() ([]TorrentDoc, error)` | every torrent-level doc in Bleve (paginated internally, batch=1000; defensive `maxPages = total/1000 + 2` ceiling; truncation logged as `indexer.all_torrent_docs_truncated`) |
| `ContentDocsForInfoHash` | `func (i *Index) ContentDocsForInfoHash(infoHash string) ([]ContentDoc, error)` | every content/chunk doc under one infohash (same batch=1000 / `total/1000 + 2` ceiling; truncation logged as `indexer.content_docs_truncated`) |

Both return freshly-allocated slices safe to retain after the index closes, and both return `errors.New("indexer: closed")` if the Bleve handle is nil.

`TorrentDoc` (the "torrents" half of the export), from `indexer/indexer.go` — **no JSON tags; this is an in-memory struct**:

```go
type TorrentDoc struct {
    InfoHash  string    // 40-char lowercase hex
    Name      string    // torrent name as shown to the user
    FilePaths []string  // all file paths inside the torrent
    Trackers  []string  // tracker URLs
    SizeBytes int64     // total torrent size in bytes
    FileCount int       // cached len(FilePaths)
    AddedAt   time.Time // when added to the index
    SignedBy  string    // 64-char hex ed25519 pubkey of signer, or ""
}
```

`ContentDoc` (the "files + chunks" half), from `indexer/content.go`:

```go
type ContentDoc struct {
    InfoHash   string    // 40-char lowercase hex infohash
    FileIndex  int       // index in the torrent's file list
    FilePath   string    // user-visible path, e.g. "Some Book/chapter3.txt"
    FileSize   int64     // bytes on disk
    Mime       string    // best-guess MIME, e.g. "text/plain"
    Text       string    // extracted text body
    Extractor  string    // name of the extractor that produced this doc
    IndexedAt  time.Time // when written to the index
    ChunkIndex int       // 0 for whole-file; increments per chunk
}
```

**Slice-10 boundary flag:** the builder's direct `*indexer.Index` dependency violates the Slice-10 "companion takes only narrow ports (no concrete indexer/dhtindex import beyond seams)" rule. The rebuild must define a narrow port — e.g. `CorpusSource` with `AllTorrents() ([]TorrentRecordSource, error)` and `ContentFor(infohash) ([]ContentDocSource, error)` — that the indexer satisfies, rather than importing `internal/indexer` inside `companion`. The two method shapes above are the exact contract that port must expose (torrents, plus per-torrent content docs carrying `FileIndex / FilePath / FileSize / Mime / Text / Extractor`).

---

### 2. `BuildFromIndex` control flow (build.go)

```go
func BuildFromIndex(idx *indexer.Index, publisherHex string, opts BuildOptions) (CompanionIndex, error)
```

1. **Nil guard:** `if idx == nil` → return `CompanionIndex{}, errors.New("companion: nil index")`.
2. `torrents, err := idx.AllTorrentDocs()`; on error return `fmt.Errorf("companion: list torrents: %w", err)`.
3. Seed the output document:
   ```go
   out := CompanionIndex{
       Publisher:   publisherHex,           // caller passes 64-char hex, or "" for anonymous
       GeneratedAt: time.Now().Unix(),       // fresh unix seconds every build
       Torrents:    make([]TorrentRecord, 0, len(torrents)),
   }
   ```
   Note `Version` and `Format` are **left zero here** — they are stamped later by `Encode` (see §4), not by the builder.
4. For each `TorrentDoc t`:
   - `rec.InfoHash = strings.ToLower(t.InfoHash)` (force lowercase; the record's json tag is `infohash`).
   - `rec.Size = t.SizeBytes`.
   - If `opts.IncludeTorrentNames` → `rec.Name = t.Name` (else name omitted).
   - If `!t.AddedAt.IsZero()` → `rec.AddedAt = t.AddedAt.Unix()` (zero time → field left 0, `omitempty` drops it).
   - If `opts.IncludeContent`:
     - `contentDocs, err := idx.ContentDocsForInfoHash(t.InfoHash)`; on error return `fmt.Errorf("companion: list content for %s: %w", t.InfoHash, err)` (note: uses the **original-case** `t.InfoHash` in the message, not the lowercased copy).
     - `rec.Files = collectFileRecords(t.FilePaths, contentDocs, opts)`.
   - Append `rec` to `out.Torrents`.
5. Return `out, nil`.

Explicit invariants of `BuildFromIndex`:
- **Holds no locks, makes no network calls** (safe to call from a refresh worker without touching query latency).
- One `TorrentRecord` per torrent doc, in the order `AllTorrentDocs` returned them (Bleve order — **not** sorted by the builder).
- `SignedBy` from the source `TorrentDoc` is **read but never propagated** into the `CompanionIndex` — the publisher's identity is carried only by the top-level `Publisher` field. (Correct for Slice-10: the subscriber must stamp imported records with the *followed* pubkey, not with any per-torrent `SignedBy`.)

---

### 3. `collectFileRecords` control flow (build.go)

```go
func collectFileRecords(filePaths []string, contentDocs []indexer.ContentDoc, opts BuildOptions) []FileRecord
```

Two-phase: bucket content by file index, then emit one record per **file path** (not per content doc).

**Phase 1 — bucket by `FileIndex`** into an internal `map[int]*fileBucket` (`fileBucket{path, size, mime, extractor, chunks}`):
- For each `ContentDoc c`: look up `byIndex[c.FileIndex]`; create the bucket if absent using `path: c.FilePath, size: c.FileSize`.
- `if b.path == "" && c.FilePath != ""` → set path (first non-empty wins).
- `if b.mime == ""` → set `b.mime = c.Mime` (first non-empty wins).
- `if b.extractor == ""` → set `b.extractor = c.Extractor` (first non-empty wins).
- Append a chunk: `b.chunks = append(b.chunks, ContentChunk{Text: c.Text})`.
  - **`ContentChunk.Offset` is never set here** — `indexer.ContentDoc` has no offset field, so every emitted chunk has `Offset == 0` (dropped by `omitempty`). The rebuild should either populate offset from a real source or keep it 0 deliberately.

**Phase 2 — emit one `FileRecord` per entry in `filePaths`** (iterating `for i, p := range filePaths`):
- `rec := FileRecord{Index: i, Path: p}` — so **files with no extracted content are still emitted** (filename-only search still works), which is an intentional invariant.
- If a bucket exists for index `i`: copy `Size`, `Mime`, `Extractor`, `Chunks` onto the record.
  - **Per-file chunk cap:** `if opts.MaxChunksPerFile > 0 && len(rec.Chunks) > opts.MaxChunksPerFile` → truncate `rec.Chunks = rec.Chunks[:opts.MaxChunksPerFile]`.
- Append `rec` to output.
- **Per-torrent file cap:** `if opts.MaxFilesPerTorrent > 0 && len(out) >= opts.MaxFilesPerTorrent` → `break` (stops after N files).

Two ambiguities to flag for the rebuild:
- **Chunk ordering is not guaranteed.** Chunks are appended in the order `ContentDocsForInfoHash` returns docs. That method issues its Bleve query with **no explicit sort**, so chunk order does **not** provably follow `ChunkIndex`. The doc-comment claims "monotonic ChunkIndex" but the code never sorts on it. Slice-10 should sort content docs by `(FileIndex, ChunkIndex)` before bucketing if chunk order matters.
- **A bucket whose `FileIndex` is not present in `filePaths`** (e.g. index out of range) is silently dropped — Phase 2 only walks `filePaths`, so orphan content indices never surface.

---

### 4. `BuildOptions` — the only per-build caps/limits

From `build.go`:

```go
type BuildOptions struct {
    IncludeContent      bool // false ⇒ omit all file chunks (still emits torrent + file list). Default true.
    MaxChunksPerFile    int  // 0 ⇒ no limit. Truncates rec.Chunks. Default 0.
    MaxFilesPerTorrent  int  // 0 ⇒ no limit. Breaks file loop. Default 0.
    IncludeTorrentNames bool // false ⇒ omit rec.Name (reserved "anonymous-torrent" mode). Default true.
}

func DefaultBuildOptions() BuildOptions {
    return BuildOptions{
        IncludeContent:      true,
        MaxChunksPerFile:    0,
        MaxFilesPerTorrent:  0,
        IncludeTorrentNames: true,
    }
}
```

There is **no total-size / total-document cap** in the builder itself. The only size ceiling anywhere in the build/serialize path is the **1 GiB decompressed cap on the *decode* side** (`serialize.go`: `const maxDecompressed = 1 << 30`, error `"companion: decompressed payload exceeds 1 GiB safety cap"`). The publisher-side subscriber fetch cap (1 file / ≤32 MiB / safe names) lives in the subscriber, not here.

---

### 5. Empty-corpus handling — where the "empty index is a FAILURE" rule actually lives

`BuildFromIndex` itself **does not treat an empty corpus as an error.** With no torrents it returns a valid `CompanionIndex` whose `Torrents` is an empty (non-nil after Encode) slice, `err == nil`.

The **"empty published index is a FAILURE"** invariant is enforced one layer up, in `publisher.refreshOnce` (seam, quoted for the contract):

```go
idx, err := BuildFromIndex(p.idx, p.pubkeyHex, p.opts.Build)
...
if len(idx.Torrents) == 0 {
    p.recordFailure(errors.New("nothing to publish (empty local index)"))
    return   // lastRefresh/lastPublished do NOT advance
}
_, mi, err := WriteCompanionFiles(p.opts.Dir, idx)
```

Slice-10 must keep this split: the builder is pure and may return an empty index; the publisher must **fail closed** on `len(Torrents) == 0` and not advance `lastPublished`/`lastRefresh`.

---

### 6. Serialization contract the builder feeds into (`Encode`, serialize.go)

`WriteCompanionFiles` calls `Encode(idx)` before wrapping. Relevant because `Encode` — **not the builder** — stamps the schema fields and defines the byte format:

- `idx.Format = "swartznet-content-index"` (overwrites whatever the caller set).
- `idx.Version = FormatVersion` where `const FormatVersion = 1`.
- `if idx.Torrents == nil` → set to `[]TorrentRecord{}` (so JSON always has a `"torrents"` array, never `null`).
- Byte format: **one gzip stream wrapping `json.Encoder.Encode` output** (note the encoder appends a trailing `\n`). Errors: `"companion: encode json: %w"`, `"companion: close gzip: %w"`.
- Uncompressed JSON begins with the magic prefix `const FormatMagic = ` `{"version":1,"format":"swartznet-content-index"` (field order matters — `Version` then `Format` are the first two struct fields).

`CompanionIndex` / `TorrentRecord` / `FileRecord` / `ContentChunk` with **exact json tags** (types.go):

```go
type CompanionIndex struct {
    Version     int             `json:"version"`
    Format      string          `json:"format"`
    Publisher   string          `json:"publisher,omitempty"`   // 64-char hex ed25519 pubkey
    GeneratedAt int64           `json:"generated_at"`
    Torrents    []TorrentRecord `json:"torrents"`
}
type TorrentRecord struct {
    InfoHash string       `json:"infohash"`            // 40-char lowercase SHA-1 hex
    Name     string       `json:"name"`
    Size     int64        `json:"size,omitempty"`
    AddedAt  int64        `json:"added_at,omitempty"`  // unix seconds
    Files    []FileRecord `json:"files,omitempty"`
}
type FileRecord struct {
    Index     int            `json:"index"`
    Path      string         `json:"path"`
    Size      int64          `json:"size,omitempty"`
    Mime      string         `json:"mime,omitempty"`
    Extractor string         `json:"extractor,omitempty"`
    Chunks    []ContentChunk `json:"chunks,omitempty"`
}
type ContentChunk struct {
    Text   string `json:"text"`
    Offset int64  `json:"offset,omitempty"`   // always 0 in the build path
}
```

Note `TorrentRecord.Name` has **no `omitempty`** — a name-suppressed record still emits `"name":""`.

---

### 7. Wrapping the gz-JSON bytes as a single-file `.torrent` (torrent.go)

**Piece length constant:**
```go
const CompanionPieceLength int64 = 256 * 1024   // 256 KiB
```

**`WriteCompanionFiles(dir string, idx CompanionIndex) (string, *metainfo.MetaInfo, error)`** — the top-level wrap. Control flow:

1. `if dir == ""` → `errors.New("companion: empty dir")`.
2. `os.MkdirAll(dir, 0o700)`; on error `fmt.Errorf("companion: mkdir %q: %w", dir, err)`.
3. `payload, err := Encode(idx)` (the gz-JSON bytes; §6).
4. `jsonPath := filepath.Join(dir, CompanionFileName(idx.Publisher))` then `atomicWrite(jsonPath, payload)`; on error `fmt.Errorf("companion: write payload: %w", err)`.
5. `mi, err := buildMetaInfoForFile(jsonPath, payload)` (§below).
6. `miBytes, err := bencode.Marshal(mi)`; on error `fmt.Errorf("companion: marshal metainfo: %w", err)`.
7. `torrentPath := filepath.Join(dir, "companion.torrent")` then `atomicWrite(torrentPath, miBytes)`; on error `fmt.Errorf("companion: write torrent: %w", err)`.
8. Return `(jsonPath, mi, nil)`.

**On-disk products** (exactly two files in `dir`, both overwritten in place every run):
- `<dir>/<CompanionFileName(idx.Publisher)>` — the gzipped JSON payload.
- `<dir>/companion.torrent` — the bencoded metainfo (constant basename `"companion.torrent"`).

**Companion filename (the torrent's display `name`)** — `CompanionFileName` (serialize/types.go):
```go
const FormatFileName = "swartznet-content-index-v1.json.gz"   // empty-publisher fallback
func CompanionFileName(pubkeyHex string) string {
    if pubkeyHex == "" { return FormatFileName }
    prefix := pubkeyHex
    if len(prefix) > 12 { prefix = prefix[:12] }
    return "swartznet-content-index-" + prefix + "-v1.json.gz"
}
```
→ real publishers get `swartznet-content-index-<first-12-hex-of-pubkey>-v1.json.gz`; empty publisher gets the generic `swartznet-content-index-v1.json.gz`. Rationale: the generic name was indistinguishable across publishers in a downloads list. **Invariant:** the file `Name` inside the metainfo == this basename, and subscribers locate the payload by the on-disk path the fetcher returns, so a renamed file stays decodable end-to-end.

**`buildMetaInfoForFile(filePath string, payload []byte) (*metainfo.MetaInfo, error)`** — builds a **v1 single-file** torrent from bytes already in memory (deliberately avoids `metainfo.Info.BuildFromFilePath`, which would re-open the file):

```go
info := metainfo.Info{
    Name:        filepath.Base(filePath),   // == CompanionFileName(...)
    Length:      int64(len(payload)),        // single-file torrent: Length set, no Files list
    PieceLength: CompanionPieceLength,        // 256 KiB
}
pieces, err := metainfo.GeneratePieces(bytes.NewReader(payload), info.PieceLength, nil)  // nil hasher ⇒ SHA-1
// err ⇒ "companion: generate pieces: %w"
info.Pieces = pieces
infoBytes, err := bencode.Marshal(info)   // err ⇒ "companion: marshal info: %w"
mi := &metainfo.MetaInfo{ InfoBytes: infoBytes }
```

Structural facts a re-implementation must reproduce:
- **Single-file torrent:** `Info.Length` is set and there is **no `Files` list** → decodes as one file named `<CompanionFileName>`. (This is what makes the subscriber's "exactly 1 file" fail-closed check pass.)
- **Piece length = 256 KiB (`262144`)**, SHA-1 pieces generated directly from the in-memory payload (no second disk read).
- **Not private:** `Info.Private` is never set → absent/`0`. Companion torrents are public (DHT-discoverable).
- **Trackerless:** `MetaInfo` sets **only `InfoBytes`**. No `Announce`, empty/absent `AnnounceList` (the comment: "companion torrents are discovered via the M11c BEP-46 pointer, not via trackers"). No `Comment`, `CreatedBy`, `CreationDate`, etc.
- The metainfo is returned **in memory** so the caller (publisher) can hand it straight to `AddTorrentMetaInfo` without re-reading `companion.torrent` from disk.

**`atomicWrite(path string, data []byte) error`** — tempfile + rename, used for both files:
- writes to `path + ".tmp"` via `os.OpenFile(tmp, O_CREATE|O_TRUNC|O_WRONLY, 0o600)`;
- `io.Copy` the bytes; on any failure — copy error, `Close` error, or `Rename` error — it `os.Remove(tmp)` so a failed publish never leaves a `.tmp` turd or a half-written product. Dir is created mode `0o700`, files mode `0o600`.

---

### 8. How the infohash is produced for seeding (seam contract)

`build.go`/`torrent.go` **do not compute or return an infohash** — they return the `*metainfo.MetaInfo` (its `InfoBytes`). The infohash is derived by the caller. In the legacy publisher (`publisher.refreshOnce`, seam):

```go
_, mi, err := WriteCompanionFiles(p.opts.Dir, idx)
...
if _, err := p.seeder.AddTorrentMetaInfo(mi); err != nil { /* AddTorrent dedupes by infohash; swallow */ }
infoHash := mi.HashInfoBytes()          // SHA-1 of InfoBytes == the v1 infohash
...
// then publishes the BEP-46 pointer at salt SaltContentIndex and recordSuccess(infoHash.HexString())
```

So: **infohash = `metainfo.MetaInfo.HashInfoBytes()` = SHA-1(`InfoBytes`)**, computed by the caller, and the same `mi` is handed to the engine seeder (`AddTorrentMetaInfo(mi *metainfo.MetaInfo) (any, error)` — a narrow interface, returns `any` to avoid importing the engine's torrent handle type). **Because `GeneratedAt` is `time.Now().Unix()` on every build (§2), `InfoBytes` — and thus the infohash — normally changes every refresh**, which is exactly what the BEP-46 mutable pointer republish depends on. If two builds land in the same unix second with identical corpus, the infohash is stable and `AddTorrentMetaInfo` dedupes (intended).

The narrow seams the legacy publisher depends on — to be preserved as the *only* couplings Slice-10's companion build/publish surface has:
- `PointerPutter{ PutInfohashPointer(ctx, salt []byte, infohash [20]byte) error }` — the BEP-46 primitive; Slice-9 already provides `PutInfohashPointer` / `GetInfohashPointer` / `GetInfohashPointerInfo` and the well-known salt `"_sn_content_index"` in the rebuilt `internal/dhtindex`, so the new companion should consume that, not re-derive the salt or reimplement the put.
- `TorrentSeeder{ AddTorrentMetaInfo(mi *metainfo.MetaInfo) (any, error) }`.

---

### 9. Invariants a second implementation MUST reproduce (build side)

1. One `TorrentRecord` per source torrent doc; `InfoHash` forced to `strings.ToLower`.
2. One `FileRecord` per **file path** in the torrent (files without content are still emitted for filename search); `FileRecord.Index` = position in the torrent's file list.
3. Chunk text copied verbatim into `ContentChunk.Text`; `Offset` unpopulated (0) in this path.
4. `IncludeContent=false` omits all chunks but keeps torrent + file records; `IncludeTorrentNames=false` omits `Name`; `MaxChunksPerFile`/`MaxFilesPerTorrent` bound per-file / per-torrent (`0` = unbounded).
5. `Version`/`Format` are stamped by `Encode` (`1` / `"swartznet-content-index"`), not by the builder; wire format = single gzip stream over JSON.
6. Single-file, **256 KiB piece**, **non-private, trackerless** v1 metainfo; file `Name` = `CompanionFileName(publisher)`; SHA-1 pieces from in-memory bytes.
7. Both output files written atomically (tempfile+rename, 0600 files / 0700 dir); a failed publish leaves no partial file and no `.tmp`.
8. `WriteCompanionFiles` returns the payload path **and** the in-memory `*metainfo.MetaInfo`; infohash for seeding = `mi.HashInfoBytes()`, computed by the caller.
9. **Empty corpus is not an error in the builder** — the publisher enforces "empty ⇒ failure, do not advance lastPublished".

### 10. Slice-10-vs-Slice-12 boundary calls & flagged ambiguities

- `build.go`/`torrent.go` are **100% the simple gzip-JSON path** — no B-tree/PPMI/PoW code touches them. `build_btree.go`, `btree.go`, `read_btree.go`, `pow.go` (and their `*_test.go`) are the deferred Slice-12 SNAGG/PPMI/hashcash path and are excluded here.
- **Concrete `internal/indexer` import in `build.go` is a Slice-10 defect to fix** — replace with a narrow `CorpusSource` port (§1).
- **Chunk ordering not guaranteed** — `ContentDocsForInfoHash` has no explicit Bleve sort, so `ChunkIndex` order is not preserved despite the doc-comment; sort `(FileIndex, ChunkIndex)` if order matters (§3).
- **`ContentChunk.Offset` is dead in this path** (indexer `ContentDoc` has no offset) — decide whether Slice-10 keeps the field or populates it.
- Orphan content docs (a `FileIndex` not present in `filePaths`) are silently dropped (§3).
- The §6 legacy defects called out in the task (no `Publisher==followed` verify, no `SignedBy` stamp on import, no `GeneratedAt` dedup) live on the **subscriber/import** side, not in `build.go`/`torrent.go` — the build side here is clean and just needs the narrow-port fix.

Files read (all on `legacy-snapshot`): `internal/companion/build.go`, `internal/companion/torrent.go`, `internal/companion/types.go`, `internal/companion/serialize.go`, `internal/companion/doc.go`, plus source-port refs `internal/indexer/indexer.go` (`TorrentDoc`, `AllTorrentDocs`, `ContentDocsForInfoHash`) and `internal/indexer/content.go` (`ContentDoc`); publisher seam quoted from `internal/companion/publisher.go`.

---

I now have a complete, exact picture of the integration surface. Compiling the section.

## integration

Scope of this section: the **daemon ↔ httpapi ↔ companion** wiring for the Slice-10 simple gzip-JSON `CompanionIndex` path. Files read on `legacy-snapshot`: `internal/daemon/daemon.go`, `internal/daemon/follows.go`, `internal/daemon/adapters.go`, `internal/httpapi/companion.go`, plus the seam definitions in `internal/companion/publisher.go`, `internal/companion/subscriber.go`, `internal/engine/engine.go`, `internal/config/config.go` (only the parts needed to pin the ports/constants the integration layer relies on). **Boundary flag:** the B-tree / PPMI / PoW path (`btree.go`, `build_btree.go`, `read_btree.go`, `pow.go`, `serialize.go` snagg encoding) is *not* touched by any of the four integration files — the daemon constructs `companion.NewPublisher` / `companion.NewSubscriber`, which use the gzip-JSON `CompanionIndex` path exclusively. `PublisherOptions.Build BuildOptions` (defaulted via `DefaultBuildOptions()`) selects that simple path; the B-tree build variant is reached through a different `build_btree.go` entry the daemon never calls. Slice-10 reproduces only what is below.

---

### 1. Config fields that gate companion (`internal/config/config.go`)

Two fields, both defaulted, both individually able to disable a leg:

| Field | Type | Default | Meaning |
|---|---|---|---|
| `CompanionDir` | `string` | `defaultCompanionDir()` → `filepath.Join(swartznetShareRoot(), "companion")` i.e. `~/.local/share/swartznet/companion` | On-disk dir for the `*.json.gz` payload + wrapping `.torrent`. **Empty string disables both publisher AND subscriber** (both `if` guards in `daemon.New` require `opts.Cfg.CompanionDir != ""`). |
| `CompanionFollowFile` | `string` | `defaultCompanionFollowFile()` → `filepath.Join(swartznetShareRoot(), "companion-follows.json")` i.e. `~/.local/share/swartznet/companion-follows.json` | JSON array of `{"pubkey":"<64-hex>","label":"<name>"}`. Empty string → subscriber still runs but follows are not loaded at startup and not persisted on Follow/Unfollow. |

A third field, `Regtest bool`, swaps `DefaultPublisherOptions()` → `RegtestPublisherOptions()` for the publisher only (subscriber has no regtest variant call). `NoIndex bool` indirectly disables **both** legs because both require `d.Index != nil`, and the index is only opened when `!opts.NoIndex`. `daemon.New` also mirrors `opts.NoIndex` into `opts.Cfg.NoIndex` at the very top so engine publisher-gating stays consistent.

There is **no dedicated `CompanionEnable` boolean** — enablement is purely the conjunction of gates below.

---

### 2. Startup order & gating inside `daemon.New` (`internal/daemon/daemon.go`)

Strict construction sequence (each subsystem in its own labelled block):

1. `bgCtx, bgCancel := context.WithCancel(ctx)` — Daemon-owned cancel for background goroutines.
2. **engine** — `engine.New(ctx, opts.Cfg, opts.Log)`; on error `bgCancel()` then return.
3. **indexer** — only if `!opts.NoIndex`: `indexer.Open(opts.Cfg.IndexDir)`; on error `bgCancel()`, `eng.Close()`, return. On success `d.Index = idx; eng.SetIndex(idx)`.
4. **companion publisher (comment tag "M11c")** — started iff **all four** hold:
   ```
   d.Index != nil && eng.PointerPutter() != nil && eng.Identity() != nil && opts.Cfg.CompanionDir != ""
   ```
   - `cpOpts := companion.DefaultPublisherOptions()`, replaced by `companion.RegtestPublisherOptions()` when `opts.Cfg.Regtest`.
   - `cpOpts.Dir = opts.Cfg.CompanionDir`
   - `cpOpts.PublisherKey = eng.Identity().PublicKeyBytes()`  ← identity is load-bearing here (publisher needs the pubkey both for the BEP-44 namespace and the `CompanionIndex.Publisher` field).
   - `compPub, err := companion.NewPublisher(d.Index, eng.PointerPutter(), eng, cpOpts, opts.Log)`. On error: **non-fatal** — `fmt.Fprintf(stderr, "warning: companion publisher start failed: %v\n", err)`, `d.CompPub` stays nil. On success: `compPub.Start(); d.CompPub = compPub`.
5. **companion subscriber (comment tag "M11d")** — started iff **three** hold (note: identity NOT required):
   ```
   d.Index != nil && eng.PointerGetter() != nil && opts.Cfg.CompanionDir != ""
   ```
   - `sub, err := companion.NewSubscriber(eng.PointerGetter(), eng, d.Index, companion.DefaultSubscriberOptions(), opts.Log)` — positional args are `(getter, fetcher(=eng), ingester(=d.Index), opts, log)`. Error → warn to stderr, subscriber leg stays nil.
   - `compSub, err := companion.NewSubscriberWorker(sub)`. Error → warn, stays nil.
   - **Follow-file load** — only if `opts.Cfg.CompanionFollowFile != ""`: `LoadFollowFile(compSub, opts.Cfg.CompanionFollowFile, stderr)`. A non-nil error is **non-fatal**; if `opts.Log != nil` it is logged `opts.Log.Warn("daemon.companion.load_follow_file_err", "err", err, "path", ...)`. Rationale in comment: unreadable/corrupt follow file leaves an empty list (valid fail-closed state; follows can still be added via HTTP).
   - `compSub.Start(); d.CompSub = compSub`.
6. **Aggregate bootstrap (P4.1)** — unrelated to companion (uses `eng.PointerGetter()` as a `PPMIGetter`, not the companion pointer). Runs `boot.runAnchorLoop(bgCtx)` under `d.bgWG`. **Slice-12-adjacent; not companion.**
7. `eng.RestoreSession()` (error ignored).
8. **HTTP API** — only if `opts.APIAddr != ""`. `apiOpts.Companion = newCompanionAdapter(d.CompPub, d.CompSub, opts.Cfg.CompanionFollowFile)`. Note the adapter is constructed **unconditionally** — even when both `d.CompPub` and `d.CompSub` are nil — so the `/companion` routes always exist and degrade gracefully (see §5). `api.Start()` error is non-fatal (warn to stderr).

**Key invariant for Slice 10:** publisher and subscriber are independent legs; each has its own gate; a failure in one never blocks the other or the daemon. All companion setup failures are warnings, never fatal — the node still serves local + Layer-D search.

### 3. Teardown order in `Daemon.Close`

Reverse of startup, explicit and deterministic:

1. `d.bgCancel()` then `d.bgWG.Wait()` — stop background bootstrap first so nothing is still touching engine/Lookup.
2. `d.API.Stop(shutdown)` with a `3*time.Second` timeout context — HTTP server down before the workers it exposes.
3. `if d.CompSub != nil { d.CompSub.Stop() }` — **subscriber before publisher** (worker `Stop()` closes `stopCh`, cancels the ctx tied to the in-flight `Sync`, joins its goroutine; bounded because the run-ctx is cancelled rather than waiting up to `FetchTimeout`).
4. `if d.CompPub != nil { d.CompPub.Stop() }`.
5. `if d.Index != nil { d.Index.Close() }` — index closed **after** both companion workers, since both hold `d.Index` and could otherwise write to a closed Bleve.
6. `return d.Eng.Close()`.

**Invariant:** companion workers are torn down before the index they write into and before the engine they seed/fetch through. Slice 10 must preserve this ordering (workers → index → engine).

---

### 4. The follow file (`internal/daemon/follows.go`)

**On-disk format:** a single JSON array of `followEntry`:
```go
type followEntry struct {
    PubKey string `json:"pubkey"`          // 64-char lowercase hex ed25519 pubkey
    Label  string `json:"label,omitempty"` // human-readable name
}
```
Written by `adapters.go`'s `persistFollows` with `json.MarshalIndent(entries, "", "  ")` (2-space indent). `followEntry` intentionally lives in `daemon`, not `companion`, because the file is a daemon-side detail.

**Constant:** `const maxFollowFileBytes = 1 << 20` (1 MiB). Comment: ~100 bytes/entry ⇒ ~10k follows.

**`LoadFollowFile(w *companion.SubscriberWorker, path string, stderr io.Writer) (int, error)`** — control flow, fail-closed:
1. `os.Open(path)`. If `errors.Is(err, os.ErrNotExist)` → `return 0, nil` (missing file is NOT an error — fresh install starts empty).
2. Any other open error → warn `"warning: companion follow file: %v\n"` + `return 0, fmt.Errorf("daemon: open companion follow file %q: %w", ...)`.
3. `io.ReadAll(io.LimitReader(f, maxFollowFileBytes+1))`. Read error → warn `"...read: %v"` + wrapped error.
4. **`if len(data) > maxFollowFileBytes`** → warn `"warning: companion follow file exceeds %d bytes; ignoring\n"` + `return 0, fmt.Errorf("daemon: companion follow file %q exceeds %d bytes", ...)`. **Fail closed wholesale** — a file over the cap is rejected entirely, never half-parsed (comment: half-loading would silently drop an arbitrary suffix of the follow list).
5. `json.Unmarshal(data, &entries)`. Parse error → warn `"...parse: %v"` + wrapped error.
6. Per-entry loop: `hex.DecodeString(e.PubKey)`; **`if err != nil || len(raw) != 32`** → warn `"warning: companion follow entry %d: bad pubkey %q\n"`, `continue` (per-entry malformed pubkeys are skipped, NOT fatal — partial loads permitted so one bad row doesn't strand the list). Else `copy(pub[:], raw)`, `w.Follow(pub, e.Label)`, `n++`.
7. `return n, nil`.

**Distinction:** file-level problems (open/read/oversize/parse) fail closed and return an error; entry-level problems are skipped and logged. This is a deliberate split Slice 10 must reproduce.

**Save side lives in `adapters.go` `persistFollows`** (see §5): atomic tmpfile+rename, mode `0o600`, under a mutex. Load and save are in *different* files but share the `followEntry` shape.

---

### 5. Adapters satisfying the companion ports (`internal/daemon/adapters.go`)

The daemon passes concrete objects as the companion ports; the ports themselves are narrow interfaces declared inside `internal/companion` so that package never hard-imports engine/dhtindex/indexer. Wiring:

**Publisher ports (constructor `companion.NewPublisher(idx, putter, seeder, opts, log)`):**
- `idx *indexer.Index` = `d.Index` — walked to build the `CompanionIndex` (the CorpusExport source; Slice 10 reads local Bleve here).
- `putter companion.PointerPutter` = `eng.PointerPutter()` returning **`*dhtindex.AnacrolixPutter`**. Interface: `PutInfohashPointer(ctx, salt []byte, infohash [20]byte) error`. **This is the Slice-9 BEP-46 primitive** — the new companion must consume the existing `dhtindex.PutInfohashPointer` at salt `"_sn_content_index"`.
- `seeder companion.TorrentSeeder` = `eng` directly. Interface: `AddTorrentMetaInfo(mi *metainfo.MetaInfo) (any, error)` — returns `any` deliberately so companion doesn't import engine. Used to seed the freshly built `.torrent` in-memory (no second disk read).

**Subscriber ports (constructor `companion.NewSubscriber(getter, fetcher, ingester, opts, log)`):**
- `getter companion.PointerGetter` = `eng.PointerGetter()` returning **`*dhtindex.AnacrolixGetter`**. Interface: `GetInfohashPointer(ctx, pubkey [32]byte, salt []byte) ([20]byte, error)`. Slice-9 primitive again. (The engine also exposes `GetInfohashPointerInfo` per the prompt; subscriber uses only `GetInfohashPointer`.)
- `fetcher companion.CompanionFetcher` = `eng`. Interface: `FetchCompanionTorrent(ctx, infohash [20]byte) (path string, err error)`. **This is the fail-closed fetch seam** (see §7).
- `ingester companion.Ingester` = `d.Index` directly. Interface: `IndexTorrent(indexer.TorrentDoc) error` + `IndexContent(indexer.ContentDoc) error`. **This is CorpusImport.**

**`companionAdapter` (satisfies `httpapi.CompanionController`):**
```go
type companionAdapter struct {
    pub        *companion.Publisher
    sub        *companion.SubscriberWorker
    followPath string
    followMu   sync.Mutex
}
func newCompanionAdapter(pub, sub, followPath) *companionAdapter
```
Either `pub` or `sub` may be nil; each method on a nil leg returns a clear error or empty value so the GUI can still render the other half:
- `PublisherStatus() httpapi.CompanionPublisherStatus` — nil pub → zero struct. Else copies `pub.Status()` fields: `LastRefresh, LastInfoHash, LastError, PublishedCount, PubKeyHex` (drops `LastAttempt` — the httpapi DTO has no such field).
- `RefreshNow() error` — nil pub → `errors.New("companion publisher not configured")`; else `pub.RefreshNow()` (returns `companion.ErrTooSoon` verbatim on throttle).
- `SubscriberStatus() []httpapi.CompanionFollowStatus` — nil sub → nil. Else iterates `sub.Following()` (`map[[32]byte]string`), for each pubkey calls `sub.LastSync(pub)` and builds a row: `PubKeyHex = hex.EncodeToString(pub[:])`, `Label`, `TorrentsImported`, `ContentImported`, `GeneratedAt`; `LastError = res.Err.Error()` if `res.Err != nil`; `PointerInfoHash = hex(res.PointerInfoHash[:])` only if not the zero `[20]byte`; `LastSyncAt = time.Unix(res.GeneratedAt, 0).UTC()` only if `res.GeneratedAt > 0`.
- `Follow(pubkey [32]byte, label string) error` — nil sub → `errors.New("companion subscriber not configured")`; else `sub.Follow(pubkey, label)` then `persistFollows()`.
- `Unfollow(pubkey [32]byte) error` — symmetric with `sub.Unfollow(pubkey)`.

**`persistFollows()`** — the save half of the follow file. `if a.followPath == "" || a.sub == nil { return nil }` (silent skip). Under `followMu`: snapshot `a.sub.Following()`, build `[]followEntry` (`PubKey: hex.EncodeToString(pub[:])`, `Label`), `json.MarshalIndent(entries, "", "  ")`, write to `a.followPath + ".tmp"` mode `0o600`, `os.Rename(tmp, a.followPath)` (on rename failure `os.Remove(tmp)` and return wrapped error). **Atomic write via tmpfile+rename** — Slice 10 must keep this so a crash mid-write cannot corrupt the follow list.

**Note the ordering subtlety Slice 10 should preserve:** `Follow`/`Unfollow` mutate the in-memory worker *first*, then persist the *resulting* snapshot from `sub.Following()` — so the file always reflects post-mutation worker state, and a persist error still leaves the in-memory follow effective (the HTTP handler surfaces the error but the follow is live until restart).

---

### 6. HTTP routes & DTOs (`internal/httpapi/companion.go` + server registration)

**Route table** (registered in server mux):
```
GET  /companion           → handleCompanionStatus
POST /companion/refresh   → handleCompanionRefresh
POST /companion/follow    → handleCompanionFollow
POST /companion/unfollow  → handleCompanionUnfollow
```
Server holds `companion CompanionController` (field), populated from `Options.Companion` in `NewWithOptions`.

**`CompanionController` interface** (local to httpapi, zero import of `internal/companion`):
`PublisherStatus() CompanionPublisherStatus`, `RefreshNow() error`, `SubscriberStatus() []CompanionFollowStatus`, `Follow([32]byte, string) error`, `Unfollow([32]byte) error`.

**DTOs (exact json tags):**
```go
type CompanionPublisherStatus struct {
    LastRefresh    time.Time `json:"last_refresh"`
    LastInfoHash   string    `json:"last_infohash"`
    LastError      string    `json:"last_error,omitempty"`
    PublishedCount int       `json:"published_count"`
    PubKeyHex      string    `json:"pubkey_hex,omitempty"`   // empty ⇒ publisher not started
}
type CompanionFollowStatus struct {
    PubKeyHex        string    `json:"pubkey_hex"`
    Label            string    `json:"label,omitempty"`
    LastSyncAt       time.Time `json:"last_sync_at,omitempty"`
    LastError        string    `json:"last_error,omitempty"`
    TorrentsImported int       `json:"torrents_imported"`
    ContentImported  int       `json:"content_imported"`
    GeneratedAt      int64     `json:"generated_at,omitempty"`
    PointerInfoHash  string    `json:"pointer_infohash,omitempty"`
}
type CompanionStatusResponse struct {
    Publisher  CompanionPublisherStatus `json:"publisher"`
    Subscriber []CompanionFollowStatus  `json:"subscriber"`
}
type followRequestBody struct {   // request body for follow & unfollow
    PubKey string `json:"pubkey"`
    Label  string `json:"label,omitempty"`
}
```

**Handler control flow / status codes:**
- Every handler first checks `if s.companion == nil` → `http.Error(w, "companion controller not configured", http.StatusServiceUnavailable)` (503). (In practice never nil in daemon builds — the adapter is always constructed — but the guard stays.)
- **`GET /companion`** → `CompanionStatusResponse{Publisher: PublisherStatus(), Subscriber: SubscriberStatus()}`, `Content-Type: application/json`, `json.NewEncoder(w).Encode`.
- **`POST /companion/refresh`** → `RefreshNow()`; on error `http.Error(w, err.Error(), http.StatusTooManyRequests)` (**429** — carries `ErrTooSoon` text verbatim); on success `{"ok": true}`.
- **`POST /companion/follow`** → decode body; JSON error → `"bad json: "+err.Error()` **400**. `parseFollowPubKey(body.PubKey)` error → `err.Error()` **400**. `Follow(pub, body.Label)` error → `"follow: "+err.Error()` **500**. Success → log `s.log.Info("httpapi.companion_follow", "pubkey", ..., "label", ...)` + `{"ok": true}`.
- **`POST /companion/unfollow`** → same shape (reads `followRequestBody`, ignores `label`), error prefix `"unfollow: "` **500**, logs `"httpapi.companion_unfollow"`, success `{"ok": true}`.

**`parseFollowPubKey(s string) ([32]byte, error)`** — fail-closed pubkey validation, distinct messages:
- `len(s) != 64` → `errHTTP("pubkey must be 64 hex characters")`.
- `hex.DecodeString` error → `errHTTP("pubkey is not valid hex: " + err.Error())`.
- else `copy(out[:], raw)`. (`httpErr string` implements `error`; `errHTTP` constructs one.)

Note: `parseFollowPubKey` requires exactly 64 hex chars but does **not** re-check `len(raw)==32` after decode (64 hex ⇒ 32 bytes always, so redundant); `LoadFollowFile` *does* check `len(raw)!=32` explicitly. Slice 10 should keep the 64-char length pre-check in the HTTP path.

---

### 7. Fail-closed fetch seam (engine `FetchCompanionTorrent`, `internal/engine/engine.go`)

The subscriber's `CompanionFetcher` port is the engine method the integration relies on for the "1 file / ≤32 MiB / safe name" guarantee. Exact behavior Slice 10 must reproduce (whether it stays in engine or moves):

- `const maxCompanionBytes int64 = 32 << 20` (**32 MiB**).
- `unsafeCompanionName(name string) bool` returns true when `name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\")` (forward slash AND backslash both rejected, so a foreign-OS separator can't smuggle a path).
- Flow: `AddInfoHash` → wait `h.T.GotInfo()` (or `ctx.Done()` → `ctx.Err()`). Then:
  1. `files := h.T.Files(); if len(files) != 1` → `"engine: companion torrent has %d files, want exactly 1"`.
  2. **Size check BEFORE requesting any piece**: `if target.Length() > maxCompanionBytes` → `"engine: companion torrent declares %d bytes, exceeds cap %d"`. (Comment: length is attacker-controlled via the untrusted info dict behind the BEP-46 pointer; the only other bound is the wall-clock `FetchTimeout`, so a hostile fast seeder could fill `DataDir` without this pre-piece gate.)
  3. `info := h.T.Info(); if info == nil` → `"engine: companion torrent has no info after GotInfo"` (paranoid).
  4. `if unsafeCompanionName(info.Name)` → `"engine: companion torrent has unsafe name %q"`.
  5. `target.Download(); h.T.DownloadAll()`; poll `target.BytesCompleted() >= target.Length()` every `250ms`, respecting `ctx.Done()`.
  6. Return `filepath.Join(e.cfg.DataDir, info.Name)`.
- ctx cancellation aborts the wait but does **not** remove the torrent (retries reuse the handle).

**Invariant:** the three bounds (exactly 1 file, ≤32 MiB checked before the first piece request, plain-filename-only) are the subscriber's entire defence against a hostile pointer target. Slice 10 must keep all three and keep the size check ahead of any piece download.

---

### 8. Subscriber sync path — and the three §6 defects the integration exposes

`Subscriber.Sync(ctx, pubkey) SyncResult` runs: `GetInfohashPointer(pubkey, []byte(SaltContentIndex))` → `FetchCompanionTorrent(ih)` → `decodeFile(path)` (gzip-JSON → `CompanionIndex`) → `res.GeneratedAt = idx.GeneratedAt` → `ingest(idx)`. `SyncResult{Publisher(hex), PointerInfoHash [20]byte, TorrentsImported, ContentImported, GeneratedAt int64, Err error}`.

**Constant:** `SaltContentIndex = "_sn_content_index"` (declared in `companion/publisher.go`, used by both legs). Target = `SHA1(pubkey || SaltContentIndex)` — but that hashing lives inside the Slice-9 dhtindex primitive; companion passes the raw salt bytes.

The legacy `ingest(idx CompanionIndex)` builds `indexer.TorrentDoc{InfoHash(lowercased, must be len 40), Name, SizeBytes, FilePaths, FileCount, AddedAt}` and `indexer.ContentDoc{InfoHash, FileIndex, FilePath, FileSize, Mime, Extractor, Text, ChunkIndex}`. **Three defects Slice 10 must NOT reproduce, all located at this integration boundary:**

1. **No `Publisher == followed` verification.** `Sync` never compares `idx.Publisher` (the `CompanionIndex` field) against the `pubkey` argument before ingesting. Slice 10 must verify `CompanionIndex.Publisher == followed pubkey` after decode, before `ingest`, and fail the sync otherwise.
2. **No `SignedBy` stamp.** `TorrentDoc`/`ContentDoc` are built with **no `SignedBy` field set** — imported records are indistinguishable from local ones. Slice 10 must stamp `SignedBy = publisher pubkey` on every imported doc (the `CorpusImport` port must accept/apply the stamp).
3. **No `GeneratedAt` dedup.** `SubscriberWorker.runOnce` calls `w.sub.Sync(ctx, pub)` unconditionally every tick; the "don't double-count unchanged snapshot" behavior is only an aspirational comment — `Sync` re-ingests the full snapshot every interval (default **1h**). Slice 10 must compare `idx.GeneratedAt` against the last-imported `GeneratedAt` (available via `LastSync(pub).GeneratedAt`) and skip re-import when unchanged, while still recording the run.

**Worker loop facts the integration depends on** (`SubscriberWorker`): `Follow(pub,label)` writes `follows[pub]=label` and non-blocking sends on `trigger` (immediate sync of a newly-followed publisher). `Unfollow(pub)` deletes from both `follows` and `lastSync`. `run()` builds a ctx cancelled by `stopCh`, runs `runOnce` immediately, then on `time.NewTicker(interval)` or `trigger`. `runOnce` snapshots pubkeys under lock, syncs each, and **re-checks `follows[pub]` membership under the lock before writing `lastSync[pub]`** so an `Unfollow` racing a slow `Sync` doesn't resurrect a discarded result. Default `SubscriberOptions`: `FetchTimeout 5m`, `PointerTimeout 30s`, `Interval 1h`.

**Publisher facts the status DTO depends on** (`Publisher`): `lastRefresh` = time of last **successful** publish (advances only on success — used for the <2h BEP-44 keepalive question); `lastAttempt` = last attempt of any outcome (RefreshNow throttles on this). `RefreshNow()` returns `ErrTooSoon = errors.New("companion: refresh throttled (too soon since last refresh)")` when `time.Since(lastAttempt) < MinInterval`, else non-blocking sends on `trigger`. `PublisherOptions` defaults: `Interval 1h`, `MinInterval 1m`, `PutTimeout 30s`; regtest: `10s / 100ms / 5s`. **Invariant (empty index = failure)** is enforced in the publisher's `refreshOnce` (not in the four integration files — flagged for the publisher/build reader); the integration only observes it via `LastError`/`lastRefresh` not advancing. Slice 10 must keep: `lastRefresh`/`lastPublished` advance only on success, and an empty published index is a failure.

---

### 9. Interface/port summary Slice 10 must reproduce (consume, don't re-derive)

| Port (companion-local iface) | Method | Slice-10 provider | Notes |
|---|---|---|---|
| `PointerPutter` | `PutInfohashPointer(ctx, salt, [20]byte)` | existing `dhtindex.AnacrolixPutter` (Slice 9) | salt = `"_sn_content_index"` |
| `PointerGetter` | `GetInfohashPointer(ctx, pubkey, salt) ([20]byte, error)` | existing `dhtindex.AnacrolixGetter` (Slice 9) | also `GetInfohashPointerInfo` exists per Slice 9 |
| `TorrentSeeder` | `AddTorrentMetaInfo(mi) (any, error)` | engine | returns `any` to avoid import |
| `CompanionFetcher` | `FetchCompanionTorrent(ctx, [20]byte) (string, error)` | engine | 1 file / ≤32 MiB / safe name |
| `Ingester` (CorpusImport) | `IndexTorrent(TorrentDoc)`, `IndexContent(ContentDoc)` | `*indexer.Index` | **must stamp SignedBy (new)** |
| CorpusExport | `*indexer.Index` walked in publisher build | `*indexer.Index` | gzip-JSON path only |
| `httpapi.CompanionController` | status/refresh/follow/unfollow | `daemon.companionAdapter` | owns follow-file persistence |

**Companion imports only narrow ports** — `internal/companion` imports `internal/indexer` (for `TorrentDoc`/`ContentDoc`/`*Index` types) and `anacrolix/torrent` (bencode/metainfo) but has **no import of `internal/dhtindex` or `internal/engine`**; those cross only through the local interfaces above. Preserve that isolation: the new companion package takes `PointerPutter`/`PointerGetter`/`TorrentSeeder`/`CompanionFetcher`/`Ingester` as injected interfaces, and the daemon supplies the concrete `dhtindex`/`engine`/`indexer` objects.

---

I now have the complete CLI-boundary picture. Here is the section.

## cli-boundary

### Summary judgment

**None of the three files I was assigned (`cmd_aggregate.go`, `cmd_aggregate_build.go`, `pow.go`) are Slice-10 work.** They are the `swartznet aggregate` subcommand tree and its hashcash miner — the **Aggregate / PPMI / signed-B-tree / RIBLT** path, which the scope memo explicitly defers to **Slice 12**. The `pow.go` header confirms this unambiguously (quoted below).

**The Slice-10 companion-index publish/subscribe has essentially NO dedicated CLI surface in the legacy.** It is started implicitly by the daemon (which is the `add` command), configured entirely through the config struct + an on-disk follow file, and its follow/status/refresh operations are exposed only over the HTTP API (consumed by the GUI). The `swartznet` CLI has no `companion`, no `follow`, no `unfollow` command, and `swartznet status` does **not** render companion-index state (only the Slice-12 Aggregate block). This is the boundary the rebuild must be aware of, and a discoverability gap the rebuild may choose to close.

### Legacy top-level command index (from `main.go` `printUsage`)

Dispatch switch and the exact `--help` one-liners:

| Command | Help line (verbatim) | Slice |
|---|---|---|
| `add <magnet\|path.torrent>` | "Add a torrent and start downloading (Ctrl-C to stop)." | Core; **is the daemon** — companion pub/sub start here |
| `search <query...>` | "Search the local index, swarm peers, and/or DHT." | Core |
| `status` | "Show the running daemon's index/swarm/publisher state." | Core (+ Slice-12 Aggregate block) |
| `confirm <infohash>` | "Mark an infohash as known-good…" | Core |
| `flag <infohash>` | "Mark an infohash as spam…" | Core |
| `create <path> -o <out.torrent>` | "Create a new .torrent file from local content." | Core |
| `index <infohash> <on\|off>` | "Toggle per-torrent indexing…" | Core |
| `files <infohash> [<idx> <prio>]` | "List files in a torrent, or set file priority…" | Core |
| `trust <list\|add\|remove>` | "Manage the local publisher trust list." | Core |
| `aggregate <inspect\|find\|build>` | "Inspect/query/build Aggregate (v0.5) index files." | **Slice 12** |
| `crawl-probe --addr <host:port>` | "One-shot BEP-51 sample_infohashes probe (ops tool)." | **Slice 12** |
| `version` / `help` | print version / usage | Core |

Note: `confirm` is a live dispatch `case` in `main.go` but is **absent** from the `printUsage` index — a pre-existing `--help` discoverability defect in the legacy (do not reproduce).

### Slice 10 CLI (companion index — what a user actually runs)

| Action | Legacy mechanism | Exact surface |
|---|---|---|
| **Start publishing a companion index** | Runs automatically inside `daemon.New` when `cfg.CompanionDir != ""` (default set). There is **no** `companion publish` command and **no** `add` flag to enable/disable it. | `swartznet add <target>` prints `Companion publisher started, dir=%s` (`cfg.CompanionDir`) when `d.CompPub != nil`. |
| **Start subscribing (following)** | Runs automatically inside the daemon when a subscriber worker is constructed. | `swartznet add <target>` prints `Companion subscriber started` when `d.CompSub != nil`. |
| **Follow / unfollow a publisher pubkey** | **No CLI command.** Two paths only: (a) edit the follow JSON file on disk; (b) POST to the HTTP API (GUI does this). | Follow file `~/.local/share/swartznet/companion-follows.json`; HTTP `POST /companion/follow`, `POST /companion/unfollow`. |
| **Force an immediate re-sync** | **No CLI command.** | HTTP `POST /companion/refresh`. |
| **Show companion status** | **Not in the `status` CLI.** Only over HTTP. | HTTP `GET /companion` → `CompanionStatusResponse{publisher, subscriber[]}`. |
| **Accelerate timings for tests** | `add` flag, gated behind `SWARTZNET_UNSAFE=1`. | `swartznet add --regtest <target>` — help string: "regtest mode: accelerated publisher/companion timings (TESTING ONLY — never run against mainnet)". Refused with exit 2 and message `swartznet: --regtest is a testing-only flag (set SWARTZNET_UNSAFE=1 to enable)` unless `SWARTZNET_UNSAFE=1`. |

**Follow-file format (the only user-editable Slice-10 config):** a single JSON array of objects `{"pubkey":"<64-char hex>","label":"<name>"}`. Default path `~/.local/share/swartznet/companion-follows.json` (`config.defaultCompanionFollowFile()`, XDG-aware via `swartznetShareRoot()`). "created on demand by the GUI; if it does not exist on startup the subscriber starts with an empty follow list."

**Companion config fields (config struct only — NOT exposed as `add` flags):**
- `CompanionDir` — on-disk dir for the gzipped JSON content index + wrapping `.torrent`. Default `~/.local/share/swartznet/companion`. **Empty string disables the publisher entirely** (node still does local search + Layer-D).
- `CompanionFollowFile` — the follow list path above.

**CLI gap to flag for the rebuild:** in the legacy there is **no** `--companion-dir`, `--follow`, `--no-companion`, or `--companion-follow-file` flag on `add`, and no `swartznet companion`/`swartznet follow` command. A user cannot follow a publisher, see companion status, or disable the companion publisher from the CLI at all — everything runs off config defaults + the GUI/HTTP API. If Slice 10 wants CLI parity it must add this surface new (and keep `--help` in sync per the discoverability rule).

### Deferred to Slice 12 (Aggregate / PPMI / B-tree / PoW) — the three assigned files

All of `cmd_aggregate.go`, `cmd_aggregate_build.go`, and `pow.go` are Slice 12. Document them here only to nail the boundary; **do not** implement them in Slice 10.

`swartznet aggregate <subcommand>` (dispatch in `cmdAggregate`; usage banner: "swartznet aggregate — Aggregate (PPMI + B-tree) ops tooling"):

| Subcommand | Flags (name / default / help) | Purpose |
|---|---|---|
| `build` | `--in` (`"-"` = stdin), `--out` (`""`, **required**), `--key` (`""` = node identity), `--seq` (`1`), `--piece-size` (`companion.MinPieceSize`), `--pow-bits` (`0`; "0 = no mining, 20 = production default") | Reads JSONL, signs each record with ed25519, optionally mines hashcash PoW, packs a signed B-tree via `companion.BuildBTree`. Offline; never touches DHT/daemon/network. |
| `inspect` | `<index-file>` (positional), `--piece-size` (`companion.MinPieceSize`) | Prints B-tree trailer: file size, piece size, pages, records, publisher pk, sequence, created ts, `min PoW bits`, tree fingerprint. |
| `find` | `<index-file> <prefix>` (positional), `--piece-size` (`companion.MinPieceSize`), `--verify` (`false`, "also run VerifyFingerprint (scans every leaf; slower)") | Prefix keyword query over B-tree leaves; prints `infohash  keyword  t=`. |

`aggregate build` JSONL input schema (`jsonRecord`): `{"kw":string, "ih":"40-char lowercase hex SHA-1", "t":int64 optional/default 0}`. Validation: `ih` must be exactly 40 chars ("line %d: ih %d chars, want 40 (hex sha-1)"), decode to exactly 20 bytes; `kw` non-empty ("line %d: empty kw"). Scanner buffer bumped to 1 MiB (`1<<20`).

`aggregate build` error messages / bounds (verbatim): `aggregate build: --out is required`; `aggregate build: --pow-bits above 40 refused (cost prohibitive)`; `aggregate build: load key: %v`; `aggregate build: read records: %v`; `aggregate build: no records in input`. Exit codes: `exitUsage` = 2, plus `exitRuntime`.

**`crawl-probe`** (`cmd_crawl_probe.go`): `--addr <host:port>` (required), `--target <20-byte hex>` (default random), `--timeout-ms`, `--json`. One-shot BEP-51 `sample_infohashes` query, "Channel-B crawler development" — PPMI crawler bootstrapping = Slice 12.

### PoW belongs to Slice 12, confirmed from `pow.go` header

The header comment states verbatim: *"Hashcash proof-of-work for **Aggregate records**. SPEC.md §1.5: each **Record** carries a `pow` nonce such that `SHA256(RecordSigMessage(r))` has at least D leading zero bits… Readers enforce the threshold from **Trailer.MinPoWBits**."* PoW is defined entirely over the B-tree `Record` type and its `Trailer` — the Aggregate path — never over the gzip-JSON `CompanionIndex` document. `MineRecordPoW`/`SignAndMineRecord` and `--pow-bits` therefore have **no** place in the Slice-10 simple companion index. Bounds worth carrying to Slice 12 only: `bits == 0` → no-op; `bits > 40` refused ("companion: MineRecordPoW refuses bits=%d (cost prohibitive)"); default production difficulty is 20.

### Shared invariant that DOES cross into Slice 10 (from `cmd_aggregate_build.go` key loading)

`loadPrivKey` / `loadIdentityNoCreate` encode the **fail-closed identity rule** the Slice-10 publisher must also honor:
- Default (`--key ""`) resolves to `identity.LoadOrCreate(config.Default().IdentityPath)` — "must be identical to the daemon's … so a CLI build is signed by the same key everything else publishes under." Auto-generation is appropriate **only** at this default XDG path.
- An **explicit** path is **load-only**: a missing file is an error (`identity file %q: %w`), never a trigger to mint a fresh key — "Signing every record with a freshly minted key … would silently sever publisher reputation, so fail closed on a missing file instead."

This is the same "identity is persistent and load-bearing / never regenerate implicitly" invariant the Slice-10 companion publisher signs its `CompanionIndex.Publisher` under.

### Ambiguities / boundary calls I had to draw

1. **No Slice-10 CLI exists** — I confirmed by (a) the `main.go` dispatch (no `companion`/`follow` case), (b) the CLI only ever hits `GET /status` and `GET /aggregate` (grep of `cmd/swartznet/*.go` for `/companion` returns nothing), and (c) follow/status/refresh living solely at `internal/httpapi/companion.go` routes `GET /companion`, `POST /companion/{refresh,follow,unfollow}`. So the "companion/follow CLI commands" the task asked me to document **do not exist in the legacy** — they are HTTP-API-only, GUI-driven. Flagging this as the single biggest boundary finding.
2. The word "companion" is overloaded in the legacy: `cmd_aggregate.go`'s own banner calls the B-tree payload an "Aggregate **companion** torrent," and it imports `internal/companion`. That is the Slice-12 B-tree path sharing the `companion` package namespace — **not** the Slice-10 gzip-JSON `CompanionIndex`. The `aggregate` CLI is Slice 12 despite the shared package name.
3. `--regtest` on `add` is the only `add`-level flag that touches companion behavior, and only its *timings*; it is testbed-only (gated by `SWARTZNET_UNSAFE=1`). It is not a companion enable/disable switch.

Files read (all on `legacy-snapshot`): `cmd/swartznet/cmd_aggregate.go`, `cmd/swartznet/cmd_aggregate_build.go`, `internal/companion/pow.go`, plus corroborating reads of `cmd/swartznet/main.go`, `cmd/swartznet/cmd_add.go`, `cmd/swartznet/cmd_status.go`, `cmd/swartznet/cmd_crawl_probe.go`, `internal/config/config.go`, `internal/httpapi/server.go`, `internal/httpapi/companion.go`, `internal/companion/subscriber.go`, `internal/daemon/adapters.go`.

---

I now have the complete picture. Here is the exhaustive extraction.

## subscriber

Scope note up front: this section documents the **simple gzip-JSON `CompanionIndex` subscriber path only**. In the legacy `internal/companion` package, `subscriber.go`, `types.go`, `serialize.go`, and `torrent.go` are the gzip-JSON path; the SNAGG B-tree (`btree.go`, `build_btree.go`, `read_btree.go`, `build.go`, `pack_leaves`/`walk_to_leaves`/`decode_leaf_*`/`decode_interior_*`), the PPMI/Aggregate path, and hashcash PoW (`pow.go`) are a **separate subsystem deferred to Slice 12** and are *not* touched by `subscriber.go`. The subscriber's decode is `serialize.Decode` (gunzip → JSON), NOT the B-tree `decodeFile`/`walkToLeaves` path. Clean boundary: `subscriber.go` imports only `internal/indexer` (plus stdlib) and reaches the DHT/engine only through the two local narrow interfaces below.

### Collaborators / ports (all narrow interfaces, no hard deps)

`subscriber.go` deliberately keeps **no import of `internal/dhtindex` or `internal/engine`**. It talks to three ports:

1. **`PointerGetter`** — the BEP-46 read primitive (Slice-9 `dhtindex.AnacrolixGetter.GetInfohashPointer` satisfies it):
   ```go
   type PointerGetter interface {
       GetInfohashPointer(ctx context.Context, pubkey [32]byte, salt []byte) ([20]byte, error)
   }
   ```
   Note it uses **only `GetInfohashPointer`** — NOT `GetInfohashPointerInfo` (the ts-returning variant). The subscriber never sees the pointer's sequence/timestamp; it discards it. Consequence: legacy dedup cannot short-circuit at the pointer and must always fetch+decode first (see dedup defect below).

2. **`CompanionFetcher`** — the fail-closed download port (satisfied by `engine.Engine.FetchCompanionTorrent`; the bounds live in the **engine**, not the companion package):
   ```go
   type CompanionFetcher interface {
       FetchCompanionTorrent(ctx context.Context, infohash [20]byte) (path string, err error)
   }
   ```
   Contract: add infohash → wait metadata → download the single file → return absolute on-disk path; respect ctx cancellation (`ctx.Err()`).

3. **`Ingester`** — the local-index write port (`*indexer.Index` satisfies it; interface exists so tests inject an in-memory recorder). This is the **CorpusImport port**:
   ```go
   type Ingester interface {
       IndexTorrent(doc indexer.TorrentDoc) error
       IndexContent(doc indexer.ContentDoc) error
   }
   ```

`NewSubscriber(getter, fetcher, ingester, opts, log)` rejects nil getter/fetcher/ingester with `errors.New("companion: nil pointer getter")`, `"companion: nil fetcher"`, `"companion: nil ingester"`. Nil `log` → `slog.Default()`.

### Options and defaults (`DefaultSubscriberOptions`)

| Field | Default | Bound applied in `NewSubscriber` when ≤ 0 |
|---|---|---|
| `FetchTimeout` | `5 * time.Minute` | reset to 5 min |
| `PointerTimeout` | `30 * time.Second` | reset to 30 s |
| `Interval` | `1 * time.Hour` | reset to 1 hour |

`FetchTimeout` bounds one companion download; `PointerTimeout` bounds one BEP-44 get traversal; `Interval` is the worker re-sync period.

### Well-known salt

`SaltContentIndex = "_sn_content_index"` — defined in `publisher.go` (not subscriber.go). The subscriber passes `[]byte(SaltContentIndex)` to `GetInfohashPointer`. Per `dhtindex/dht.go`, the getter computes `target = SHA1(pubkey || salt)`. Comment on the constant: "Stable constant; never change without bumping FormatVersion." Slice 10 must consume the **already-existing** Slice-9 primitives (`GetInfohashPointer`/`GetInfohashPointerInfo`) at this exact salt.

### `Sync(ctx, pubkey [32]byte) SyncResult` — the security-critical pipeline

`pubHex := hexEncode(pubkey[:])` (64-char hex). `res := SyncResult{Publisher: pubHex}` is populated even on failure (caller never nil-checks).

**Step 1 — resolve pointer** (own timeout ctx):
```go
getCtx, cancel := context.WithTimeout(ctx, s.opts.PointerTimeout)
ih, err := s.getter.GetInfohashPointer(getCtx, pubkey, []byte(SaltContentIndex))
cancel()
if err != nil { res.Err = fmt.Errorf("get pointer: %w", err); return res }
res.PointerInfoHash = ih
```

**Step 2 — fetch torrent fail-closed** (own timeout ctx):
```go
fetchCtx, cancel := context.WithTimeout(ctx, s.opts.FetchTimeout)
path, err := s.fetcher.FetchCompanionTorrent(fetchCtx, ih)
cancel()
if err != nil { res.Err = fmt.Errorf("fetch companion torrent: %w", err); return res }
```

**Step 3+4 — decode + ingest:**
```go
idx, err := s.decodeFile(path)   // os.Open(path) → serialize.Decode(f)
if err != nil { res.Err = fmt.Errorf("decode %s: %w", path, err); return res }
res.GeneratedAt = idx.GeneratedAt
tCount, cCount, err := s.ingest(idx)
res.TorrentsImported = tCount; res.ContentImported = cCount
if err != nil { res.Err = fmt.Errorf("ingest: %w", err); return res }
s.log.Info("companion.subscriber.synced", "publisher", pubHex,
    "infohash", fmt.Sprintf("%x", ih), "torrents_imported", tCount, "content_imported", cCount)
```

`IngestReader(r io.Reader)` is a test/in-process shortcut: `Decode(r)` (error `"decode: %w"`) → `ingest`, returns `(CompanionIndex, tCount, cCount, err)`.

### FAIL-CLOSED fetch bounds — quoted exactly (live in `internal/engine/engine.go`, satisfied by `FetchCompanionTorrent`)

These are the bounds Slice 10 MUST preserve. They are enforced **before any piece is requested**:

- **Size cap constant:** `const maxCompanionBytes int64 = 32 << 20 // 32 MiB`. Rationale in-code: the length in the info dict is "fully attacker-controlled — the BEP-46 pointer resolves to an untrusted infohash", so oversize metadata is "rejected BEFORE the first piece is ever requested."
- **Exactly one file:** after `<-h.T.GotInfo()`,
  ```go
  files := h.T.Files()
  if len(files) != 1 {
      return "", fmt.Errorf("engine: companion torrent has %d files, want exactly 1", len(files))
  }
  ```
- **Size check (before download):**
  ```go
  if target.Length() > maxCompanionBytes {
      return "", fmt.Errorf("engine: companion torrent declares %d bytes, exceeds cap %d", target.Length(), maxCompanionBytes)
  }
  ```
- **Safe filename check** — `unsafeCompanionName(name string) bool` returns true (rejects) when:
  ```go
  return name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`)
  ```
  i.e. empty, `.`, `..`, or containing either `/` or `\` (backslash included so a Windows-authored manifest can't smuggle a separator). On violation:
  ```go
  if unsafeCompanionName(info.Name) {
      return "", fmt.Errorf("engine: companion torrent has unsafe name %q", info.Name)
  }
  ```
- **`info == nil` guard** (paranoid, after GotInfo): `errors.New("engine: companion torrent has no info after GotInfo")`.
- **Download + wait:** `target.Download(); h.T.DownloadAll()`, then poll `target.BytesCompleted() >= target.Length()` every **250 ms** (`time.NewTicker(250 * time.Millisecond)`); ctx cancel → `ctx.Err()`.
- **Returned path:** `filepath.Join(e.cfg.DataDir, info.Name)` — hand-built, which is exactly why the name is validated first. ctx cancel does NOT remove the torrent (retry reuses the handle).

There is a **second independent cap in `serialize.Decode`** (defence in depth on the decompressed stream): `const maxDecompressed = 1 << 30` (**1 GiB**). Read via `io.ReadAll(io.LimitReader(gz, maxDecompressed+1))`; over-cap → `errors.New("companion: decompressed payload exceeds 1 GiB safety cap")`.

### Decode + FormatVersion / format refuse (`serialize.Decode`)

`Decode`:
1. `gzip.NewReader(r)` → error `"companion: open gzip: %w"`.
2. LimitRead to 1 GiB+1; over → the 1 GiB cap error above; read error → `"companion: read gzip: %w"`.
3. `json.Unmarshal` → `"companion: parse json: %w"`.
4. **Format refuse:** `if out.Format != "swartznet-content-index"` → `fmt.Errorf("companion: bad format %q, want 'swartznet-content-index'", out.Format)`.
5. **Version refuse:** `if out.Version != FormatVersion` → `fmt.Errorf("companion: unsupported version %d, this build understands %d", out.Version, FormatVersion)` where `const FormatVersion = 1`.
6. `nil` `Torrents` normalized to `[]TorrentRecord{}`.

Related constants (`types.go`): `FormatMagic = '{"version":1,"format":"swartznet-content-index"'` (a fast-fail leading-byte check — note: **`Decode` does NOT actually use `FormatMagic`**; it validates via the unmarshaled `Format`/`Version` fields. The magic is a documented byte prefix, unused in the decode path — flag as latent/dead for the rebuild). `FormatFileName = "swartznet-content-index-v1.json.gz"`; `CompanionFileName(hex)` → `"swartznet-content-index-<first-12-hex>-v1.json.gz"` (publisher-side; the subscriber locates the payload by returned path, so the renamed file stays decodable).

### CompanionIndex schema (JSON tags — exact)

```go
type CompanionIndex struct {
    Version     int             `json:"version"`
    Format      string          `json:"format"`
    Publisher   string          `json:"publisher,omitempty"`   // 64-char hex ed25519, "" = anonymous
    GeneratedAt int64           `json:"generated_at"`
    Torrents    []TorrentRecord `json:"torrents"`
}
type TorrentRecord struct {
    InfoHash string       `json:"infohash"`          // 40-char lowercase SHA-1 hex
    Name     string       `json:"name"`
    Size     int64        `json:"size,omitempty"`
    AddedAt  int64        `json:"added_at,omitempty"`
    Files    []FileRecord `json:"files,omitempty"`
}
type FileRecord struct {
    Index     int            `json:"index"`
    Path      string         `json:"path"`
    Size      int64          `json:"size,omitempty"`
    Mime      string         `json:"mime,omitempty"`
    Extractor string         `json:"extractor,omitempty"`
    Chunks    []ContentChunk `json:"chunks,omitempty"`
}
type ContentChunk struct {
    Text   string `json:"text"`
    Offset int64  `json:"offset,omitempty"`
}
```

### `ingest(idx) (torrents, contents int, err error)` — import to Bleve

Loop `idx.Torrents`:
- `ih := strings.ToLower(tr.InfoHash)`; if `len(ih) != 40` → skip with `s.log.Debug("companion.subscriber.skip_torrent", "reason", "bad infohash", "infohash", ih)`, `continue`.
- Build `paths` from `tr.Files` where `f.Path != ""`.
- Construct and write:
  ```go
  td := indexer.TorrentDoc{InfoHash: ih, Name: tr.Name, SizeBytes: tr.Size, FilePaths: paths, FileCount: len(paths)}
  if tr.AddedAt > 0 { td.AddedAt = time.Unix(tr.AddedAt, 0).UTC() }
  if err := s.ingester.IndexTorrent(td); err != nil {
      return torrents, contents, fmt.Errorf("index torrent %s: %w", ih, err)  // first hard error aborts
  }
  torrents++
  ```
- Per file, per chunk `ci`: skip when `ch.Text == ""` (silent `continue`); else:
  ```go
  cd := indexer.ContentDoc{InfoHash: ih, FileIndex: fr.Index, FilePath: fr.Path, FileSize: fr.Size,
      Mime: fr.Mime, Extractor: fr.Extractor, Text: ch.Text, ChunkIndex: ci}
  if err := s.ingester.IndexContent(cd); err != nil {
      return torrents, contents, fmt.Errorf("index content %s/%d/%d: %w", ih, fr.Index, ci, err)
  }
  contents++
  ```
The **first hard `IndexTorrent`/`IndexContent` error aborts the loop** and is returned with partial counts; validation-skips (bad infohash, empty text) do not abort.

### SubscriberWorker — lifecycle, per-follow state, follow-set

State (mutex-guarded, concurrent-safe):
```go
follows   map[[32]byte]string      // pubkey → human label (the in-memory follow-set)
lastSync  map[[32]byte]SyncResult  // per-follow last outcome
trigger   chan struct{} (buffered 1)
stopCh    chan struct{}
interval  time.Duration            // taken from sub.opts.Interval
totalRuns int
```
`NewSubscriberWorker(sub)` errors `"companion: nil subscriber"` on nil.

- **`Follow(pubkey, label)`**: sets `follows[pubkey]=label` (re-follow updates label), then non-blocking `trigger <- struct{}{}` so the new publisher is synced within moments, not at next tick.
- **`Unfollow(pubkey)`**: deletes from BOTH `follows` and `lastSync`. In-flight syncs are NOT cancelled — they run to completion and the result is dropped (see runOnce re-check).
- **`Following()`**, **`LastSync(pubkey)`**, **`AllResults()`**, **`TotalRuns()`**: snapshot getters under mutex.
- **`Start()`**: `startOnce`-guarded, launches `run()` goroutine (idempotent).
- **`Stop()`**: `stopOnce`-guarded `close(stopCh)`, then `wg.Wait()` (idempotent).
- **`run()`**: builds a `context.WithCancel(context.Background())`; a helper goroutine does `<-stopCh; cancel()` so an in-flight `Sync` is cancelled the moment Stop is called (otherwise Stop could block up to `FetchTimeout`=5 min). Runs `runOnce(ctx)` immediately, then loops on `select` over `stopCh` (return), `tick.C` (`time.NewTicker(interval)`), and `trigger` — both fire `runOnce(ctx)`.
- **`runOnce(ctx)`**: snapshot pubkeys under lock, then per pubkey: early-return if `stopCh` closed; `res := sub.Sync(ctx, pub)`; under lock **re-check membership** — `if _, ok := follows[pub]; ok { lastSync[pub] = res }` (drops a result for a publisher unfollowed during the slow Sync). After the loop, `totalRuns++`.

### `SyncResult` fields
`Publisher string` (64-hex), `PointerInfoHash [20]byte` (zero on pointer failure), `TorrentsImported int`, `ContentImported int`, `GeneratedAt int64` (publisher-side snapshot ts; 0 unless decode succeeded), `Err error`.

### Follow-set persistence (daemon layer — `internal/daemon`, not the companion package)

The worker holds no on-disk state; the daemon owns the file. `internal/daemon/adapters.go` `companionAdapter` wraps `(pub, sub, followPath)`:
- `Follow`/`Unfollow` mutate the worker then call `persistFollows()`.
- `persistFollows()`: skip when `followPath == "" || sub == nil` (empty path → in-memory only, no error). Otherwise mutex-guarded atomic write: `json.MarshalIndent([]followEntry{…}, "", "  ")` → write `followPath+".tmp"` mode `0o600` → `os.Rename`; on marshal/write/rename error returns `"marshal follows: %w"` / `"write tmp: %w"` / `"rename: %w"` (removes tmp on rename failure).

`internal/daemon/follows.go`:
- `followEntry{ PubKey string json:"pubkey"; Label string json:"label,omitempty" }` — the file is a **single JSON array** of these.
- `const maxFollowFileBytes = 1 << 20` (**1 MiB**, ~10k follows).
- `LoadFollowFile(w, path, stderr) (int, error)`: missing file → `(0, nil)` (fresh install). Open error → warn stderr + `"daemon: open companion follow file %q: %w"`. Read via `io.LimitReader(f, maxFollowFileBytes+1)`; over cap → **fail closed wholesale** `"daemon: companion follow file %q exceeds %d bytes"` (half-loading would silently drop a suffix). JSON parse error → `"daemon: parse companion follow file %q: %w"`. Per-entry: `hex.DecodeString(PubKey)` must yield exactly 32 bytes, else warn `"companion follow entry %d: bad pubkey %q"` and skip (partial load permitted so one bad row doesn't strand the list); good entries → `w.Follow(pub, e.Label)`, count returned.
- `SubscriberStatus()` maps `lastSync` into `httpapi.CompanionFollowStatus{PubKeyHex, Label, TorrentsImported, ContentImported, GeneratedAt, LastError(=res.Err.Error()), PointerInfoHash(hex, omitted when zero), LastSyncAt(=time.Unix(res.GeneratedAt,0).UTC() only when GeneratedAt>0)}`.

### Legacy §6 DEFECTS confirmed — Slice 10 must NOT reproduce these

1. **No `Publisher == followed` verification.** `ingest()` never reads `idx.Publisher`. `Sync` imports whatever the pointer resolved to regardless of who actually authored the snapshot. A pointer/infohash swap or a snapshot authored by a different key is imported silently. `types.go` *claims* subscribers "use it to attribute imported records and to maintain reputation against the publisher" — but the code does neither. **Fix required:** after decode, reject when `CompanionIndex.Publisher != hex(followedPubkey)` (quote a hard error and return before ingest).

2. **Import does NOT stamp `SignedBy`.** `indexer.TorrentDoc` and (per `indexer.go`) documents carry a `SignedBy` field (a searchable filter), but the subscriber constructs `TorrentDoc`/`ContentDoc` with `SignedBy` left `""`. Imported records are indistinguishable from local/unsigned ones. **Fix required:** stamp `SignedBy = publisher pubkey hex` on every imported `TorrentDoc`/`ContentDoc`.

3. **No `GeneratedAt` dedup — re-ingests every hour.** `runOnce` always calls `Sync`, which always fetches+decodes+ingests; it records `res.GeneratedAt` but never compares it against the prior `lastSync[pub].GeneratedAt` to skip an unchanged snapshot. The `runOnce` comment ("we still record the run but don't double-count it as new content") is aspirational — **no such guard exists in code**. Every interval tick re-downloads and re-writes the entire corpus (idempotent in Bleve only because IndexTorrent/IndexContent upsert by ID, but wasteful and re-attributed each time). **Fix required:** before fetch (or at least before ingest) compare the incoming snapshot timestamp to the last successfully-imported one and skip on no-op — and because the legacy uses only `GetInfohashPointer` (not `…Info`), the rebuild should use the Slice-9 `GetInfohashPointerInfo` to read the pointer's ts/seq and short-circuit before the download entirely.

### Invariants to PRESERVE (from FOCUS)

- Fail-closed fetch: exactly **1 file**, **≤ `maxCompanionBytes = 32 << 20` (32 MiB)** checked before any piece, safe name (reject empty/`.`/`..`/contains `/` or `\`). Plus the decode-side 1 GiB decompressed cap.
- **Format/version refuse** (`Format == "swartznet-content-index"`, `Version == FormatVersion == 1`).
- **Empty published index = FAILURE** — this invariant lives on the **publisher** side (publisher.go), not the subscriber; the subscriber accepts a zero-torrent snapshot without error (it just imports nothing). Flag for Slice 10: the "empty is a failure" gate is a publish-side concern.
- **Narrow ports only**: subscriber depends on `PointerGetter`, `CompanionFetcher`, `Ingester` (CorpusImport) — **no concrete `dhtindex`/`engine` import**; only `internal/indexer` for the doc types. Preserve these seams.
- Consume the **existing** Slice-9 BEP-46 primitives at salt `"_sn_content_index"`.

### Ambiguities / boundaries flagged

- **"lastRefresh advances only on success"** is precisely a **publisher** invariant (`publisher.go` `lastPublished`/`LastRefresh`). The subscriber analogue `lastSync[pub]` is recorded on **every** run including failures; the only field that advances solely on decode-success is `res.GeneratedAt` (0 on any pre-decode failure), which is why `httpapi.LastSyncAt` only moves when `GeneratedAt > 0`. If Slice 10 wants a subscriber "advance only on successful import" semantic, it must add it — legacy does not have it.
- **Reputation attribution on import: ABSENT.** Neither `subscriber.go` nor `adapters.go` touches `reputation.Tracker` on a successful sync. The `types.go` comment about maintaining reputation is unimplemented. Treat reputation attribution as new Slice-10 (or later) work, contingent on defect #1 (you can only attribute once you verify `Publisher == followed`).
- **`FormatMagic` is defined but unused** by `Decode` (validation is via unmarshaled fields). Do not port the dead magic-byte check unless you actually wire it.
- **Slice-10 vs Slice-12 boundary in `internal/companion`:** everything reachable from `subscriber.go` is the simple gzip-JSON path. The B-tree/PPMI/PoW files listed in the package (`btree*.go`, `build_btree.go`, `read_btree.go`, `build.go`, `pow.go`, and their `walk_to_leaves`/`pack_*`/`decode_leaf_*`/`find_pow` tests) are **Slice 12** and share only the `CompanionIndex` types — the subscriber does not call into any of them.