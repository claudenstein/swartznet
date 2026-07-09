# SwartzNet rebuild — working directory

This directory tracks a from-scratch rebuild of SwartzNet run against the
["extract the spec → fresh architecture → rebuild slice by slice"](../../SPEC.md)
playbook. The legacy tree is preserved untouched on the **`legacy-snapshot`**
branch and is used only as a *behavioral* reference, never copied structurally.

## Deliverables (top of repo)

| Document | Phase | Status |
|---|---|---|
| [`SPEC.md`](../../SPEC.md) | 1 — extracted spec | Done; §6 defects each adversarially verified |
| [`SPEC.md` §0](../../SPEC.md) | 2 — original vision | **Provisional** — reconstructed by Claude, awaiting author confirmation |
| [`ARCHITECTURE.md`](../../ARCHITECTURE.md) | 3 — fresh architecture | Done; critic's required fixes applied |
| [`PLAN.md`](../../PLAN.md) | 3 — vertical-slice build order | Done (14 slices) |

## Provenance kept here

- [`phase1-verification-ledger.md`](phase1-verification-ledger.md) — how `SPEC.md` was
  reverse-engineered (175 agents) and the full per-claim verifier notes, including the
  **refuted** claims that must NOT be "fixed" in the rebuild.
- [`phase3-design-record.md`](phase3-design-record.md) — the three architecture proposals
  considered, the judge-panel scoring, and the critic's 16 required fixes. The *why* behind
  the chosen design.
- [`raw/`](raw/) — the raw workflow outputs (JSON) and the workflow scripts, for full
  reproducibility.

## Phase state

- [x] **Phase 0 — Preserve.** `legacy-snapshot` branch created and pushed.
- [x] **Phase 1 — Extract the spec.** `SPEC.md` §1–7.
- [~] **Phase 2 — Inject the original idea (human gate).** `SPEC.md` §0 drafted
  **provisionally**; the author must confirm/correct the 5-item checklist at the end of §0
  (mission framing, Aggregate endgame, scope exclusions, the four invariants, design-center user).
- [x] **Phase 3 — Fresh architecture.** `ARCHITECTURE.md` + `PLAN.md`.
- [ ] **Phase 4 — Rebuild slice by slice.** NOT STARTED. Gated on author review of Phases 2 and 3.

## Chosen architecture (one line)

Evolutionary backbone — *"Keep the Seams, Kill the Defect Classes"* — which preserves the
legacy's good boundaries (strict three-layer search isolation, single `daemon.New` wiring
point, `httpapi` with zero subsystem imports) and turns the `SPEC.md` §6 defect classes into
mistakes that are *unrepresentable by construction*; grafted with the hexagonal proposal's
swappable Layer-D port (legacy BEP-44 ↔ Aggregate behind one seam) and the vertical
proposal's frozen `contracts/*` golden-vector tier.

## Before Phase 4 — what the author needs to decide

1. Confirm or correct `SPEC.md` §0 (the vision is genuinely the author's, not derivable from code).
2. Review `ARCHITECTURE.md` and `PLAN.md`.

Then Phase 4 proceeds one slice at a time (Slice 0 = walking skeleton), tests first, stopping
for review between big slices, per `PLAN.md` § "How to use this plan".
