# Slice 11 — Native Fyne GUI — extraction ledger

_Behavioral extraction from legacy-snapshot internal/gui + cmd/swartznet-gui. Pure presentation over daemon.Daemon._

I have everything needed for the app-window reader. Here is the exhaustive section.

---

## app-window

Covers the App/window construction, lifecycle, theme, icon, the CLI shim, and `FyneApp.toml`. Files: `internal/gui/app.go`, `internal/gui/window.go`, `internal/gui/theme.go`, `internal/gui/icon.go`, `cmd/swartznet-gui/main.go`, `cmd/swartznet-gui/FyneApp.toml`, plus the ldflags stamp in `scripts/build-gui.sh`.

### 1. The `App` struct (`internal/gui/app.go`)

```go
type App struct {
	fyne      fyne.App             // app.NewWithID("net.swartznet.gui")
	win       fyne.Window          // the single main window
	daemon    *daemon.Daemon       // the ONE shared node (not owned here — see §7)
	cancel    context.CancelFunc   // cancels the GUI-local ctx that stops the goroutines
	version   string               // passed in from main.Version (ldflags)
	buildDate string               // passed in from main.BuildDate (ldflags)
	dl        *downloadsTab
	sr        *searchTab
	tabs      *container.AppTabs
	lastNotified map[string]bool   // dedup guard for completion notifications, keyed by InfoHash
}
```

Note: it holds handles only to the Downloads (`dl`) and Search (`sr`) tabs — the Status/Companion/Settings tab structs are constructed, wired into the tab container, then discarded (only their `.content` is retained inside `container.NewScroll(...)`). `dl` and `sr` are kept because shortcuts/menus need to call back into them (`dl.showAddMagnetDialog()`, `dl.removeSelected()`, focusing `sr.queryEntry`).

The `daemon` handle is the single seam to every subsystem. Every action goes through `a.daemon.Eng` / `a.daemon.API`. **No subsystem is constructed inside the GUI** — good, this matches the Slice-11 "pure presentation" rule.

### 2. Window / tab-container assembly — `gui.New(d *daemon.Daemon, version, buildDate string) *App`

Construction order, exactly:

1. `a := app.NewWithID("net.swartznet.gui")` — app ID **must** match `FyneApp.toml` `ID` and `main.go`'s `daemon.Options`. It's the key under which Fyne `Preferences()` are stored.
2. `a.Settings().SetTheme(&swartzTheme{})` — installs the custom dark theme (§4).
3. `a.SetIcon(AppIcon)` — the embedded PNG (§5).
4. `win := a.NewWindow("SwartzNet " + version)` — window title is `"SwartzNet " + version`, e.g. `"SwartzNet v0.8.0"`. Sets `win.SetIcon(AppIcon)`.
5. `win.SetFixedSize(false)` — explicitly resizable (comment: some WM+compositor combos ignore the implicit default).
6. Restores window size from prefs: `win.Resize(fyne.NewSize(prefs.FloatWithFallback("window.width", 900), prefs.FloatWithFallback("window.height", 600)))` — default **900x600**, persisted under prefs keys `"window.width"` / `"window.height"`.
7. `ctx, cancel := context.WithCancel(context.Background())` — a **GUI-local context, independent of the daemon's ctx**. This ctx is handed to every tab constructor and to the two background goroutines; `cancel` stops them. (It does *not* close the daemon — see §7.)
8. Builds the `App` struct.
9. Constructs the five tabs, each `newXxxTab(ctx, d)`:
   - `dl := newDownloadsTab(ctx, d)`
   - `sr := newSearchTab(ctx, d)`
   - `st := newStatusTab(ctx, d)`
   - `cp := newCompanionTab(ctx, d)`
   - `se := newSettingsTab(d)`  ← Settings takes **no ctx** (no polling loop).
   Stores `guiApp.dl = dl`, `guiApp.sr = sr`.
10. Assembles the tab container, **each tab's `.content` wrapped in `container.NewScroll(...)`** (comment: without this Fyne advertises a 1000+px min size on both axes and small laptops can't shrink the window):

```go
tabs := container.NewAppTabs(
	container.NewTabItem("Downloads", container.NewScroll(dl.content)),
	container.NewTabItem("Search",    container.NewScroll(sr.content)),
	container.NewTabItem("Status",    container.NewScroll(st.content)),
	container.NewTabItem("Companion", container.NewScroll(cp.content)),
	container.NewTabItem("Settings",  container.NewScroll(se.content)),
)
tabs.SetTabLocation(container.TabLocationTop)
```
Tab index order is fixed and load-bearing (used by `SelectTab`, shortcuts, and the Delete key guard): **0=Downloads, 1=Search, 2=Status, 3=Companion, 4=Settings.**
11. `guiApp.installShortcuts()` (§3).
12. Builds the main menu (§3).
13. `win.SetContent(tabs)`.
14. `guiApp.setupSystemTray()` (§3).
15. `win.SetCloseIntercept(...)` (§7).
16. Launches the two background goroutines: `go guiApp.notificationLoop(ctx)` and `go guiApp.titleLoop(ctx)` (§6).

### 3. Shortcuts, menu, system tray

**`installShortcuts()`** wires canvas shortcuts (all `desktop.CustomShortcut` with `fyne.KeyModifierControl` except Delete):
- `Ctrl+N` → `a.tabs.SelectIndex(0)` then `a.dl.showAddMagnetDialog()`.
- `Ctrl+F` → `a.tabs.SelectIndex(1)` then `canvas.Focus(a.sr.queryEntry)`.
- `Ctrl+Q` → `a.cancel(); a.fyne.Quit()`.
- `Delete` (bare key via `canvas.SetOnTypedKey`) → only when `a.tabs.SelectedIndex()==0`: `a.dl.removeSelected()`.

Comment notes macOS Cmd is not separately mapped — Ctrl is the "widely-compatible default."

**Main menu** (`win.SetMainMenu`): a `File` menu (`Add Magnet...` [Ctrl+N], separator, `Find in Search` [Ctrl+F], separator, `Quit` [Ctrl+Q, `IsQuit=true`]) and a `Help` menu (`About SwartzNet` → `showAbout()`). Menu handlers duplicate the canvas-shortcut handlers by design.

**`setupSystemTray()`** — desktop-only via `desk, ok := a.fyne.(desktop.App)` type assertion; no-op otherwise. Tray menu `SwartzNet`: `Show SwartzNet`, `Add Magnet...`, separator, `About`. Sets `SetSystemTrayMenu`, `SetSystemTrayIcon(AppIcon)`, `SetSystemTrayWindow(a.win)`.

### 4. Theme — `internal/gui/theme.go`

`swartzTheme struct{}` implements `fyne.Theme`. It is a **fixed dark theme** (ignores the `ThemeVariant` argument) mirroring the web UI's CSS variables. Only `Color()` is overridden; `Font()`, `Icon()`, `Size()` all delegate to `theme.DefaultTheme()`. The full color map (hex → CSS var comment):

| Fyne color name | value | web var |
|---|---|---|
| Background | `0x0e1116` | `--bg` |
| Button | `0x21262d` | `--surface-2` |
| DisabledButton | `0x161b22` | `--surface` |
| Disabled | `0x8b949e` | `--text-dim` |
| Foreground | `0xc9d1d9` | `--text` |
| Hover | `0x30363d` | `--border` |
| InputBackground | `0x161b22` | `--surface` |
| InputBorder | `0x30363d` | `--border` |
| MenuBackground | `0x1b2028` | `--bg-elev` |
| OverlayBackground | `0x1b2028` | `--bg-elev` |
| PlaceHolder | `0x8b949e` | `--text-dim` |
| Pressed | `0x1f6feb` | `--accent-2` |
| Primary | `0x58a6ff` | `--accent` |
| ScrollBar | `0x30363d` | `--border` |
| Separator | `0x30363d` | `--border` |
| Success | `0x3fb950` | `--good` |
| Error | `0xf85149` | `--bad` |
| Warning | `0xd29922` | `--warn` |
| default | `theme.DefaultTheme().Color(name, theme.VariantDark)` |

All alpha `0xff`. Rebuild should carry this table over verbatim (it's the visual contract with the web UI).

### 5. Icon — `internal/gui/icon.go`

```go
//go:embed assets/Icon.png
var iconBytes []byte
var AppIcon = fyne.NewStaticResource("Icon.png", iconBytes)
```
Embedded static resource named `"Icon.png"`, used for `a.SetIcon`, `win.SetIcon`, and the tray icon. The rebuild needs `internal/gui/assets/Icon.png` present at compile time (`//go:embed` fails the build if missing).

`internal/gui/window.go` holds one helper, `windowForObject(obj fyne.CanvasObject) fyne.Window`, which walks `app.Driver().AllWindows()` and matches on `Canvas()` identity (falls back to the first window, or nil on an empty driver). It's a shared "find my window" utility for dialogs raised from tab code; no daemon interaction.

### 6. Background goroutines / refresh cadence

Two goroutines are owned by the App (both stop when the GUI-local `ctx` is cancelled via `a.cancel`):

- **`titleLoop(ctx)`** — `time.NewTicker(2 * time.Second)`. Each tick sums `s.DownloadRate + s.UploadRate` over `a.daemon.Eng.TorrentSnapshots()`. Title base is `"SwartzNet " + a.version`; when both rates are zero the title is just the base, else `fmt.Sprintf("%s  —  ↓ %s/s   ↑ %s/s", base, humanBytes(totalDown), humanBytes(totalUp))`. **Only calls `win.SetTitle` when the string actually changed** (comment: frequent title churn makes some WMs throttle interactive resize). The `SetTitle` is wrapped in `fyne.Do(func(){...})` for thread-safety. `humanBytes` lives in `downloads.go` (shared helper — cross-file dependency for the rebuild).
- **`notificationLoop(ctx)`** — `time.NewTicker(3 * time.Second)`. Each tick calls `a.daemon.Eng.TorrentSnapshots()` and passes them to `a.pollNotifications(snaps, primed)`, sending each returned `*fyne.Notification` via `a.fyne.SendNotification(n)`. `primed` starts false; **on the first poll it seeds `lastNotified` for every already-`"seeding"` torrent WITHOUT notifying** (restored-from-session torrents shouldn't fire a "Download complete" toast), then sets `primed=true`.
  - `pollNotifications(snaps []engine.TorrentSnapshot, primed bool) []*fyne.Notification`: for each snapshot with `s.Status == "seeding"` not already in `lastNotified`, records it; if `primed` it emits `&fyne.Notification{Title: "Download complete", Content: s.Name}`. Also **prunes** `lastNotified` entries whose infohash is no longer present, to bound map growth. Extracted specifically to be unit-testable without a live torrent.

Cadence summary: **title = 2s, notifications = 3s** (app.go). The Downloads tab has its own 2s poll (in downloads.go — other reader); the comment in `titleLoop` says the 2s cadence is chosen to "match the Downloads tab's own polling so the two views stay in sync."

### 7. Lifecycle / shutdown — single Daemon ownership

**The GUI does NOT own the Daemon lifecycle.** Ownership sits in the shim (`cmd/swartznet-gui/main.go`) which does `defer d.Close()`. Flow:

- `main()` builds the daemon, calls `app := gui.New(d, ...)`, `app.Run()` (which blocks on `a.win.ShowAndRun()`), then `app.Cleanup()`; the deferred `d.Close()` fires when `run()` returns.
- **`App.Run()`** = `a.win.ShowAndRun()`.
- **`App.Cleanup()`** = `a.cancel()` — only cancels the GUI-local ctx (stops the two goroutines and tab loops). It never touches the daemon.
- **`win.SetCloseIntercept`**: persists window size (`prefs.SetFloat("window.width"/"window.height", ...)`), then branches: if the app is a `desktop.App`, `win.Hide()` (minimize to tray, daemon keeps running); otherwise `a.cancel(); win.Close()` (real teardown → `ShowAndRun` returns → main's `defer d.Close()`).
- `Ctrl+Q` / File→Quit both do `cancel(); a.Quit()` → `ShowAndRun` returns → `d.Close()` in main.

Net: exactly one `daemon.New` and one `d.Close`, both in the shim; the GUI only manages its own goroutine ctx and window prefs. This satisfies the Slice-11 "no independent lifecycle" requirement — **preserve this split in the rebuild** (daemon constructed and closed in the shim, GUI given the handle).

### 8. `SelectTab(name string)`

Case-insensitive, whitespace-trimmed name → `a.tabs.SelectIndex(n)`: `downloads`→0, `search`→1, `status`→2, `companion`→3, `settings`→4; unknown names silently ignored. Used by the `--tab` startup flag.

### 9. The About dialog — §6 DEFECT (must fix)

`showAbout()` builds a `widget.NewForm` with rows:
- `"Version"` → `widget.NewLabel(a.version)`
- `"Built"` → `widget.NewLabel(buildDate)` where empty `buildDate` becomes `"(dev build)"`
- `"Identity"` → `copyableValue(pubKey)` from `a.daemon.Eng.Identity().PublicKeyHex()`
- `"BitTorrent port"` → `copyableValue(port)` from `a.daemon.Eng.LocalPort()` (else `"unknown"`)
- `"HTTP API"` → `copyableValue(apiAddr)` from `a.daemon.API.Addr()` (else `"disabled"`)
- `"License"` → `widget.NewLabel("MPL 2.0 (engine) + MIT (SwartzNet code)")`

Shown via `dialog.ShowCustom("About SwartzNet", "Close", content, a.win)`.

**§6 DEFECT — the License string is wrong.** Legacy shows exactly:
> `"MPL 2.0 (engine) + MIT (SwartzNet code)"`

The rebuild MUST show **Apache-2.0** for first-party SwartzNet code (the repo's stated license per CLAUDE.md: "New files in this repo stay Apache 2.0"). It MAY note the MPL-2.0 `anacrolix/torrent` dependency separately. Suggested rebuild string form: first-party = `Apache-2.0`, with an optional note like `(embeds anacrolix/torrent, MPL-2.0)`. Do not call the first-party code "MIT".

`copyableValue(value string)` — renders the value `Label` next to a low-importance `theme.ContentCopyIcon()` button that writes `value` to `fyne.CurrentApp().Clipboard().SetContent(value)`. Suppresses the Copy button when the value is `""`, `"unknown"`, or `"disabled"` (returns the bare Label). Reason: Fyne text Labels aren't selectable, so a 64-char pubkey would otherwise be un-copyable.

### 10. Version / license single-source — DEFECT (three sources, must collapse to one)

The DoD requires "version + license come from ONE build-stamped source." Legacy has **three** version sources and a split license:

1. `cmd/swartznet-gui/main.go`: `var Version = "v0.8.0"` and `var BuildDate = ""`, both overridden by ldflags. `scripts/build-gui.sh` stamps them:
   ```
   -ldflags "-s -w -X main.Version=$VERSION -X main.BuildDate=$BUILD_DATE"
   ```
   with `BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"` and `VERSION="${1:-dev}"`. This `Version` flows two ways: `daemon.New(..., Version: Version)` **and** `gui.New(d, Version, BuildDate)` → the window title, About "Version" row, and title-loop base string.
2. `cmd/swartznet-gui/FyneApp.toml`:
   ```toml
   [Details]
     Icon = "Icon.png"
     Name = "SwartzNet"
     ID = "net.swartznet.gui"
     Version = "0.3.0"
     Build = 1
   ```
   **`Version = "0.3.0"` is a second, stale, independent version** (the ldflags default is `v0.8.0`; the running About dialog would say `v0.8.0` while the packaged metadata says `0.3.0`). The rebuild should either drive this from the same build stamp or acknowledge it's Fyne-packaging metadata only. `Name`/`ID`/`Icon` here must stay consistent with `app.NewWithID("net.swartznet.gui")`, the window title, and `assets/Icon.png`.
3. The License string is hard-coded inline in `showAbout()` (the §6 defect). Rebuild: source version **and** license from one build-stamped location (e.g. a single `buildinfo` var block or the daemon) and render both the About dialog and the title from it.

### 11. The CLI shim — `cmd/swartznet-gui/main.go`

`main()` → `os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))`. `run` uses a `flag.NewFlagSet("swartznet-gui", flag.ContinueOnError)` writing errors to stderr; parse failure returns `2`. Flags:

| flag | var / default | meaning |
|---|---|---|
| `-data-dir` | `dataDir` "" | data dir for downloaded content |
| `-index-dir` | `indexDir` "" | Bleve index dir |
| `-port` | `port` `-1` | listen port (`0`=OS-assigned; `-1` sentinel = don't override) |
| `-no-dht` | `noDHT` false | disable mainline DHT entirely |
| `-no-dht-publish` | `noDHTPublish` false | join DHT but don't publish BEP-44 |
| `-api-addr` | `apiAddr` `"localhost:7654"` | HTTP API listen addr (empty disables) |
| `-torrent` | `loadFiles` (repeatable `torrentFileFlag`) | load a `.torrent` at startup |
| `-tab` | `startTab` "" | open a tab at startup (`downloads\|search\|status\|companion\|settings`) |

Config assembly: `cfg := config.Default()`, then **layer GUI-saved overrides**: `config.LoadUserOverrides(config.DefaultUserConfigPath())` returns `savedData, savedIndex` which override `cfg.DataDir`/`cfg.IndexDir` if non-empty (a load error is a non-fatal `warning:` to stderr). **Then CLI flags win** over the saved file: `dataDir`/`indexDir` override if non-empty; `port>=0` sets `cfg.ListenPort`; `cfg.DisableDHT = noDHT`; `cfg.DisableDHTPublish = noDHTPublish`.

Logging: `newLogger(stderr)` → `slog.NewTextHandler` at level from `SWARTZNET_LOG` env (`debug`/`warn`/`error`, default `info`).

Signal context: `ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` — this ctx is passed to `daemon.New` (so SIGINT/SIGTERM tears the daemon down).

Daemon construction:
```go
d, err := daemon.New(ctx, daemon.Options{
	Cfg:     cfg,
	Log:     log,
	APIAddr: apiAddr,
	Version: Version,
	Stderr:  stderr,
})
// err → "swartznet-gui: %v" to stderr, return 1
defer d.Close()
```
If `d.API != nil`, prints `"HTTP API listening on %s\n"` (`d.API.Addr()`) to stdout.

Startup torrent load: for each `--torrent` path, `d.Eng.AddTorrentFile(path)` → on error `warning: load %s: %v`, on success `Loaded %s`.

GUI handoff & run:
```go
app := gui.New(d, Version, BuildDate)
if startTab != "" { app.SelectTab(startTab) }
app.Run()
app.Cleanup()
return 0
```

`torrentFileFlag []string` implements `flag.Value` (`String()`, `Set()` appends).

### 12. Notes / flags for the rebuild (Daemon-routing & API drift)

- **`Eng.TorrentSnapshots()` won't exist in the rebuilt engine.** `titleLoop`, `notificationLoop`, and (per the tab readers) the Downloads tab all iterate `a.daemon.Eng.TorrentSnapshots()` returning `[]engine.TorrentSnapshot` with fields `.DownloadRate`, `.UploadRate`, `.Status` (string, e.g. `"seeding"`), `.InfoHash`, `.Name`. The rebuilt `d.Eng` exposes `Torrents() []*Handle` (plus `PauseTorrent/ResumeTorrent/RemoveTorrent`, etc.). The rebuild must derive the same per-torrent snapshot (rate/status/name/infohash) from `*Handle` — either add an engine snapshot accessor or read it off the Handle. This is the one real API-drift point; still routed through `d.Eng`, so no §5 violation, just a signature change.
- **`--torrent` startup load** goes through `d.Eng.AddTorrentFile` — correct (through the Daemon's engine). The rebuild's `d.Eng` has `AddMagnet/AddTorrentFile/AddInfoHash`, so this maps directly.
- **All About-dialog data** (`Eng.Identity().PublicKeyHex()`, `Eng.LocalPort()`, `API.Addr()`) is read through Daemon-exposed subsystems — no direct package reach-around. Keep it that way.
- **No §5 violation found:** the GUI constructs no engine/indexer/companion/api itself, owns no reconciliation, and delegates all confirm/flag/search to the tabs (which per the other readers must call `d.Confirm`/`d.Flag`/`searchmux.Mux`). The App layer itself only touches `d.Eng` and `d.API` for read-only display + the startup torrent load. The single behavioral defect at this layer is the About **License** string (§9) and the multi-source **Version** (§10, incl. stale `FyneApp.toml` `0.3.0`).
- **GUI-local ctx vs daemon ctx:** the App makes its own `context.WithCancel(context.Background())` for goroutine/tab lifetime, independent of the daemon's `signal.NotifyContext`. This is correct (presentation-local) and should be preserved — do not fold GUI goroutine cancellation into the daemon ctx, and do not close the daemon from the GUI.

---

I have everything needed. Producing the section.

## downloads-create

Scope: legacy `internal/gui/downloads.go`, `internal/gui/create.go`, `internal/gui/context_menu.go`, `internal/gui/files_dialog.go`, plus the engine snapshot/priority types they render (`internal/engine/engine.go`, `internal/engine/files.go`). This is the **Downloads tab**, add/create flows, per-torrent context actions, and the per-file priority dialog.

---

### 1. The `downloadsTab` struct (state object)

```go
type downloadsTab struct {
    content fyne.CanvasObject          // the built Border layout (toolbar top, body center)
    d       *daemon.Daemon             // the ONE dependency — all actions go through here

    mu    sync.RWMutex                 // guards snaps + selection + sort state
    snaps []engine.TorrentSnapshot     // last poll result, re-sorted in place

    table *widget.Table                // widget.NewTableWithHeaders

    selectedKey  string                // primary selection, keyed by INFOHASH (not row index)
    selectedSet  map[string]struct{}   // multi-selection set, keyed by infohash
    emptyState   *fyne.Container       // "No torrents yet" overlay, Show/Hide in pollLoop
    sortCol      int                   // -1 = engine insertion order (FIFO by add-time)
    sortDesc     bool
    selectionLbl *widget.Label         // "(N selected)" italic toolbar label
}
```

Key design invariant repeated in comments: **selection is keyed by infohash, never row index**, because `pollLoop` re-sorts `dl.snaps` every 2s and a stored index would silently point at a different torrent between click and action ("catastrophic for Remove"). The rebuild must preserve this.

Constructors: `newDownloadsTab(ctx, d)` builds the tab **and** spawns `go dl.pollLoop(ctx)`; `buildDownloadsTab(d)` builds it **without** the goroutine (tests use this to avoid racing widget ops under `-race`).

Test seams (nil in production, set only under the Fyne test driver): package vars `afterRemoveSelected`, `afterAddMagnet` (downloads.go), `afterCreateTorrent` (create.go), `afterSetAllPriorities` (files_dialog.go). Each is invoked as the last action of its async goroutine so tests can join it. The rebuild can keep or drop these; they change no production timing.

---

### 2. The torrent table

Built with `widget.NewTableWithHeaders`. **9 columns**, defined by `var dlColumns`:

| Col | Header string | minWidth | Cell content |
|-----|--------------|----------|--------------|
| 0 | `"Name"` | 250 | `s.Name`; if empty → `s.InfoHash[:16] + "..."`; prefixed `"✓ "` when in `selectedSet` |
| 1 | `"Status"` | 90 | `s.Status` verbatim (engine string) |
| 2 | `"Progress"` | 100 | `fmt.Sprintf("%.1f%%", s.Progress*100)` |
| 3 | `"Size"` | 90 | `humanBytes(s.Size)` |
| 4 | `"Peers"` | 60 | `fmt.Sprintf("%d", s.ActivePeers)` |
| 5 | `"↓ speed"` | 90 | `rateStr(s.DownloadRate)` |
| 6 | `"↑ speed"` | 90 | `rateStr(s.UploadRate)` |
| 7 | `"Indexed"` | 70 | `s.Indexing` → `"yes"` / `"no"` |
| 8 | `"Signed"` | 100 | `SignedBy==""` → `"—"`; trusted → `"★ " + SignedBy[:8]`; else `"✓ " + SignedBy[:8]` |

Cell factory creates a `widget.NewLabel("placeholder text here")` with `lbl.Truncation = fyne.TextTruncateEllipsis` (comment: prevents a long Name painting past its column and overprinting the next column — the overlap once seen in `scree.png`). `UpdateCell` takes `dl.mu.RLock()`, guards `id.Row >= len(dl.snaps)` (sets `""`), then switches on `id.Col`.

**Header rendering** (`CreateHeader` returns a `widget.NewLabel("Header")`; `UpdateHeader`): row-header cells (`id.Col == -1`) are blanked so the placeholder "Header" text doesn't show; column headers (`id.Row == -1`) are bold, and the active sort column gets `" ▼"` (desc) or `" ▲"` (asc) appended.

Column widths set via `dl.table.SetColumnWidth(i, col.minWidth)` in a loop.

#### Status vocabulary (from the engine, NOT the GUI)

`TorrentSnapshot.Status` is set in `engine.go`. The complete vocabulary is **five strings**: `"metadata"`, `"downloading"`, `"seeding"`, `"paused"`, `"queued"` (the doc comment on the field lists `"metadata"/"downloading"/"seeding"/"complete"/"paused"`, but `"complete"` is never assigned — `missing==0 && size>0` yields `"seeding"`; this is a doc/code drift worth fixing in the rebuild). Derivation:
- No metadata (`t.Info()==nil`): `"metadata"`, overridden by `paused`→`"paused"`, else `queued`→`"queued"`.
- With metadata, precedence: `paused`→`"paused"`; `missing==0 && size>0`→`"seeding"`; `queued`→`"queued"`; `completed>0`→`"downloading"`; default `"downloading"`.

The GUI renders `s.Status` verbatim — the rebuild's Status column must render the engine string as-is, not compute its own.

#### Number formatting helpers (downloads.go, must be reproduced identically)

- `rateStr(bps int64)`: `bps<=0` → `"—"` (so idle torrents don't show "0 B/s"); else `humanBytes(bps) + "/s"`.
- `humanBytes(n int64)`: `< 1024` → `"%d B"`; else `"%.1f %ciB"` with binary prefixes `"KMGTPE"` (e.g. `"12.3 MiB"`).
- Progress: `"%.1f%%"` (one decimal, e.g. `"42.7%"`).

---

### 3. Selection model

`OnSelected` handler:
- Header click (`id.Row == -1`) → `dl.toggleSort(id.Col)` and return.
- Row click → **toggles** membership in `selectedSet` (Fyne doesn't surface Ctrl/Shift through `OnSelected`, so every tap is a toggle; paired with explicit "Select All"/"Clear" buttons) and sets `selectedKey = ih`. Then `refreshSelectionLabel()` + `table.Refresh()`.

`actionTargets() []string` — the resolver every bulk action uses: returns `selectedSet` in **display order** (walks `snaps`) when non-empty, else the single `resolveSelectedKeyLocked()` primary, else nil. `resolveSelectedKeyLocked()` resolves `selectedKey` against current `snaps` and returns `""` if that torrent is gone — so a stale selection can never drive an action.

`selectAll()` adds every displayed infohash; `clearSelection()` resets the set to a fresh map. `refreshSelectionLabel()` sets the label to `""` (n==0) or `fmt.Sprintf("(%d selected)", n)`.

`toggleSort(col)` cycles: different col → ascending; active col → descending; already-descending → clear (`sortCol=-1`). `sortSnapsLocked()` / `snapLess(col, desc)` / `sortSnapsSlice` implement a stable insertion sort with a per-column comparator (Name/Status/Progress/Size/Peers/rates lexical or numeric; Indexed sorts un-indexed first then by name; Signed sorts signed-first then by pubkey).

---

### 4. Refresh / polling cadence

`pollLoop(ctx)`: `time.NewTicker(2 * time.Second)`. Each tick calls **`dl.d.Eng.TorrentSnapshots()`** (off the UI thread), then inside `fyne.Do(...)`: takes the write lock, replaces `dl.snaps`, calls `sortSnapsLocked()`, drops `selectedKey` if its torrent vanished, unlocks, `table.Refresh()`, and toggles the empty-state overlay (`emptyState.Show()` when `len(snaps)==0`, else `Hide()`). Cadence to preserve: **2s**, one engine snapshot call per tick, all widget mutation inside `fyne.Do`.

The files dialog (below) also polls at **2s**.

---

### 5. Toolbar (top of the Border layout)

`container.NewHBox` of buttons (exact labels + icons + handler):

| Label | Icon | Handler |
|-------|------|---------|
| `"Add Magnet"` | `theme.ContentAddIcon()` | `dl.showAddMagnetDialog()` |
| `"Add .torrent"` | `theme.FolderOpenIcon()` | `dl.showAddFileDialog()` |
| `"Create Torrent"` | `theme.DocumentCreateIcon()` | `createTorrentDialog(dl.d, dl.win())` |
| — separator — | | |
| `"Pause"` | `theme.MediaPauseIcon()` | `dl.pauseSelected()` |
| `"Resume"` | `theme.MediaPlayIcon()` | `dl.resumeSelected()` |
| `"Remove"` | `theme.DeleteIcon()` | `dl.removeSelected()` |
| — separator — | | |
| `"Files..."` | `theme.StorageIcon()` | `dl.showFilesForSelected()` |
| `"Toggle Index"` | `theme.SearchIcon()` | `dl.toggleIndexSelected()` |
| — separator — | | |
| `"Select All"` | `theme.ContentCopyIcon()` | `dl.selectAll()` |
| `"Clear"` | `theme.ContentClearIcon()` | `dl.clearSelection()` |
| `selectionLbl` (italic) | | shows `"(N selected)"` |

Body: `container.NewStack(tableWithMenu, dl.emptyState)`, wrapped `container.NewBorder(toolbar, nil, nil, nil, body)`. `tableWithMenu = newRightClickCapture(dl.table, dl.buildContextMenu)`.

Empty-state overlay: `container.NewCenter(container.NewVBox(emptyLabel, emptyHint))`, hidden by default; strings — bold centered `"No torrents yet"` and centered hint `"Add a magnet link, import a .torrent file, or create a new torrent from local content."`.

#### Engine methods behind each bulk action (all iterate `actionTargets()` in a goroutine)

- `pauseSelected()` → `dl.d.Eng.PauseTorrent(ih)`
- `resumeSelected()` → `dl.d.Eng.ResumeTorrent(ih)`
- `toggleIndexSelected()` → reads each torrent's current `Indexing` under lock, then `dl.d.Eng.SetTorrentIndexing(ih, !state[ih])`
- `removeSelected()` → confirm dialog first (see below), then `dl.d.Eng.RemoveTorrent(ih)` per target, then clears selection inside `fyne.Do`
- `showFilesForSelected()` → resolves name, calls `showFilesDialog(dl.d, dl.win(), ih, name)`

**Remove confirm dialog** — `dialog.ShowConfirm("Remove torrent?", body, cb, win)`. Body (single): `Remove "<name>" from the download list and stop seeding/leeching?\n\nDownloaded files on disk are kept; the torrent entry in your list is removed.`; body (bulk): `Remove %d torrents from the download list and stop seeding/leeching?\n\nDownloaded files on disk are kept; the torrent entries in your list are removed.` Comment documents that `RemoveTorrent` → `t.Drop()` does **not** delete on-disk files or purge Bleve docs. This is a plain UI confirm modal (not the spam `d.Confirm`/`d.Flag` path).

---

### 6. Right-click context menu (context_menu.go + `buildContextMenu`)

`rightClickCapture` (context_menu.go) is a `widget.BaseWidget` implementing `fyne.SecondaryTappable`; it wraps the table and, on `TappedSecondary`, rebuilds the menu via the passed `menuBuilder` and calls `widget.ShowPopUpMenuAtPosition(menu, canvas, ev.AbsolutePosition)`. It exists because `widget.Table` has no per-cell secondary-tap event; the gesture is "right-click the table after selecting a row." No Daemon interaction — pure widget plumbing; reusable verbatim.

`buildContextMenu()` returns `nil` when nothing selected. Otherwise it finds the primary snapshot and computes `bulkN = len(selectedSet)`. When `bulkN >= 2` a `bulkSuffix = fmt.Sprintf(" (%d selected)", bulkN)` is appended to Pause/Resume/Remove/index labels. Menu title `"Torrent actions"`, items:

1. `"Files..."` → `showFilesForSelected()`
2. separator
3. `"Pause"`/`"Resume"` (+suffix) — label & action flip on `snap.Paused` → `pauseSelected()`/`resumeSelected()`
4. `"Remove"`(+suffix) → `removeSelected()`
5. separator
6. `"Stop indexing"`/`"Start indexing"` (+suffix) — flips on `snap.Indexing` → `toggleIndexSelected()`
7. **Only if `snap.Queued`**: separator + `"Move to top of queue"` → `go dl.d.Eng.QueueMoveToFront(ih)`; `"Move to bottom of queue"` → `go dl.d.Eng.QueueMoveToBack(ih)`
8. separator, `"Copy magnet link"` → `fyne.CurrentApp().Clipboard().SetContent(magnetLink(ih, snap.Name))`; `"Copy infohash"` → clipboard `ih`
9. **Only if `snap.SignedBy != ""`**: separator + signature items (below)

`magnetLink(infoHash, name)` builds `"magnet:?xt=urn:btih:" + infoHash` and appends `"&dn=" + url.QueryEscape(name)` when name set. The `url.QueryEscape` is a **security control** (torrent names are attacker-controlled from DHT/peers; an unescaped `&`/`=` could inject bogus `tr=` trackers). Keep it.

**Signature sub-items** (when signed):
- `"Verify signature..."` → `showSignatureDialog(snap)`
- If `snap.TrustedPublisher`: `"Revoke trust for this publisher"` → `dl.d.Eng.TrustStore().Remove(signer)`
- else: `"Trust this publisher"` → `dl.d.Eng.TrustStore().Add(signer, "")`
- `"Copy publisher pubkey"` → clipboard `signer`

`showSignatureDialog(snap)` — a `dialog.ShowCustom("Signature verified", "Close", form, win)` with a `widget.NewForm` of: `"Torrent"`=name, `"InfoHash"`=infohash, `"Publisher pubkey"`=SignedBy, `"Trust status"`= `"✓ trusted"` (bold) or `"untrusted"`, `"Trust label"`= `dl.d.Eng.TrustStore().Label(SignedBy)` or `"—"`.

> **§5/§6 DEFECT TO FIX (direct subsystem reach).** The trust actions reach **straight into the engine's TrustStore** — `dl.d.Eng.TrustStore().Remove(signer)`, `.Add(signer, "")`, `.Label(signer)`. This bypasses the Daemon's shared reputation/trust path. Per the Slice-11 contract, trust/confirm/flag must move the **same** state as the web UI **through the Daemon** (`d.Confirm`/`d.Flag`, and the Daemon's publisher/trust surface). The rebuilt GUI must not call `Eng.TrustStore()` directly; route add/remove/label/verify through a Daemon-level method (e.g. the same `d.Eng.PublisherStatus`/trust API the web UI uses), so GUI-driven trust changes are observable identically to the web UI.

---

### 7. Add-magnet flow

`showAddMagnetDialog()` → `showAddMagnetDialogPrefilled("", true)`.

`showAddMagnetDialogPrefilled(prefill, indexChecked)` builds a `dialog.NewForm("Add Magnet URI", "Add", "Cancel", items, submit, win)`, resized `fyne.NewSize(500, 180)`:
- Entry with placeholder `"magnet:?xt=urn:btih:..."`, single-line, pre-filled with `prefill`.
- `widget.NewCheck("Index this torrent's files after download", nil)`, initialized to `indexChecked`.

`submit(ok)`:
1. Trim URI; empty → `showAddMagnetError(dl, "paste a magnet URI starting with \"magnet:?xt=urn:btih:\"", uri, shouldIndex)`.
2. Client-side `validateMagnetURI(uri)`: no `"magnet:?"` prefix → `"magnet URI must start with \"magnet:?\" — did you paste a regular URL?"`; missing `"xt=urn:btih:"` → `"magnet URI is missing the \"xt=urn:btih:\" infohash parameter"`.
3. In a goroutine: **`ih, err := dl.d.Eng.AddMagnetURI(uri)`**. On error → `showAddMagnetError(dl, "Could not add this magnet: "+friendlyAddErr(err), uri, shouldIndex)` inside `fyne.Do`. On success, if `!shouldIndex` → **`dl.d.Eng.SetTorrentIndexing(ih, false)`** immediately (comment: flip before `autoIndex`'s 5-minute metadata wait fires on GotInfo).

`friendlyAddErr(err)` rewrites common engine strings: contains `"zero infohash"` → "the magnet URI's infohash is all zeros — it needs a real 40-character btih value"; `"parse magnet"` → "the magnet URI is malformed and couldn't be parsed"; `"closed"` → "the engine is shutting down; try again after restart"; else passthrough.

`showAddMagnetError(dl, msg, uri, indexChecked)` shows `dialog.NewInformation("Add Magnet failed", msg, win)` whose `SetOnClosed` **re-opens the dialog prefilled** with the bad URI + checkbox state (so a one-char typo is editable, not re-pasted).

> Rebuild API note: the method is **`AddMagnetURI`** in legacy; the rebuilt Daemon spec names it `AddMagnet`. Rebuild should call `d.Eng.AddMagnet(uri)` and keep the same validate→add→conditional-`SetTorrentIndexing` sequence.

### 8. Add-.torrent flow

`showAddFileDialog()` → `dialog.NewFileOpen(cb, win)` with `fd.SetFilter(&torrentFilter{})` (`torrentFilter.Matches` = `uri.Extension() == ".torrent"`). On pick: `path := reader.URI().Path()`, `reader.Close()`, then in a goroutine **`dl.d.Eng.AddTorrentFile(path)`**; error → `dialog.ShowError(err, win)` in `fyne.Do`. Comment: `.torrent` adds default to indexing = on (no prompt).

---

### 9. Create-torrent dialog (create.go)

`createTorrentDialog(d *daemon.Daemon, win)` — a `dialog.NewCustomConfirm("Create Torrent", "Create", "Cancel", scroll, cb, win)` resized `fyne.NewSize(650, 600)`; inner `VScroll` min `fyne.NewSize(600, 500)`. Three `widget.NewCard`s:

**Card "Source"**: label `"Root (file or folder to share)"` + `rootRow` (entry placeholder `"/path/to/file-or-folder"` with `"Choose File..."`/`"Choose Folder..."` buttons on the right). `"Choose File..."` → `dialog.NewFileOpen`; `"Choose Folder..."` → `dialog.NewFolderOpen`. Then label `"Torrent name (becomes the top-level folder for downloaders)"` + `nameEntry` (placeholder `"Torrent display name — edit to rename"`).

Autofill behavior: picking/typing a root fires `autofillName` (basename into Name) and `autofillOutput` (`<root>.torrent` into Output), each only overwriting when the field is empty or still equals the last auto-fill (so user edits survive re-browsing). `rootEntry.OnChanged` calls both.

**Card "Pieces & Metadata"**:
- `"Piece length"` → `widget.NewSelect(pieceLengthLabels(), nil)`, default `"Auto"`. Options `var pieceLengthOptions`: `"Auto"`=0 (auto → `metainfo.ChoosePieceLength`), `"64 KiB"`, `"256 KiB"`, `"1 MiB"`, `"2 MiB"`, `"4 MiB"`, `"8 MiB"`, `"16 MiB"`. `pieceLengthFromLabel` maps back to bytes (default 0).
- `"Trackers"` → multiline entry, placeholder `"One tracker URL per line (optional — empty = DHT-only)"`, `SetMinRowsVisible(3)`.
- `"Webseeds"` → multiline, placeholder `"Optional: HTTP(S) webseed URLs, one per line (BEP-19)"`, `SetMinRowsVisible(2)`.
- `"Comment"` → entry, placeholder `"Optional human-readable comment"`.
- `privateCheck` = `widget.NewCheck("Private torrent (disable DHT/PEX discovery, BEP-27)", nil)`.
- `signCheck` = `widget.NewCheck("Sign with my ed25519 identity (SwartzNet downloaders can verify publisher)", nil)`, **default checked** (comment: every node has an identity, signing is ~free).

**Card "Output"**: label `"Output .torrent path"` + `outRow` (entry placeholder `"/path/to/output.torrent"` + `"Save As..."` button). `"Save As..."` → `dialog.NewFileSave` that, on pick, sets `outEntry`, **closes the Fyne writer without writing and `storage.Delete`s the empty file** it created (comment: the real `CreateTorrentFile` path does the atomic rename itself). Default Save-As filename = basename of `outEntry.Text` or `"new.torrent"`. Then `seedCheck` = `widget.NewCheck("Start seeding immediately after creation", nil)`, **default checked**.

**Submit (`ok`)**: require non-empty root (`dialog.ShowError(fmt.Errorf("root path required"), win)`); defensively autofill empty output to `<root>.torrent`. Build:
```go
opts := engine.CreateTorrentOptions{
    Root, Name, PieceLength: pieceLengthFromLabel(pieceSelect.Selected),
    Trackers: splitLines(trackersEntry.Text), WebSeeds: splitLines(webseedsEntry.Text),
    Private: privateCheck.Checked, Comment: strings.TrimSpace(commentEntry.Text),
}
if signCheck.Checked {
    if id := d.Eng.Identity(); id != nil { opts.SignWith = id.PrivateKey }
}
runCreateTorrent(d, win, opts, outPath, seedCheck.Checked)
```
`splitLines` trims and drops blank lines.

**`runCreateTorrent`**: shows `dialog.NewCustomWithoutButtons("Hashing pieces...", VBox("Reading "+opts.Root, ProgressBarInfinite, "Large torrents can take several minutes."), win)`. In a goroutine: **`ih, mi, err := d.Eng.CreateTorrentFile(opts, outPath)`**; inside `fyne.Do` hide progress, on error `dialog.ShowError`; else build message `"Created:\n  <outPath>\n\nInfoHash:\n  <ih>"`, and if `andSeed && mi != nil` call **`d.Eng.AddTorrentMetaInfoSeedFrom(mi, opts.Root)`** (comment: seed from the user's source location, keyed on real basename, so content isn't invisible under DataDir), appending `"\n\nSeeding started."` or `"\n\nSeed start failed: "+err`. Final `dialog.ShowInformation("Torrent created", msg, win)`.

> **§5 DIRECT-SUBSYSTEM REACH (flag).** Create reaches into engine identity: `d.Eng.Identity()` then `opts.SignWith = id.PrivateKey` — the GUI extracts the raw ed25519 **private key** and hands it into create-options. The rebuild should let the engine's create path resolve the signing identity itself (e.g. an `opts.Sign bool` / a Daemon-level "create signed" call), not have the presentation layer touch key material. This composes with the identity-is-load-bearing rule.

> Rebuild API notes: legacy uses `CreateTorrentFile`, `AddTorrentMetaInfoSeedFrom`, `Identity()` — none of these appear in the Slice-11 Daemon surface (`AddMagnet/AddTorrentFile/AddInfoHash`, `Torrents()`, pause/resume/remove, `SetTorrentIndexing`, rates, `SwarmSearch`, `DHTLookup`, `PublisherStatus`). If the rebuilt engine keeps create/seed, the GUI must call the rebuilt equivalents; if create is out of scope for Slice 11, the Create button + this dialog are the piece to defer. Confirm which create surface exists before wiring the button.

---

### 10. Per-file priority dialog (files_dialog.go)

`showFilesDialog(d, win, infoHashHex, torrentName)`: first **`files, err := d.Eng.TorrentFiles(infoHashHex)`**; error → `dialog.ShowError`; empty → `dialog.ShowInformation("Waiting for metadata", "Torrent metadata has not arrived yet. Try again in a few seconds.", win)`. Else builds a `filesDialog{d, win, infoHashHex, files}`.

`filesDialog` struct: `d *daemon.Daemon`, `win`, `infoHashHex`, `mu sync.RWMutex`, `files []engine.FileSnapshot`, `list *widget.List`, `dlg dialog.Dialog`, `sortBy string`.

`build(torrentName)`:
- `widget.NewList` — each row: `container.NewBorder(nil,nil,nil, HBox(sizeLabel, prioSelect), VBox(nameLabel, progressBar))`. Name label is ellipsis-truncated. Priority select = `widget.NewSelect([]string{"none", "normal", "high"}, nil)`.
- Update binds per row: `nameLbl.SetText(f.DisplayPath)`, `sizeLbl.SetText(humanBytes(f.Length))`, `progress.SetValue(f.Progress)`, `fd.bindPrioSelect(prioSelect, f)`.
- Bulk buttons: `"Select All"` → `setAllPriorities(engine.FilePriorityNormal)`; `"Deselect All"` → `setAllPriorities(engine.FilePriorityNone)`.
- Sort: `widget.NewSelect([]string{"index","path","size","progress","priority"}, ...)` default `"index"`; changing sorts `fd.files` in place (`sortFilesLocked`) and `list.Refresh()`.
- Header VBox: bold `"Files in "+torrentName`, the bulk row (`"Sort by:"` label + select), separator.
- Dialog: `dialog.NewCustom("Torrent Files", "Close", content, win)` resized `fyne.NewSize(760, 580)`; content resized `fyne.NewSize(720, 520)`. Spawns `go fd.pollLoop(ctx)`; `SetOnClosed(cancel)` stops polling.

**`bindPrioSelect(prioSelect, f)`** — subtle and important: sets `prioSelect.OnChanged = nil` **before** `SetSelected(f.Priority)` (Fyne's `Select.SetSelected` unconditionally fires `OnChanged`, and the still-attached handler is the *previous* row's closure capturing the previous file index — leaving it attached would call `SetFilePriority` for the wrong file on every scroll/refresh/re-sort). The fresh handler captures `idx := f.Index` / `current := f.Priority`, skips no-op writes (`selected == current`), and otherwise in a goroutine calls **`fd.d.Eng.SetFilePriority(fd.infoHashHex, idx, engine.FilePriority(selected))`**; error → `dialog.ShowError` in `fyne.Do`. The rebuild's recycled-widget priority binding must reproduce this detach-before-set discipline.

`pollLoop`: 2s ticker → `tickRefresh()` → `d.Eng.TorrentFiles(infoHashHex)`, then `fyne.Do` replaces `fd.files`, `sortFilesLocked()`, `list.Refresh()`.

`sortFilesLocked()`: comparator by `sortBy` — `"path"` (DisplayPath), `"size"` (Length), `"progress"` (Progress), `"priority"` (rank none<normal<high), default `"index"` (Index). Insertion sort.

`setAllPriorities(priority)`: collects all indices under lock, then per index `fd.d.Eng.SetFilePriority(...)`, collecting failures into `"#%d: %v"` and, if any, `dialog.ShowError(fmt.Errorf("some files failed: %v", failed), win)`.

**File priority vocabulary** (`engine/files.go`): `FilePriority` string enum `"none"`/`"normal"`/`"high"` (`FilePriorityNone`/`FilePriorityNormal`/`FilePriorityHigh`), mapping to anacrolix `PiecePriorityNone`/`Normal`/`High` (empty string → Normal). `"none"` = file will not be downloaded. The Select's three options and the sort ranks must match this enum exactly.

---

### 11. Engine methods this reader's surface calls (route ALL through `d.Eng` — the Daemon's engine subsystem)

`TorrentSnapshots()`, `TorrentFiles(ih)`, `AddMagnetURI(uri)`, `AddTorrentFile(path)`, `SetTorrentIndexing(ih,bool)`, `PauseTorrent(ih)`, `ResumeTorrent(ih)`, `RemoveTorrent(ih)`, `QueueMoveToFront(ih)`, `QueueMoveToBack(ih)`, `SetFilePriority(ih,idx,prio)`, `CreateTorrentFile(opts,out)`, `AddTorrentMetaInfoSeedFrom(mi,root)`, `Identity()`, `TrustStore()` (→ `.Add/.Remove/.Label`).

Of these, **`TrustStore()` and `Identity()`/`PrivateKey` are the two direct subsystem reaches to eliminate** (see §6 and §9 flags) — everything else is already a clean `d.Eng.*` method call and satisfies "no independent lifecycle, all actions flow through the Daemon." The GUI holds **no** state the Daemon doesn't own: `snaps`/`files` are ephemeral render caches refreshed from the engine every 2s, selection/sort are pure view state. There is no reconciliation, no confirm/flag logic, no retry/lock logic here — consistent with the Slice-11 §5 requirement.

Not present in these four files: the About/license strings, `FyneApp.toml`, the theme, the app/window struct, and the build shim `cmd/swartznet-gui` (those belong to the about-status / app-shell reader). The only license-adjacent trust surface here is the signature dialog, which is content, not the About box.

---

## search

Legacy source: `internal/gui/search.go` (branch `legacy-snapshot`). Rebuilt target-surface anchors verified in the working tree: `internal/searchmux/searchmux.go` (`Mux.Search(ctx, Query) Result`), `internal/daemon/confirm_flag.go` (`d.Confirm`/`d.Flag`), `internal/engine/{add,layerd,reputation,capability}.go`.

This is the load-bearing DoD surface. Two structural defects (§5) below are the most important output of this reader: **the legacy Search tab does its own three-layer fan-out reconciliation** and **its own confirm/flag reputation logic**, both of which the rebuild must delete and route through `searchmux.Mux` and `d.Confirm`/`d.Flag`.

### `searchTab` struct (widget handles)

```go
type searchTab struct {
    content    fyne.CanvasObject
    d          *daemon.Daemon
    queryEntry *widget.Entry
    localChk   *widget.Check
    swarmChk   *widget.Check
    dhtChk     *widget.Check
    limitEntry *widget.Entry
    searchBtn  *widget.Button
    statusLbl  *widget.Label
    progress   *widget.ProgressBarInfinite
    resultBox  *fyne.Container   // VBox of hit cards
    emptyState *fyne.Container   // centered hint overlay
}
```

Constructor: `newSearchTab(_ context.Context, d *daemon.Daemon) *searchTab`.

### Widget tree (exact)

- **header** = `container.NewVBox(queryRow, optionsRow, statusLbl, progress)`
  - **queryRow** = `container.NewBorder(nil, nil, nil, st.searchBtn, st.queryEntry)` — entry fills, Search button pinned right.
    - `queryEntry`: `widget.NewEntry()`, placeholder `"Search query..."`, `OnSubmitted = func(_ string){ st.runSearch() }` (Enter triggers search).
    - `searchBtn`: `widget.NewButtonWithIcon("Search", theme.SearchIcon(), func(){ st.runSearch() })`.
  - **optionsRow** = `container.NewHBox(st.localChk, st.swarmChk, st.dhtChk, widget.NewLabel("Limit:"), st.limitEntry)`.
    - `localChk` = `widget.NewCheck("Local", nil)`, **`SetChecked(true)`** (default on).
    - `swarmChk` = `widget.NewCheck("Swarm", nil)` — default **off**.
    - `dhtChk` = `widget.NewCheck("DHT", nil)` — default **off**.
    - `limitEntry` = `widget.NewEntry()`, placeholder `"20"`, `SetText("20")`.
  - `statusLbl` = `widget.NewLabel("")`, `TextStyle.Italic = true` — per-layer count line.
  - `progress` = `widget.NewProgressBarInfinite()`, `.Stop()` + `.Hide()` at build.
- **body** = `container.NewStack(container.NewVScroll(st.resultBox), st.emptyState)` — scrollable result VBox with the empty-state hint stacked on top; visibility flipped in `buildResults`.
- **content** = `container.NewBorder(header, nil, nil, nil, body)`.

**Empty-state panel** (`st.emptyState = container.NewCenter(container.NewVBox(emptyTitle, emptyHint))`), both `TextAlignCenter`:
- title (Bold): `"Search across your local index, the swarm, and the DHT"`
- hint: `"Type a query above and press Enter. Toggle Swarm / DHT\nto broaden the search beyond your own index."`

Shown before the first search; re-shown when every enabled layer returns zero cards (`len(st.resultBox.Objects) == 0`). A code comment admits the empty-state text is NOT rebuilt to say "no results for '<q>'" — it just re-shows the generic hint because `container.NewStack` children can't be swapped without re-layout.

### How a query is issued — **§5 DEFECT: independent reconciliation, NOT `searchmux.Mux`**

`runSearch()` does the fan-out itself. It does **not** call `searchmux.Mux` or any daemon search closure. Sequence:

1. `q := strings.TrimSpace(st.queryEntry.Text)`; empty → return.
2. `searchBtn.Disable()`, `statusLbl.SetText("Searching...")`, `progress.Show()` + `.Start()`, `resultBox.RemoveAll()`, `emptyState.Hide()`.
3. `limit := parseSearchLimit(st.limitEntry.Text)`.
4. Snapshots `doLocal/doSwarm/doDHT` from the three checkboxes.
5. Spawns a goroutine that **builds its own `sync.WaitGroup` and its own `context.WithTimeout(context.Background(), 10*time.Second)`**, then fans out to three subsystems directly:
   - Local — guarded by `st.d.Index != nil`:
     ```go
     st.d.Index.Search(indexer.SearchRequest{Query: q, Limit: limit})  // -> *indexer.SearchResponse
     ```
   - Swarm — guarded by `st.d.Eng.SwarmSearch() != nil`:
     ```go
     st.d.Eng.SwarmSearch().Query(ctx, swarmsearch.QueryRequest{
         Q: q, PerPeerLimit: limit, Timeout: 2 * time.Second})     // -> *swarmsearch.QueryResponse
     ```
   - DHT — guarded by `st.d.Eng.Lookup() != nil`:
     ```go
     st.d.Eng.Lookup().Query(ctx, q)                               // -> *dhtindex.LookupResponse
     ```
6. `wg.Wait()`, then `fyne.Do(...)`: re-enable button, stop/hide progress, call `buildResults(q, localResp, localErr, swarmResp, swarmErr, dhtResp, dhtErr)`.
7. Test seam: `var afterRunSearch func()` invoked after the `fyne.Do` returns; nil in production. (A join point for the Fyne test driver — the rebuild's test can keep an equivalent seam.)

**Why this is a defect (§5):** the GUI owns the layer set, the parallelism, the 10s deadline, the per-layer request construction, and per-layer error collection — a second, GUI-private reconciliation that can drift from the web/CLI path. **Rebuild:** issue ONE `d`-level call into `searchmux.Mux.Search(ctx, searchmux.Query{Text:q, Limit:limit, Swarm:doSwarm, DHT:doDHT, Highlight:true, SignedBy:…, Scope:…})` and render the returned `searchmux.Result{Local, LocalErr, Swarm, SwarmErr, DHT, DHTErr}`. The Mux already fans out Local (always), Swarm (when `q.Swarm && wired`), DHT (when `q.DHT && wired`) concurrently and bounds swarm/DHT by the passed ctx deadline — the GUI must stop constructing `indexer.SearchRequest` / `swarmsearch.QueryRequest` / calling `Lookup().Query` itself. Note the Mux's swarm request is `{Q, Scope, Limit}` bounded by ctx, not the legacy `{PerPeerLimit, Timeout: 2s}`.

**Field/method renames the rebuild must apply** (legacy names → rebuilt surface):
- `st.d.Index` → `d.Idx` (`*indexer.Index`; the daemon field is now `Idx`).
- `st.d.Eng.Lookup()` (DHT) → routed via `searchmux`/`d.Eng.DHTLookup()` (engine method is now `DHTLookup`, not `Lookup`).
- `st.d.Eng.AddMagnetURI(magnet)` (menu) → `d.Eng.AddMagnet(uri)` per the rebuilt engine surface (`AddMagnet` returns `(*Handle, error)`; `AddMagnetURI` returning `(string,error)` still exists but the task names `AddMagnet`).

### `parseSearchLimit(text string) int`

Strict: `TrimSpace`; empty → default `20`; `strconv.Atoi`, and `err != nil || n <= 0` → `20`; else `n`. A comment records the fix history: prior Sscanf-lax parsing let negatives and `"5; rm"` through. Keep the strict version.

### `buildResults(...)` — per-layer rendering and the status line

`resultBox.RemoveAll()`, then builds a `parts []string` status list per layer and appends cards. **Never merges layers — each layer keeps its native response type and gets its own card constructor.**

- **Local** (`localResp != nil`): status part `fmt.Sprintf("Local: %d hits", localResp.Total)`; for each `h` in `localResp.Hits` add `st.makeLocalHitCard(h)`. Else if `localErr != nil`: `fmt.Sprintf("Local: error: %v", localErr)`.
- **Swarm** (`swarmResp != nil`): status part `fmt.Sprintf("Swarm: %d hits (asked=%d, responded=%d)", len(swarmResp.Hits), swarmResp.Asked, swarmResp.Responded)`; each `h` → `makeSwarmHitCard(h)`. Else `fmt.Sprintf("Swarm: error: %v", swarmErr)`.
- **DHT** (`dhtResp != nil`): status part `fmt.Sprintf("DHT: %d hits (indexers=%d/%d)", len(dhtResp.Hits), dhtResp.IndexersResponded, dhtResp.IndexersAsked)`; each `h` → `makeDHTHitCard(h)`. Else `fmt.Sprintf("DHT: error: %v", dhtErr)`.
- If `len(parts) == 0`: `parts = append(parts, "No search layers enabled")`.
- `st.statusLbl.SetText(strings.Join(parts, "  |  "))` (separator is two-spaces-pipe-two-spaces).
- If `len(st.resultBox.Objects) == 0` → `emptyState.Show()`, else `emptyState.Hide()`; `resultBox.Refresh()`.

Counts shown per layer, exactly: **Local = `Total`** (index-reported total, not `len(Hits)`); **Swarm = `len(Hits)` plus `asked`/`responded`**; **DHT = `len(Hits)` plus `indexers=responded/asked`**.

### Hit cards — exact labels/subtitles

Each card is `widget.NewCard(title, subtitle, actions)` where `title = h.Name` or (if empty) the infohash, `actions = container.NewHBox(confirmBtn, flagBtn)` (both `widget.NewButton`, labels **`"Confirm"`** and **`"Flag"`**), wrapped by `wrapHitMenu(...)`.

- **`makeLocalHitCard(h indexer.SearchHit)`** subtitle:
  - base `fmt.Sprintf("[%s] %s  score=%.2f", h.DocType, h.InfoHash[:16], h.Score)`.
  - if `h.DocType == "content"`: append `fmt.Sprintf("  file=%s", h.FilePath)`.
  - if `h.SizeBytes > 0`: append `"  " + humanBytes(h.SizeBytes)`.
  - if `h.SignedBy != ""`: append `fmt.Sprintf("  ✓ signed by %s", h.SignedBy[:8])`. A comment notes trusted-publisher name resolution is deferred (would need a TrustStore round-trip); only the raw pubkey prefix is shown.
  - `wrapHitMenu(card, h.InfoHash, h.Name, h.SignedBy)`.
- **`makeSwarmHitCard(h swarmsearch.MergedHit)`** subtitle: `fmt.Sprintf("[swarm] %s  score=%d  seeders=%d  sources=%d", h.InfoHash[:16], h.Score, h.Seeders, len(h.Sources))`; if `h.Size > 0` append `"  " + humanBytes(h.Size)`. `wrapHitMenu(card, h.InfoHash, h.Name, "")` (no signer).
- **`makeDHTHitCard(h dhtindex.LookupHit)`** subtitle: `fmt.Sprintf("[dht] %s  score=%.2f  seeders=%d  sources=%d", h.InfoHash[:16], h.Score, h.Seeders, len(h.Sources))`; if `h.Size > 0` append `"  " + humanBytes(h.Size)`; if `h.BloomHit` append `"  (known-good)"`. `wrapHitMenu(card, h.InfoHash, h.Name, "")`.

**Signed-by rendering:** only local cards, only when `SignedBy != ""`, rendered as the literal `"✓ signed by " + SignedBy[:8]` prefix; also surfaces a "Copy publisher pubkey" menu item (below). Swarm/DHT cards never show a signer.

**Highlight fragments — GAP to fix:** the legacy tab requests **no** highlight (`indexer.SearchRequest{Query, Limit}` sets neither `Highlight` nor `SignedBy`), and `makeLocalHitCard` renders no fragment/snippet field at all. The rebuilt `searchmux.Query`/`indexer.SearchRequest` carry `Highlight bool` and `SignedBy string`; the rebuild's Layer-L card SHOULD set `Highlight:true` and render the returned fragment. This is a feature gap, not just a routing change.

### Right-click menu — `wrapHitMenu(card, infoHash, name, signedBy)`

Returns `newRightClickCapture(card, build)`; `build()` returns `fyne.NewMenu("Search hit", items...)`:
1. `"Add to downloads"` → goroutine calls **`st.d.Eng.AddMagnetURI(magnet)`**; on error `fyne.Do(dialog.ShowError(err, st.win()))`. `magnet := magnetLink(infoHash, name)` — a comment stresses the name is URL-escaped because it's remote-sourced and fed straight into `AddMagnetURI` (injection guard). **Rebuild routes this via `d.Eng.AddMagnet`.**
2. separator
3. `"Copy magnet link"` → `fyne.CurrentApp().Clipboard().SetContent(magnet)`.
4. `"Copy infohash"` → clipboard `infoHash`.
5. separator
6. `"Confirm (mark known-good)"` → `st.confirmHit(infoHash)`.
7. `"Flag as spam"` → `st.flagHit(infoHash)`.
8. if `signedBy != ""`: separator + `"Copy publisher pubkey"` → clipboard the signer.

### Confirm / Flag — **§5 DEFECT: independent reputation logic, NOT `d.Confirm`/`d.Flag`**

Both the card buttons and the two menu items call `st.confirmHit` / `st.flagHit`, which reach **directly into engine subsystems and reimplement the reputation policy** instead of calling the shared daemon path.

`confirmHit(infoHashHex)`:
- `bloom := st.d.Eng.KnownGoodBloom()`; nil → silent return.
- `hex.DecodeString(infoHashHex)`; err or `len != 20` → silent return.
- `bloom.Add(ih)`.
- `tracker := st.d.Eng.ReputationTracker()`; `sources := st.d.Eng.SourceTracker()`; if both non-nil and `pks := sources.Sources(infoHashHex)` non-empty → `tracker.RecordConfirmed(pks...)`.
- Success dialog: `dialog.ShowInformation("Confirmed", fmt.Sprintf("Marked %s as known-good", infoHashHex[:16]), st.win())`.

`flagHit(infoHashHex)`:
- `tracker := st.d.Eng.ReputationTracker()`; nil → return.
- `sources := st.d.Eng.SourceTracker()`; `pks := sources.Sources(infoHashHex)`.
- if `len(pks) == 0` → **anti-weaponization no-op** with dialog `dialog.ShowInformation("Flagged", fmt.Sprintf("No indexer attribution recorded for %s — no reputations changed", infoHashHex[:16]), st.win())` (a comment explains it deliberately refuses to demote every indexer on unattributed spam).
- else `tracker.RecordFlagged(pks...)`, `sources.Forget(infoHashHex)`, dialog `dialog.ShowInformation("Flagged", fmt.Sprintf("Flagged %s as spam (%d indexers demoted)", infoHashHex[:16], len(pks)), st.win())`.

**Why this is a defect (§5/§6):** this is the entire spam-path policy — Bloom add, tracker `RecordConfirmed`/`RecordFlagged`, source `Sources`/`Forget`, and the "no attribution → no-op" guard — reimplemented inside the GUI. It can diverge from the web UI's reputation feedback (exactly the §6 inconsistency `confirm_flag.go` was written to end). **Rebuild:** call `d.Confirm(infoHash)` and `d.Flag(infoHash)` only. The daemon already encapsulates all of the above via `d.Eng.ConfirmHit(ih, ihHex)` / `d.Eng.FlagHit(ihHex)` and returns `httpapi.ConfirmResult{InfoHash, IndexersConfirmed}` / `httpapi.FlagResult{InfoHash, IndexersFlagged, Attribution}` with fail-closed zero-attribution reporting and trusted-publisher exemption (D21). The GUI keeps ONLY the dialog presentation, driven off those return structs (e.g. `IndexersConfirmed` / `IndexersFlagged` / `Attribution` and the mapped errors `ErrBloomNotConfigured` / `ErrTrackerNotConfigured` / `ErrBadInfohash`), and must not touch `KnownGoodBloom`/`ReputationTracker`/`SourceTracker` at all.

### `win()`

`func (st *searchTab) win() fyne.Window { return windowForObject(st.content) }` — a package helper; used only to parent dialogs.

### Rebuild checklist for the Search tab (DoD-relevant)

- Replace the whole `runSearch` goroutine fan-out with a single `searchmux.Mux.Search(ctx, Query{...})` call; render `Result.{Local,Swarm,DHT}` + their `*Err` fields into the three card families. Keep the L/S/D per-layer status line with the exact count formats above (`Local: N hits` from `Total`; `Swarm: N hits (asked=…, responded=…)`; `DHT: N hits (indexers=r/a)`), and the `"  |  "` join and `"No search layers enabled"` fallback.
- Set `Highlight: true` (and `SignedBy` when filtering) on the Query and render the returned highlight fragment in the Local card — closing the legacy no-highlight gap.
- Replace `confirmHit`/`flagHit` bodies with `d.Confirm`/`d.Flag`; delete all direct `KnownGoodBloom`/`ReputationTracker`/`SourceTracker` access.
- Rename `d.Index` → `d.Idx`; route "Add to downloads" through `d.Eng.AddMagnet`; drop `d.Eng.Lookup()`.
- Preserve verbatim UI strings: placeholder `"Search query..."`, limit `"20"`, checkbox labels `"Local"`/`"Swarm"`/`"DHT"`, `"Limit:"`, button `"Search"`, `"Searching..."`, the two empty-state strings, card action labels `"Confirm"`/`"Flag"`, the menu title `"Search hit"` and its items, subtitle formats (`[type] hash16 score`, `✓ signed by`, `(known-good)`, `file=`), and the dialog titles/bodies (`"Confirmed"`/`"Marked %s as known-good"`, `"Flagged"`/`"...%d indexers demoted"`/`"No indexer attribution recorded..."`). Default checkboxes: Local on, Swarm/DHT off. `parseSearchLimit` strict (default 20).

---

I have everything needed. Here is my report.

## status-companion-settings

Reader coverage: `internal/gui/status.go`, `internal/gui/companion.go`, `internal/gui/settings.go`, plus the shared pieces those tabs depend on — `internal/gui/app.go` (App struct, tab wiring, `showAbout`), `internal/gui/theme.go`, `cmd/swartznet-gui/main.go`, `cmd/swartznet-gui/FyneApp.toml`. All paths under `/home/kartofel/Claude/swartznet`, read from branch `legacy-snapshot`.

---

### 0. Shared frame these three tabs plug into (app.go)

**App struct** (`internal/gui/app.go`):
```go
type App struct {
    fyne      fyne.App
    win       fyne.Window
    daemon    *daemon.Daemon
    cancel    context.CancelFunc
    version   string
    buildDate string
    dl        *downloadsTab
    sr        *searchTab
    tabs      *container.AppTabs
    lastNotified map[string]bool
}
```
Note the legacy struct only retains handles to `dl` and `sr`; **status, companion, and settings tab structs are constructed and dropped** (`st`, `cp`, `se` locals) — they self-manage via their own poll goroutines and never need to be re-poked by `App`.

**Tab construction order & wrapping** (in `New`):
```go
dl := newDownloadsTab(ctx, d)
sr := newSearchTab(ctx, d)
st := newStatusTab(ctx, d)
cp := newCompanionTab(ctx, d)
se := newSettingsTab(d)          // NOTE: no ctx — settings has no poll loop
...
tabs := container.NewAppTabs(
    container.NewTabItem("Downloads", container.NewScroll(dl.content)),
    container.NewTabItem("Search",    container.NewScroll(sr.content)),
    container.NewTabItem("Status",    container.NewScroll(st.content)),
    container.NewTabItem("Companion", container.NewScroll(cp.content)),
    container.NewTabItem("Settings",  container.NewScroll(se.content)),
)
tabs.SetTabLocation(container.TabLocationTop)
```
Every tab's `.content` is wrapped in `container.NewScroll(...)` — the comment explains this stops Fyne advertising a 1000+px minimum on both axes to the WM. The five tab titles are exactly `"Downloads"`, `"Search"`, `"Status"`, `"Companion"`, `"Settings"`. `newStatusTab` and `newCompanionTab` take `(ctx, d)` and spawn a poll goroutine; `newSettingsTab` takes `(d)` only.

**App/window creation & version flow:**
```go
a := app.NewWithID("net.swartznet.gui")
a.Settings().SetTheme(&swartzTheme{})
a.SetIcon(AppIcon)
win := a.NewWindow("SwartzNet " + version)     // title = "SwartzNet v0.8.0"
```
`version`/`buildDate` arrive via `gui.New(d, version, buildDate string)`.

---

### 1. Status tab (`internal/gui/status.go`)

**Struct** `statusTab`: holds `content fyne.CanvasObject`, `d *daemon.Daemon`, and one `*widget.Card` + `[]*widget.Label` group per section, plus a `repList *widget.List` and `repSnap []repRow`. Row helper type:
```go
type repRow struct { pubkey string; score string; hits string }
```

**Constructor split:** `newStatusTab(ctx, d)` calls `buildStatusTab(d)` then `go st.pollLoop(ctx)`. `buildStatusTab` is a test seam that builds the widget tree without the goroutine (avoids `-race` on Fyne's shared widget caches).

**Widget tree** — seven cards in a `container.NewAdaptiveGrid(2, ...)`, with the Reputation card docked via `container.NewBorder(grid, nil, nil, nil, st.repCard)` (grid on top, rep list fills center):

| Card title (verbatim) | Rows (label text verbatim) | Source method(s) |
|---|---|---|
| `"Torrents"` | `Total:` `Downloading:` `Seeding:` `Queued:` `Paused:` — separator — `Download rate:` `Upload rate:` | `st.d.Eng.TorrentSnapshots()` → count by `s.Status` (`"downloading"`/`"seeding"`/`"queued"`/`"paused"`), sum `s.DownloadRate`/`s.UploadRate` |
| `"Local Index"` | `Documents:` `Torrents:` `Content docs:` `Disk size:` | `st.d.Index.Stats()` → `stats.DocCount`, `.TorrentCount`, `.ContentCount`, `.DirBytes` (nil-guarded on `st.d.Index`) |
| `"Swarm Peers"` | `Known peers:` `Search-capable:` | `st.d.Eng.SwarmSearch().KnownPeers()`; count `p.Supported` for capable |
| `"DHT Routing"` | `Good nodes:` `Total nodes:` | `st.d.Eng.DHTRoutingTableSize()` → `(good, total)` |
| `"Aggregate (v0.5)"` | `PPMI enabled:` `Known indexers:` `Record source:` `Cache size:` `Bootstrap anchors:` `Bootstrap admitted:` `Bootstrap pending:` | `st.d.Eng.Lookup()` → `.PPMIGetter()`, `.Indexers()`; `st.d.Bootstrap` → `.AnchorCount()`/`.AdmittedCount()`/`.PendingCount()`; `st.d.Eng.SwarmSearch().RecordSource()` type-asserted to `*swarmsearch.RecordCache` → `.Len()` |
| `"DHT Publisher"` | `Keywords:` `Total hits:` `Pubkey:` | `st.d.Eng.Publisher().Status()` → `ps.TotalKeywords`, `ps.TotalHits`; `st.d.Eng.Identity().PublicKeyHex()` (truncated to 16 chars + `"..."`) |
| `"Known-Good Filter"` | `Estimated items:` | `st.d.Eng.KnownGoodBloom().EstimatedItems()` |
| `"Reputation"` (List, docked) | 3-column `HBox` rows: `pubkey`, `score`, `hits` | `st.d.Eng.ReputationTracker().Snapshot()` → per entry: `pk` (16-char trunc + `"..."`), `fmt.Sprintf("%.3f", e.Score)`, `fmt.Sprintf("%d/%d/%d", e.Counters.HitsReturned, e.Counters.HitsConfirmed, e.Counters.HitsFlagged)` |

**Refresh/polling cadence:** `pollLoop` does an initial `st.refresh()`, then `time.NewTicker(4 * time.Second)` — **4 s cadence**. All widget mutations are batched inside a single `fyne.Do(func(){...})` at the end of `refresh()`.

**Empty-state convention:** `makeLabelGroup(n)` seeds every value label to `"-"`; nil subsystems (DHT disabled, no aggregate wiring) leave the label at its formatted zero. Aggregate defaults are `aggPPMI = "no"`, `aggSourceKind = "-"`.

**§5 / subsystem-reach concern (Status is the worst offender):** The Status tab reaches *through* `d.Eng` into a large set of deep getters that the rebuilt Slice-11 Daemon surface does **not** expose: `Eng.TorrentSnapshots()`, `Eng.DHTRoutingTableSize()`, `Eng.Lookup()` (`.PPMIGetter()`, `.Indexers()`), `Eng.Publisher()`, `Eng.Identity()`, `Eng.KnownGoodBloom()`, `Eng.ReputationTracker()`, plus `d.Bootstrap` and `d.Index`. The rebuild's stated engine surface is narrower — `Torrents() []*Handle`, `SwarmSearch`, `DHTLookup`, `PublisherStatus`, rate limits — and the shared index handle is `d.Idx` (not `d.Index`). **Rebuild actions:**
- Map `TorrentSnapshots()` → iterate `d.Eng.Torrents()` `[]*Handle`.
- Map `d.Index.Stats()` → `d.Idx` stats.
- Map `Eng.Publisher().Status()` → `d.Eng.PublisherStatus`.
- Map `Eng.DHTRoutingTableSize()`/`Eng.Lookup()` → the rebuilt `d.Eng.DHTLookup` surface.
- The **entire `"Aggregate (v0.5)"` card and `d.Bootstrap`** are legacy v0.5 features absent from the rebuild's Daemon surface — drop the card (or gate it) unless the rebuilt daemon re-exposes an aggregate accessor. Do **not** re-introduce `d.Bootstrap`, `Eng.Lookup`, `Eng.KnownGoodBloom`, or `Eng.ReputationTracker` as new daemon fields just to feed the GUI; route Reputation display through whatever `d.Confirm`/`d.Flag` reputation snapshot the rebuild exposes.
- `Eng.Identity().PublicKeyHex()` pubkey should come from the rebuilt `d.Eng.PublisherStatus` rather than a direct `Identity()` reach.

None of these are lifecycle/reconciliation violations (Status is read-only polling) — they are surface-drift: the tab pulls from getters the rebuilt Daemon deliberately narrowed. Keep the tab pure-read; just re-point each label at the narrower surface.

---

### 2. Companion tab (`internal/gui/companion.go`)

**Struct** `companionTab`: `content`, `d`, publisher labels (`pubKeyLbl`, `pubRefreshLbl`, `pubCountLbl`, `pubErrorLbl`, `pubInfoHashLbl`), the follow `*widget.List` + `follows []followRow` + `followsEmpty *widget.Label`, and `followSelectedKey string` (the **pubkey hex**, not a row index — deliberately, because rows re-sort every 4 s). Row type:
```go
type followRow struct { pubkey, label string; torrents, content int; lastSync, lastErr string }
```

**Constructor split:** `newCompanionTab(ctx, d)` → `buildCompanionTab(d)` + `go ct.pollLoop(ctx)`.

**Widget tree** — a `container.NewVBox(pubCard, followForm, followCard)`:

1. **`widget.NewCard("Companion Publisher", "", ...)`** — VBox of:
   - `pubKeyRow`: a `Border` with `boldLabel("Public Key:")` left, a `widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), ...)` (LowImportance) right, and `ct.pubKeyLbl` center. The key label is **full 64-char**, `Wrapping = fyne.TextWrapBreak`, `Selectable = true`, `TextStyle = {Monospace: true}` (comment: truncating it broke the "paste your pubkey to a friend" flow).
   - `labelRow("Last Refresh:", ct.pubRefreshLbl)`, `labelRow("Published:", ct.pubCountLbl)`, `labelRow("Last InfoHash:", ct.pubInfoHashLbl)`, `ct.pubErrorLbl`, and a `widget.NewButton("Refresh Now", ct.refreshPublisher)`.
2. **`widget.NewCard("Follow Publisher", "", ...)`** — a `widget.NewForm` with items `"Public Key"` (entry, placeholder `"64-char hex public key"`) and `"Label"` (entry, placeholder `"Label (e.g. MyIndexer)"`), plus `widget.NewButton("Follow", ...)` → `ct.doFollow(pubkeyEntry.Text, labelEntry.Text)`.
3. **`widget.NewCard("Followed Publishers", "", ...)`** — a `container.NewStack(followListWithMenu, ct.followsEmpty)`. `followListWithMenu = newRightClickCapture(ct.followList, ct.buildFollowMenu)`. Each list row template is a VBox of `label (pubkey)`, `stats`, and an inline `"Unfollow"` button. Row bind: line 0 = `fmt.Sprintf("%s (%s)", f.label, shortPK)` (shortPK = 16-char trunc + `"..."`); line 1 = `fmt.Sprintf("torrents=%d  content=%d  sync=%s", f.torrents, f.content, f.lastSync)` with `"  err=" + f.lastErr` appended when non-empty; the Unfollow button captures the **full pubkey** and calls `ct.unfollowByKey(fullPK)`. Empty-state label text (verbatim): `"No publishers followed yet. Paste a 64-char public key above\nand press Follow to start syncing a remote Bleve index."` (centered, italic, word-wrap), shown/hidden by `refresh()` based on `len(rows)`.

**Daemon methods called (all correctly through the Daemon — no subsystem violations):**
- `ct.d.CompPub.Status()` → `.PubKeyHex`, `.LastRefresh` (formatted `time.RFC3339`), `.LastInfoHash`, `.LastError`, `.PublishedCount`.
- `ct.d.CompPub.RefreshNow()` (from the "Refresh Now" button, run in a goroutine; error rendered via `dialog.ShowError`).
- `ct.d.CompSub.Following()` → `map[[32]byte]string` (pubkey→label).
- `ct.d.CompSub.LastSync(pub)` → `res.GeneratedAt`, `res.TorrentsImported`, `res.ContentImported`, `res.Err`.
- `ct.d.CompSub.Follow(pub [32]byte, label)` (from `doFollow`; validates `len(pubkeyHex) == 64` and hex-decodes, else `dialog.ShowError`).
- `ct.d.CompSub.Unfollow(pub [32]byte)` (from `unfollowByKey`).

**Refresh cadence:** `pollLoop` → initial `refresh()` then `time.NewTicker(4 * time.Second)` — **4 s**. Rows are `sort.Slice`d by `label` then `pubkey` so a publisher keeps a stable screen row across refreshes (comment: prevents rows jumping every 4 s and stale-selection targeting). All mutations inside one `fyne.Do`.

**Right-click context menu** (`buildFollowMenu`, resolved via `selectedRow()` keyed on `followSelectedKey`): `"Copy public key"`, optionally `"Copy label"`, a separator, and `"Unfollow"`. Menu title `"Publisher actions"`. `selectedRow` returns `ok=false` if the selected pubkey is no longer in `ct.follows`.

**Test seam:** package var `afterRefreshPublisher func()` (nil in production) invoked at the end of the `refreshPublisher` goroutine so tests can join it deterministically.

**Rebuild verdict:** Companion is already clean — it routes every action through `d.CompPub` / `d.CompSub` exactly as the rebuild wants. The button-triggered `RefreshNow()` and `Follow`/`Unfollow` are deterministic engine calls, so they satisfy the production-architecture separation (GUI is pure trigger, subsystem owns state). Carry the tree over as-is.

---

### 3. Settings tab (`internal/gui/settings.go`)

**Struct** `settingsTab`: `content`, `d`, and widgets `shareRadio *widget.RadioGroup`, `fileHitsChk`/`contentChk *widget.Check`, `uploadEntry`/`downloadEntry *widget.Entry`, `maxActiveEntry *widget.Entry`. Share-level labels (package var):
```go
var shareLevels = []string{
    "L0 - Don't answer queries",
    "L1 - In-swarm peers only",
    "L2 - Full local index",
}
```

**Constructor:** `newSettingsTab(d)` — **no ctx, no poll loop.** Values load once at build via `loadCurrent()`, `loadRateLimits()`, `loadQueueSettings()`, and re-read only on user action. Content is `container.NewVBox(sharingCard, rateCard, queueCard, dirsCard, cfgInfo)`.

**Cards (title / subtitle verbatim):**

1. **`"Sharing Capabilities"` / `"Controls what this node shares with sn_search peers"`** — label `"Share level:"`, `st.shareRadio`, separator, `st.fileHitsChk` (`"Share file paths in search results"`), `st.contentChk` (`"Share content text snippets"`), separator, `widget.NewButton("Save", st.save)`.
   - Load (`loadCurrent`): `st.d.Eng.SwarmSearch().Capabilities()` → maps `caps.ShareLocal` 0/1/≥2 to the three radio labels; `caps.FileHits == 1`, `caps.ContentHits == 1`.
   - Save (`save`): builds `swarmsearch.Capabilities{ShareLocal, FileHits: boolInt(...), ContentHits: boolInt(...)}` and calls `st.d.Eng.SwarmSearch().SetCapabilities(caps)`; then `dialog.ShowInformation("Saved", "Sharing capabilities updated", ...)`.

2. **`"Bandwidth Limits"` / `"Zero means unlimited. Applies immediately."`** — a Form with `"Download (KiB/s)"` and `"Upload (KiB/s)"` entries (placeholder `"0 (unlimited)"`), `widget.NewButton("Apply", st.applyRateLimits)`.
   - Load (`loadRateLimits`): `st.d.Eng.UploadLimitBytesPerSec()` / `st.d.Eng.DownloadLimitBytesPerSec()` → `kibStr` (bytes/1024).
   - Apply (`applyRateLimits`): `parseKiB` each field, then `st.d.Eng.SetUploadLimitBytesPerSec(ulKiB*1024)` / `SetDownloadLimitBytesPerSec(dlKiB*1024)`; success dialog `"Saved"` with `"Upload: %s KiB/s\nDownload: %s KiB/s"` (`limitDisplay`, `0`→`"unlimited"`).

3. **`"Queue Management"` / `"Cap concurrent active downloads. Zero means unlimited. Paused/complete torrents don't count."`** — Form `"Max active downloads"` (placeholder `"0 (unlimited)"`), `widget.NewButton("Apply", st.applyQueueSettings)`.
   - Load: `st.d.Eng.MaxActiveDownloads()`. Apply: `st.d.Eng.SetMaxActiveDownloads(n)` (validates non-negative int), success `"Saved"` / `"Max active downloads: %s"` (`0`→`"unlimited"`).

4. **`"Storage Paths"` / `"Where downloaded content and the local search index live. Changes take effect on next restart."`** — `"Data directory"` + `"Index directory"` entries each with a `"Browse..."` button (`dialog.NewFolderOpen`), plus `HBox(saveDirsBtn "Save", resetDirsBtn "Reset to defaults")`.
   - Entries seeded from `d.Cfg.DataDir` / `d.Cfg.IndexDir`.
   - Save (`savePaths`): trims + rejects empty, then **`config.SaveUserOverrides(config.DefaultUserConfigPath(), dataDir, indexDir)`** and an info dialog: `"Saved"` / `"Data and index directories saved.\n\nRestart SwartzNet to switch over to the new paths. Existing torrents and indexed content stay where they are; the new paths apply to future downloads and indexing only."`.
   - Reset: `config.Default()` → repopulate entries.

5. **`"Runtime"` / `""`** (read-only) — `labelRow("Listen port:", portStr(d.Cfg.ListenPort))` (0 → `"OS-assigned"`) and `labelRow("DHT:", boolStr(!d.Cfg.DisableDHT))` (`"enabled"`/`"disabled"`).

**Helpers in this file:** `parseKiB` (empty→0, rejects negatives with `"must be ≥ 0"`, non-int with `"must be a whole number"`), `kibStr`, `limitDisplay`, `boolInt`, `boolStr`, `portStr`, `win()`.

**§5 / subsystem-reach concern:** Two things bypass the Daemon:
- **`config.SaveUserOverrides` / `config.LoadUserOverrides` / `config.DefaultUserConfigPath` / `config.Default()`** — the Settings tab writes/reads the on-disk user config **directly through the `config` package**, not through the Daemon. Per the rebuild's "no independent lifecycle/reconciliation; every action flows through the Daemon" rule, config persistence should be a Daemon method (e.g. `d.SaveStoragePaths(...)`), not a direct `config.SaveUserOverrides` call from the GUI. Flag for rerouting.
- **`st.d.Cfg.DataDir` / `.IndexDir` / `.ListenPort` / `.DisableDHT`** — direct reads of the daemon's config struct. Acceptable as reads, but note the rebuild exposes config via the Daemon, so keep them as `d.Cfg.*` reads only (no writes).
- `st.d.Eng.SwarmSearch()` capabilities get/set and the rate-limit / max-active getters+setters are on the engine surface the rebuild keeps (`SwarmSearch`, rate limits) — those are fine.

---

### 4. About / license dialog (`internal/gui/app.go` `showAbout`) — the §6 defect

`showAbout()` builds a `widget.NewForm` and shows it via `dialog.ShowCustom("About SwartzNet", "Close", content, a.win)`. Form items (verbatim):
```go
content := widget.NewForm(
    widget.NewFormItem("Version", widget.NewLabel(a.version)),
    widget.NewFormItem("Built", widget.NewLabel(buildDate)),
    widget.NewFormItem("Identity", copyableValue(pubKey)),
    widget.NewFormItem("BitTorrent port", copyableValue(port)),
    widget.NewFormItem("HTTP API", copyableValue(apiAddr)),
    widget.NewFormItem("License", widget.NewLabel("MPL 2.0 (engine) + MIT (SwartzNet code)")),
)
```
- **License string (verbatim):** `"MPL 2.0 (engine) + MIT (SwartzNet code)"`.
- `buildDate` falls back to `"(dev build)"` when `a.buildDate == ""`.
- `pubKey` from `a.daemon.Eng.Identity().PublicKeyHex()`; `port` from `a.daemon.Eng.LocalPort()` (`>0` else `"unknown"`); `apiAddr` from `a.daemon.API.Addr()` (else `"disabled"`). `copyableValue` suppresses the Copy button for `""`/`"unknown"`/`"disabled"`.
- Dialog title `"About SwartzNet"`, dismiss button `"Close"`. Reached from Help menu item `"About SwartzNet"` (and a tray `"About"` item at app.go:257).

**§6 DEFECT (must fix in rebuild):** the License line claims **`MIT (SwartzNet code)`** for first-party code, which is wrong — SwartzNet first-party code is **Apache-2.0** (CLAUDE.md: "New files in this repo stay Apache 2.0"). The rebuild MUST render **Apache-2.0** for first-party code, and *may* note the MPL-2.0 `anacrolix/torrent` dependency separately, e.g. a License value of `"Apache-2.0 (SwartzNet) · engine anacrolix/torrent: MPL-2.0"`. Do not keep the `+ MIT` claim.

**Version/BuildDate source — and a second defect (single build-stamped source):** `Version`/`BuildDate` originate as package vars in `cmd/swartznet-gui/main.go`:
```go
Version = "v0.8.0"   // overridable via -ldflags at build time
BuildDate = ""       // set by build-gui.sh / build-release.sh via -ldflags; "" → "(dev build)"
```
`main.go` threads `Version` to **both** `daemon.Options{Version: Version}` and `gui.New(d, Version, BuildDate)`, and `New` builds the window title `"SwartzNet " + version`. **However `cmd/swartznet-gui/FyneApp.toml` hardcodes a *different, drifted* version:**
```toml
[Details]
  Icon = "Icon.png"
  Name = "SwartzNet"
  ID = "net.swartznet.gui"
  Version = "0.3.0"
  Build = 1
```
So there are **two disagreeing version sources** (`main.go` → `v0.8.0` shown in About/title/`AppTabs`; `FyneApp.toml` → `0.3.0` baked into the app metadata). The DoD requires version + license to come from **ONE build-stamped source**. Rebuild fix: derive the Fyne metadata version from the same ldflags-stamped `Version` (or drop the hardcoded TOML version), and expose both `Version` and `License` from a single stamped location (ideally the shared `daemon`/build-info the CLI/web already use) so About, window title, and `FyneApp.toml` never disagree again.

---

### 5. Theme (`internal/gui/theme.go`)

`swartzTheme struct{}` implements `fyne.Theme`; installed via `a.Settings().SetTheme(&swartzTheme{})`. It is a **fixed dark theme** mirroring the web UI's CSS variables (each color has an inline `// --var` comment). `Color(name, _)` ignores the variant and returns hard-coded `color.NRGBA`, falling back to `theme.DefaultTheme().Color(name, theme.VariantDark)` for unlisted names. Key values: Background `#0e1116` (`--bg`), Foreground `#c9d1d9` (`--text`), Primary `#58a6ff` (`--accent`), Pressed `#1f6feb` (`--accent-2`), Success `#3fb950` (`--good`), Error `#f85149` (`--bad`), Warning `#d29922` (`--warn`), Button `#21262d`, InputBackground/DisabledButton `#161b22`, Border/Separator/ScrollBar/InputBorder `#30363d`, Hover `#30363d`, Menu/Overlay `#1b2028`, Disabled/PlaceHolder `#8b949e`. `Font`, `Icon`, `Size` all defer to `theme.DefaultTheme()`. The rebuild theme should keep tracking the web UI palette so GUI and web stay visually identical.

---

### 6. Build shim (`cmd/swartznet-gui/main.go`) & FyneApp.toml

**Build shim responsibilities:** parse flags, build config, construct the daemon, hand it to `gui.New`, run, cleanup. It is thin and correctly Daemon-centric — **all lifecycle lives in `daemon.New`/`d.Close()`**, and the GUI adds none of its own (satisfies §5). Flags: `-data-dir`, `-index-dir`, `-port` (default `-1` = untouched), `-no-dht`, `-no-dht-publish`, `-api-addr` (default `"localhost:7654"`), `-torrent` (repeatable via `torrentFileFlag`), `-tab` (`downloads|search|status|companion|settings`). Config layering order (verbatim intent): `config.Default()` → `config.LoadUserOverrides(config.DefaultUserConfigPath())` (GUI-saved paths) → CLI flags win last. Daemon built with `daemon.New(ctx, daemon.Options{Cfg, Log, APIAddr, Version, Stderr})`. Startup `--torrent` files loaded via `d.Eng.AddTorrentFile(path)`. Then:
```go
app := gui.New(d, Version, BuildDate)
if startTab != "" { app.SelectTab(startTab) }
app.Run()
app.Cleanup()
```
`newLogger` sets level from env `SWARTZNET_LOG` (`debug|warn|error`, default info).

**FyneApp.toml:** `Icon = "Icon.png"`, `Name = "SwartzNet"`, `ID = "net.swartznet.gui"` (matches `app.NewWithID("net.swartznet.gui")`), `Version = "0.3.0"` (the drift bug above), `Build = 1`.

---

### 7. Rebuild checklist distilled from these three tabs

1. **Companion tab** — copy over almost verbatim; already routes through `d.CompPub`/`d.CompSub`. Keep pubkey-keyed selection, 4 s poll, sorted rows, right-click menu, empty-state hint.
2. **Status tab** — keep as pure-read 4 s poll, but re-point every label at the narrower rebuilt surface: `d.Eng.Torrents()` (not `TorrentSnapshots`), `d.Idx` (not `d.Index`), `d.Eng.PublisherStatus`, `d.Eng.DHTLookup`, `d.Eng.SwarmSearch`. **Drop the `"Aggregate (v0.5)"` card and all `d.Bootstrap` / `Eng.Lookup` / `Eng.KnownGoodBloom` / `Eng.ReputationTracker` / `Eng.Identity` reaches** unless the rebuilt daemon re-exposes them; do not add daemon fields solely to feed the GUI. Reputation display should hang off the shared `d.Confirm`/`d.Flag` reputation state so GUI and web move the same numbers.
3. **Settings tab** — keep the four editable cards + read-only Runtime card, but **route storage-path persistence through a Daemon method instead of calling `config.SaveUserOverrides`/`config.DefaultUserConfigPath` directly** (the one place Settings bypasses the Daemon). Keep capability get/set on `d.Eng.SwarmSearch`, rate-limit and max-active getters/setters on `d.Eng`.
4. **About dialog (§6 fix)** — replace `"MPL 2.0 (engine) + MIT (SwartzNet code)"` with **Apache-2.0 for first-party code** (optionally noting the MPL-2.0 anacrolix dependency separately).
5. **Single version/license source** — reconcile `main.go` `Version = "v0.8.0"` with `FyneApp.toml` `Version = "0.3.0"`; stamp both (and the License string) from one build-info source shared with CLI/web.

---
