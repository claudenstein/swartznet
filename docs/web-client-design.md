# SwartzNet Web Client — Build Spec

Status: design. Target: a **fully functional** localhost web client at feature parity with
the Fyne GUI (`internal/gui`) and the CLI. Vanilla JS, no build step, no framework, no CDN.
Served by the daemon out of `internal/httpapi/web` via `go:embed`. This document is the
implementation contract for `internal/httpapi/web/{index.html,static/*}`.

Authoritative API reference: the per-group contracts in the task brief, backed by
`internal/httpapi/{server,dto,torrents,search,capabilities,confirmflag,companion,csrf,bodylimit}.go`.
GUI feature parity reference: `internal/gui/{app,downloads,search,status,companion,settings}.go`.

---

## 1. Overview + the three-frontends-one-daemon principle

SwartzNet has **three frontends over one daemon**: the CLI, the native Fyne GUI, and this
web client. All three are **pure presentation**. The daemon (`internal/daemon`) owns every
subsystem; the web client owns **zero state** beyond ephemeral view state (current tab,
last poll snapshot, form inputs). It never computes node state, never fans out searches,
never reconciles layers — it renders what the HTTP API returns and issues actions back.

Load-bearing consequences, carried over verbatim from the GUI discipline:

- **The L/S/D search layers are NEVER merged.** `POST /search` returns three independent
  blocks (`local`, `swarm`, `dht`), each with its own hit type, its own counters, and its
  own score semantics. The web client renders **three separate card groups**, exactly like
  `gui/search.go` renders three sets of `widget.NewCard`. There is no unified result list,
  no cross-layer sort, no dedupe. This mirrors the "three search layers, strict isolation"
  architecture anchor in `CLAUDE.md`.
- **Deterministic control stays in the daemon.** The page issues one request per user
  action and one poll per tick; it does not implement retries, queue logic, or state
  machines (per the Production Architecture Rules). A failed action surfaces an error and
  leaves the daemon as the single source of truth; the next poll reconciles the view.
- **Security model = loopback bind + CSRF/DNS-rebind guard, no auth.** The page is served
  from `localhost:7654` (or `127.0.0.1:7654`), so its `Origin`/`Host`/`Referer` are loopback
  and every `POST`/`PATCH`/`DELETE` passes the `withCSRFGuard` check by construction. The
  client sends **no** auth token (there is none). It must not add an `Origin` override or be
  hosted off-loopback, or writes 403.

### Contract invariants the client must honor (from the API brief)

1. **Error bodies are plain text, not JSON.** On any non-2xx, read `response.text()`, trim
   the trailing newline, and show it. Never `JSON.parse` an error body. The only structured
   errors are the inline `swarm.error` / `dht.error` strings inside a **200** `/search`
   response (the §5.9 convention).
2. **Arrays are never `null`.** `torrents`, `files`, `local.hits`, `swarm.hits`, `dht.hits`,
   `subscriber`, `reputation.top_indexers` are always `[]` when empty.
3. **`omitempty` fields may be absent.** Treat missing as the zero value: `indexed_files`,
   `index_extracted`, `signed_by`, `trusted_publisher` on a torrent; `file_index` on a
   content hit (absent ⇒ **index 0**, a frozen quirk); the whole `dht`/`bloom`/`reputation`
   blocks on `/status` (absent `dht` ⇒ **DHT disabled**, not "0 nodes").
4. **Merge/PATCH endpoints use pointer semantics.** For `/config/rate-limit`, `/config/queue`,
   `/capabilities`: **omit** a field to leave it unchanged; **send** it to set it. Sending
   `0`/`false` is a real value (`0` = unlimited for rate/queue caps). Always trust the
   returned document (it echoes the merged/clamped result — `share_local` is clamped `[0,2]`).
5. **`503` means "subsystem not wired on this node," not "temporarily down."** For companion,
   confirm/flag, aggregate, index-stats: treat 503 as "feature unavailable" — disable/hide the
   control, do not retry-loop.
6. **Score types differ across blocks.** `local.hits[].score` and `dht.hits[].score` are
   floats; `swarm.hits[].score` is an **int**. Format each block's score with its own type.
7. **`status` is a closed 5-value enum:** `metadata|downloading|seeding|paused|queued`.
   `"complete"` is never emitted (a finished torrent reports `seeding`).

### Divergences from the GUI (deliberate, documented)

- **Add is magnet-only** over HTTP (`POST /torrent` rejects nothing but takes only `uri`;
  `.torrent`/infohash adds are in-process CLI/GUI forms). The Add dialog accepts a magnet URI
  only and says so. (The GUI's file-path branch has no HTTP equivalent.)
- **File priority gets a real UI** in the web client (the GUI has none), reaching CLI parity
  via `GET /torrents/{ih}/files` + `POST .../files/{index}/priority`.
- **About** shows what HTTP exposes: version (`/healthz`) and publisher pubkey (`/status`).
  BitTorrent port / API addr are not on the wire, so they are omitted (the GUI reads them
  from in-process accessors the web client does not have).

---

## 2. Page / tab structure

A single-page app with a top tab bar and five tabs, matching the GUI's `container.AppTabs`
order exactly (`gui/app.go`):

| Tab | Renders | Poll |
|-----|---------|------|
| **Downloads** | Torrent list with live %/status/peers/rates; Add-magnet; per-row Pause/Resume/Remove(+forget); indexing toggle; expandable file list with per-file priority. | 1 s |
| **Search** | Query box + Swarm/DHT/highlight toggles + limit; three **separate** result-card groups (L/S/D) each with its own status line; Confirm/Flag per hit. | none (on submit) |
| **Status** | Node health: torrent rollup, local index stats, swarm peers, DHT routing, Layer-D publisher + per-keyword list, aggregate/bootstrap, reputation top-N, bloom. | 2 s |
| **Companion** | Publisher status card + follow-list card; Follow / Unfollow / Re-publish-now. | 3 s |
| **Settings** | Rate limits + queue cap (merge-save); Sharing/Capabilities toggles (merge-save); read-only services mask + publisher bit; About. | none (read on open, re-read on save) |

Poll intervals are copied from the GUI constants (`downloadsPollInterval=1s`,
`statusPollInterval=2s`, `companionPollInterval=3s`). Only the **visible** tab polls; on tab
switch the client stops the old tab's timer and starts the new one, and does an immediate
fetch so the tab is never blank for a poll interval.

Tab state lives in the URL hash (`#downloads`, `#search`, …) so reload/deep-link works
without an SPA server fallback (the daemon only serves `index.html` at exact `/`).

---

## 3. Per-page endpoint map (what it calls, when, and how it maps to the UI)

Timing legend: **[load]** on tab activation, **[poll]** on the tab's timer, **[action]** on
a user gesture.

### 3.1 Downloads

| When | Method + path | Request | UI mapping |
|------|---------------|---------|------------|
| [load]/[poll] | `GET /torrents` | — | `torrents[]` → one row each. Row = name (or `infohash[:16]` if empty) · progress bar (`progress`, 0–1) · `status` badge · `bytes_completed`/`size` (humanized) · `active_peers` · `↓ download_rate` `↑ upload_rate` /s. `paused` styles the row; `queued` shows a "queued" chip; `signed_by` present ⇒ a ✓ badge, green if `trusted_publisher`. `indexing` drives the toggle state; `indexed_files`/`index_extracted` (absent⇒0) shown as `idx N/M`. |
| [action] Add | `POST /torrent` | `{"uri": "<magnet>"}` | On 200 (`{ok, infohash}`) close dialog, immediate `GET /torrents` refresh. On 4xx/5xx show `response.text()` (e.g. `add: <detail>`, `missing 'uri' field`). |
| [action] Create torrent | `POST /torrents/create` | `{"root","output","trackers"?,"comment"?,"private"?,"sign"?,"seed"?}` — `root`/`output` are paths on the DAEMON machine (it does the hashing). | On 200 (`{ok, infohash, seeded, seed_error?}`) toast + `GET /torrents` refresh; a non-empty `seed_error` means the .torrent was created but the seed leg failed (warn, don't treat as failure). On 4xx show `response.text()`. Handler extends its write deadline so a long hash isn't cut off. |
| [action] Pause | `POST /torrents/{ih}/pause` | no body | 200 ⇒ refresh. Error text inline on the row. |
| [action] Resume | `POST /torrents/{ih}/resume` | no body | same. |
| [action] Remove | `DELETE /torrents/{ih}` (add `?forget=1` when the "also forget index docs" box is checked) | no body | 200 (`{ok, forgot}`) ⇒ refresh + toast noting files kept on disk always. |
| [action] Indexing toggle | `POST /torrents/{ih}/indexing` | `{"enabled": <bool>}` | 200 ⇒ refresh (row reflects `indexing`). |
| [action] Expand files | `GET /torrents/{ih}/files` | — | `files[]` → sub-rows: `display_path` · `length` · progress · a priority `<select>` (`none|normal|high`) preset from `priority`. Files list is lazy (fetched on expand), refreshed on expand and after a priority change. |
| [action] Set priority | `POST /torrents/{ih}/files/{index}/priority` | `{"priority": "none\|normal\|high"}` | 200 ⇒ re-fetch that torrent's files. Controller-rejected value ⇒ 400 text shown on the sub-row. |

`{ih}` must be validated client-side as 40-hex before the call to avoid a wasted 400.

### 3.2 Search

| When | Method + path | Request | UI mapping |
|------|---------------|---------|------------|
| [action] Submit | `POST /search` | `{"q", "limit", "swarm":bool, "dht":bool, "highlight":true, "swarm_timeout_ms":8000?, "dht_timeout_ms":8000?}` | Render three card groups — see §4. Trim `q` client-side; enforce ≤1024 bytes (the server 400s longer). `limit` from the box, default 20 (server coerces ≤0→50, caps 500). |
| [action] Confirm (per hit) | `POST /confirm` | `{"infohash":"<40hex>"}` | 200 (`{ok, indexers_confirmed}`) ⇒ status line "confirmed X (boosted N indexer(s))". 503 ⇒ disable the button + "not available on this node". |
| [action] Flag (per hit) | `POST /flag` | `{"infohash":"<40hex>"}` | 200 (`{ok, indexers_flagged, attribution}`) ⇒ if `indexers_flagged>0` "demoted N"; else honest "no reputations changed: `attribution`" (`targeted`/`trusted-exempt`/`none`). |

Confirm/Flag route through the same shared spam path as the GUI (`gui/search.go` `confirm`/`flag`)
and CLI — no web-private logic. Only the `swarm`/`dht` blocks that were **requested** are
rendered; a present `swarm.error`/`dht.error` (still HTTP 200) renders as that block's status
line, not a thrown error.

### 3.3 Status

Fires several reads in parallel each [load]/[poll] and composes them into labeled sections
mirroring `gui/status.go` (plus what the API exposes beyond it):

| Section | Method + path | UI mapping |
|---------|---------------|------------|
| Node/version | `GET /healthz` | `version` in the header (once). |
| Torrents rollup | `GET /torrents` | Count by `status`; sum `download_rate`/`upload_rate`. (Same derivation the GUI does locally.) |
| Local index | `GET /status` → `local` + `GET /index/stats` | `local.indexed`, `local.doc_count`; stats: `doc_count`/`torrent_count`/`content_count`/`dir_bytes`/`corpus_text_bytes`/`inflation_ratio`. `/index/stats` 503 ⇒ "index disabled". |
| Swarm | `GET /status` → `swarm` | `known_peers`, `capable_peers`. |
| DHT | `GET /status` → `dht` (omitempty) | present ⇒ `good_nodes`/`nodes`; **absent ⇒ "DHT disabled"**. |
| Layer-D publisher | `GET /status` → `publisher` + `GET /publish` | `pubkey`, `total_keywords`, `total_hits`; `/publish` adds the per-keyword table (`keyword`, `hits_count`, `publish_count`, `last_published`, `last_error`). |
| Aggregate | `GET /aggregate` | `known_indexers`, `bootstrap{anchors,admitted,pending}`, `cache_size`, `reconciliation`, `services` mask. 503 ⇒ "aggregate backend off". |
| Reputation | `GET /status` → `reputation` (omitempty) | `known_indexers` + `top_indexers[]` (`pubkey`,`score`,`hits_returned/confirmed/flagged`), truncated by the server to 10. Absent ⇒ "reputation disabled". |
| Bloom | `GET /status` → `bloom` (omitempty) | `bit_size`,`hash_functions`,`population_bits`,`estimated_items`. Absent ⇒ hide. |

`/status` never 503s (renders honest degraded blocks); `/index/stats` and `/aggregate` do —
handle their 503 as a disabled section, not a page error.

### 3.4 Companion

| When | Method + path | Request | UI mapping |
|------|---------------|---------|------------|
| [load]/[poll] | `GET /companion` | — | `publisher`: `pubkey_hex` (empty/absent ⇒ "publisher not started"), `published_count`, `last_infohash?`, `last_refresh` (zero time `0001-01-01…` ⇒ "never"), `last_error?`. `subscriber[]` (never null): one row per follow — `pubkey_hex`(+`label?`), `torrents_imported`, `content_imported`, `last_sync_at?` (zero ⇒ never), `last_error?`, `generated_at?` (unix **seconds** int, not RFC3339), `pointer_infohash?`. |
| [action] Follow | `POST /companion/follow` | `{"pubkey":"<64hex>","label":"<opt>"}` | Validate 64-hex client-side; 200 ⇒ refresh. 400 text (`pubkey must be 64 hex characters` / `pubkey is not valid hex`) inline; 500 `follow: <detail>`; 503 ⇒ whole tab shows "companion disabled". |
| [action] Unfollow | `POST /companion/unfollow` | `{"pubkey":"<64hex>"}` | 200 ⇒ refresh (per-row Unfollow button on each subscriber). |
| [action] Re-publish now | `POST /companion/refresh` | no body | 200 (`{ok:true}`) ⇒ refresh + toast. **429** ⇒ show the throttle text verbatim ("too soon…") — a normal, expected outcome, not an error state. |

### 3.5 Settings

| When | Method + path | Request | UI mapping |
|------|---------------|---------|------------|
| [load] | `GET /config/rate-limit` | — | `upload_bps`/`download_bps` into inputs (0 = unlimited, labeled). |
| [load] | `GET /config/queue` | — | `max_active_downloads` into input (0 = unlimited). |
| [load] | `GET /capabilities` | — | `share_local` (tri-state select 0/1/2), `file_hits`, `content_hits` checkboxes; **read-only** `publisher` bit + `services` mask shown but not editable. |
| [action] Save rate/queue | `PATCH /config/rate-limit`, `PATCH /config/queue` | send **only changed** fields (pointer merge) | Response echoes fresh document → repopulate inputs from it (authoritative). |
| [action] Save sharing | `PATCH /capabilities` | send **only changed** `share_local`/`file_hits`/`content_hits` | Response is the fresh `CapabilitiesResponse`; repopulate from it (`share_local` may be clamped; `publisher`/`services` are live and never sent). |
| About | reuse `GET /healthz` + `GET /status` | — | version + `publisher.pubkey`; static license line `Apache-2.0 (SwartzNet) · engine anacrolix/torrent: MPL-2.0`. |

Sharing maps to GUI `settings.go`: the GUI's "Answer local-index queries" checkbox ⇔
`share_local` 0↔2; expose the full tri-state (0 off / 1 in-swarm / 2 full) since the API
supports it. Use `PATCH` (not POST) as the primary verb per the contract; POST is only an
alias.

---

## 4. Per-layer search-card model

Derived directly from the `SearchResponse` shape (`dto.go`). Three groups, each preceded by
a one-line **status header** (counts) and then N cards. Groups render in fixed order L, S, D
and are visually separated (heading + divider). A group renders only if that layer ran.

**Group L — Local (Bleve).** Always present. Header: `Local — {local.total} match(es)`.
One card per `local.hits[]` element:

- Title: `name` (omitempty) else `infohash`.
- Badge row: `doc_type` (`torrent`|`content`); `score` (**float**, 2dp); `signed_by` present
  ⇒ ✓ `signed_by[:8]`; `size_bytes` (omitempty) humanized.
- If `doc_type == "content"`: show `file_path`, `mime`, `extractor` (all omitempty);
  `file_index` **absent ⇒ 0**.
- `fragments` (omitempty, `{field: [html-ish strings]}`) — render highlight snippets when
  `highlight:true` was sent; escape then re-emphasize, never inject raw.
- Actions: **Confirm** / **Flag** (→ §3.2), keyed by `infohash`.

**Group S — Swarm (`sn_search` peer-wire).** Rendered only if `swarm:true` was requested.
Header: `Swarm — {swarm.hits.length} hit(s) · asked {asked} · responded {responded} · rejected {rejected}`.
If `swarm.error` is set (inline §5.9, still 200): header becomes `Swarm — error: {error}` and
no cards. Else one card per `swarm.hits[]`:

- Title: `name` else `infohash`.
- Badges: `score` (**int** — do not format as float), `seeders` (omitempty), `size`
  (omitempty) humanized, `sources.length` sources.
- Actions: Confirm / Flag by `infohash`.

**Group D — DHT (BEP-44 keyword index).** Rendered only if `dht:true` requested. Header:
`DHT — {dht.hits.length} hit(s) · indexers {indexers_responded}/{indexers_asked}`. `dht.error`
set ⇒ `DHT — error: {error}`, no cards. Else one card per `dht.hits[]`:

- Title: `name` else `infohash`.
- Badges: `score` (**float**, 2dp), `seeders` (omitempty), `size` (omitempty) humanized,
  `sources.length` sources, `bloom_hit` (omitempty) ⇒ a "known-good ✓" chip.
- Actions: Confirm / Flag by `infohash`.

Empty state: a group whose layer ran but returned zero hits shows its header + "(no results)".
When no cards exist in any group, the page shows a single "(no results)" line (matches GUI).

**Never** merge, sort across groups, or dedupe by infohash — the isolation is the point.

---

## 5. File layout under `internal/httpapi/web/`

The embed contract is frozen as `index.html` at the root plus a `static/` tree (`embed.go`,
`//go:embed index.html static/*`; server serves `/static/` via `http.FileServer` and
`index.html` at exact `/`). All JS is native **ES modules** (`<script type="module">`) —
browsers load these over http with **no build step**; imports are relative paths under
`/static/`. Everything is self-contained (no CDN, no external font/img).

```
internal/httpapi/web/
  embed.go                 # unchanged (frozen embed contract)
  index.html               # shell: <header> tabs, <main id="view">, module script tag
  static/
    style.css              # theme-aware (prefers-color-scheme), mobile-first, one stylesheet
    app.js                 # entry: hash router, tab lifecycle, poll-loop mgr, boot
    api.js                 # thin fetch layer: one function per endpoint (§ list below)
    util.js                # humanBytes, shortHex(16/8), fmtRate, timeAgo, isHex40/64,
                           #   escapeHtml, el() DOM helper, toast(), badge()
    pages/
      downloads.js         # renderDownloads(container): list, add dialog, row actions, file drawer
      search.js            # renderSearch(container): form + three-group renderer (§4)
      status.js            # renderStatus(container): parallel reads → sections
      companion.js         # renderCompanion(container): publisher + follow list + actions
      settings.js          # renderSettings(container): rate/queue/capabilities + About
```

Each `pages/*.js` exports `render(container)` returning a `{ start(), stop() }` handle so
`app.js` can drive the poll lifecycle (start on activate, stop on leave). `api.js` centralizes
the two hard rules: (a) on non-2xx, read `.text()`, throw an `ApiError{status, text}`; (b)
GET helpers for reads, and a JSON-body helper that sets `Content-Type: application/json` for
writes (same-origin, so the CSRF guard is satisfied automatically — no extra headers).

**`api.js` surface (the full dependency set):**

```
getHealthz()                         GET  /healthz
getStatus()                          GET  /status
getPublish()                         GET  /publish
getTorrents()                        GET  /torrents
addTorrent(uri)                      POST /torrent
createTorrent(body)                  POST /torrents/create
getFiles(ih)                         GET  /torrents/{ih}/files
setFilePriority(ih, index, prio)     POST /torrents/{ih}/files/{index}/priority
pauseTorrent(ih)                     POST /torrents/{ih}/pause
resumeTorrent(ih)                    POST /torrents/{ih}/resume
removeTorrent(ih, forget)            DELETE /torrents/{ih}[?forget=1]
setIndexing(ih, enabled)             POST /torrents/{ih}/indexing
search(body)                         POST /search
getIndexStats()                      GET  /index/stats
confirm(infohash)                    POST /confirm
flag(infohash)                       POST /flag
getAggregate()                       GET  /aggregate
getCapabilities()                    GET  /capabilities
patchCapabilities(patch)             PATCH /capabilities
getRateLimit()                       GET  /config/rate-limit
patchRateLimit(patch)                PATCH /config/rate-limit
getQueue()                           GET  /config/queue
patchQueue(patch)                    PATCH /config/queue
getCompanion()                       GET  /companion
refreshCompanion()                   POST /companion/refresh
followPublisher(pubkey, label)       POST /companion/follow
unfollowPublisher(pubkey)            POST /companion/unfollow
```

**Theming / responsiveness:** one `style.css`, mobile-first, using `prefers-color-scheme`
for dark/light (CSS custom properties for palette). Tab bar collapses to a horizontal scroll
on narrow widths; the torrent/file lists and the reputation/keyword tables live in
`overflow-x:auto` wrappers so the page body never scrolls sideways. Progress bars are plain
`<progress>`/styled `<div>` — no canvas. No JS animation library.

`index.html` replaces the current placeholder: it keeps the frozen `<link rel="stylesheet"
href="/static/style.css">`, adds `<script type="module" src="/static/app.js"></script>`, a
`<header>` nav with the five tab buttons, and `<main id="view">`.

---

## 6. Build / test plan

**Build.** Pure static assets — no Go code change beyond content of `web/`. The embed picks
them up automatically (`//go:embed index.html static/*`; note the pattern is non-recursive at
one level — `static/pages/*` is covered because `static/*` embeds the `pages` dir and its
subtree via `embed.FS` directory semantics; if a nested dir is ever missed, widen to
`static/**` — **verify with a build**). After editing assets, rebuild both binaries per the
repo rule:

```
/usr/local/go/bin/go build -o dist/swartznet ./cmd/swartznet
./scripts/build-gui.sh dev
```

**Static/unit gates (no daemon):**
1. `go build ./...` and `go vet ./...` — proves the embed compiles and the assets are found.
2. A tiny Go test in `internal/httpapi/web` asserting `Assets()` contains `index.html`,
   `static/style.css`, `static/app.js`, and each `pages/*.js` (catches an embed glob that
   silently drops a file).
3. Optional: `node --check static/*.js static/pages/*.js` in the smoke script to catch JS
   syntax errors without a browser (Node used only as a linter; not a runtime dependency).

**Live smoke test against a running daemon** (`scripts/` addition, e.g. `smoke-web.sh`):

1. Start a daemon on a temp data dir: `dist/swartznet add --http :7654 --data $TMP …`
   (the CLI `add` IS the daemon; no `serve`). Wait for `GET /healthz` → `{ok:true}`.
2. **Serving:** `curl -s localhost:7654/` returns the SPA HTML; `curl -sI
   localhost:7654/static/app.js` is `200 application/javascript`; a bogus `/static/nope.js`
   is `404`; a non-API unknown path is `404` (no SPA fallback — expected).
3. **Read endpoints** each return 200 with the documented shape: `/status`, `/torrents`,
   `/index/stats` (or 503 if `--no-index`), `/capabilities`, `/config/rate-limit`,
   `/config/queue`, `/companion`, `/aggregate` (or 503), `/publish`.
4. **CSRF guard proof (the security assumption the client relies on):**
   - Loopback write succeeds: `curl -X POST -H 'Content-Type: application/json' -d
     '{"q":"test"}' localhost:7654/search` → 200.
   - Cross-origin write is rejected: same call with `-H 'Origin: http://evil.example'` →
     `403 forbidden: cross-origin request` (plain text). This is what keeps a browser page
     off-loopback from driving the daemon.
5. **Write round-trips:** `PATCH /config/rate-limit` with `{"upload_bps":1024}` then
   `GET /config/rate-limit` shows the merge (download untouched); `PATCH /capabilities` with
   `{"share_local":9}` returns `share_local:2` (clamp proof).
6. **Error-shape proof:** `POST /torrent` with `{}` → `400 missing 'uri' field` as
   **text/plain** (the client must render `.text()`, not parse JSON).
7. **Browser pass (manual, on the same loopback origin):** open `http://localhost:7654/`,
   walk all five tabs, add a known magnet, confirm the poll updates progress, run a search
   with Swarm+DHT toggled and confirm three separate card groups render (never merged),
   exercise Confirm/Flag, toggle a Sharing bit and re-read it, Follow/Unfollow a dummy 64-hex
   pubkey (expect the plain-text validation error path too), and Re-publish-now twice to see
   the 429 throttle message rendered as informational.

**Parity checklist (must all be reachable in the web client):** add(magnet), pause, resume,
remove(+forget), indexing toggle, file priority, search L, search S, search D (separate
cards), highlight, confirm, flag, status health, index stats, aggregate, publisher keywords,
reputation, companion follow, unfollow, refresh, rate-limit set, queue set, sharing/capability
set, About. Each maps to exactly one endpoint (or a small fixed set) enumerated in §3 / §5.
