# Phase 22 — Borrowing the Best Ideas from ThreatCaddy

**Date:** 2026-04-29
**Status:** Approved (design); pending implementation plan
**Branch:** `feat/phase-22-foundations`
**Tracking issue:** TBD (open after first PR)

## Background

ThreatCaddy ([peterhanily/threatcaddy](https://github.com/peterhanily/threatcaddy)) is a local-first browser-based investigation workspace built around analyst-side workflows: notes, IOCs, timelines, playbooks, and a multi-agent autonomous "AgentCaddy" team. Its design space overlaps with Okesu in interesting ways: both run agents, both deal in indicators of compromise, both want to drive humans toward action rather than reading walls of text.

Okesu's current strengths — fleet-side daimons, federated control planes, declarative orchestrations with engine-applied actions — are complementary to ThreatCaddy's analyst-side strengths. Several ThreatCaddy concepts can be borrowed and re-implemented in a way that is native to Okesu's primitives (agents, daimons, orchestrations) and that materially improves how findings are produced, deduplicated, classified, and resolved.

This spec defines a five-phase plan to borrow those ideas. Phase 22.1 lands foundations everything else rides; Phases 22.2 through 22.5 are vertical slices delivering operator-visible value on top.

## Goals

1. Promote IOCs from ad-hoc finding attributes to a first-class CP entity with a declarative (YAML) catalog and an observed (DB) population.
2. Reduce T1 noise by deduplicating findings on IOC identity and propagating classification + severity floors from curated indicators.
3. Add structured agent capabilities ThreatCaddy has shipped: hypothesis-as-finding, agent reflection / lessons memory, and multi-agent meeting orchestration steps.
4. Make federation-level patterns visible via a cross-CP supervisor daimon riding the IOC entity.
5. Provide a coherent place to store rule-shaped artifacts (YARA, Sigma) without overcommitting to runtime evaluation.

## Non-goals

- Do not replicate ThreatCaddy's analyst UI surface (notes editor, whiteboards, browser extension, quick-capture). Okesu's UI is fleet-ops oriented; analyst workflows remain out of scope.
- Do not implement Sigma rule execution in v1. Catalog storage only; per-host log-adapter execution is out of scope until a real customer asks.
- Do not implement TLP/PAP classification labels. Only worth it if a sharing-control use case appears.
- Do not implement IOC enrichment for arbitrary user-supplied vendors in v1. Pluggable adapter pattern with three named adapters (VirusTotal, AbuseIPDB, Shodan); broader extensibility is later.
- Do not change agent-file frontmatter or wire format in ways that break existing agents in `agents/` or `examples/agents/`. All additions are backward-compatible.

## Approach

Hybrid foundations-then-vertical-slices. Phase 22.1 bundles infrastructure adds (IOC entity, action-class taxonomy, adaptive scheduling) so they ship together; the cost of a single migration is lower than splitting them, and Phase 22.1 also rewrites one existing orchestration to demonstrate immediate value. Subsequent phases each ride 22.1 and deliver one cohesive end-user story.

Each phase is intended to land as a single PR (or a small handful of related PRs if size demands), branched from `main` via the worktree convention at `.claude/worktrees/`. Branch protection on `main` requires PRs for all changes; admin (mrbrutti) can self-merge but cannot push directly.

## Phases at a glance

| Phase | Title | Theme | Depends on |
|---|---|---|---|
| 22.1 | Foundations | IOC entity + extraction + action-class taxonomy + adaptive scheduling | none |
| 22.2 | Smarter findings | Dedup + classification propagation + agent reflection | 22.1 |
| 22.3 | Hypothesis-driven T2 | Investigation entity + hypothesis findings + meeting step | 22.1 |
| 22.4 | Cross-fleet pattern surfacing | Supervisor daimon + enrichment + relationships + STIX | 22.1 |
| 22.5 | Catalog rule extensions | YARA + Sigma in catalog + binary-analyzer YARA integration | 22.1 |

---

## Phase 22.1 — Foundations

### Scope

Promote IOCs to a first-class CP entity with curated (YAML) and observed (DB) populations. Add an action-class taxonomy to the action protocol. Add adaptive interval to the daimon scheduler. Rewrite one existing orchestration (`t2-fleet-ioc-hunt`) to ride the new entity so the phase is not pure plumbing.

### Deliverables

1. **IOC entity schema (CP DB).**
   - New table `iocs`, keyed by `(kind, normalized_value)` with a `UNIQUE` constraint.
   - Columns: `id`, `kind`, `value`, `normalized_value`, `source` (`catalog` | `observed`), `definition_path` (catalog only, nullable), `confidence` (`low` | `medium` | `high`, nullable), `attribution` (string, nullable), `severity_floor` (severity enum, nullable), `classification` (string, nullable), `first_seen`, `last_seen`, `observation_count`, `created_at`, `updated_at`.
   - New table `ioc_observations` linking IOCs to findings and runs: `id`, `ioc_id`, `finding_id` (nullable), `run_id` (nullable), `host` (nullable), `observed_at`. At least one of `finding_id` / `run_id` must be non-null.
   - Migration files for both sqlite and postgres (`controlplane/db/migrations/sqlite/030_iocs.sql`, postgres equivalent).

2. **Normalizer module.**
   - New Go package `controlplane/ioc/normalize`.
   - Pure functions: `NormalizeHash`, `NormalizeIPv4`, `NormalizeIPv6`, `NormalizeDomain`, `NormalizeURL`, `Refang`.
   - Behavior: lowercase hashes; canonicalize IPv6 (RFC 5952); punycode IDN domains; refang `example[.]com` → `example.com`, `hxxps://` → `https://`, `hxxp://` → `http://`.
   - Used by both the catalog loader and the ingest extractor so the same input produces identical `normalized_value`.

3. **Extraction at finding ingest.**
   - New CP middleware `controlplane/ioc/extract` invoked in the finding-ingest path.
   - Regex extraction over `title`, `body` (or summary), and string-typed `attribute` values for: IPv4, IPv6, domains, URLs, MD5, SHA-1, SHA-256, CVE IDs, MITRE ATT&CK technique IDs.
   - Each extracted value is normalized, then upsert-fetched against `iocs`; an `ioc_observations` row links it to the finding.
   - Extraction is best-effort; a malformed input does not block ingest.

4. **YAML IOC catalog format.**
   - New directory `catalog/iocs/` (next to `examples/`, sibling to `agents/`).
   - One YAML file per logical group; multiple IOCs per file allowed via a top-level list.
   - File schema:
     ```yaml
     iocs:
       - kind: sha256
         value: "abc123..."
         confidence: high
         attribution: "apt-foo"
         severity_floor: high
         classification: "malware-c2"
         notes: "Observed in 2026-04 phishing campaign."
     ```
   - Loaded by the CP at startup and on `SIGHUP`. Federated parent ↔ child the same way agents are.
   - Catalog entries get `source: catalog` and `definition_path` set to the source file.
   - Reconciliation: if a catalog entry is loaded for an IOC that already exists as `observed`, the row is updated to `source: catalog` and the catalog metadata wins. Operator-visible.

5. **Action-class taxonomy.**
   - Extend the action protocol so each action emitted by an agent declares a `class: read | enrich | fetch | create | modify`.
   - CP settings gain per-class auto-approve toggles (`policy.auto_approve_read`, `policy.auto_approve_enrich`, etc.).
   - Orchestrator consults the toggles before deciding whether to gate an action for operator approval.
   - Backwards-compatible: actions without a declared class default to `modify` (most restrictive).
   - Documented in `docs/orchestrations.md`.

6. **Adaptive daimon scheduling.**
   - Daemon scheduler reads `interval_min` and `interval_max` from agent frontmatter; `interval` (existing) is treated as a fallback that sets both equal.
   - On consecutive successful ticks with no new findings: lengthen current interval geometrically toward `interval_max` (factor 1.5, capped).
   - On a tick that produces ≥1 new finding or errors: reset to `interval_min`.
   - At ceiling, log a structured `daemon.scheduler.at_ceiling` event so operators can spot agents that have backed off entirely.

7. **Rewrite `t2-fleet-ioc-hunt`.**
   - Replace the `scope` step's free-form IOC validation prompt with a single CP-side lookup against the new `iocs` table (kind, normalized_value, attribution).
   - Pass the canonical IOC record into downstream `hunt` and `heatmap` steps.
   - Smaller, more deterministic prompts.

### Architecture

```
                    finding ingest API
                            │
                            ▼
                ┌──────────────────────┐
                │ extract IOCs (regex) │
                └──────────┬───────────┘
                           │
                           ▼
                ┌──────────────────────┐
                │ normalize each value │
                └──────────┬───────────┘
                           │
                           ▼
                ┌──────────────────────┐      ┌────────────────────┐
                │ upsert into `iocs`   │◄─────│ catalog loader     │
                │ (kind, normalized)   │      │ (SIGHUP, startup)  │
                └──────────┬───────────┘      └────────────────────┘
                           │
                           ▼
                ┌──────────────────────┐
                │ insert ioc_obser-    │
                │ vation row           │
                └──────────────────────┘
```

### Exit criteria

- New tables migrate cleanly on sqlite + postgres; a CP started against an existing pre-22.1 database upgrades without manual intervention.
- A finding posted via the existing API auto-extracts and links its IOCs, observable via a new read-only endpoint `GET /iocs?finding_id={id}` (also serves as the smoke test).
- A YAML catalog file under `catalog/iocs/*.yaml` loads on CP start; a SIGHUP picks up changes without restart.
- An orchestration step declaring `class: read` actions runs without operator approval when `policy.auto_approve_read = true`; one declaring `class: modify` still gates.
- Existing orchestration files without `class:` declarations continue to function (default `modify`).
- A daimon configured with `interval_min: 30s, interval_max: 600s` lengthens its interval over a 30-minute idle period in the lab; emits a finding and resets to `interval_min`.
- `t2-fleet-ioc-hunt` runs end-to-end against a finding carrying a sha256, hits the new IOC entity, and produces a heatmap.

### Risks and mitigations

- **Risk:** action-class taxonomy needs a sane default for in-flight orchestration files.
  **Mitigation:** default to most-restrictive (`modify`); document migration for orchestration authors; ship a one-time CP startup log warning for actions seen without a class declaration in the last 24h.
- **Risk:** adaptive scheduling can mask misconfigured agents that emit no findings (interval grows, never recovers).
  **Mitigation:** clamp `interval_max` (recommended ≤ 1h); structured log when at ceiling; dashboard surfaces "agents at scheduling ceiling" count.
- **Risk:** catalog reconciliation rules — what if a catalog entry's metadata conflicts with an observed IOC's accumulated enrichment?
  **Mitigation:** v1 rule is "catalog wins on metadata, observed accumulates `last_seen` / `observation_count`". Document this as an operator-visible expectation.

---

## Phase 22.2 — Smarter findings

### Scope

Use the IOC entity to clean up the finding firehose. Ship dedup, classification propagation, and agent reflection.

### Deliverables

1. **IOC-driven finding dedup at ingest.**
   - When a new finding extracts an IOC matching one or more findings observed within a configurable window, the engine clusters them.
   - Two cluster modes, per-IOC-kind configurable in CP settings: `collapse` (one finding with a host-list and `cluster_size` count) and `link` (kept per-host but linked under a shared `cluster_id`).
   - Default windows: sha256 = 24h, ipv4 / domain = 1h, others = 15m.

2. **Severity-floor + classification propagation.**
   - At ingest, after IOC extraction, the engine consults the matched IOCs' `severity_floor` and `classification`.
   - Finding severity is raised to the highest matching `severity_floor` if currently lower; never lowered.
   - Finding classification field populated from the matched IOC's `classification` (first match wins; later override resolution deferred to a follow-up).

3. **Confidence + attribution propagation.**
   - New finding fields `ioc_confidence`, `ioc_attribution`, populated from the matched IOC entity.
   - Surfaced in finding-detail UI under a new "IOC context" section.

4. **Agent reflection / lessons KV.**
   - New action `reflect_with_lessons` (class: `create`).
   - Writes one or more lesson strings to a per-agent `lessons` KV in agent state.
   - Daemon and one-shot runs read this on startup; top-N lessons (default N=10) are prepended to the system prompt under a "## Lessons from prior runs" header.
   - Bounded: max 10 entries per agent, max 200 chars per entry, oldest-evicted on overflow.
   - Surfaced in agent-detail UI; operator can edit/delete lessons.

5. **Defang/refang UI toggle.**
   - Single global toggle in CP UI settings (default: defang on).
   - Rendering hook on IOC and finding views; original `value` stored as-is, defanged form computed for display.

### Exit criteria

- A sha256 hitting 12 hosts inside 24h produces 1 collapsed finding (or 12 linked findings under one `cluster_id`, depending on settings); cluster size is correct.
- A curated IOC entry with `severity_floor: high` raises a triggering finding from `medium` → `high`; reverse direction does not lower.
- An agent that emitted `reflect_with_lessons` in 5 prior runs shows the latest lessons in its next run's system prompt.
- Defang/refang toggle changes IOC display from `1.2.3.4` to `1.2.3[.]4` site-wide without a page reload.

### Risks and mitigations

- **Risk:** dedup window choice is contentious; "the same incident" varies by environment.
  **Mitigation:** per-IOC-kind configurable; ship sane defaults; document tuning guidance.
- **Risk:** reflection lessons can drift into prompt bloat or stale advice.
  **Mitigation:** hard cap (10 entries, 200 chars); operator-visible and editable in UI.

---

## Phase 22.3 — Hypothesis-driven T2

### Scope

Make T2 case work first-class. Investigation entity, hypothesis findings, meeting orchestration step.

### Deliverables

1. **Investigation/Case CP entity.**
   - New table `investigations`: `id`, `title`, `status` (`active` | `closed` | `archived`), `resolution` (`resolved` | `false_positive` | `duplicate` | `wont_fix`, nullable), `summary`, `created_by`, `created_at`, `closed_at`, `updated_at`.
   - Linking tables: `investigation_findings` and `investigation_runs` (many-to-many).
   - New endpoints: `POST /investigations` (create from finding), `PATCH /investigations/{id}` (status + resolution), `POST /investigations/{id}/notes`.
   - Notes are a simple table — `id`, `investigation_id`, `author`, `body`, `created_at`. Markdown-rendered in UI.

2. **Hypothesis finding subtype.**
   - New finding subtype `hypothesis` with structured attributes:
     ```yaml
     attributes:
       claim: string
       evidence_for: array of {source, observation}
       evidence_against: array of {source, observation}
       confidence: low | medium | high
       how_to_test: string
     ```
   - New shared agent file `agents/hypothesis-writer.md` that emits findings of this subtype.
   - UI rendering: dedicated card in finding-detail when `subtype: hypothesis`.

3. **Meeting orchestration step type.**
   - New step kind `meeting` with parameters:
     ```yaml
     - id: discuss
       kind: meeting
       participants: [investigator, threat-hunter, malware-analyst]
       synthesizer: incident-responder
       context_window: trigger + last_5_findings
     ```
   - Engine runs each participant sequentially; each sees the trigger + prior participants' outputs.
   - Synthesizer agent receives all participant outputs and emits a finding subtype `meeting_minutes` with structured agenda, positions, and action items.
   - Token budget: capped at top-N most-recent finding records (configurable, default 5) plus the trigger.

4. **War-bridge variant.**
   - Trigger predicate `severity == critical` plus a `war_bridge: true` flag on the meeting step.
   - When set, the resulting `meeting_minutes` finding is flagged for immediate operator attention in the dashboard (red banner, top of pending-approval list).

### Exit criteria

- An investigation can be created from a finding, accumulates linked findings/runs/notes, and closes with a resolution.
- An orchestration with a meeting step produces a `meeting_minutes` finding with structured output (agenda, positions, action items).
- A `severity: critical` triggering finding with `war_bridge: true` surfaces a red banner in the dashboard.

### Risks and mitigations

- **Risk:** investigation entity tempts over-design (assignees, due dates, SLA, kanban boards).
  **Mitigation:** ship minimum (status + resolution + notes + linked entities); iterate based on real use.
- **Risk:** meeting step's shared context can blow token budgets.
  **Mitigation:** explicit `context_window` parameter; default capped at 5 findings + trigger; participants run sequentially so cost is bounded.

---

## Phase 22.4 — Cross-fleet pattern surfacing

### Scope

Federation-aware supervisor daimon, IOC enrichment + cache, typed IOC relationships, STIX 2.1 export.

### Deliverables

1. **Cross-CP IOC pattern supervisor daimon.**
   - New agent file `agents/ioc-pattern-supervisor.md`.
   - Runs on the parent CP via the existing CP-local subprocess execution path.
   - Ticks every 5 minutes (interval configurable).
   - Queries the federated IOC view: "IOCs observed on ≥ M child CPs in the last hour" (M default = 2).
   - Emits a finding on the parent CP for each pattern, with attributes `ioc_id`, `child_cps[]`, `total_observations`.

2. **IOC enrichment action + cache.**
   - New action `enrich_ioc` (class: `enrich`).
   - Pluggable adapter interface; v1 adapters: VirusTotal (sha256, ipv4, domain), AbuseIPDB (ipv4), Shodan (ipv4).
   - Adapter credentials in CP secrets (existing `controlplane/secrets.go`).
   - Results cached on the IOC entity in a new `ioc_enrichments` table (per-adapter row), with TTL (default 24h, per-adapter override).
   - Per-adapter rate limiter respecting vendor limits.

3. **Typed IOC relationships.**
   - New table `ioc_relationships`: `id`, `subject_id`, `predicate`, `object_id`, `source` (`enrichment` | `agent`), `confidence`, `created_at`.
   - Predicate vocabulary (fixed in v1): `resolves-to`, `exploits`, `hosted-at`, `belongs-to`, `signed-with`, `dropped-by`.
   - Populated automatically by enrichment adapters; agents can also emit relationships via a new `link_iocs` action (class: `create`).

4. **STIX 2.1 export endpoint.**
   - `GET /stix2/iocs?since={iso8601}&kind={kind}` returns a STIX 2.1 bundle.
   - Read-only; one indicator object per IOC; relationships exported as STIX `relationship` objects.

### Exit criteria

- Parent CP shows a finding "IOC X observed on N child CPs in the last hour" within 10 minutes of the pattern emerging in the lab.
- A hunt for a fresh sha256 hits VirusTotal once; second run reads from cache; cache expiry triggers re-fetch.
- Exported STIX bundle validates against an off-the-shelf STIX 2.1 validator.

### Risks and mitigations

- **Risk:** vendor API rate limits.
  **Mitigation:** per-adapter rate limiter; cache TTL; adapter clearly logs throttling.
- **Risk:** relationship vocabulary will pressure to grow over time.
  **Mitigation:** fixed vocab in v1; extensions deferred until a real use case demands it.

---

## Phase 22.5 — Catalog rule extensions

### Scope

Store YARA + Sigma rules in the IOC catalog. Wire one consumer (binary-analyzer for YARA). Sigma is storage-only in v1.

### Deliverables

1. **YARA rules in IOC catalog.**
   - New IOC kind `yara_rule`.
   - Rule body stored verbatim in the `value` column.
   - Metadata extracted from the YARA rule header into normal IOC fields: `name` from rule name, `tags` from `tags:` clause, `severity_floor` from a `meta.severity` line if present.

2. **`binary-analyzer` integration.**
   - When the agent inspects a binary, it auto-applies all `yara_rule` catalog entries (or a tag-filtered subset configured per run).
   - Matches emit findings linked to both the binary and the rule's IOC entity (via a new `ioc_observation` row).
   - Implementation uses the `go-yara` binding.

3. **Sigma rules stored as catalog entries.**
   - New IOC kind `sigma_rule`. Rule body stored verbatim.
   - Storage only — no runtime translation in v1.
   - Documented as "library use only; per-host log-adapter execution deferred to a later phase."

### Exit criteria

- A YARA rule in `catalog/iocs/` is auto-applied by `binary-analyzer` when it inspects a binary, and a match produces a finding linked to the rule's IOC entity.
- A Sigma rule loads into the catalog and is queryable via `GET /iocs?kind=sigma_rule`, but does not execute.

### Risks and mitigations

- **Risk:** `go-yara` requires CGO and may add build/deploy friction (libyara dependency).
  **Mitigation:** if material friction, ship Phase 22.5 as catalog-storage-only and defer the `binary-analyzer` integration to a follow-up. Decision deferred to implementation-time investigation.

---

## Open questions

1. **Cluster ID format.** For Phase 22.2 dedup, do we mint a stable `cluster_id` per IOC, per IOC-and-day, or per IOC-and-host-population? Default plan: per IOC, until we see what gets cluttered.
2. **Federation propagation of catalog entries.** Phase 22.1 says catalog files federate parent↔child like agents. Confirm this matches the existing agent-file federation behavior, and that operators can override at the child level if a parent ships an IOC the child wants to suppress.
3. **Investigation entity's relationship to existing "incidents" concept.** If Okesu already has any incident-shaped concept in the dashboard, the investigation entity should reuse it rather than introduce a parallel notion. Check during 22.3 implementation.

## Out of scope (and not deferred)

- Browser extension / quick-capture parity with ThreatCaddy. Off-thesis.
- Excalidraw whiteboards / freeform drawing. Off-thesis.
- Per-investigation real-time presence and team feed. Out of scope for an SRE/security tool.
- Multi-language UI translations. Premature.

## References

- ThreatCaddy: <https://github.com/peterhanily/threatcaddy>
- Okesu architecture: `docs/architecture.md`
- Okesu orchestration DSL: `docs/orchestrations.md`
- Existing IOC-touching orchestration: `examples/orchestrations/t2-fleet-ioc-hunt.md`
- Action protocol: `agents/_orchestration-actions.md`
- Action protocol data block: `agents/_orchestration-data.md`
- CP-local subprocess execution: commit `5f8fa1c`
