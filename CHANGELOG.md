# Changelog

All notable changes to SwartzNet are documented here. The
format follows [Keep a Changelog][kac]; the project follows
[Semantic Versioning][semver] starting from v1.

[kac]: https://keepachangelog.com/en/1.1.0/
[semver]: https://semver.org/spec/v2.0.0.html

## Unreleased

Targeting **v1.0.0** — first GA release. v1.0.0 still wants
real-world data for the reputation prior weight and at least
one second client implementing `sn_search` (the BEP-1
requirement to take a draft to Final). Both require
engagement from actual users of the v0.x prereleases.

## v0.5.0 — 2026-04-13

**Highlight: publisher trust.** Builds on v0.4.0's signed
torrents with a user-managed whitelist: `.torrent` files
signed by a trusted publisher are auto-confirmed to the
known-good Bloom filter as soon as their metadata arrives,
surfaced with a star badge in the Downloads table, and
available via a new context menu for one-click trust /
revoke.

Also in this release: three new metadata extractors (MOBI
ebooks, MP3 ID3 tags, JPEG EXIF) pushing the total to 14,
plus a polished Verify Signature dialog.

### Publisher trust — `internal/trust` package

Persistent JSON trust list (default:
`~/.local/share/swartznet/trust.json`). Atomic tempfile+rename
writes. Load at daemon startup, mutate through the Store API.

New API:

  - `trust.LoadOrCreate(path)` returns a `*Store`.
  - `Store.Add/Remove/IsTrusted/Label/List`.
  - `engine.Engine.TrustStore()` accessor.
  - `TorrentSnapshot.TrustedPublisher bool`.

Engine behaviour: when `autoIndex` sees a handle whose
`SignedBy()` is non-empty AND the pubkey is in the trust
store, the torrent's infohash is added to the known-good
Bloom filter immediately — no waiting for download
completion.

CLI:

  - `swartznet trust list [--json] [--file path]`
  - `swartznet trust add <pubkey> [<label>]`
  - `swartznet trust remove <pubkey>`

GUI:

  - Downloads "Signed" column shows ★ prefix for trusted
    publishers, ✓ prefix for signed-but-not-trusted, — for
    unsigned.
  - Right-click on a signed torrent: "Verify signature..."
    opens a form dialog with the full pubkey, trust status,
    trust label, and signed infohash. A separate menu item
    toggles trust ("Trust this publisher" /
    "Revoke trust for this publisher").

HTTP API: `GET /torrents` items gain optional
`trusted_publisher bool`.

Five trust-store tests; the signature-dialog integration is
covered by the existing engine signing round-trip tests.

### New metadata extractors

  - **MOBI** (`internal/indexer/extractors/mobi.go`) —
    pure-Go reader for Amazon Kindle .mobi / .azw / .azw3
    metadata. Walks the PalmDB + MOBI header, extracts the
    full-title field, parses EXTH records for author,
    publisher, description, ISBN, subject, published date,
    language. 3 tests with a synthetic MOBI byte stream.
  - **ID3** (`internal/indexer/extractors/id3.go`) — ID3v2.3
    and ID3v2.4 tag reader for MP3 files. Surfaces TIT2
    title, TPE1 artist, TALB album, TDRC/TYER year, TCON
    genre, TRCK track, TPUB publisher, COMM comment, USLT
    lyrics. Handles all four ID3 encodings (ISO-8859-1,
    UTF-16 BOM, UTF-16 BE, UTF-8). 4 tests.
  - **EXIF** (`internal/indexer/extractors/exif.go`) — JPEG
    APP1 EXIF reader. Walks the TIFF header + IFD0,
    extracts camera make/model, software, artist,
    description, copyright, date taken, and GPS coordinates
    (if present). 3 tests.

The existing `TestDispatchRefusesBinary` updated: .jpg now
dispatches to the EXIF extractor (correctly — for metadata,
not pixel data). .mkv still returns nil.

**Total extractors now: 14** — plaintext, subtitle, PDF,
EPUB, DOCX, ODT, RTF, archive, FB2, PPTX, ODP, MOBI, ID3,
EXIF.

## v0.4.0 — 2026-04-13

**Highlight: signed torrents.** SwartzNet can now sign and
verify `.torrent` files with ed25519 publisher signatures.
Downloads coming from a trusted publisher (anyone whose pubkey
you know) can be attributed with cryptographic certainty;
vanilla BitTorrent clients ignore the signature fields and
treat the `.torrent` as any other — full wire compatibility.

Also in this release: three new content extractors pushing the
total to 11 (FB2, PPTX, ODP join plaintext, subtitle, PDF,
EPUB, DOCX, ODT, RTF, archive).

### Signed torrents — `internal/signing` package

Two new optional top-level fields are added to the .torrent
metainfo dictionary:

  - `snet.pubkey`  32-byte ed25519 public key
  - `snet.sig`     64-byte ed25519 signature

The signature payload is `"SN-TORRENT-V1|" || <infohash>`.
The domain prefix prevents signature reuse across future
uses of the same key; the infohash binds the signature to
the content.

Compatibility: every other BitTorrent client (qBittorrent,
Transmission, libtorrent, anacrolix/torrent itself, etc.)
already ignores unknown top-level metainfo fields. A signed
`.torrent` downloads normally in every client; only SwartzNet
reads the signing fields.

New API:

  - `internal/signing.SignBytes(raw, priv) ([]byte, error)`
  - `internal/signing.VerifyBytes(raw) (Signature, error)`
  - `internal/signing.SignFile(path, priv) error`
  - `internal/signing.VerifyFile(path) (Signature, error)`
  - `engine.CreateTorrentOptions.SignWith ed25519.PrivateKey`
  - `engine.Handle.SignedBy() string`
  - `engine.TorrentSnapshot.SignedBy string` (hex pubkey)

CLI:

  - `swartznet create --sign` signs with the node's identity
    (loaded from `~/.local/share/swartznet/identity.key` by
    default; override with `--identity <path>`).

GUI:

  - Create Torrent dialog gains a "Sign with my ed25519
    identity" checkbox (enabled by default).
  - Downloads table gains a "Signed" column showing "✓ <prefix>"
    for verified signatures or "—" for unsigned.
  - Right-click context menu gains a "Copy publisher pubkey"
    action when the torrent is signed.

HTTP API:

  - `GET /torrents` response items gain an optional
    `signed_by` field containing the 64-char hex pubkey.

Five signing tests plus two engine integration tests cover
round-trip, unsigned files, tampered content, file-on-disk
round-trip, and the pubkey-hex encoder.

### New content extractors

  - **FB2** (`internal/indexer/extractors/fb2.go`) — pure-Go
    XML walker for FictionBook 2.x ebooks. Extracts body
    paragraphs + titles, skips `<binary>` cover art and
    `<stylesheet>`. Permissive charset handling so
    windows-1251-declared documents don't bounce out.
  - **PPTX** (`internal/indexer/extractors/pptx.go`) —
    PowerPoint presentations. Iterates
    `ppt/slides/slideN.xml` in numeric order, pulls text
    from every `<a:t>` element. Mirrors the DOCX design.
  - **ODP** (`internal/indexer/extractors/odp.go`) —
    LibreOffice Impress presentations. Reuses the ODT
    extractor's XML walker (same `<text:p>` / `<text:h>` /
    `<text:span>` shape).

Seven new extractor tests covering all three formats plus
dispatch-by-extension checks.

## v0.3.3 — 2026-04-13

GUI polish pass #2 plus two new text extractors — shipped the
same day as v0.3.2.

### GUI polish

- **Files dialog sort:** new "Sort by" dropdown (index /
  path / size / progress / priority) in the per-torrent Files
  modal. Persists across the 2-second poll refresh.
- **Search in-flight indicator:** `ProgressBarInfinite`
  appears under the status label while any of the Local /
  Swarm / DHT layers is running. Visible feedback that the
  query is in progress, especially important for swarm and
  DHT queries that can take seconds.
- **Torrents card on Status tab:** new card at the top of
  the Status grid showing total count, counts broken down by
  status (downloading / seeding / queued / paused), plus
  aggregate download and upload throughput.

### New content extractors

- **RTF** (`internal/indexer/extractors/rtf.go`): pure-Go
  parser for the subset of RTF used by every mainstream
  generator (Word, LibreOffice, Apple TextEdit, Pages export).
  Strips control words, groups, and common destinations
  (fonttbl / stylesheet / colortbl / info / pict / bin /
  header / footer / ...); decodes `\uN` Unicode escapes and
  `\'XX` hex escapes; emits `\par` as newline, `\tab` as tab.
  Claims by MIME (application/rtf, text/rtf) or `.rtf`
  extension. 4 tests.
- **Archive** (`internal/indexer/extractors/archive.go`):
  indexes the *file names* inside ZIP / TAR / TAR.GZ / TGZ
  archives, sorted and newline-joined. Lets searches match
  "changelog.md" inside a source tarball without unpacking.
  Pure-Go stdlib (archive/zip, archive/tar, compress/gzip).
  Detects format via magic bytes. 4 tests.

## v0.3.2 — 2026-04-13

Quality-of-life point release on top of v0.3.1. Every item is
small in isolation but the cumulative effect is that the GUI
now feels like a real desktop app rather than a proof of
concept.

### GUI polish

- **Transfer speed:** new ↓/↑ columns in the Downloads table
  showing per-torrent bytes/sec. Window title shows aggregate
  throughput when transfers are active.
- **Sortable columns:** click any Downloads column header to
  sort ascending; click again for descending; a third click
  clears the sort. An arrow (▲/▼) marks the active column.
- **Keyboard shortcuts:** `Ctrl+N` opens the Add Magnet
  dialog, `Ctrl+F` switches to Search and focuses the query
  entry, `Ctrl+Q` quits, `Delete` removes the selected
  torrent.
- **Persistent window size:** Fyne Preferences stores the
  window dimensions on close and restores them on next
  launch.
- **Empty state:** "No torrents yet" message replaces the
  previously blank Downloads table for new installs.
- **Row-header bug fix:** Fyne's `NewTableWithHeaders`
  exposes both a column-header row and a row-header column.
  The row-header cells used to show the CreateHeader
  placeholder text "Header"; they now render blank.
- **Resizable window fix:** the GUI advertised a 1110×1216
  minimum size to the window manager (the sum of every tab's
  content minimum size). On a 1366×768 laptop screen, the WM
  correctly refused to shrink below that minimum, which users
  reported as "the resize cursor appears but nothing
  happens". Fix: wrap each tab's content in a scroll
  container. Minimum drops to 209×33; scroll bars appear
  automatically when the content is bigger than the window.

### New GUI flags

- `--torrent <path.torrent>` — repeatable, loads the given
  `.torrent` file at startup. Useful for demos, scripted
  reproductions, and screenshots.
- `--tab <downloads|search|status|companion|settings>` —
  opens on a specific tab instead of the default Downloads.

### README overhaul

README is now product-focused: hero paragraph, feature
bullets, install (pre-built binaries + build-from-source),
quick-start (add/search/create/GUI), three-frontends table,
documentation pointers, configuration, non-goals, dev/license.
The previous 24-row milestone matrix moved to
[docs/MILESTONES.md](docs/MILESTONES.md). New
[docs/README.md](docs/README.md) indexes every document by
audience. Three native GUI screenshots under
`docs/screenshots/` illustrate the Downloads, Status, and
Settings tabs.

### CI/CD

- `.github/workflows/test.yml` runs `go mod tidy` check,
  `gofmt -l -s`, `go vet`, and `go test -race` on every
  push to main and every PR. Excludes `internal/testlab/`
  (integration tests that are flaky on shared runners).
- `.github/workflows/release.yml` fires on every `v*` tag,
  builds five CLI binaries (linux amd64/arm64, darwin
  amd64/arm64, windows amd64) and four GUI binaries (linux,
  darwin amd64 via cross-compile from arm64, darwin arm64,
  windows), regenerates SHA256SUMS, extracts the matching
  CHANGELOG section into release notes, and publishes the
  GitHub Release. Prerelease flag is automatic for v0.* tags.

## v0.3.1 — 2026-04-13

Feature-packed point release on top of v0.3.0. Six new user-
facing capabilities, a product-focused documentation rewrite,
and a full GitHub Actions CI/CD pipeline so future tags build
cross-platform binaries automatically.

**Highlights:** file selection for multi-file torrents,
bandwidth rate limits, download queue with concurrency cap and
reorderable queue positions, CLI parity for the v0.3.0 features
(`create` / `index` / `files`), right-click context menu in the
Downloads tab, and — for the first time in any SwartzNet
release — pre-built GUI binaries for **macOS (Intel + Apple
Silicon) and Windows** alongside the existing Linux GUI binary.

### File selection for multi-file torrents

- New `Engine.TorrentFiles(infoHashHex) ([]FileSnapshot, error)`
  returns a per-file view (path, size, bytes completed, progress,
  priority).
- New `Engine.SetFilePriority(ih, fileIndex, priority)` flips a
  single file between "none", "normal", and "high". Takes effect
  immediately even on an already-downloading torrent.
- New `Engine.autoDownload` goroutine: after metadata arrives,
  every file is set to Normal priority so the GUI flow matches
  CLI behaviour. The CLI's existing `DownloadAll()` call stays
  as a harmless duplicate.
- New HTTP endpoints:
  - `GET /torrents/{infohash}/files` — per-file snapshot list.
  - `POST /torrents/{infohash}/files/{index}/priority` —
    `{"priority": "none"|"normal"|"high"}`.
- GUI Downloads toolbar gains a "Files..." button that opens a
  modal with a live-updating list of every file in the selected
  torrent: path, size, progress bar, and a per-file priority
  dropdown. "Select All" / "Deselect All" bulk actions at the
  top. Polls every 2 s while open.
- Two new engine tests:
  `TestTorrentFilesAndSetPriority`,
  `TestTorrentFilesUnknownInfohash`.

### Bandwidth rate limits

- The Engine now installs mutable `*rate.Limiter` instances
  (from `golang.org/x/time/rate`) on the anacrolix client's
  `UploadRateLimiter` / `DownloadRateLimiter` fields. Defaults
  to `rate.Inf` (unlimited). Users can tune limits at runtime
  without restarting the client.
- New `Engine.SetUploadLimitBytesPerSec(bps)`,
  `SetDownloadLimitBytesPerSec(bps)`,
  `UploadLimitBytesPerSec() int64`,
  `DownloadLimitBytesPerSec() int64`. Zero or negative bps
  disables the cap.
- GUI Settings tab gains a new "Bandwidth Limits" card with
  two numeric entries (KiB/s) and an Apply button. Current
  limits are read on tab open; Apply updates the limiter in
  place so every active peer connection sees the new rate.
- Two new engine tests: `TestRateLimitDefaultsUnlimited`,
  `TestRateLimitSetAndGet`.

### CLI parity for v0.3.0 features

Three new `swartznet` subcommands so scripting against the
daemon doesn't need the GUI:

- `swartznet create <path> -o out.torrent [flags]` — build a
  new `.torrent` from local content. Spins up a headless
  engine (no DHT, no upload unless `--seed`) just long enough
  to hash pieces and write the file. Flags: `--tracker URL`
  (repeat), `--webseed URL` (repeat), `--piece-kib N`,
  `--private`, `--comment STR`, `--name STR`, `--seed`,
  `--data-dir PATH`.
- `swartznet index <infohash> on|off [--api-addr]` — flips
  the per-torrent indexing toggle on a running daemon via
  `POST /torrents/{ih}/indexing`.
- `swartznet files <infohash> [--json] [--api-addr]` and
  `swartznet files <infohash> <index> <priority>` — lists
  every file in a torrent with priority + progress (table or
  JSON), or flips a single file's priority to none/normal/high.

### Right-click context menu in Downloads

GUI right-click on any row in the Downloads table opens a
context menu operating on the selected row:

  - Files…
  - Pause / Resume (contextual, based on current state)
  - Remove
  - Stop / Start indexing (contextual)
  - Copy magnet link (to system clipboard)
  - Copy infohash

The menu is implemented via a small `SecondaryTappable` wrapper
around the table (Fyne doesn't expose per-cell secondary-tap
events directly).

### Queue management

anacrolix/torrent has no built-in "max N active downloads"
concept. SwartzNet now layers a simple FIFO queue on top so
users can cap concurrency (matching qBittorrent's behaviour):

- `Engine.MaxActiveDownloads() int` / `SetMaxActiveDownloads(n)`.
  Zero = unlimited (the default and previous behaviour).
- New `Handle.IsQueued()` and `TorrentSnapshot.Queued bool`
  surface the "waiting for a slot" state.
- Queued torrents still fetch metadata and run the indexing
  pipeline; only the file-priority flip (to PiecePriorityNormal)
  is deferred.
- Pause / remove / completion hooks call `promoteQueuedLocked`
  to fill the freed slot with the oldest queued torrent.
- Raising the cap at runtime immediately promotes everything
  that was waiting.
- GUI Settings tab gains a new "Queue Management" card with a
  single numeric entry + Apply button. Current cap is read on
  tab open.
- Four new engine tests:
  `TestMaxActiveDownloadsDefaultsUnlimited`,
  `TestMaxActiveDownloadsClampsNegative`,
  `TestQueueOrderThirdTorrentQueuedUnderCap2`,
  `TestQueueRaisingCapPromotesQueued`.

Still pending:

- **Cross-platform GUI release** (darwin + windows GUI
  binaries via `fyne-cross` once Docker is available on the
  build machine).

## v0.3.0 — 2026-04-12

**Highlight:** SwartzNet now ships a **native Fyne GUI** as a
third frontend alongside the CLI and the web UI. All GUI code
is Go — no HTML/CSS/JS. The same daemon (engine, indexer,
companion, HTTP API) powers all three. Two other big features
land in this release: **per-torrent indexing control** and
**torrent creation from local content**.

### G0–G7 — Native Fyne GUI (v0.3.0)

- **G0**: Extracted engine+indexer+companion+httpapi wiring from
  `cmd/swartznet/cmd_add.go` into a new `internal/daemon/`
  package. `Daemon` struct with `New(ctx, opts)` / `Close()` is
  now shared by both CLI and GUI. `controllerAdapter` and
  `companionAdapter` moved from `cmd/swartznet/` to
  `internal/daemon/adapters.go`. Three new tests
  (`TestDaemonStartStop`, `TestDaemonNoIndex`, `TestDaemonWithAPI`)
  all pass under `-race`. CLI behavior unchanged.
- **G1**: New `cmd/swartznet-gui` entry point and
  `internal/gui/` package. Fyne v2.7.3 (BSD 3-Clause) chosen over
  Wails/Gio because it is pure Go for the UI layer. Window with
  AppTabs layout. Downloads tab: `widget.Table` polling
  `engine.TorrentSnapshots` every 2 s via `fyne.Do()`. Add
  magnet dialog; file picker for `.torrent`. Pause/resume/remove
  buttons.
- **G2**: Search tab with Local / Swarm / DHT checkboxes, all
  three layers fanned out in parallel via goroutines. Results as
  cards with Confirm / Flag buttons using the same source
  attribution logic as the HTTP API handler.
- **G3**: Status tab — adaptive grid of Card widgets
  (local index, swarm peers, DHT publisher, Bloom filter) plus a
  reputation list. 4-second refresh, matches web UI cadence.
- **G4**: Companion tab — publisher status card with Refresh
  Now button, plus a follow-list List widget and a pubkey +
  label form.
- **G5**: Settings tab — sharing-level RadioGroup (L0 / L1 / L2),
  file/content hit Checks, save button calls
  `swarmsearch.Protocol.SetCapabilities`.
- **G6**: System tray via `desktop.App` assertion. Tray menu:
  Show, Add Magnet, About, Quit. Close intercept minimises to
  tray when available, otherwise quits. Download-complete OS
  notifications via `app.SendNotification`. About dialog shows
  version, identity pubkey, listen port, HTTP API address.
  `//go:embed` PNG icon.
- **G7**: `scripts/build-gui.sh` for native builds with CGo +
  trimpath + stripped symbols (~46 MB). Docs in
  `docs/08-operations.md#native-gui-v030` covering
  dependencies and the `fyne-cross` Docker path for release
  builds on all 5 platforms (linux-amd64/arm64,
  darwin-amd64/arm64, windows-amd64).

**Trade-off accepted:** Fyne needs a CGo toolchain, so the GUI
binary is not statically linked and can't be cross-compiled from
a vanilla Go toolchain. The CLI continues to build with
`CGO_ENABLED=0` and stays ~40 MB with the existing
`build-release.sh` pipeline.

### G8 — Per-torrent indexing control

- New `Handle.IsIndexing() bool` and `Engine.SetTorrentIndexing(
  hex, enabled) error` let the user decide, per torrent, whether
  file downloads feed the extraction pipeline and whether the
  torrent-level document is written to Bleve. Default remains on
  — existing behaviour is preserved.
- `TorrentSnapshot` gains an `Indexing bool` field, mirrored
  through the httpapi struct as `"indexing":bool` so the web UI
  can surface it in future work.
- New HTTP endpoint `POST /torrents/{infohash}/indexing` with
  body `{"enabled": true|false}`.
- GUI Downloads tab gains a new "Indexed" column (yes/no) and a
  "Toggle Index" toolbar button. The Add Magnet dialog gains an
  "Index this torrent's files after download" checkbox (default
  on). The `.torrent` file picker keeps indexing on by default;
  the user can toggle afterwards via the toolbar button.
- Two new tests: `TestSetTorrentIndexingUnknownInfohash`,
  `TestSetTorrentIndexingReflectedInSnapshot`.

### G9 — Create Torrent

- New `Engine.CreateTorrent(CreateTorrentOptions) (*metainfo.MetaInfo, error)`
  and `Engine.CreateTorrentFile(opts, outPath) (infohash, mi, error)`
  wrap `metainfo.Info.BuildFromFilePath` plus bencode serialization
  and atomic tempfile+rename for the on-disk variant.
  `CreateTorrentOptions` exposes: Root (file or folder),
  Name override, PieceLength (0 = Auto via
  `metainfo.ChoosePieceLength`), Trackers, WebSeeds (BEP-19),
  Private (BEP-27), Comment, CreatedBy.
- GUI Downloads toolbar gains a "Create Torrent" button that
  walks the user through every field, with file/folder pickers
  for Root and a Save As… picker for output. "Start seeding
  immediately" calls `AddTorrentMetaInfo` right after the file
  is written.
- Piece hashing runs in a background goroutine; a
  `ProgressBarInfinite` modal stays up until completion so the
  UI never blocks.
- Four new tests: `TestCreateTorrentSingleFile`,
  `TestCreateTorrentMultiFile`, `TestCreateTorrentFileWritesValid`,
  `TestCreateTorrentMissingRoot`.

### G10 — Documentation refresh

- `docs/05-integration-design.md` §2 updated with the v0.3.0
  architecture diagram (three frontends + `internal/daemon`
  layer), plus new sections on per-torrent indexing control and
  torrent creation.
- `docs/08-operations.md` gains "Creating a new torrent" and
  "Per-torrent indexing control" subsections explaining every
  dialog field and the two paths for opting a torrent out.
- `README.md` gains a "Three frontends" table and status-matrix
  entries for G0-G9.

### M9 — Per-hit source tracking + targeted flag

- New `internal/reputation.SourceTracker`: an LRU-bounded map of
  infohash → set of indexer pubkeys that returned that infohash
  in a Layer-D query. Always non-nil after `engine.New`; no
  on-disk persistence (it repopulates naturally as the user
  searches).
- `dhtindex.Lookup` now records source attributions after
  merging hits, and `httpapi.handleFlag` uses the tracker to
  demote only the indexers that actually returned the flagged
  infohash, falling back to "demote everyone we saw recently"
  if the tracker has no record.

### M10 — GUI download controls

- **M10a**: New `engine.TorrentSnapshot` plus
  `Engine.TorrentSnapshots`, `PauseTorrent`, `ResumeTorrent`,
  `RemoveTorrent`, `IsPaused`. The pause state is mirrored on
  the `Handle` because anacrolix's internal flag is private.
  `snapshotOf` carries a nil-Info guard so torrents that haven't
  fetched metadata yet do not crash the API.
- **M10b**: Four new HTTP endpoints — `GET /torrents`,
  `POST /torrents/{infohash}/pause`, `POST /torrents/{infohash}/resume`,
  `DELETE /torrents/{infohash}`. Bridged through a new
  `httpapi.TorrentController` interface and a small adapter in
  `cmd/swartznet/torrent_controller.go`.
- **M10c**: New "Downloads" tab in the web UI with per-torrent
  progress bars, status pills, and pause/resume/remove buttons.
  Polls `/torrents` every 2 s while the tab is active.

### M11 — F3 companion content-index torrents (in progress)

- **M11a**: New `internal/companion` package with the on-disk
  schema for SwartzNet's distributed content index. Top-level
  `CompanionIndex` carrying `TorrentRecord`/`FileRecord`/
  `ContentChunk` types, gzip+JSON `Encode`/`Decode` with a
  format-magic guard and a 1 GiB safety cap. Format constants:
  `FormatVersion=1`, `FormatFileName="swartznet-content-index-v1.json.gz"`.
- **M11b**: `companion.BuildFromIndex` walks the local Bleve
  index (via two new `indexer.AllTorrentDocs` /
  `indexer.ContentDocsForInfoHash` paginated MatchAll queries)
  and produces a CompanionIndex. `companion.WriteCompanionFiles`
  serialises it and wraps the bytes in a v1 .torrent metainfo
  with a 256 KiB piece length, written atomically to the
  publisher's companion directory.
- **M11c**: New `companion.Publisher` worker — every hour
  (configurable) it builds the index, seeds the wrapping torrent
  through the engine, and publishes a BEP-46-style mutable
  pointer at salt `_sn_content_index` whose value is the new
  infohash. Manual `RefreshNow` is throttled by `MinInterval`.
  `dhtindex.AnacrolixPutter.PutInfohashPointer` and
  `AnacrolixGetter.GetInfohashPointer` are the new BEP-44
  primitives. `engine.AddTorrentMetaInfo` lets the publisher
  hand the in-memory metainfo back to the engine for seeding,
  and `engine.PointerPutter` exposes the shared
  `*dhtindex.AnacrolixPutter`. `cmd_add.go` constructs and
  starts the companion publisher after `SetIndex` whenever the
  daemon has both an index and an identity. New
  `config.CompanionDir` (default `~/.local/share/swartznet/companion`)
  controls the on-disk artefacts directory.
- **M11d**: New `companion.Subscriber` and
  `companion.SubscriberWorker` — the read side of the F3 story.
  `Subscriber.Sync` resolves a publisher's BEP-46 pointer,
  fetches the underlying torrent, decodes the gzipped JSON, and
  ingests every record into the local Bleve index.
  `SubscriberWorker` runs the same pipeline against a follow
  list every hour. Narrow `PointerGetter`, `CompanionFetcher`,
  and `Ingester` interfaces keep the package decoupled from
  internal/engine and internal/dhtindex.
  `engine.AddInfoHash` adds a torrent by raw 20-byte infohash;
  `engine.FetchCompanionTorrent` orchestrates add → wait for
  metadata → wait for download → return the on-disk path, and
  satisfies `companion.CompanionFetcher`. `engine.PointerGetter`
  exposes the shared `*dhtindex.AnacrolixGetter`. New
  `config.CompanionFollowFile` (default
  `~/.local/share/swartznet/companion-follows.json`) is the JSON
  array of `{pubkey, label}` rows the subscriber follows on
  startup; the file is loaded by a tiny `cmd/swartznet/companion_follows.go`
  helper. `cmd_add.go` starts the subscriber worker after the
  publisher whenever the daemon has both an index and a DHT
  getter. Six new tests covering happy path, pointer/fetcher
  failures, partial-ingest failure, the worker lifecycle, and
  IngestReader. All pass under `-race`.
### M13 — v1.0.0 blocker research follow-through

The research pass over the six v1-blocking open questions in
`docs/05-integration-design.md` §13 produced a report
(`docs/09-v1-blocker-research.md`) plus a set of concrete action
items. Nothing turned out to require a protocol redesign; the
below commits are the straightforward follow-through.

- **M13a — `THIRD_PARTY_LICENSES` + PDF attribution fix**: Audit
  of the extractor dependency tree confirmed the v1 hot path is
  license-clean (BSD-3-Clause for `ledongthuc/pdf` and
  `golang.org/x/net`, stdlib for everything else). The only
  finding was a mis-attribution — the project docs called
  `ledongthuc/pdf` MIT-licensed; the upstream `LICENSE` file is
  actually BSD-3-Clause (© The Go Authors, inherited from
  `rsc/pdf`). New `THIRD_PARTY_LICENSES` file lists every heavy
  dependency with its full notice text; `README.md` and
  `internal/indexer/extractors/pdf.go` updated to call it
  BSD-3-Clause.
- **M13b — publisher `MinPutInterval` hard cap**: v1 blocker 2
  research noted `anacrolix/dht/v2` has no default rate cap on
  concurrent mutable-item puts, so the client must enforce its
  own per-keyword budget to avoid self-DoS'ing the publisher.
  `dhtindex.PublisherOptions` gains a `MinPutInterval` field
  (default 55 minutes), and `publishOne` now short-circuits if
  the keyword was published less than `MinPutInterval` ago,
  regardless of whether the trigger was a `Submit()` or a
  refresh tick. `TestPublisherMinPutIntervalThrottles` covers
  the new path.
- **M13e — chunker shrink (10 KiB → 2 KiB)**: v1 blocker 1
  research converges on 0.5–4 KiB for content-chunk targets
  (Elastic's docs default to ~250 words ≈ 1.25 KiB; production
  RAG/BM25 stacks sit at 1–2 KiB). SwartzNet was using 10 KiB,
  an order of magnitude above the sweet spot. Shrinking
  `DefaultChunkTargetBytes` improves BM25 relevance per-hit and
  tightens highlight fragments at a small index-size cost.
- **M13c — seed reputation list + decaying bonus**: v1 blocker 4
  research recommended a signed/versioned seed list of ~20
  curated pubkeys with an exponentially-decaying score boost
  (90-day half-life) so organic reputation dominates after one
  quarter. `reputation.Counters` gains `SeededAt` + `SeedLabel`;
  `scoreOf` adds `SeedBonus × 2^(-age/SeedHalfLife)` on top of
  the organic Bayesian score. New `MarkSeeded`, `IsSeeded`,
  `AnySeeded`, and `LoadSeedList` methods; new `SeedList` JSON
  schema with `version` gate. A fresh seed scores ~0.95, well
  above any reasonable `MinIndexerScore` cutoff, so the existing
  `Threshold` pre-filter in `dhtindex.Lookup` gets heavy-tail
  semantics for free (a bootstrap node with zero traffic still
  passes the cutoff if it's in the seed list). New
  `config.SeedListPath` (default `~/.local/share/swartznet/seeds.json`)
  is loaded by `engine.New` after `LoadOrCreateTracker`. Three
  new tests cover the bypass, the 90-day decay via backdated
  `SeededAt`, and the JSON loader (including malformed
  entries). The actual shipping seed list file is not bundled —
  distribution is a post-v1 operational decision.

- **M13d — privacy & threat model (blocker 6)**: The original
  research recommendation was "SOCKS5 for the put path", but
  BEP-44 is UDP and SOCKS5/Tor don't cleanly carry UDP — so
  the v1 response is the honest subset of that plan instead.
  New "Privacy and threat model" section in
  `docs/08-operations.md` enumerates exactly what's visible
  (IP → ~8 closest DHT nodes; stable pubkey; hourly timing
  fingerprint; BEP-42 geographic bias) and exactly what isn't
  (downloads, local queries, companion contents, web UI).
  Shipping mitigations: `--no-dht` (full disable) and the new
  `--no-dht-publish` / `cfg.DisableDHTPublish` leech-only mode
  that keeps the node on the DHT for gets + companion
  pointers but skips every outbound `put`.
  `dhtindex.KeywordValue` gains an optional `NextPubKey`
  bencode field — v1.0.0 ships the field on the wire but
  never populates it; the rotation logic is scheduled for
  v1.1 so future clients can adopt it without a format bump.
  For real publisher anonymity, users layer their own Tor /
  VPN / i2p on top, as documented.

### M12 — v1.0.0 preparation

Everything below is work toward answering the six "open questions
that block v1" in `docs/05-integration-design.md` §13, plus
tactical post-v1 items from §12 that were already clear enough to
ship without research.

- **M12a — README status table refresh**: Backfilled M9, M10, and
  M11 into the top-of-README milestone table, removed the stale
  "Planned" rows for M2.3 / M3 / M4 / M5 / M6 that had been
  complete since earlier releases.
- **M12b — index-size measurement tooling**: New `indexer.Stats()`
  method + `GET /index/stats` endpoint reporting on-disk directory
  bytes, per-type document counts (torrents vs. content chunks),
  sum of every stored `ContentDoc.Text` byte, and the resulting
  inflation ratio. The Status tab in the web UI now shows these as
  part of the Local Index card, so anyone running the daemon for a
  day can produce the data that answers v1 open question #1 ("how
  big is Bleve's index per TB of indexed text"). `TestIndexStats`
  pins down every field against a seeded index.
- **M12c — dht-smoke concurrent-publish stress test**: Added
  `-stress N`, `-stress-concurrent`, and `-stress-timeout` flags
  to `cmd/dht-smoke`. After the single-put smoke, the stress phase
  publishes `N` distinct BEP-44 mutable items against the live
  mainline DHT with bounded concurrency and reports per-put
  latency (min / p50 / p95 / max), total success rate, wall-clock
  elapsed, post-run DHT routing stats, and a round-trip Get from
  one successful keyword. Answers v1 open question #2 ("how many
  concurrent BEP-44 publishes can anacrolix/dht/v2 sustain"). Fails
  the exit status only when every put fails — a partial failure is
  the interesting measurement.
- **M12f — per-peer sn_search rate limiter**: Design doc §5.4
  asks for rate limiting on inbound `sn_search` queries. A noisy
  peer used to be able to DoS the Bleve query path; this commit
  adds a token-bucket per peer (default 5 q/s steady, burst 10)
  that gets a `RejectRateLimited` reply when over quota. Runtime
  configurable via `Protocol.SetRateLimit`. Per-peer buckets are
  evicted in `OnPeerClosed` so long-running daemons don't leak.
  Six unit tests on the bucket math plus one end-to-end
  `TestHandleInboundRateLimit` through the full `HandleMessage`
  path (including peer isolation). All pass under `-race`.
- **M12e — search result snippet highlighting**: `SearchRequest`
  gains a `Highlight` bool; when true, Bleve's HTML highlighter
  runs on the `name` / `files` / `text` fields and returns
  matched-text fragments wrapped in `<mark>...</mark>`. Fragments
  flow through to `LocalHit.Fragments` on `POST /search` and are
  rendered in the web UI as a small indented snippet block under
  each hit. `TestSearchHighlight` covers both the nil-when-off
  case and the marked-fragment-when-on case. The CLI still omits
  `Highlight` (its output is plain text), so this is strictly a
  GUI enhancement.
- **M12d — multi-word + boolean query support**: Bleve's
  `QueryStringQuery` already supported `+required` / `-excluded` /
  `"phrase"` / `field:term` / `fuzzy~1` — this commit just pins
  those guarantees down behind `TestSearchQueryOperators` (8
  sub-cases covering each operator), rewrites the `Index.Search`
  docstring to enumerate the supported syntax, and adds a one-line
  hint under the web UI's search box so end-users can discover the
  advanced operators.

- **M11e**: GUI integration. New `httpapi.CompanionController`
  interface and four endpoints: `GET /companion` (status of
  publisher + every followed publisher), `POST /companion/refresh`
  (proxies to `Publisher.RefreshNow`, returns 429 on throttle),
  `POST /companion/follow {pubkey, label}` (adds to the follow
  list AND persists to disk), `POST /companion/unfollow {pubkey}`
  (removes + persists). The `cmd/swartznet/companion_controller.go`
  adapter bridges the running publisher and subscriber worker
  to the controller and owns the on-disk follow file
  (atomic-rename writes). New "Companion" tab in the web UI
  showing publisher status (last refresh, infohash, count), a
  manual refresh button, the follow form, and one card per
  followed publisher with last-sync stats and an unfollow
  button. Six new httpapi tests using a fake controller —
  status, refresh happy path, refresh-too-soon (429), follow,
  follow with bad pubkey (400), and unfollow.

## v0.2.0 — 2026-04-10

Second preview release. Adds a complete browser-based GUI on
top of the existing CLI + JSON API, validates the Layer-D
publish path against the live mainline DHT, and ships
release tooling so future cuts are one command.

### M8 — Local web UI

- **M8a+b**: HTML/CSS/JS embedded into the binary via go:embed
  and served from the existing httpapi daemon at `GET /` and
  `GET /static/*`. Four tabs (Search, Add torrent, Status,
  Sharing) using the same JSON endpoints the CLI uses. No build
  step, no JavaScript bundler, no external dependencies. Lives
  at `internal/httpapi/web/{embed.go, index.html,
  static/style.css, static/app.js}`. `/healthz` now reports the
  build version so the UI badge can show the running version.
- **M8c**: Three new HTTP endpoints to round out the UI's
  functionality:
  - `POST /torrent {uri}` adds a magnet via the new
    `httpapi.TorrentAdder` interface (`engine.AddMagnetURI`
    satisfies it). Includes a `recover()` guard so a malformed
    magnet returns a clean 400 instead of crashing the daemon.
  - `GET /capabilities` reports the current `sn_search`
    `share_local` / `file_hits` / `content_hits` / `publisher`
    flags from `swarmsearch.Protocol`.
  - `POST /capabilities` updates them with input clamping.

The web UI is **localhost-only by design** because it controls
the daemon and is fundamentally separate from the per-peer
search-result interfaces (sn_search peer wire, BEP-44 DHT)
which the user controls via capability flags and the
`--no-dht` flag at startup.

### Release tooling and validation

- **`scripts/build-release.sh`**: one-command cross-compile for
  linux/amd64+arm64, darwin/amd64+arm64, windows/amd64. Pure-Go,
  CGO-disabled, fully static binaries with stripped symbols and
  trimpath. Generates a SHA256SUMS file alongside.
- **`cmd/dht-smoke`**: one-shot live mainline DHT smoke test for
  the BEP-44 publisher path. Joins the real DHT, runs an
  AnacrolixPutter Put + AnacrolixGetter Get round trip with an
  ephemeral keypair so the user's real publisher identity is
  never touched. Run with `go run ./cmd/dht-smoke`.
- **Validation**: the `dht-smoke` target was run against the live
  mainline DHT on 2026-04-10. 25 good DHT nodes after bootstrap,
  Put accepted by 7 of 8 closest nodes in ~10s (1 timeout, normal
  network reality), Get round-tripped the signed payload back in
  ~7s with the synthetic Hit data unchanged. **The "BEP-44
  publish path not yet validated against the live mainline
  DHT" caveat from v0.1.0 is now retired.**

All nine packages pass under `go test -race ./...`.

## v0.1.0 — 2026-04-10

First numbered preview release. M1-M7 feature-complete in 26
commits. Five cross-platform release binaries (Linux x64+arm64,
macOS x64+arm64, Windows x64) attached to the GitHub Release at
<https://github.com/claudenstein/swartznet/releases/tag/v0.1.0>.

## M7 — Documentation polish

- **M7a**: Draft BEP-style spec for the `sn_search` peer-wire
  extension (`docs/06-bep-sn_search-draft.md`).
- **M7b**: Draft BEP-style spec for the BEP-44 keyword index
  (`docs/07-bep-dht-keyword-index-draft.md`).
- **M7c**: Operations guide
  (`docs/08-operations.md`), this CHANGELOG, and README
  polish.

## M6 — Office-document extractors

- **M6a**: EPUB extractor with shared HTML-text helper
  (`internal/indexer/extractors/htmltext.go`,
  `epub.go`). Pure stdlib + `golang.org/x/net/html`.
- **M6b**: DOCX and ODT extractors via stdlib
  `archive/zip` + `encoding/xml`.

## M5 — Spam resistance

- **M5a**: `internal/reputation/bloom.go` — pure-Go Bloom
  filter (1M items @ 1% FP, ~1.2 MB) with custom on-disk
  format and double-hashing trick.
- **M5b**: `internal/reputation/reputation.go` —
  per-pubkey reputation tracker with Bayesian-smoothed
  scoring.
- **M5c**: Lookup path now consults both. Low-reputation
  indexers are skipped before any DHT traversal; Bloom-hit
  results sort to the top with a +0.25 score boost.
- **M5d**: `swartznet flag` and `swartznet confirm`
  CLI commands; auto-confirm on torrent download
  completion via `Torrent.Complete().On()`.

## M4 — BEP-44 keyword publisher (Layer D)

- **M4a**: `internal/identity` — persistent ed25519
  publisher keypair with 0600 permissions enforcement.
- **M4b**: `internal/dhtindex/tokenize.go` — torrent name
  → keyword tokenisation with stop-word and extension
  filtering.
- **M4c**: `internal/dhtindex/{schema,dht}.go` — BEP-44
  mutable-item put/get wrapper around
  `anacrolix/dht/v2/exts/getput`, plus an in-memory
  test double. Includes a race fix in `httpapi`.
- **M4d**: `internal/dhtindex/{manifest,publisher}.go` —
  long-running publisher worker with on-disk shard
  manifest and 1h refresh ticker. Engine wiring loads the
  identity, builds the publisher, and feeds new torrents
  to it on add.
- **M4e**: `internal/dhtindex/lookup.go` — parallel BEP-44
  get fan-out across known indexer pubkeys, merging by
  infohash with per-source attribution.
- **M4f**: `swartznet search --dht` CLI flag,
  `swartznet status` command, HTTP API plumbing for both.

## M3 — Peer-wire `sn_search` extension (Layer S)

- **M3a**: `internal/swarmsearch/protocol.go` — registers
  `sn_search` in the LTEP `m` dictionary via the
  `PeerConnAdded` callback, observes remote handshakes
  via `ReadExtendedHandshake` to discover capable peers.
- **M3b**: `internal/swarmsearch/{wire,handler}.go` —
  bencoded query/result/reject messages, inbound query
  handler that runs against the local Bleve index via an
  adapter.
- **M3c**: `internal/swarmsearch/query.go` — outbound
  `Query()` fan-out with txid routing and merge-by-infohash.
- **M3d**: `internal/httpapi/server.go` — loopback HTTP
  daemon with POST /search and GET /healthz endpoints;
  `swartznet search --swarm` CLI flag.

## M2 — Local Bleve index (Layer L)

- **M2.0**: `internal/indexer/{indexer,schema}.go` — Bleve
  full-text index over torrent metadata; `swartznet
  search <query>` command.
- **M2.1**: `internal/engine/{filemap,file_tracker}.go` —
  piece-to-file completion tracker that synthesises
  `FileCompleteEvent`s from the piece-state subscription.
- **M2.2a**: `internal/indexer/{content,pipeline}.go`
  + `internal/indexer/extractors/{extractor,plaintext}.go`
  — extractor framework, plain-text extractor, ingestion
  pipeline that consumes file-complete events and writes
  content docs.
- **M2.2b**: SRT and WebVTT subtitle extractor
  (`internal/indexer/extractors/subtitle.go`).
- **M2.2c**: Paragraph-boundary chunker
  (`internal/indexer/extractors/chunker.go`).
- **M2.3**: PDF extractor via `github.com/ledongthuc/pdf`
  (`internal/indexer/extractors/pdf.go`).

## M1 — Foundation

- **M1**: `cmd/swartznet` + `internal/{config,engine}` —
  minimal CLI built on `anacrolix/torrent` v1.61.0 with
  the extension hooks the later milestones depend on.

## Pre-history

Five research / design documents in `docs/01-…05-` covering
the comparison of torrent-client implementations, prior art
for distributed search, the relevant BEPs, and the
SwartzNet integration design.
