export const meta = {
  name: 'spec-extract',
  description: 'Phase 1 of rebuild playbook: reverse-engineer SwartzNet behavior into SPEC.md source material',
  phases: [
    { title: 'Read', detail: '12 subsystem readers + 3 cross-checkers in parallel' },
    { title: 'Verify', detail: 'one skeptic per broken/half-finished claim' },
    { title: 'Critique', detail: 'completeness critic + gap readers' },
  ],
}

const ROOT = '/home/kartofel/Claude/swartznet'

const READER = {
  type: 'object',
  required: ['area', 'purpose', 'capabilities', 'inputs_outputs', 'data_models', 'external_deps', 'nonobvious_rules', 'broken_or_unfinished', 'open_questions'],
  properties: {
    area: { type: 'string' },
    purpose: { type: 'string' },
    capabilities: { type: 'array', items: { type: 'object', required: ['behavior'], properties: { behavior: { type: 'string' }, evidence: { type: 'string' } } } },
    inputs_outputs: { type: 'array', items: { type: 'string' } },
    data_models: { type: 'array', items: { type: 'string' } },
    external_deps: { type: 'array', items: { type: 'string' } },
    nonobvious_rules: { type: 'array', items: { type: 'object', required: ['rule'], properties: { rule: { type: 'string' }, weird: { type: 'boolean' }, evidence: { type: 'string' } } } },
    broken_or_unfinished: { type: 'array', items: { type: 'object', required: ['issue'], properties: { issue: { type: 'string' }, evidence: { type: 'string' } } } },
    open_questions: { type: 'array', items: { type: 'string' } },
  },
}

const FINDINGS = {
  type: 'object',
  required: ['summary', 'findings'],
  properties: {
    summary: { type: 'string' },
    findings: { type: 'array', items: { type: 'object', required: ['title', 'detail'], properties: { title: { type: 'string' }, detail: { type: 'string' }, evidence: { type: 'string' }, is_defect: { type: 'boolean' } } } },
  },
}

const VERDICT = {
  type: 'object',
  required: ['verdict', 'note'],
  properties: {
    verdict: { type: 'string', enum: ['CONFIRMED', 'REFUTED', 'UNCLEAR'] },
    note: { type: 'string' },
  },
}

const AREAS = [
  { key: 'cli', paths: ['cmd/swartznet', 'cmd/dht-smoke'], hint: 'CLI entrypoint: every subcommand, flag, default, exit behavior. dht-smoke is an ops probe.' },
  { key: 'gui', paths: ['cmd/swartznet-gui', 'internal/gui'], hint: 'Fyne native GUI: tabs, actions, what a user can see/do, how it binds to the daemon.' },
  { key: 'daemon-config', paths: ['internal/daemon', 'internal/config'], hint: 'The wiring point: startup order, subsystem construction, shutdown, config struct + XDG path resolution, every config field and default.' },
  { key: 'engine', paths: ['internal/engine'], hint: 'BitTorrent engine wrapper around anacrolix/torrent: piece-completion callbacks, file priorities, rate limits, session save/restore, .torrent creation, signing integration, auto-indexing hooks.' },
  { key: 'indexer', paths: ['internal/indexer', 'internal/index'], hint: 'Bleve schema (v3), ingestion pipeline, extractor registry (PDF/EPUB/DOCX/ODT/plaintext/subtitles), Layer-L query path. Note internal/index is a separate dir — figure out what it is vs internal/indexer.' },
  { key: 'swarmsearch', paths: ['internal/swarmsearch'], hint: 'Layer S: sn_search BEP-10 LTEP extension. Wire envelope, capability negotiation via services bit, LRU hit cache, query fan-out.' },
  { key: 'dht-companion', paths: ['internal/dhtindex', 'internal/companion'], hint: 'Layer D: BEP-44 mutable-item keyword index publish/fetch; companion-index torrents via BEP-46 pointer pattern.' },
  { key: 'httpapi', paths: ['internal/httpapi'], hint: 'Localhost HTTP API (default :7654) + embedded web UI. Every route, request/response shape, how the three search layers reconcile at this boundary.' },
  { key: 'identity-trust', paths: ['internal/identity', 'internal/signing', 'internal/trust', 'internal/reputation'], hint: 'ed25519 identity, infohash-preserving .torrent signing (snet.pubkey/snet.sig), publisher allowlist, Bayesian reputation + known-good Bloom filter.' },
  { key: 'wire-specs', paths: ['docs/05-integration-design.md', 'docs/06-bep-sn_search-draft.md', 'docs/07-bep-dht-keyword-index-draft.md', 'docs/11-signing-protocol.md', 'README.md', 'CHANGELOG.md'], hint: 'The normative design + BEP drafts. Extract the INTENDED behavior and guarantees (esp. mainline-compatibility constraints and the wire-compat test matrix in 05 section 8). These are spec-of-record for the wire.' },
  { key: 'ops-tests', paths: ['testbed', 'tests', 'internal/testlab', 'scripts', 'docs/08-operations.md', '.claude-iterate.sh', 'AGENTS.md'], hint: 'Test/ops surface: Docker+netem testbed scenarios, multi-peer fixtures, build/release scripts. Also audit test coverage: which behaviors are pinned by tests vs conspicuously untested; any skipped/disabled tests.' },
  { key: 'intent-history', paths: ['docs/MILESTONES.md', 'docs/09-v1-blocker-research.md', 'docs/10-bitcoin-lessons.md', 'internal/notes', 'research', 'docs/reviews', 'CODE_REVIEW_2026-06-09.md'], hint: 'Project intent and history: what was planned, what was deferred, known blockers, review findings. Extract planned-but-unbuilt capabilities and deliberately-rejected designs.' },
]

const readerPrompt = (a) => `You are one of several parallel readers reverse-engineering the SwartzNet codebase at ${ROOT} (a Go BitTorrent client with built-in distributed full-text search, mainline-compatible). Your assigned area: "${a.key}" — paths (relative to repo root): ${a.paths.join(', ')}. ${a.hint}

Read your area thoroughly (skim outside it only for context). Your job is to reverse-engineer what this software is MEANT to do, not judge how it does it. Do NOT modify any files. Skip vendored code and dist/.

Report dense, evidence-backed structured findings:
- area: "${a.key}"
- purpose: what this area exists to do, 2-3 sentences.
- capabilities: every distinct user-facing or system-facing behavior this area provides, described as BEHAVIOR (what a user/peer/file observes), not implementation. Include CLI subcommands and flags, API endpoints, GUI actions, wire messages, background jobs, file outputs. evidence = file:line.
- inputs_outputs: what flows in and out (files, network traffic, config, events, signals).
- data_models: on-disk formats, wire formats, persisted state, index schemas — name + shape + where defined.
- external_deps: libraries, protocols (BEP numbers), services this area integrates with and what for.
- nonobvious_rules: business rules, magic constants, edge-case handling, ordering/back-compat constraints that a from-scratch rewrite MUST preserve to stay behavior-compatible. Set weird=true for anything that looks intentional but odd — those are exactly what a rewrite would silently lose.
- broken_or_unfinished: dead code, TODO paths, features wired but unreachable, code contradicting comments/docs, half-finished work. Only claim what you can back with file:line evidence.
- open_questions: ambiguities only the original author could resolve (intent questions, not code questions).

Be selective: merge trivia, but keep every load-bearing fact. No filler prose. Your output is raw material for a behavioral SPEC.md, so precision beats coverage padding.`

const XCHECKS = [
  {
    key: 'docs-drift',
    prompt: `In the SwartzNet repo at ${ROOT}: read docs/*.md (especially 05/06/07/11), README.md, CHANGELOG.md and extract the significant behavioral claims (what the software says it does: wire formats, guarantees, defaults, paths, flags). Then verify each significant claim against the actual code. Do NOT list claims that check out. Report ONLY: (a) contradictions between docs and code, (b) documented behavior with no implementation, (c) implemented behavior the docs never mention. Give file:line evidence on both sides. Mark is_defect=true for (a) and (b). Do not modify files.`,
  },
  {
    key: 'todo-scan',
    prompt: `In the SwartzNet repo at ${ROOT}: grep non-vendored Go source (skip dist/, .git/) for TODO, FIXME, XXX, HACK, "not implemented", "unimplemented", "deprecated", panic(. For each hit suggesting unfinished or suspicious work (skip trivial/cosmetic ones), read the surrounding code and report what is actually half-finished and what the finished version was probably meant to do. Also look for: exported identifiers never referenced outside their package, config fields or feature flags nothing reads, error paths that swallow errors silently. file:line evidence. Mark is_defect=true when it is genuinely unfinished/broken rather than a stylistic note. Do not modify files.`,
  },
  {
    key: 'surface-audit',
    prompt: `In the SwartzNet repo at ${ROOT}: enumerate the COMPLETE user-facing surface: (1) every CLI subcommand + flag + default in cmd/swartznet; (2) every HTTP route (method, path, params, response shape) in internal/httpapi; (3) every GUI tab and user action in internal/gui; (4) every config field in internal/config with default and which flag/env overrides it; (5) files the program reads/writes on disk (paths, formats, modes). Put the enumeration in findings (one finding per surface group, detail = dense list). Then report mismatches: surface that exists in code but is absent from README/docs, or documented but absent from code — mark those is_defect=true. Do not modify files.`,
  },
]

phase('Read')
const tasks = []
for (const a of AREAS) {
  tasks.push(() => agent(readerPrompt(a), { label: 'read:' + a.key, phase: 'Read', schema: READER }))
}
for (const x of XCHECKS) {
  tasks.push(() => agent(x.prompt, { label: 'xcheck:' + x.key, phase: 'Read', schema: FINDINGS }))
}
// Barrier justified: the Verify stage needs claims deduped across ALL readers, and the critic needs the full inventory.
const results = await parallel(tasks)
const readers = results.slice(0, AREAS.length).filter(Boolean)
const xchecks = results.slice(AREAS.length).filter(Boolean).map((r, i) => ({ key: XCHECKS[i] ? XCHECKS[i].key : 'xcheck-' + i, ...r }))
log('Readers done: ' + readers.length + '/' + AREAS.length + ' areas, ' + xchecks.length + '/3 cross-checks')

// Collect + dedup broken/half-finished claims across all sources
const claims = []
for (const r of readers) {
  for (const b of r.broken_or_unfinished || []) claims.push({ source: r.area, issue: b.issue, evidence: b.evidence || '' })
}
for (const x of xchecks) {
  for (const f of x.findings || []) {
    if (f.is_defect) claims.push({ source: x.key, issue: f.title + ': ' + f.detail, evidence: f.evidence || '' })
  }
}
const seen = new Set()
const deduped = []
for (const c of claims) {
  const evFile = (c.evidence.match(/[\w./-]+\.go/) || [''])[0]
  const key = evFile + '|' + c.issue.toLowerCase().replace(/[^a-z0-9]+/g, ' ').trim().slice(0, 60)
  if (seen.has(key)) continue
  seen.add(key)
  deduped.push(c)
}
log('Broken/unfinished claims: ' + claims.length + ' raw, ' + deduped.length + ' after dedup')

phase('Verify')
const verified = await parallel(deduped.map((c, i) => () =>
  agent(`Adversarially verify this claim about the SwartzNet repo at ${ROOT}. Claim (from ${c.source}): "${c.issue}". Evidence given: ${c.evidence || '(none)'}.

Read the relevant code and try to REFUTE the claim — maybe the thing is actually implemented elsewhere, intentional and documented, handled by a caller, or already fixed. Verdict CONFIRMED only if the code really is broken, half-finished, or contradictory as claimed. REFUTED if the claim is wrong or the behavior is intentional. UNCLEAR only if you genuinely cannot tell from the code. note: one or two sentences of justification with file:line. Do not modify files.`,
    { label: 'verify:' + i + ':' + c.issue.slice(0, 40), phase: 'Verify', schema: VERDICT })
    .then(v => ({ ...c, verdict: v ? v.verdict : 'UNCLEAR', note: v ? v.note : 'verifier died' }))
))
const confirmed = verified.filter(Boolean).filter(v => v.verdict === 'CONFIRMED')
log('Verified: ' + confirmed.length + ' confirmed of ' + deduped.length + ' claims')

phase('Critique')
const inventory = readers.map(r => ({ area: r.area, purpose: r.purpose, capability_count: (r.capabilities || []).length, capability_titles: (r.capabilities || []).map(c => c.behavior.slice(0, 80)) }))
const critic = await agent(`A team of parallel readers just reverse-engineered the SwartzNet repo at ${ROOT} to build a behavioral spec. Coverage inventory (JSON): ${JSON.stringify(inventory)}

Cross-checks run: docs-vs-code drift, TODO/unfinished scan, user-surface audit.

Your job: find what is MISSING for a complete behavioral spec. Explore the repo tree yourself (ls, grep — do not modify). Look for: directories or packages no reader covered, behavior surfaces not in any capability list (signal handling, logging, metrics, persistence formats, migration/upgrade paths, license/legal constraints), and doc files never read. Return gaps: each with what (short name), why (one sentence), paths (comma-separated repo-relative paths to read). Return an empty gaps array if coverage is genuinely complete.`,
  { label: 'critic:completeness', phase: 'Critique', schema: { type: 'object', required: ['gaps'], properties: { gaps: { type: 'array', items: { type: 'object', required: ['what', 'why', 'paths'], properties: { what: { type: 'string' }, why: { type: 'string' }, paths: { type: 'string' } } } } } } })

let gapReaders = []
if (critic && critic.gaps && critic.gaps.length) {
  log('Critic found ' + critic.gaps.length + ' gaps; spawning gap readers')
  gapReaders = (await parallel(critic.gaps.slice(0, 6).map(g => () =>
    agent(readerPrompt({ key: 'gap:' + g.what, paths: g.paths.split(','), hint: g.why }), { label: 'gapread:' + g.what.slice(0, 30), phase: 'Critique', schema: READER })
  ))).filter(Boolean)
} else {
  log('Critic found no coverage gaps')
}

return {
  readers: readers.concat(gapReaders),
  xchecks: xchecks,
  broken_verified: verified.filter(Boolean),
  critic_gaps: critic ? critic.gaps : [],
}