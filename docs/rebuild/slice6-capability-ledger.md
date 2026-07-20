# Slice 6 Behavioral Ledger — Capability / Services-Mask Producer

Authoritative, self-contained. Where sources conflict, SPEC/ARCHITECTURE/PLAN + the frozen wire bit-table win over legacy code; every such divergence is called out inline and again in §9. Legacy line numbers are cited as reference anchors only — the rebuild copies **behavior**, never structure.

---

## 1. Services bit-assignment table (frozen, append-only)

`type ServiceBits uint64`; every constant is `1 << N`. Numbering is **frozen and append-only** — never shrink, never reuse a bit. Documented wire invariant (verbatim from legacy `services.go`): **"unknown bits must be ignored, never rejected."** A receiver honors every bit it defines and ignores bits ≥ its highest-understood bit; it never rejects a frame for unknown bits. (Resolves the self-contradictory doc-06 masking sentence per ARCHITECTURE.md:366 / SPEC §7-A3.) A peer that sends no `peer_announce` before its first query is treated as `services = 0`, **not** an error, and its query is still answered (SPEC §5.1, SPEC.md:717; doc-06 L128-138,278-283).

| Bit | `1<<N` | Constant | Value (dec/hex) | **Owner** | Source field | Meaning |
|----:|:------:|----------|----------------:|-----------|--------------|---------|
| 0 | 1<<0 | `BitShareLocal` | 1 / `0x001` | **Sharing** | `ShareLocal == 2` | answers queries against full local Bleve index |
| 1 | 1<<1 | `BitShareSwarm` | 2 / `0x002` | **Sharing** | `ShareLocal == 1` | answers only for torrents currently in-swarm (mutually exclusive w/ bit 0) |
| 2 | 1<<2 | `BitFileHits` | 4 / `0x004` | **Sharing** | `FileHits` | per-file matches (else torrent-name only) |
| 3 | 1<<3 | `BitContentHits` | 8 / `0x008` | **Sharing** | `ContentHits` | indexes/returns extracted-content matches |
| 4 | 1<<4 | `BitLayerDPublisher` | 16 / `0x010` | **RuntimeFacts** | `Publishing` | publishes keyword→infohash to BEP-44 DHT (Layer-D) |
| 5 | 1<<5 | `BitCompanionPublisher` | 32 / `0x020` | **RuntimeFacts** | `CompanionPub` | publishes F3 companion content-index torrents |
| 6 | 1<<6 | `BitCompanionSubscriber` | 64 / `0x040` | **RuntimeFacts** | `CompanionSub` | follows companion publishers, ingests indexes |
| 7 | 1<<7 | `BitSnippetHighlight` | 128 / `0x080` | **RuntimeFacts** | `SnippetHighlight` | returns Bleve highlight fragments |
| 8 | 1<<8 | `BitRegtest` | 256 / `0x100` | **RuntimeFacts** | `Regtest` (`cfg.Regtest`) | regtest mode — deliberately "loud" (SPEC §5.1, SPEC.md:722) |
| 9 | 1<<9 | `BitSetReconciliation` | 512 / `0x200` | **RuntimeFacts** | `Reconciliation` | speaks Aggregate sync protocol (msg_types 4..8) |
| 10–63 | — | *(reserved)* | — | reserved | — | always allocate the next free bit; never reuse |

Helpers to preserve: `Has(bit) = s&bit == bit`; `With(bit) = s | bit`; `Without(bit) = s &^ bit`.

Derived constants (arithmetic verified):
- `DefaultServices()` (legacy) = bits {0,2,3,5,6,7,9} = **749 = `0x2ED`**. Excludes bits 1, 4, 8.
- **Build-feature bits** {5,6,7,9} = 32+64+128+512 = **736 = `0x2E0`**.
- Publisher bit 4 = `0x010`; Regtest bit 8 = `0x100`.

---

## 2. `capability.Announced(Sharing, RuntimeFacts) uint64` — corrected semantics, bit by bit

### 2.1 The corrected mapping (honors downgrades)

`Announced` is a **pure, total** function. It derives every bit straight from its two inputs — **there is NO static `DefaultServices()` floor OR'd over the operator bits.** It performs no clamping (that is an HTTP-boundary concern); it treats already-valid inputs and is total over out-of-range `ShareLocal` (any value ∉{1,2} → neither bit 0 nor bit 1, matching legacy's `switch`).

Reference implementation (module `github.com/swartznet/swartznet`, package `ltepwire`):

```go
func Announced(s Sharing, f RuntimeFacts) uint64 {
    var m ServiceBits
    // bits 0/1 — operator ShareLocal tri-state, mutually exclusive
    switch s.ShareLocal {
    case 2:
        m |= BitShareLocal          // bit 0
    case 1:
        m |= BitShareSwarm          // bit 1
    }                               // 0 / any other → neither (downgrade honored)
    if s.FileHits          { m |= BitFileHits }            // bit 2
    if s.ContentHits       { m |= BitContentHits }         // bit 3
    if f.Publishing        { m |= BitLayerDPublisher }     // bit 4  (daemon-owned)
    if f.CompanionPub      { m |= BitCompanionPublisher }  // bit 5
    if f.CompanionSub      { m |= BitCompanionSubscriber } // bit 6
    if f.SnippetHighlight  { m |= BitSnippetHighlight }    // bit 7
    if f.Regtest           { m |= BitRegtest }             // bit 8
    if f.Reconciliation    { m |= BitSetReconciliation }   // bit 9
    return uint64(m)
}
```

Bit-by-bit contract:

| bit | set iff |
|----:|---------|
| 0 `ShareLocal` | `Sharing.ShareLocal == 2` |
| 1 `ShareSwarm` | `Sharing.ShareLocal == 1` |
| 2 `FileHits` | `Sharing.FileHits` |
| 3 `ContentHits` | `Sharing.ContentHits` |
| 4 `LayerDPublisher` | `RuntimeFacts.Publishing` |
| 5 `CompanionPublisher` | `RuntimeFacts.CompanionPub` |
| 6 `CompanionSubscriber` | `RuntimeFacts.CompanionSub` |
| 7 `SnippetHighlight` | `RuntimeFacts.SnippetHighlight` |
| 8 `Regtest` | `RuntimeFacts.Regtest` |
| 9 `SetReconciliation` | `RuntimeFacts.Reconciliation` |

The four build-feature RuntimeFacts (5,6,7,9) are set **true** by the daemon in Slice 6 (see §3.3), so a default node still yields `0x2ED`. Because they live in `RuntimeFacts`, not `Sharing`, an operator "share nothing" downgrade clears only bits 0–3 and leaves 5,6,7,9 intact — that is intentional (they describe what the build does, not operator policy).

> **Implementation-shape note (discrepancy).** `rebuild-seams` proposed a leaner `RuntimeFacts{Publisher, Regtest bool}` plus a `const buildFeatures = 0x2E0` OR'd unconditionally. That yields byte-identical output only while the four build bits are always-on. ARCHITECTURE.md:117 (the authority) defines the full 6-boolean `RuntimeFacts`. **Use the 6-boolean struct** — it keeps `Announced` a pure total function over explicit inputs and lets each build bit be re-gated per subsystem slice later without touching the producer. The `0x2E0` constant may still appear internally as a documented shorthand for "these four default true," but the bits must be real struct fields.

### 2.2 Explicit contrast with the legacy expression (the fix, unambiguous)

Legacy producer, inline inside `onRemoteHandshake` (`internal/swarmsearch/protocol.go`, ~L547-556):

```go
pubOn := p.caps.Publisher > 0
services := ServicesFromCapabilities(p.caps) |
    (DefaultServices() &^ BitLayerDPublisher)   // == 0x2ED, a CONSTANT floor
if pubOn {
    services = services.With(BitLayerDPublisher) // bit 4
}
```

`DefaultServices() &^ BitLayerDPublisher == 0x2ED` because bit 4 was never in `DefaultServices()` — so the `&^` removes nothing and the term is a **constant `0x2ED` unconditionally OR'd in**. Since `0x2ED` already contains bits 0,2,3,5,6,7,9, the reachable operator bits {0,1,2,3} are a subset of `{0x2ED} ∪ {bit4}`. Net effect: **only bit 4 tracks runtime**; every operator downgrade of bits 0/2/3 is a silent no-op on the wire, and `ShareLocal==1` even sets **both** bit 0 (from the floor) and bit 1 (from `ServicesFromCapabilities`) — contradictory.

The rebuild's `Announced` deletes the `| (DefaultServices() &^ …)` term entirely. Operator bits come solely from `Sharing`; there is no floor over them. Same-value divergence proof:

| input | legacy `services` | corrected `Announced` |
|---|---|---|
| share-nothing `ShareLocal=0,FileHits=f,ContentHits=f`, `Publishing=f` | `0x2ED` (bug) | **`0x2E0`** |
| swarm-only `ShareLocal=1,FileHits=t,ContentHits=f`, `Publishing=f` | `0x2EF` (bit0+bit1 both) | **`0x2E6`** (bit1 only) |

---

## 3. The two input types

### 3.1 `Sharing` — operator-controlled prefs

Per ARCHITECTURE.md:117:

```go
type Sharing struct {
    ShareLocal  uint8 // 0=don't answer; 1=swarm-only(bit1); 2=full local index(bit0)
    FileHits    bool
    ContentHits bool
}
```

- **Ranges / clamp:** `ShareLocal ∈ {0,1,2}`; `FileHits`,`ContentHits` are booleans. **`Announced` never clamps** (deterministic-envelope rule). Clamping lives only at the HTTP decode boundary (`ShareLocal` clamped to `[0,2]`) and at `config.Validate()`. `Announced` is total: out-of-range `ShareLocal` → neither bit 0 nor 1.
- Legacy predecessor was the 4-field int `Capabilities{ShareLocal,FileHits,ContentHits,Publisher int}` with `DefaultCapabilities() = {2,1,1,0}`. The rebuild **removes `Publisher` from the operator type entirely** — that is the structural fix for defect #2.
- **JSON type discrepancy (flagged):** legacy `file_hits`/`content_hits` were ints (0/1). SPEC/ARCHITECTURE model them as `bool`. Rebuild uses `bool` (JSON `true`/`false`); `share_local` stays int. See §9-Q4.

### 3.2 `RuntimeFacts` — daemon-owned truths (recomputed live, never cached)

Per ARCHITECTURE.md:117,171,388:

```go
type RuntimeFacts struct {
    Publishing       bool // bit 4 — actively publishing to Layer-D DHT
    Reconciliation   bool // bit 9 — build feature
    Regtest          bool // bit 8 — cfg.Regtest
    CompanionPub     bool // bit 5 — build feature
    CompanionSub     bool // bit 6 — build feature
    SnippetHighlight bool // bit 7 — build feature
}
```

The operator setter cannot reach any of these — that is what makes "Save sharing clobbers Publisher" **unrepresentable by type**. The daemon recomputes `RuntimeFacts` from **live engine getters** on every announce/readout; it is never a cached snapshot.

### 3.3 Live engine state each fact derives from

| Field | Derived from (Slice-6) |
|---|---|
| `Publishing` | `publishingActive()` = `!cfg.NoIndex && !cfg.DisableDHTPublish` — the legacy config gate (see §3.4). |
| `Reconciliation` | `true` (build feature; Aggregate-sync subsystem is a build capability). |
| `CompanionPub` | `true` (build feature). |
| `CompanionSub` | `true` (build feature). |
| `SnippetHighlight` | `true` (build feature). |
| `Regtest` | `cfg.Regtest` (read at engine.go:207; default `false`). |

The four build-feature bits are hardcoded `true` in Slice 6. This is a **deliberate, temporary over-advertisement** (the companion/snippet/reconciliation subsystems land in later slices) — record it in `DECISIONS.md`, to be re-gated to real subsystem availability as each slice lands. Hardcoding true now keeps the default golden = `0x2ED`.

### 3.4 The `--no-index → Publisher=0` cascade

Legacy gate (`internal/engine/engine.go:1094-1098`):

```go
if !e.cfg.DisableDHTPublish && !e.cfg.NoIndex {
    currentCaps := e.swarm.Capabilities()
    currentCaps.Publisher = 1
    e.swarm.SetCapabilities(currentCaps)   // daemon writes it, not the operator
}
```

So `Publishing = !cfg.NoIndex && !cfg.DisableDHTPublish`. Flag origin: CLI `cmd_add.go` `--no-index` → `daemon.Options.NoIndex` → `daemon.go:96` mirrors `opts.Cfg.NoIndex = true` → engine gate fires. Passive nodes must never gossip themselves as indexers (legacy comment). In the rebuild this becomes `RuntimeFacts.Publishing`, **never operator-settable**. Slice 6 proves the cascade at daemon **start** (runtime toggling is out of scope).

> **Resolved divergence (§9-Q1):** `rebuild-seams` observed Layer-D does not exist on disk yet and proposed stubbing `Publishing=false`. `spec-authority` (the authority) derives `Publishing = !NoIndex && !DisableDHTPublish`. **Decision: implement the config gate now** so the cascade is observably testable at daemon start and matches legacy semantics (legacy set the bit at wiring time from config alone, before any entry was pushed). If `cfg.DisableDHTPublish` does not yet exist in the rebuild config, use `!cfg.NoIndex` alone and add `DisableDHTPublish` when Layer-D lands. Consequence: a **default daemon's `/aggregate` shows `0x2FD`** (Publishing on), a `--no-index` daemon shows `0x2ED` — this is the observable proof of the defect-#1 fix. Note in `DECISIONS.md` that this advertises the Publisher bit before Layer-D actually pushes entries (faithful to legacy, re-tightened when Layer-D lands).

---

## 4. `contracts/ltepwire` — frozen constants + golden vector

New stdlib-only frozen-contract package `contracts/ltepwire` (mirrors `contracts/token`'s shape: `services.go` with frozen constants + pure funcs; `services_test.go` with a table-driven `*Golden` var asserting exact 16-hex output, `t.Parallel()`). `contracts/ltepwire` does **not** exist on disk yet — Slice 6 creates it.

**Constants to freeze (verbatim bit numbers):**

```go
const (
    BitShareLocal         ServiceBits = 1 << 0 // 0x001
    BitShareSwarm         ServiceBits = 1 << 1 // 0x002
    BitFileHits           ServiceBits = 1 << 2 // 0x004
    BitContentHits        ServiceBits = 1 << 3 // 0x008
    BitLayerDPublisher    ServiceBits = 1 << 4 // 0x010
    BitCompanionPublisher ServiceBits = 1 << 5 // 0x020
    BitCompanionSubscriber ServiceBits = 1 << 6 // 0x040
    BitSnippetHighlight   ServiceBits = 1 << 7 // 0x080
    BitRegtest            ServiceBits = 1 << 8 // 0x100
    BitSetReconciliation  ServiceBits = 1 << 9 // 0x200
    // bits 10..63 reserved — allocate next free bit, never reuse
)
```

**Hex renderer** (16 lowercase hex, big-endian MSB-first, no `0x`, zero-padded to 8 bytes):

```go
func FormatHex(v uint64) string { return fmt.Sprintf("%016x", v) }
```

`fmt.Sprintf("%016x", v)` is **byte-identical** to the legacy `formatServicesHex` loop (`b[7-i] = byte(v>>(i*8)); hex.EncodeToString(b)`) — verified for `0x2ed/0x2fd/0x2e0/0x2e6/0x3ed`.

**Proposed golden vector table** (each row: full explicit inputs → exact `uint64` → 16-hex). For every "on" row, the four build RuntimeFacts (`Reconciliation`,`CompanionPub`,`CompanionSub`,`SnippetHighlight`) are `true`:

| # | `Sharing` | `RuntimeFacts` (beyond the 4 build bits) | bits set | uint64 | 16-hex |
|---|---|---|---|---|---|
| 1 (default, pub off) | `{2,true,true}` | build=t, `Publishing=f`,`Regtest=f` | 0,2,3,5,6,7,9 | `0x2ED` (749) | `00000000000002ed` |
| 2 (default, publishing) | `{2,true,true}` | build=t, `Publishing=t` | 0,2,3,**4**,5,6,7,9 | `0x2FD` (765) | `00000000000002fd` |
| 3 (**downgrade** share-nothing) | `{0,false,false}` | build=t, `Publishing=f` | 5,6,7,9 | `0x2E0` (736) | `00000000000002e0` |
| 4 (swarm-only) | `{1,true,false}` | build=t, `Publishing=f` | **1**,2,5,6,7,9 | `0x2E6` (742) | `00000000000002e6` |
| 5 (regtest) | `{2,true,true}` | build=t, `Regtest=t` | 0,2,3,5,6,7,**8**,9 | `0x3ED` (1005) | `00000000000003ed` |

- Row 1 is the **primary DoD anchor** (equals the historic `0x2ED`; also anchors the `/aggregate` fix baseline for a non-publishing node).
- Row 3 is the **defect-#3 regression guard**: legacy would still emit `0x2ED`; the rebuild MUST emit `0x2E0`.
- Row 4 proves `ShareLocal==1` sets bit 1 **only** (not bit 0+1 as legacy did).
- Row 5 realizes the "loud" regtest announce legacy never actually emitted (behavior improvement — see §9-Q5).

> **Vector discrepancy resolved:** `legacy-producer` computed swarm-only as `{ShareLocal=1, FileHits=0}` → `0x2E2`; three other sources use `{1,true,false}` → `0x2E6`. **Standardize on `0x2E6`** (`FileHits=true`), a cleaner discriminator agreed by legacy-services, legacy-http, rebuild-seams.

---

## 5. HTTP surface

### 5.1 `GET /capabilities` — response shape

- Requires the capabilities collaborator wired, else **503** (`"swarmsearch not configured"` in legacy; rebuild: `Capabilities == nil ⇒ 503`).
- **200**, `Content-Type: application/json`. Response DTO (JSON keys preserved from legacy `CapabilitiesBody`, plus live `services`):

```go
type CapabilitiesResponse struct {
    ShareLocal  int    `json:"share_local"`   // 0..2
    FileHits    bool   `json:"file_hits"`     // was int 0/1 in legacy — now bool
    ContentHits bool   `json:"content_hits"`  // was int 0/1 in legacy — now bool
    Publisher   bool   `json:"publisher"`     // READ-ONLY daemon fact (RuntimeFacts.Publishing)
    Services    string `json:"services"`      // 16-hex live mask (Announced)
}
```

Default readout for a non-`--no-index` node: `{"share_local":2,"file_hits":true,"content_hits":true,"publisher":true,"services":"00000000000002fd"}`. `publisher` is surfaced **read-only** so the UI can display the operator-vs-daemon split (resolved §9-Q2).

### 5.2 `PATCH /capabilities` — request shape (mutates ONLY Sharing)

- Method: **`PATCH`** primary, **`POST` alias** to the same handler (parity with `/config/rate-limit` and `/config/queue`, and any legacy client that POSTed).
- **CSRF-guarded** (state-mutating): `Host` must be loopback (`127.0.0.0/8`, `::1`, literal `localhost`) else **403** `"forbidden: non-loopback Host"`; if `Origin` present it must be loopback else **403** `"forbidden: cross-origin request"`; else if `Referer` present it must be loopback else **403** `"forbidden: cross-origin Referer"`; fails closed on unparseable Host/Origin. CLI/curl (loopback Host, no Origin/Referer) pass.
- Status codes: **403** (CSRF) / **503** (no collaborator) / **400** (`"bad json: "+err`) / **200** (echoes fresh GET body).
- Request DTO — **pointer/merge (preserve-unset PATCH) semantics; NO `publisher` field** (defect-#2 structural fix). Merge mirrors `handleSetRateLimit` (torrents.go:238-254): read current `Sharing()`, overlay only non-nil pointers, clamp `ShareLocal` to `[0,2]`, call `SetSharing`, echo fresh GET body.

```go
type CapabilitiesPatch struct {
    ShareLocal  *int  `json:"share_local"`  // clamped [0,2] if present
    FileHits    *bool `json:"file_hits"`
    ContentHits *bool `json:"content_hits"`
    // no publisher — daemon-owned, unrepresentable here
}
```

Because `publisher`/`Publishing` cannot appear in the setter path and is never written by the merge, a "Save sharing" that omits it can never zero it.

### 5.3 `GET /aggregate` — `services` live rendering (defect #1 fix)

- `AggregateStatusResponse.Services string json:"services"` (rebuild dto.go:279; legacy used `services,omitempty`). Value = **16 lowercase hex chars, big-endian, no `0x`**.
- **Not** the static `DefaultServices()` constant — it renders the **live** mask from the same `Announced(liveSharing, liveRuntimeFacts)` the wire will consume. Concretely: default publishing node → `00000000000002fd`; a sharing downgrade immediately changes it (e.g. share-nothing → `00000000000002e0`); `--no-index` node → `00000000000002ed`.
- GET is CSRF-exempt; `/aggregate` always returns **200** (fields omitted when subsystems nil). The rebuild placeholder to replace is the static `"0000000000000000"` at `internal/daemon/search_adapter.go:123`.

---

## 6. Rebuild seams (honoring the httpapi zero-import law)

`internal/httpapi` imports **no** subsystem package and **no** `contracts/*` (verified: only import is `internal/httpapi/web`). Keep it that way: the collaborator traffics in **plain `uint64` + httpapi-local DTOs**, and httpapi renders hex itself with `fmt.Sprintf("%016x", v)`. Only `internal/daemon` (and `contracts/ltepwire` itself) may import `contracts/ltepwire`.

### 6.1 `contracts/ltepwire` (new)
- `contracts/ltepwire/services.go` — `type ServiceBits uint64`, the 10 frozen constants, `Sharing`, `RuntimeFacts`, `Announced(Sharing, RuntimeFacts) uint64`, `FormatHex(uint64) string`, `Has/With/Without`.
- `contracts/ltepwire/services_test.go` — golden vector table (§4).

### 6.2 `internal/config/config.go` (modify)
Add three fields (no JSON tags — struct built via `Default()`+flags):
```go
ShareLocal       int  // default 2
ShareFileHits    bool // default true
ShareContentHits bool // default true
```
`Default()` sets `{2,true,true}`. `Validate()` range-checks `ShareLocal ∈ 0..2`. No Publisher field (runtime fact). Update `config_test.go`.

### 6.3 `internal/engine` (modify + new file)
State owner = the engine (mirrors the `UploadLimitBytesPerSec`/`MaxActiveDownloads` runtime-knob precedent, adapters.go:96-106), giving Slice-7 wire code and Slice-6 HTTP one source of truth.
- Field `sharing ltepwire.Sharing` + mutex, seeded from `cfg` in `engine.New`.
- New `internal/engine/capability.go`:
  - `Engine.Sharing() ltepwire.Sharing`
  - `Engine.SetSharing(ltepwire.Sharing)`
  - `Engine.RuntimeFacts() ltepwire.RuntimeFacts` — builds all 6 booleans: `Publishing: e.publishingActive()`, `Reconciliation/CompanionPub/CompanionSub/SnippetHighlight: true`, `Regtest: e.cfg.Regtest`.
  - `Engine.publishingActive() bool` — `!e.cfg.NoIndex && !e.cfg.DisableDHTPublish` (config gate; see §3.4).
  - `Engine.ServicesMask() uint64 { return ltepwire.Announced(e.Sharing(), e.RuntimeFacts()) }` — the single call site both consumers use.
- Tests: `capability_test.go`.

### 6.4 `internal/httpapi` (modify + new file)
- `server.go` `Options` additions:
  ```go
  ServicesReporter func() uint64            // live mask; nil ⇒ all-zero placeholder
  Capabilities     CapabilitiesController   // nil ⇒ /capabilities 503
  ```
- New `internal/httpapi/capabilities.go`:
  ```go
  type CapabilitiesController interface {
      Sharing() SharingPrefs                 // current operator prefs
      SetSharing(SharingPrefs) SharingPrefs  // httpapi does the merge; returns stored
      Publisher() bool                       // read-only daemon fact (never written)
  }
  func (s *Server) capabilitiesRoutes(mux *http.ServeMux) {
      mux.HandleFunc("GET /capabilities",   s.handleGetCapabilities)
      mux.HandleFunc("PATCH /capabilities", s.handleSetCapabilities)
      mux.HandleFunc("POST /capabilities",  s.handleSetCapabilities) // alias
  }
  ```
  Register `s.capabilitiesRoutes(mux)` in `routes()` (server.go ~L181, beside `confirmFlagRoutes`).
- `dto.go`: add `SharingPrefs{ShareLocal int; FileHits, ContentHits bool}`, `CapabilitiesResponse`, `CapabilitiesPatch` (§5); `AggregateStatusResponse.Services` now filled live (semantics change only).

### 6.5 `internal/daemon` (modify + new file)
- New `capabilities_adapter.go` (or extend `adapters.go`) — `controllerAdapter` satisfies `httpapi.CapabilitiesController`, translating `ltepwire.Sharing ↔ httpapi.SharingPrefs`:
  ```go
  func (a *controllerAdapter) Sharing() httpapi.SharingPrefs {
      s := a.eng.Sharing(); return httpapi.SharingPrefs{s.ShareLocal, s.FileHits, s.ContentHits}
  }
  func (a *controllerAdapter) SetSharing(p httpapi.SharingPrefs) httpapi.SharingPrefs {
      a.eng.SetSharing(ltepwire.Sharing{ShareLocal: p.ShareLocal, FileHits: p.FileHits, ContentHits: p.ContentHits})
      return a.Sharing()
  }
  func (a *controllerAdapter) Publisher() bool { return a.eng.RuntimeFacts().Publishing }
  ```
- `daemon.go` (~L186) `apiOpts` wiring:
  ```go
  apiOpts.ServicesReporter = a.eng.ServicesMask
  apiOpts.Capabilities     = adapter
  ```
- `search_adapter.go` `aggregate()` (L123): replace `"0000000000000000"` with `ltepwire.FormatHex(a.eng.ServicesMask())`.

### 6.6 Cross-cutting (global rules)
Rebuild `dist/swartznet` **and** `dist/swartznet-gui-dev-linux-amd64`. If the CLI `status` surface renders `services`, keep `--help` in sync (CLI-discoverability rule). Update `docs/` (05-integration-design + the sn_search BEP draft in docs/06) + `CHANGELOG.md`. Run `go test ./... -count=1 -short` then `-race`, log the Publishing-gate + build-bit + Regtest decisions in `DECISIONS.md`, commit + push.

---

## 7. The three §6 defects — legacy behavior, fix, closure

| # | Defect | Legacy behavior | Rebuild fix | Closure |
|---|--------|-----------------|-------------|---------|
| **1** | `/aggregate` static `0x2ED` | `aggregate.go:~131`: `services := swarmsearch.DefaultServices(); resp.ServicesAdvertised = formatServicesHex(...)` — a hardcoded constant, ignoring live caps and the Publisher bit. | `/aggregate` `services` fed from `ServicesReporter` → `Engine.ServicesMask()` → `ltepwire.Announced(liveSharing, liveRuntimeFacts)`. Same producer as the (future) wire. Daemon adapter renders `ltepwire.FormatHex(...)`. | **Fully in Slice 6.** |
| **2** | "Save sharing" clobbers Publisher | `handleSetCapabilities` decoded a whole `CapabilitiesBody` incl. `publisher`, clamped, and did `s.swarm.SetCapabilities(...)` — a full-struct replace (`p.caps = c`, no merge). Web `app.js:894` / gui `settings.go` posted only `{share_local,file_hits,content_hits}` → `req.Publisher` JSON-defaulted to `0` → `clamp(0,0,1)=0` overwrote the daemon-set `Publisher=1`. | Type split: `Publishing ∈ RuntimeFacts`; the setter reaches only `Sharing`; `CapabilitiesPatch` has **no `publisher` field**; merge is preserve-unset (pointer overlay). The daemon-owned bit is **unrepresentable** in the setter path → clobber impossible. | **Fully in Slice 6.** |
| **3** | Producer OR's `DefaultServices()` → downgrades never change bits | `protocol.go:~547-556`: `services := ServicesFromCapabilities(p.caps) \| (DefaultServices() &^ BitLayerDPublisher)` — constant `0x2ED` floor; only bit 4 tracked runtime; `ShareLocal==1` set both bit 0 and bit 1. | `Announced` derives the mask purely from `Sharing`+`RuntimeFacts` with **no `DefaultServices()` OR**. Downgrades clear bits 0–3; `ShareLocal==1` sets bit 1 only. Golden #3 (`0x2E0`) and #4 (`0x2E6`) pin it. | **Leaf producer + HTTP readout: fully in Slice 6.** The **wire half** — the outbound `peer_announce` actually consuming `Announced()` so a downgrade reaches a peer — is **Slice 7** (PLAN.md:99-107). |

---

## 8. DoD → concrete test/assertion mapping

| DoD bullet | Concrete proof |
|---|---|
| Exactly one pure producer `Announced` exists & is pinned | `contracts/ltepwire/services_test.go` golden table (§4 rows 1–5) asserts `FormatHex(Announced(in)) == want` for all five vectors; `t.Parallel()`. |
| Producer **honors downgrades** (defect #3) | Golden row 3: `Announced({0,false,false}, buildOn) == 0x2E0` (`00000000000002e0`) — a comment asserts legacy would have emitted `0x2ED`. Row 4: `ShareLocal==1` → bit 1 set, bit 0 clear (`0x2E6`). |
| `ShareLocal` tri-state maps 2→bit0, 1→bit1, else neither | Rows 1/4/3 cover 2, 1, 0 respectively; add a row for an out-of-range value (e.g. `ShareLocal=9`) asserting neither bit 0 nor 1. |
| `/aggregate` renders the **live** mask (defect #1) | httpapi/daemon integration test: build a daemon with default sharing → GET `/aggregate`, assert `services == "00000000000002fd"` (publishing) ; then `SetSharing(share-nothing)` → GET `/aggregate`, assert `services == "00000000000002e0"`. Both differ from the legacy static `0x2ED`, and `len(services)==16`. |
| `PATCH /capabilities` mutates only `Sharing`; Publisher untouched (defect #2) | Handler test: seed `Publishing=true` (default node), `PATCH /capabilities {"share_local":0}` (no `publisher` key), assert 200; then `Engine.RuntimeFacts().Publishing` still `true` and GET `/capabilities` shows `"publisher":true`, `"share_local":0`, and `services` bit 4 still set. |
| PATCH is preserve-unset / merge | `PATCH {"file_hits":false}` leaves `share_local` and `content_hits` unchanged (compare to prior GET). |
| PATCH clamps `ShareLocal` | `PATCH {"share_local":9}` → GET shows `share_local:2`; `PATCH {"share_local":-3}` → `0`. |
| `--no-index → Publisher=0` cascade at start | Engine unit test: `cfg.NoIndex=true` → `Engine.RuntimeFacts().Publishing == false` and `ServicesMask()` bit 4 clear (`0x2ED`); `cfg.NoIndex=false` (+ `!DisableDHTPublish`) → `Publishing==true`, bit 4 set (`0x2FD`). Daemon integration: `--no-index` node's `/aggregate services == "00000000000002ed"`. |
| Bit constants frozen / append-only / unknown bits ignored | `services_test.go`: assert each constant's numeric value (`BitLayerDPublisher==0x10`, etc.); a test ORs bits 50/51/55 onto a known mask and asserts `Announced`/round-trip logic ignores them (never rejects). |
| Hex rendering 16 lowercase big-endian no `0x` | Golden rows assert exact 16-char strings; a `FormatHex` unit test checks `len==16`, all-lowercase, `0x3ED → "00000000000003ed"`. |
| httpapi zero-import law preserved | A guard test / grep in CI: `internal/httpapi` imports no `contracts/*` and no subsystem pkg; collaborator returns `uint64`. |
| Regtest realizes the "loud" bit (improvement) | Golden row 5: `Regtest=true → 0x3ED`; engine test: `cfg.Regtest=true → ServicesMask()` bit 8 set. |
| Both binaries rebuilt, suites green | `go test ./... -count=1 -short` then `-race` pass; `dist/swartznet` + `dist/swartznet-gui-dev-linux-amd64` rebuilt same turn. |

---

## 9. Resolved open questions

**Q1 — Publisher (`Publishing`) derivation: config-gate vs stub-false.**
Sources split: `spec-authority` = `!NoIndex && !DisableDHTPublish`; `rebuild-seams` = `false` stub (Layer-D absent on disk). **Decision (SPEC authority):** implement the **config gate** `publishingActive() = !cfg.NoIndex && !cfg.DisableDHTPublish` now — matching legacy semantics (bit set at wiring from config, before entries pushed) and making the `--no-index` cascade observable at daemon start per the DoD. If `cfg.DisableDHTPublish` doesn't yet exist, gate on `!cfg.NoIndex` alone and add the field with Layer-D. Record in `DECISIONS.md` that this advertises Publisher before Layer-D pushes, to be tightened when Layer-D lands. Default daemon `/aggregate` = `0x2FD`; `--no-index` = `0x2ED`.

**Q2 — Does GET `/capabilities` surface `publisher`?**
**Decision:** yes, as a **read-only** field (`"publisher": bool`) plus the live `services` string, so the operator-vs-daemon split is visible; the PATCH body still omits it entirely. (Reconciles spec-authority's "maybe /aggregate only" toward showing it read-only in both places.)

**Q3 — HTTP method: legacy POST vs ARCHITECTURE "PATCH".**
**Decision:** register **`PATCH` primary + `POST` alias** to the same merge handler (parity with `/config/rate-limit` and `/config/queue`, and any legacy client that POSTed). Semantics are preserve-unset regardless of verb.

**Q4 — `file_hits`/`content_hits` JSON type: legacy int(0/1) vs bool.**
**Decision (SPEC authority):** **`bool`** (`Sharing.FileHits/ContentHits bool`; JSON `true`/`false`). `share_local` stays `int` (clamped 0..2). This changes the wire JSON type of `file_hits`/`content_hits` from legacy `0/1` — acceptable because the API surface is being rebuilt and there is no external consumer contract on the integer form; note it in `CHANGELOG.md`.

**Q5 — Regtest (bit 8) advertised by `Announced`?**
Legacy `DefaultServices()` omitted it and the producer never set it, so no legacy node ever advertised `BitRegtest` despite the "loud" intent. **Decision (behavior improvement):** honor `RuntimeFacts.Regtest → bit 8` (default `cfg.Regtest=false`, so default mask stays `0x2ED` — safe strict improvement that finally realizes the loud announce). Golden row 5 (`0x3ED`) pins it; note in `DECISIONS.md`.

**Q6 — Build-feature bits (5,6,7,9) always-on, or gated on subsystem?**
Legacy hardcoded them via `DefaultServices()` regardless of subsystem presence. **Decision:** model them as real `RuntimeFacts` booleans (per ARCHITECTURE) but set them **`true`** in Slice 6 (companion/snippet/reconciliation subsystems arrive later). Documented as deliberate temporary over-advertisement, re-gated to actual availability per slice. Keeps default golden = `0x2ED`.

**Q7 — `RuntimeFacts` shape: 6 booleans vs 2 booleans + `0x2E0` const.**
**Decision (SPEC authority):** the **6-boolean struct** (`Publishing, Reconciliation, Regtest, CompanionPub, CompanionSub, SnippetHighlight`). `Announced` reads all six; the `0x2E0` constant may appear only as an internal comment/shorthand, never as an unconditional OR that would resurrect defect #3.

**Q8 — Sharing-state owner: engine vs daemon.**
**Decision:** the **engine** (mirrors the existing rate-limit / `MaxActiveDownloads` runtime-knob precedent; gives Slice-7 wire code and Slice-6 HTTP one source of truth via `Engine.ServicesMask()`).

**Q9 — `Sharing.ShareLocal` type: `uint8` (ARCHITECTURE) vs `int` (seams/JSON).**
**Decision:** `Sharing.ShareLocal` is **`uint8`** in `contracts/ltepwire` (per ARCHITECTURE.md:117); the config field and the `share_local` JSON stay **`int`** (clamped 0..2 at the HTTP boundary and in `config.Validate()`). The daemon adapter converts. `Announced` treats any value ∉{1,2} as "neither bit," so the type width is not load-bearing for correctness.

**Q10 — Swarm-only golden vector value.**
Resolved in §4: standardize on `{ShareLocal=1, FileHits=true, ContentHits=false}` → **`0x2E6`** (three sources agree; `legacy-producer`'s `0x2E2` used `FileHits=false` and is dropped).

**Q11 — Where do the golden/assertions live?**
**Decision:** one unit golden table in `contracts/ltepwire/services_test.go` (pins `Announced`), plus one httpapi/daemon integration assertion that `/aggregate services == Announced(live)` for both a publishing and a `--no-index`/downgraded daemon (legacy `aggregate_test.go` only checked `len==16`, never the value).

---

### Discrepancies flagged against authority
1. `Publishing` derivation (Q1): `rebuild-seams` stub-false overruled by SPEC config-gate.
2. `RuntimeFacts` shape (Q7): `rebuild-seams` 2-field+const overruled by ARCHITECTURE 6-field.
3. `file_hits`/`content_hits` JSON type (Q4): legacy int overruled by SPEC bool.
4. Regtest advertising (Q5): legacy never set bit 8; rebuild deliberately does (documented improvement).
5. Swarm-only vector (Q10): `legacy-producer` `0x2E2` dropped in favor of `0x2E6`.
6. Legacy producer line citation ranges vary across readers (544-549 / 547-556 / 548-552 / 549-556 / 551-552) — treat as `internal/swarmsearch/protocol.go` ~L547-556 inside `onRemoteHandshake`; exact lines are reference-only.