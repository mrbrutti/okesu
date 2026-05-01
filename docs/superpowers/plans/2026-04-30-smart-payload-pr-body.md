## Summary

Replaces raw JSON dumps in orchestration runs, agent runs, harness tool I/O, and finding metadata with entity-aware rendering. When prompts/results contain Findings, IOCs, Nodes, Daimons, Runs, Investigations, or Orchestrations, operators see styled chips with click-to-drawer + navigate-to-page affordances instead of unformatted JSON.

- New `<SmartPayload>` component (`web/src/components/SmartPayload/`) with prose↔JSON tokenizer, per-kind detectors, and per-kind chips.
- New `prompt_entities` typed side-channel on `orchestration_steps` (migration 047), populated by the orchestrator at template render time, threaded through the API, consumed by SmartPayload's prompt mode for hash-matched rendering.
- Single `EntityDrawerHost` portal at the App root reuses the existing `FindingDrawer`; other kinds navigate to their detail page on click via the chip's `↗` link (per-kind in-place drawers are a follow-up).
- Federation: every ref carries `cp_instance_id`; chip → drawer + detail-page links carry the same param so cross-CP deep-links work.
- New test infrastructure: vitest + @testing-library/react + jsdom (the project had none before this PR).

Build matrix unchanged: `CGO_ENABLED=0` everywhere.

## Test plan

Unit tests landed in this PR (40 vitest + Go tests across the affected packages):
- `controlplane/orchestrator/prompt_entities_test.go` — classifier per kind, dedup, hash stability (16 tests).
- `controlplane/db/orchestrations_test.go` — column round-trip + omit-when-null.
- `controlplane/api/orchestrations_test.go` — wire shape includes `prompt_entities` + omits when null.
- `web/src/components/SmartPayload/tokenize.test.ts` — tokenizer (9 tests).
- `web/src/components/SmartPayload/literalHash.test.ts` — hash matches server (3 tests).
- `web/src/components/SmartPayload/detectors.test.ts` — per-kind shape sniffers (10 tests).
- `web/src/components/SmartPayload/SmartPayload.test.tsx` — prompt + tree mode + auto + server-emitted ref preference (7 tests).
- `web/src/components/SmartPayload/chips/chips.test.tsx` — chip rendering (7 tests).
- `web/src/components/EntityDrawerHost.test.tsx` — drawer host event handling (4 tests).

Manual lab smoke (post-merge):

- [ ] Run an orchestration whose first step templates `{{trigger.finding}}` into the prompt. Open the run detail. Verify the prompt shows a FindingChip in place of the JSON literal; click opens the drawer; ↗ navigates to `/findings?id=…`.
- [ ] Same with an IOC trigger.
- [ ] Federated child finding (`cp_instance_id` set): chip → drawer and ↗ both carry `cp=` param.
- [ ] Open the underlying agent run from the orchestration step; verify tool call inputs / tool result outputs that contain finding-shaped JSON also render as chips (HarnessOutput integration).
- [ ] Open a finding drawer; verify the `raw` event panel renders entities as chips when applicable.

## Files

- DB: migration 047 + column + Store wiring.
- Orchestrator: new `prompt_entities.go` + engine integration at both render call sites.
- API: `prompt_entities` field on the orchestration step JSON wire shape.
- Web: `components/SmartPayload/` (new module — 8 source + 4 test files), `EntityDrawerHost`, `App.tsx` mount.
- Web integrations: `Orchestrations.tsx` (the user's primary complaint area), `HarnessOutput.tsx` (tool I/O), `Findings.tsx` (raw event block).
- Test infra: vitest + @testing-library/react + jsdom (added to `web/package.json`).
- Docs: `docs/architecture.md` "Entity-aware payload rendering" section.

## Spec / plan

- Spec: `docs/superpowers/specs/2026-04-30-smart-payload-design.md`
- Plan: `docs/superpowers/plans/2026-04-30-smart-payload.md`

## Open follow-ups

- Per-kind drawers for IOC, Node, Daimon, Run, Investigation, Orchestration (today they navigate to the detail page on body-click via the chip's `↗` link).
- Server-typed `result_entities` for results and tool I/O — v1 relies on client-side shape detection in those modes.
- Plain-text mention detection ("finding 42" in prose) — v1 only handles JSON literals.
- Inline action affordances on chips (mark-resolved, suspend, etc.).
- Diff highlighting when the same entity appears before/after a step.
- SmartPayload integration on `InvestigationDetail.tsx` / `DaimonDetail.tsx` evidence and last-tick blocks once those pages introduce raw JSON dumps (currently they don't have any — already use dedicated entity surfaces).
