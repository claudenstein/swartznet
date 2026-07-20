export const meta = {
  name: 'phase3-architecture',
  description: 'Rebuild playbook Phase 3: design fresh ARCHITECTURE.md + PLAN.md from SPEC.md via a judge panel',
  phases: [
    { title: 'Design', detail: '3 architects, independent organizing principles' },
    { title: 'Judge', detail: '3-judge panel scores each proposal' },
    { title: 'Synthesize', detail: 'ARCHITECTURE.md from winner + grafts' },
    { title: 'Plan', detail: 'PLAN.md vertical-slice build order' },
    { title: 'Critique', detail: 'coverage + invariant check vs SPEC' },
  ],
}

const SPEC = '/home/kartofel/Claude/swartznet/SPEC.md'
const REPO = '/home/kartofel/Claude/swartznet'

const CONTEXT = `You are working on a from-scratch rebuild of SwartzNet — a mainline-compatible BitTorrent client with built-in distributed full-text search (three layers: L=local Bleve index, S=sn_search LTEP peer-wire extension, D=BEP-44 DHT keyword index), one daemon behind three frontends (CLI+embedded web UI, native Fyne GUI), backed by a persistent ed25519 identity with local/social trust (signing, allowlists, Bayesian reputation, known-good Bloom filter).

The behavioral spec is the SOURCE OF TRUTH: read ${SPEC} IN FULL before doing anything (it is ~270KB / 1160 lines — read it in multiple passes with offsets). Pay special attention to:
- §0 Original vision (PROVISIONAL) — the mission, the four hard INVARIANTS (mainline wire compat / identity persistence / localhost-only unauth API / one-daemon-three-frontends), the Aggregate endgame position, and scope exclusions.
- §2 Capabilities — every behavior the rebuild must reproduce.
- §5 Non-obvious rules — things a rewrite would silently lose (preserve them).
- §6 Verified defects — things the LEGACY does wrong; the rebuild should FIX these, not reproduce them.
- §7 Open questions — unresolved intent; where an answer is needed to design, pick the most defensible default and NOTE it as an assumption.

You must treat the legacy code (branch legacy-snapshot) purely as a behavioral reference — do NOT copy its module structure. You may glance at ${REPO} for grounding but do NOT modify any files. This is Go (pinned 1.24.1); anacrolix/torrent is the embedded engine and is MPL-2.0, so integration must stay behind extension APIs (callbacks, LTEP user protocols, direct DHT lib access) — never patch the vendored lib. New first-party code is Apache-2.0.`

const PROPOSAL_SCHEMA = {
  type: 'object',
  required: ['name', 'organizing_principle', 'modules', 'data_flow', 'stack_choices', 'key_decisions', 'invariant_strategy', 'risks', 'build_order'],
  properties: {
    name: { type: 'string' },
    organizing_principle: { type: 'string', description: 'the single idea that structures this architecture, 2-3 sentences' },
    modules: { type: 'array', items: { type: 'object', required: ['name', 'responsibility', 'depends_on'], properties: { name: { type: 'string' }, responsibility: { type: 'string' }, depends_on: { type: 'array', items: { type: 'string' } }, key_types: { type: 'string' } } } },
    data_flow: { type: 'string', description: 'how a download-then-index-then-search request flows through the modules; how the three search layers reconcile; 1-2 paragraphs' },
    stack_choices: { type: 'array', items: { type: 'object', required: ['choice', 'rationale', 'tradeoff'], properties: { choice: { type: 'string' }, rationale: { type: 'string' }, tradeoff: { type: 'string' } } } },
    key_decisions: { type: 'array', items: { type: 'object', required: ['decision', 'why'], properties: { decision: { type: 'string' }, why: { type: 'string' }, spec_ref: { type: 'string' } } } },
    invariant_strategy: { type: 'string', description: 'how the design structurally enforces the four hard invariants — esp. how it keeps ALL wire traffic mainline-compatible and makes that testable' },
    risks: { type: 'array', items: { type: 'string' } },
    build_order: { type: 'array', items: { type: 'object', required: ['slice', 'ships'], properties: { slice: { type: 'string' }, ships: { type: 'string', description: 'the working vertical capability a user/peer can observe after this slice' } } } },
  },
}

const SCORE_SCHEMA = {
  type: 'object',
  required: ['scores', 'strengths', 'weaknesses', 'best_ideas_to_graft', 'overall'],
  properties: {
    scores: {
      type: 'object',
      required: ['spec_completeness', 'simplicity', 'rebuild_feasibility', 'invariant_safety', 'seam_swappability'],
      properties: {
        spec_completeness: { type: 'integer', minimum: 1, maximum: 10, description: 'covers every §2 capability + preserves §5 rules + fixes §6 defects' },
        simplicity: { type: 'integer', minimum: 1, maximum: 10, description: 'fewest moving parts, clearest boundaries, least ceremony' },
        rebuild_feasibility: { type: 'integer', minimum: 1, maximum: 10, description: 'build order truly ships working vertical slices; low integration risk' },
        invariant_safety: { type: 'integer', minimum: 1, maximum: 10, description: 'structurally protects the four hard invariants, esp. mainline wire compat' },
        seam_swappability: { type: 'integer', minimum: 1, maximum: 10, description: 'Layer-D format and other volatile parts (Aggregate migration) are cleanly swappable' },
      },
    },
    strengths: { type: 'array', items: { type: 'string' } },
    weaknesses: { type: 'array', items: { type: 'string' } },
    best_ideas_to_graft: { type: 'array', items: { type: 'string' }, description: 'specific ideas from THIS proposal worth keeping even if it does not win' },
    overall: { type: 'string' },
  },
}

phase('Design')
const ARCHITECTS = [
  {
    key: 'evolutionary',
    lens: `ORGANIZING PRINCIPLE: "Keep the seams that already work; the rebuild job is to fix defects, not re-invent boundaries." The legacy already has genuinely good structural ideas — strict three-layer search isolation (each layer owns its own response type, reconciled only at the HTTP boundary), a single daemon.New wiring point, httpapi holding zero engine imports (narrow local interfaces). Preserve those proven boundaries and focus design effort on structurally preventing the §6 defect classes (capability-not-reflected-on-wire, silent-clobber of publisher bit, admission-gate placeholder, sequential search, etc). Argue for evolution where the legacy is sound and surgical redesign only where §6 shows it broke.`,
  },
  {
    key: 'hexagonal',
    lens: `ORGANIZING PRINCIPLE: "Ports and adapters (hexagonal)." Put the domain core (torrents, documents/index, identity, trust, reputation) at the center with NO knowledge of BitTorrent, DHT, Bleve, HTTP, or Fyne. Everything external — the anacrolix engine, the DHT, the Bleve store, the sn_search wire, the three frontends, the Layer-D record format — is a replaceable adapter behind a port interface. This maximizes the swappable-Layer-D seam the vision wants (legacy BEP-44 vs Aggregate PPMI/B-tree as two adapters behind one port) and makes the whole system testable without sockets. Argue how ports make the four invariants and the Aggregate migration structurally clean.`,
  },
  {
    key: 'capability-vertical',
    lens: `ORGANIZING PRINCIPLE: "Vertical capability slices." Organize around user-observable capabilities (run/download, search-local, search-swarm, search-dht, create/sign/publish, trust/reputation) rather than technical layers. Each slice owns its storage, its wire, and its frontend surface end-to-end, sharing only a thin kernel (identity, config, the daemon lifecycle bus, the event stream). Optimize explicitly for the playbook "each build step ships a working vertical slice" requirement — the module map and the build order should be nearly the same shape. Argue how you avoid the coupling that pure verticals risk (shared index, shared engine) via a small shared kernel.`,
  },
]

const architectPrompt = (a) => `${CONTEXT}

You are ONE of three independent architects; you will not see the others. Design a COMPLETE fresh architecture for the SwartzNet rebuild under this lens:

${a.lens}

Commit fully to your lens — do not hedge toward the others; the point is three genuinely different designs a panel can compare. Deliver a rigorous, specific architecture: real module names and responsibilities, real dependency directions (must be acyclic), concrete stack choices with tradeoffs, and a build order where every slice ships something a user or peer can actually observe. Ground every major decision in a SPEC section reference. Be concrete enough that a team could start building from your output.`

const proposals = (await parallel(ARCHITECTS.map(a => () =>
  agent(architectPrompt(a), { label: 'arch:' + a.key, phase: 'Design', schema: PROPOSAL_SCHEMA, effort: 'high' })
))).filter(Boolean)
log('Architects delivered: ' + proposals.length + '/3 proposals — ' + proposals.map(p => p.name).join(' | '))

phase('Judge')
const proposalsJson = JSON.stringify(proposals)
const JUDGES = [
  { key: 'spec-fidelity', bias: 'You weight spec_completeness and invariant_safety above all: does the design reproduce EVERY §2 capability, preserve EVERY §5 non-obvious rule, FIX the §6 defects rather than re-encode them, and structurally guarantee mainline wire compatibility? Penalize any design that would silently lose a §5 rule.' },
  { key: 'buildability', bias: 'You weight rebuild_feasibility and simplicity above all: is the build order genuinely a sequence of working vertical slices with low integration risk, or does it hide a big-bang integration at the end? Is the module count justified, or is there ceremony? Penalize cleverness that raises rebuild risk.' },
  { key: 'evolvability', bias: 'You weight seam_swappability and long-term maintainability above all: can Layer-D swap legacy BEP-44 and Aggregate PPMI/B-tree behind one seam? Are the three search layers still independently replaceable? Does adding a fourth frontend or a new extractor stay local? Penalize designs that harden current choices into a future forced rewrite.' },
]
const panel = (await parallel(JUDGES.map(j => () =>
  agent(`${CONTEXT}

You are a judge on a 3-architect design panel for the SwartzNet rebuild. Here are all three proposals (JSON): ${proposalsJson}

${j.bias}

Score EACH of the three proposals on all five axes (1-10), then give strengths, weaknesses, and the specific best ideas from each worth grafting into the final design even if that proposal loses. Return one score object per proposal, keyed by proposal name, plus an overall ranking.`,
    { label: 'judge:' + j.key, phase: 'Judge', schema: { type: 'object', required: ['ranking', 'per_proposal'], properties: { ranking: { type: 'array', items: { type: 'string' }, description: 'proposal names, best first' }, per_proposal: { type: 'array', items: { type: 'object', required: ['proposal_name', 'evaluation'], properties: { proposal_name: { type: 'string' }, evaluation: SCORE_SCHEMA } } } } }, effort: 'high' })
    .then(r => ({ judge: j.key, ...r }))
))).filter(Boolean)
log('Panel scored: ' + panel.length + '/3 judges reported')

phase('Synthesize')
const panelJson = JSON.stringify(panel)
const architecture = await agent(`${CONTEXT}

Three architects proposed designs; a 3-judge panel scored them. Proposals (JSON): ${proposalsJson}

Panel scores + rankings + graft suggestions (JSON): ${panelJson}

Your job: write the FINAL ARCHITECTURE.md for the SwartzNet rebuild. Take the highest-scoring proposal as the backbone, but GRAFT the best ideas the panel flagged from the runners-up — do not just pick one and discard the rest. Where judges split, favor spec fidelity and invariant safety over elegance.

Write a complete, decisive ARCHITECTURE.md in markdown (output ONLY the markdown, no fences around the whole doc). Required sections:
# SwartzNet Architecture (Rebuild)
## Guiding principles — including the four hard invariants restated as design constraints, and the "legacy as behavioral reference only" rule
## System overview — the one-daemon / three-frontends / three-search-layers shape, with an ASCII module/dependency diagram (dependencies must be acyclic; state the direction)
## Modules — each: name, responsibility, what it MUST NOT know about (boundary), key exported types, and which SPEC §2 capabilities it owns
## Data flow — download to extract to index to search (all three layers) and publish/lookup; how the layers reconcile at the API seam; how events propagate
## The swappable Layer-D seam — how legacy BEP-44 and the Aggregate PPMI/B-tree format sit behind one port, so the §7-C migration is a config/adapter choice not a re-architecture
## Wire-compatibility strategy — how the design makes "a vanilla client sees only BEP-3/5/9/10/44/46/51" a structurally enforced, testable property
## Cross-cutting concerns — identity/signing, trust/reputation, config/paths, logging, persistence & crash-safety (note §6 says spam-resistance state is lost on crash — design for periodic checkpoint), shutdown ordering
## How this fixes the §6 defect classes — map each major defect CLASS from SPEC §6 to the structural change that prevents it
## Stack & dependencies — Go version, anacrolix/torrent (MPL boundary discipline), Bleve, Fyne, ed25519; each with the tradeoff
## Assumptions & deferred questions — the §7 open questions this architecture had to answer, with the default chosen and why; and the ones it deliberately leaves open
## Open risks — the honest top risks of this design

Be specific and buildable. Preserve every §5 rule that touches structure. Do not reintroduce any §6 defect.`, { label: 'synth:architecture', phase: 'Synthesize', effort: 'high' })
log('ARCHITECTURE.md drafted: ' + architecture.length + ' chars')

phase('Plan')
const plan = await agent(`${CONTEXT}

Here is the agreed ARCHITECTURE.md for the SwartzNet rebuild:

${architecture}

Write PLAN.md: a vertical-slice build order where EACH step ships a working, observable capability and is independently testable. Output ONLY markdown.

# SwartzNet Rebuild Plan
## How to use this plan — the "one slice at a time, tests first, stop and review between big slices" loop from the rebuild playbook; the rule that where behavior must match legacy, read the relevant legacy-snapshot file and preserve OBSERVABLE behavior but write clean new code.
## Milestones — ordered slices. For EACH slice give: **Goal** (the observable capability it ships), **Modules touched**, **Depends on** (earlier slices), **Behavioral references** (which SPEC §§ and which legacy files to consult), **Definition of done** (what test/observation proves it works — an actual command or wire observation, not "tests pass"), **§6 defects this slice must NOT reproduce**, and **§5 rules it must preserve**.
Order so the earliest slices ship a usable client (add+download+seed, then local search) before the distributed layers; put Layer S and Layer D and the Aggregate seam later; identity/signing/trust threaded in where first needed. The first slice must be a walking skeleton (daemon lifecycle + config + one frontend saying hello) that everything else grows from.
## Test strategy — mirror SPEC §5.11 / §2.11: the in-process multi-engine harness for wire-compat, the vanilla-client interop matrix as the gate, unit coverage per module.
## Sequencing rationale — one paragraph on why this order minimizes integration risk.
## What is explicitly NOT in this plan — the §7 items and scope exclusions deferred to after the rebuild reaches parity.

Make the build order match the architecture module boundaries. Aim for 10-16 slices.`, { label: 'plan:build-order', phase: 'Plan', effort: 'high' })
log('PLAN.md drafted: ' + plan.length + ' chars')

phase('Critique')
const critique = await agent(`${CONTEXT}

Adversarially review the proposed rebuild ARCHITECTURE.md and PLAN.md against the SPEC. Read ${SPEC} in full first.

ARCHITECTURE.md:
${architecture}

PLAN.md:
${plan}

Find real problems, not nitpicks. Check:
1. COVERAGE — is any SPEC §2 capability owned by no module / shipped by no plan slice? List each gap.
2. INVARIANTS — could anything in this design violate one of the four hard invariants (esp. leak non-mainline traffic onto the wire)?
3. §5 RULES LOST — any non-obvious rule the architecture would silently drop?
4. §6 REGRESSION — does the design accidentally re-encode any verified §6 defect?
5. BUILD ORDER — any slice that secretly depends on a later slice, or any "vertical slice" that is not actually observable end-to-end?
6. CYCLES — any dependency cycle in the module graph?
Return findings ranked by severity. For each: what, why it matters, and the concrete fix. If coverage is genuinely complete, say so and return an empty gaps list.`, { label: 'critic:coverage', phase: 'Critique', schema: { type: 'object', required: ['coverage_gaps', 'invariant_risks', 'rules_lost', 'defect_regressions', 'build_order_issues', 'verdict'], properties: { coverage_gaps: { type: 'array', items: { type: 'object', required: ['what', 'fix'], properties: { what: { type: 'string' }, severity: { type: 'string' }, fix: { type: 'string' } } } }, invariant_risks: { type: 'array', items: { type: 'object', properties: { what: { type: 'string' }, fix: { type: 'string' } } } }, rules_lost: { type: 'array', items: { type: 'string' } }, defect_regressions: { type: 'array', items: { type: 'string' } }, build_order_issues: { type: 'array', items: { type: 'string' } }, verdict: { type: 'string' } } }, effort: 'high' })
log('Critique done: ' + (critique.coverage_gaps || []).length + ' coverage gaps, verdict=' + (critique.verdict || '').slice(0, 60))

return { architecture, plan, panel, critique, proposal_names: proposals.map(p => p.name) }