export const meta = {
  name: 'spec-synthesize',
  description: 'Synthesize SPEC.md sections from reverse-engineering slices',
  phases: [{ title: 'Write', detail: '7 parallel section writers' }],
}

const DIR = '/tmp/claude-1000/-home-kartofel-Claude-swartznet/d59937c3-6a4f-4e4a-bd81-c5edbf8cd4a3/scratchpad/spec'
const REPO = '/home/kartofel/Claude/swartznet'

const COMMON = `You are writing one part of SPEC.md for the SwartzNet repo (${REPO}) — a behavioral specification reverse-engineered from the code, which will serve as the source of truth for a potential from-scratch rebuild. Readers of the spec may never look at the legacy code, so behaviors must be stated precisely enough to reimplement.

Style rules:
- Output ONLY the markdown for your assigned section(s) — no preamble, no meta-commentary, no code fences around the whole thing.
- Use the exact heading lines given to you (## / ### levels as specified).
- Dense bullets; complete sentences; behavior-first language ("the node does X when Y"), never implementation narration ("the function calls...").
- Keep file:line evidence in parentheses at the end of bullets — e.g. (cmd/swartznet/cmd_add.go:96) — it anchors the rebuild to the legacy reference.
- Merge duplicates across areas; drop trivia that no rebuild would need; keep every load-bearing fact, default value, magic constant, path, port, timeout, and format detail.
- You may spot-check ${REPO} to resolve contradictions in your input, but do not re-crawl the repo and do NOT modify any files.
- The input JSON files are large; read them fully (multiple Read calls with offsets if needed) before writing.`

phase('Write')
const results = await parallel([
  () => agent(`${COMMON}

Input: ${DIR}/caps_A.json (areas: cli, daemon-config, engine).
Write these sections of "## 2. Capabilities (user-facing behavior)":
### 2.1 Running a node (daemon lifecycle, CLI \`add\`, config & paths)
### 2.2 Downloading, seeding, and torrent management (engine)
### 2.3 Creating and signing torrents
Cover every CLI subcommand/flag/default/exit code relevant to these areas, daemon startup/shutdown order and wiring, engine behaviors (piece completion, priorities, rate limits, session save/restore, auto-indexing hooks).`, { label: 'w:caps-node', phase: 'Write' }),

  () => agent(`${COMMON}

Input: ${DIR}/caps_B.json (areas: indexer, swarmsearch, dht-companion).
Write these sections of "## 2. Capabilities (user-facing behavior)":
### 2.4 Search Layer L — local full-text index
### 2.5 Search Layer S — sn_search peer-wire protocol
### 2.6 Search Layer D — BEP-44 DHT keyword index
### 2.7 Companion indexes and aggregate B-tree indexes
Cover the ingestion pipeline, extractor registry behaviors, index schema versioning, wire message envelopes and negotiation, caching, publish/fetch cycles, BEP-46 pointer pattern, and the aggregate index format.`, { label: 'w:caps-search', phase: 'Write' }),

  () => agent(`${COMMON}

Input: ${DIR}/caps_C.json (areas: httpapi, gui, identity-trust, wire-specs, ops-tests, intent-history, plus gap:* areas).
Write these sections of "## 2. Capabilities (user-facing behavior)":
### 2.8 Identity, signing, trust, and reputation
### 2.9 HTTP API and embedded web UI
### 2.10 Native GUI
### 2.11 Ops, testbed, and release tooling
### 2.12 Design constraints of record (from the BEP drafts and integration design)
### 2.13 Planned but unbuilt / deliberately rejected (from project history)
For 2.12 state the normative guarantees (mainline compatibility, wire-compat matrix) as MUST-level constraints on any rebuild. For 2.13 be brief: a rebuild should know what was consciously deferred or rejected and why.`, { label: 'w:caps-surface', phase: 'Write' }),

  () => agent(`${COMMON}

Input: ${DIR}/io.json (per-area inputs_outputs + data_models, plus a surface-audit with the complete CLI/HTTP/GUI/config/disk enumeration).
Write "## 3. Inputs, outputs, and data models" with:
### 3.1 On-disk state (every file the program reads/writes: path, format, mode, when)
### 3.2 Wire formats (BitTorrent extensions, LTEP messages, DHT items, HTTP API shapes — names + shapes + where defined)
### 3.3 Configuration surface (every field: default, flag/env override)
The surface-audit enumeration is authoritative for completeness; the per-area data adds shape detail. Merge them.`, { label: 'w:io', phase: 'Write' }),

  () => agent(`${COMMON}

Inputs: ${DIR}/deps.json and ${DIR}/questions.json.
Write two sections:
"## 4. External dependencies and integrations" — libraries (with role), protocols by BEP number (with role), external services (DHT bootstrap nodes, trackers). Group; dedupe; note which deps are load-bearing for wire compatibility and the MPL-2.0 boundary discipline around anacrolix/torrent.
"## 7. Open questions for the author" — merge and dedupe the per-area open_questions into a numbered list grouped by theme; these are intent questions only the original author can answer. Put the sharpest, most decision-forcing questions first. Include anything from critic_gaps that remains genuinely open.`, { label: 'w:deps-questions', phase: 'Write' }),

  () => agent(`${COMMON}

Input: ${DIR}/rules.json (per-area nonobvious_rules, each with a weird flag and evidence). This is the most valuable section of the whole spec — the rules a from-scratch rewrite would silently lose.
Write "## 5. Non-obvious rules and edge cases (a rebuild MUST preserve these)" with subsections grouped by theme (wire compatibility, indexing, identity/trust, engine behavior, config/paths, UX contracts...). Every rule one bullet: the rule, why it exists if inferable, evidence. Prefix genuinely odd-but-intentional items with "**[weird]**" and one sentence on what makes them look wrong but be right. Merge duplicates across areas aggressively. Keep every magic constant and threshold with its exact value.`, { label: 'w:rules', phase: 'Write' }),

  () => agent(`${COMMON}

Inputs: ${DIR}/broken.json (148 claims of broken/half-finished/contradictory behavior, each adversarially verified with verdict CONFIRMED or REFUTED plus a note) and ${DIR}/xchecks_drift.json (docs-vs-code drift + TODO scan findings).
Write "## 6. Broken, half-finished, or contradictory (verified)" with:
### 6.1 Contradictions and defects (things that misbehave today)
### 6.2 Half-finished or dead (wired but unreachable, TODO paths, abandoned work)
### 6.3 Doc drift (docs claim X, code does Y — or behavior exists undocumented)
Include ONLY CONFIRMED items (drop all REFUTED). Cluster related claims into single entries (the 135 confirmed items contain overlaps); rank by severity within each subsection — user-visible misbehavior first, cosmetic TODO notes last, and drop pure-style notes entirely. Each entry: what is wrong, observable consequence, evidence file:line, and the verifier's note where it sharpens the claim. Aim for a decision-ready list, not a dump.`, { label: 'w:broken', phase: 'Write' }),
])

const keys = ['caps_node', 'caps_search', 'caps_surface', 'io', 'deps_questions', 'rules', 'broken']
const out = {}
keys.forEach((k, i) => { out[k] = results[i] || 'MISSING — writer failed' })
const missing = keys.filter((k, i) => !results[i])
log(missing.length ? 'Writers missing: ' + missing.join(', ') : 'All 7 sections written')
return out