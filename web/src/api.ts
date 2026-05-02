// Typed fetch client for the Control Plane API.

export interface User {
  id: number;
  email: string;
  role: 'admin' | 'operator' | 'viewer';
}

export interface EventItem {
  id: number;
  ts: number;
  type: string;
  agent?: string;
  host?: string;
  severity?: string;
  title?: string;
  raw: Record<string, unknown>;
  /** Phase 9.6: federation source — populated when this event came
   *  from a federated child CP. Local events leave this undefined. */
  cp_source?: CPSourceRef;
}

export class ApiError extends Error {
  status: number;
  body: string;
  constructor(status: number, message: string, body = '') {
    super(message);
    this.status = status;
    this.body = body;
  }
}

// cpQuery threads a federated-CP routing parameter onto a path. Used
// by orchestration / finding / agent / node detail endpoints that
// support `?cp=<instance_id>` proxy on the parent CP.
function cpQuery(path: string, cp?: string): string {
  if (!cp) return path;
  const sep = path.includes('?') ? '&' : '?';
  return `${path}${sep}cp=${encodeURIComponent(cp)}`;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    throw new ApiError(res.status, text || res.statusText, text);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

// DaimonItem describes a registered long-running agent (a "daimon") as
// returned by /api/agents. Backend types/routes still use "agent" — this
// is the UI-side rename; backend renames are a slower follow-up.
export interface DaimonItem {
  name: string;
  host: string;
  provider?: string;
  model?: string;
  version?: string;
  registered_at: string;
  last_heartbeat_at?: string;
  last_tick_count: number;
  /** Open findings (status='open') for this exact (name, host) pair.
   *  Driven by /api/agents server-side join, not the SSE stream — so
   *  we get an authoritative count without aggregating raw events. */
  open_findings: number;
  heartbeat_age_sec: number;
  healthy: boolean;
  desired_max_turns?: number;
  desired_effort?: string;
  desired_suspended: boolean;
  config_updated_at?: string;
  /** sha256 of the definition the daemon currently has loaded.
   *  Compared client-side against the library hash to flag drift. */
  current_definition_hash?: string;
  /** Operator-set version label from the daimon file's frontmatter
   *  (e.g. "2", "v3"). Human-readable counterpart to the hash. */
  definition_version?: string;
  /** Phase 9.6: federation source. Non-null iff this row was fetched
   *  from a federated child CP. Local rows leave this undefined. */
  cp_source?: CPSourceRef;
}

/** Tag attached to a federated row identifying which child CP it came
 *  from. Surfaces as a small chip in list rows. */
export interface CPSourceRef {
  instance_id: string;
  display_name?: string;
  region?: string;
}

export interface DaimonLibraryItem {
  name: string;
  description?: string;
  /** Operator-set version label from the daimon file frontmatter
   *  (e.g. "2", "v3"). Surfaces alongside the hash so the rollout
   *  indicator is human-readable. */
  version?: string;
  provider?: string;
  model?: string;
  mode?: string;
  interval?: string;
  modified_at: string;
  size_bytes: number;
  /** Canonical sha256 of the file content. UI compares against each
   *  registered daimon's current_definition_hash to flag drift. */
  hash?: string;
  /** Operator-set version label of the <name>.previous.md slot.
   *  When non-empty, a one-click rollback is available. */
  previous_version?: string;
  previous_modified_at?: string;
}

export interface DaimonLibraryDetail extends DaimonLibraryItem {
  content: string;
  /** Restart-required warnings populated on save responses. Surfaces
   *  fields that changed but don't hot-reload (interval, stateDir). */
  warnings?: string[];
}

export interface AgentLibraryItem {
  name: string;
  description?: string;
  provider?: string;
  model?: string;
  max_turns?: number;
  effort?: string;
  // Filesystem dir the file lives in. Surfaces which search-path entry
  // a given agent came from when multiple are configured.
  dir: string;
  modified_at: string;
  size_bytes: number;
}

export interface AgentLibraryDetail extends AgentLibraryItem {
  content: string;
}

export interface DaimonConfigPatch {
  max_turns?: number;
  effort?: string;
  suspended?: boolean;
}

export interface Finding {
  id: number;
  event_id: number;
  ts: number;
  agent?: string;
  host?: string;
  severity?: string;
  title?: string;
  resource?: string;
  evidence?: string;
  dedup_key?: string;
  acknowledged: boolean;
  acknowledged_at?: string;
  acknowledged_by?: number;
  ack_note?: string;
  created_at: string;
  raw?: Record<string, unknown>;

  // Phase 12 enrichment.
  category?: string;
  process_pid?: number;
  process_name?: string;
  path?: string;
  network_endpoint?: string;
  cve?: string;
  tags?: string[];
  attributes?: Record<string, unknown>;

  // Phase 22.3 — finding subtype. Drives per-subtype rendering in the
  // drawer (e.g. "hypothesis" → HypothesisCard). Empty/missing = no
  // special card.
  subtype?: string;

  // Phase 22.10 — recurrence tracking. recurrence_count sums every
  // emission rolled up onto this finding (1 = first sighting). Used
  // by the UI to render a "fired N×" chip next to repeated noise.
  recurrence_count?: number;
  last_seen_at?: string;

  // Phase 13 — triage state. `acknowledged` (boolean) is kept for
  // backward-compat — any non-open status maps to true.
  status?: FindingStatus;
  triage_note?: string;
  triaged_at?: string;
  triaged_by_email?: string;

  // Phase 14 — severity override. `severity` is the EFFECTIVE value
  // (operator override if set, else original). `original_severity` is
  // the LLM's untouched assignment. `operator_severity` is set iff an
  // override is in effect — its absence is the signal that there's no
  // override. `fingerprint` is the canonical key used for severity rules.
  original_severity?: string;
  operator_severity?: string;
  severity_override_at?: string;
  fingerprint?: string;
  /** Phase 9.6: federation source. */
  cp_source?: CPSourceRef;
}

export type Severity = 'CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW' | 'INFO';

export const ALL_SEVERITIES: Severity[] = ['CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'INFO'];

// Phase 22.9 — generic labels. Mirrors the LabelKind* constants in
// controlplane/db/labels.go. Adding a new kind needs (1) the const
// here, (2) the const + allowedLabelKinds entry on the server, and
// (3) the wiring on the per-entity detail page.
export type LabelKind =
  | 'node'
  | 'daimon'
  | 'finding'
  | 'investigation'
  | 'run'
  | 'orchestration'
  | 'cp'
  | 'secret'
  | 'group';

export const ALL_LABEL_KINDS: LabelKind[] = [
  'node', 'daimon', 'finding', 'investigation', 'run',
  'orchestration', 'cp', 'secret', 'group',
];

// LabelTarget is what /api/labels/search returns — identity of one
// entity that matched the selector.
export interface LabelTarget {
  kind: LabelKind;
  id?: number;
  key?: string;
}

// SeverityCeiling is one (selector → max_severity) row that caps
// agent-assigned severity at finding-ingest time. Lab/staging
// nodes commonly carry a ceiling (env=staging → MEDIUM).
export interface SeverityCeiling {
  id: number;
  selector: string;
  max_severity: 'CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW' | 'INFO';
  reason?: string;
  created_at?: string;
  updated_at?: string;
}

// LabelRow is one row of the labels table — what /api/labels/all
// returns. Drives the Settings → Labels admin page. target_id is
// 0 for kinds with composite-key identity (daimon = name@host); in
// those cases target_key carries the identity instead.
export interface LabelRow {
  id: number;
  target_kind: LabelKind;
  target_id: number;
  target_key: string;
  key: string;
  value: string;
  source: string;
  created_at?: string;
  updated_at?: string;
}

export interface SeverityRule {
  fingerprint: string;
  severity: Severity;
  note?: string;
  created_at: string;
  updated_at?: string;
  created_by?: number;
}

export type FindingStatus =
  | 'open'
  | 'acknowledged'
  | 'investigating'
  | 'resolved'
  | 'false_positive'
  | 'wontfix'
  /** Engine-level Tier-0 dedup closure status. Set by the
   *  eventpipeline when a newer finding with the same dedup_key
   *  lands. Operators almost never want to see these; the
   *  Operator-queue filter hides them by default. */
  | 'superseded';

export const ALL_FINDING_STATUSES: FindingStatus[] = [
  'open', 'acknowledged', 'investigating', 'resolved', 'false_positive', 'wontfix', 'superseded',
];

export interface FindingsSummary {
  total: number;
  open: number;
  critical: number;
  high: number;
  medium: number;
  low: number;
  info: number;
  last_24h: number;
  by_agent: FindingsByAgent[];
  by_category: FindingsByCategory[];
  trend: FindingsTrendBucket[];
}

export interface FindingsByAgent {
  agent: string;
  open: number;
  critical: number;
  high: number;
}

export interface FindingsByCategory {
  category: string;
  open: number;
  critical: number;
  high: number;
}

export interface FindingsTrendBucket {
  hour_ts: number; // unix ms at the start of the hour (UTC)
  count: number;
}

export interface FindingGroup {
  group_key: string;
  dedup_key?: string;
  severity?: string;
  title?: string;
  agent?: string;
  resource?: string;
  count: number;
  hosts: string[];
  first_seen: number;
  last_seen: number;
  latest_id: number;
  /** Phase 9.6: federation source. Set when this group came from
   *  a single child CP. */
  cp_source?: CPSourceRef;
  /** Phase 9.7: when the same dedup_key was reported on multiple
   *  child CPs, the parent merges the groups and lists every
   *  contributing CP here. UI renders a chip per source. */
  cp_sources?: CPSourceRef[];
}

export interface FindingsFilter {
  severity?: string[];   // any of CRITICAL|HIGH|MEDIUM|LOW|INFO
  agent?: string;
  host?: string;
  category?: string;     // process|file|network|cert|cloud|identity|config|other
  tag?: string;          // exact match against any of the comma-separated tags
  /** open: status='open'. acked: anything triaged (any non-open).
   *  all: every status. queue: status='open' AND no auto-* tag — i.e.
   *  the operator queue (Phase 13). */
  state?: 'open' | 'acked' | 'all' | 'queue';
  since?: number;        // unix ms
  until?: number;        // unix ms
  limit?: number;
  offset?: number;
  /** Phase 22.9 — K8s-style label selector against the finding's
   *  host node labels (e.g. "env=prod, role=db"). Resolved to the
   *  matching set of host strings server-side. */
  host_selector?: string;
}

// IOCRecord mirrors controlplane/db.IOCRecord. JSON encoder uses Go's
// default capitalized field names since the struct has no `json:"…"`
// tags — keep the casing here in lockstep with the server type.
export interface IOCRecord {
  ID: number;
  Kind: string;            // ipv4 | ipv6 | domain | url | sha256 | md5 | cve | mitre | …
  Value: string;            // canonical (refanged) form, suitable for matching
  NormalizedValue: string;  // lower-cased / dedup form
  Source: string;           // "catalog" | "observed"
  DefinitionPath: string;
  Confidence: string;
  Attribution: string;
  SeverityFloor: string;    // CRITICAL|HIGH|MEDIUM|LOW|INFO
  Classification: string;
  Notes: string;
  Name: string;             // Phase 22.5 — operator-friendly name (catalog: from rule header; observed: empty)
  Tags: string;             // Phase 22.5 — comma-separated tag list
  ObservationCount: number;
  FirstSeen: string;        // RFC3339
  LastSeen: string;         // RFC3339
}

// IOCObservation mirrors controlplane/db.IOCObservation. No json tags
// on the Go struct — capital-case field names. ObservedAt is a
// time.Time on the server, encoded to RFC3339Nano in JSON.
export interface IOCObservation {
  IOCID: number;
  FindingID: number;            // 0 if not linked to a finding
  OrchestrationRunID: number;   // 0 if not linked to a run
  Host: string;
  ObservedAt: string;           // RFC3339
}

// IOCRelationship mirrors controlplane/db.IOCRelationship. The fixed
// v1 predicate vocabulary lives server-side; we treat Predicate as a
// free-form string on the client.
export interface IOCRelationship {
  ID: number;
  SubjectID: number;
  Predicate: string;
  ObjectID: number;
  Source: string;
  Confidence: string;
}

// FederatedIOCRecord is the merged wire shape returned by federated
// catalog endpoints — IOCRecord fields plus the list of CPs that
// contribute to this merged row. Server marshals the embedded record
// flat (PascalCase) plus snake_case cp_sources.
export interface FederatedIOCRecord extends IOCRecord {
  cp_sources: CPSourceRef[];
}

// FederatedIOCObservation tags each observation row with its origin CP.
// No dedup across CPs — observations are per-host events.
export interface FederatedIOCObservation extends IOCObservation {
  cp_source: CPSourceRef;
}

// FederatedIOCRelationship: deduped tuple-based edge with origin CPs.
// Replaces the int-id-based IOCRelationship for federation paths
// (per-CP int row ids aren't comparable across the fleet).
export interface FederatedIOCRelationship {
  SubjectKind: string;
  SubjectValue: string;
  Predicate: string;
  ObjectKind: string;
  ObjectValue: string;
  Source: string;
  Confidence: string;
  cp_sources: CPSourceRef[];
}

// Investigation mirrors controlplane/db.Investigation. The Go struct
// has no `json:"…"` tags, so the encoder uses Go's default capitalized
// field names — keep the casing here in lockstep with the server type.
// time.Time fields encode to RFC3339 strings; ClosedAt is the Go zero
// value ("0001-01-01T00:00:00Z") when the case is still active.
export interface Investigation {
  ID: number;
  Title: string;
  Status: 'active' | 'closed' | 'archived';
  Resolution: '' | 'resolved' | 'false_positive' | 'duplicate' | 'wont_fix';
  Summary: string;
  CreatedBy: string;
  CreatedAt: string; // RFC3339
  ClosedAt: string;  // RFC3339, "0001-01-01T00:00:00Z" when not closed
  UpdatedAt: string;
  /** Auto-opener dedup handle (Phase 22.5+). Empty for manual cases. */
  ExternalKey?: string;
  /** Phase 22.6 — present iff this case lives on a federated child CP.
   *  UI shows the child's display_name as a badge and routes detail
   *  navigation through ?cp=<instance_id>. */
  cp_source?: CPSourceRef;
}

// InvestigationNote mirrors controlplane/db.InvestigationNote — same
// Go-default capitalization rule as Investigation above.
export interface InvestigationNote {
  ID: number;
  InvestigationID: number;
  Author: string;
  Body: string;
  CreatedAt: string;
}

// InvestigationFindingItem — narrow projection of `findings` joined
// to investigation_findings. Server enriches at query time so the
// workspace can render the Findings tab without N+1 fetches.
export interface InvestigationFindingItem {
  ID: number;
  Ts: number;
  Agent: { String: string; Valid: boolean };
  Host: { String: string; Valid: boolean };
  Severity: { String: string; Valid: boolean };
  Title: { String: string; Valid: boolean };
  Status: { String: string; Valid: boolean };
  Tags: { String: string; Valid: boolean };
  Subtype: { String: string; Valid: boolean };
  LinkedAt: string;
  /** How the finding was linked to this case. NULL on legacy rows
   *  that pre-date provenance tracking — UI renders those neutrally. */
  LinkMethod: { String: string; Valid: boolean };
  /** Operator email or 'system:<actor>' for engine-driven links.
   *  NULL on legacy rows. */
  LinkedBy: { String: string; Valid: boolean };
}

export type LinkMethod = 'manual' | 'bulk' | 'auto-promote' | 'autolink' | 'import';

// Secret — Phase 22.8 PR γ. Credential metadata; the value never
// crosses the wire. Bindings (in SecretDetail) tie the secret to a
// label selector + scope so the right credential applies on the
// right node automatically.
export type SecretKind = 'env_var' | 'ssh_key' | 'api_key';
export type SecretScope = 'node' | 'daimon' | 'agent_run' | 'any';

export interface Secret {
  id: number;
  name: string;
  kind: SecretKind | string;
  description: string;
  owner_group_id?: number;
  created_at: string;
  created_by_email?: string;
}

export interface SecretBinding {
  id: number;
  secret_id: number;
  selector: string;
  scope: SecretScope | string;
  created_at: string;
}

export interface SecretDetail {
  secret: Secret;
  bindings: SecretBinding[];
}

// Group — Phase 22.8 PR α. external_id is non-empty for OIDC-bound
// groups (the IdP-side identifier matched against the groups claim);
// empty for local-only groups.
export interface Group {
  id: number;
  name: string;
  description: string;
  external_id: string;
  created_at: string;
  updated_at: string;
}

// GroupRole — one (role, selector) tuple attached to a group. The
// selector string is reserved for PR β's label-aware evaluator;
// rows in PR α all carry empty selector meaning "CP-wide".
export interface GroupRole {
  id: number;
  group_id: number;
  role: 'admin' | 'operator' | 'viewer' | string;
  selector: string;
}

// GroupDetail — what GET /api/groups/{id} returns. Members are
// projected to {id, email, role} so password hashes never leave the
// auth package.
export interface GroupDetail {
  group: Group;
  roles: GroupRole[];
  members: Array<{ id: number; email: string; role: string }>;
}

// MyGroups — what /api/users/me/groups returns. The Settings →
// Profile section uses this so a user can see (but not edit) their
// effective access. `groups[].source` distinguishes manual / oidc /
// auto memberships so the UI can show OIDC ones as IdP-managed.
export interface MyGroups {
  groups: Array<{
    group_id: number;
    name: string;
    description: string;
    source: 'manual' | 'oidc' | 'auto';
    added_at: string;
  }>;
  effective_roles: string[];
}

// SavedSearch — operator's named filter set. `scope='findings'` is
// the only consumer in v1; the field stays so future surfaces (cases,
// runs, IOCs) get the same primitive without API changes.
export interface SavedSearch {
  id: number;
  user_id: number;
  name: string;
  scope: string;
  /** JSON-encoded filter shape; opaque to the server. The Findings
   *  page parses this as FindingsFilterConfig. */
  config_json: string;
  is_default: boolean;
  created_at: string;
  updated_at: string;
}

/** Findings-scoped saved-search payload. The page persists every
 *  field it filters by here. Fields are optional so an empty saved
 *  search renders the default "all queue" view. */
export interface FindingsFilterConfig {
  view?: 'grouped' | 'recent' | 'kanban';
  state?: 'queue' | 'all' | string;
  severity?: string[];
  agent?: string;
  host?: string;
  category?: string;
  /** Phase 22.9 — K8s-style selector against host node labels. */
  host_selector?: string;
}

// InvestigationAuditEvent — one row in the case timeline. Kinds are
// stable enums; the `details` shape is dictated by `kind` (see
// controlplane/db/investigation_audit.go for the wire contract).
export interface InvestigationAuditEvent {
  ts: string;
  kind: 'created' | 'closed' | 'note' | 'finding_linked' | 'run_linked';
  by: string;
  title: string;
  details?: {
    body?: string;            // note kind
    finding_id?: number;      // finding_linked
    method?: LinkMethod;      // finding_linked
    run_id?: number;          // run_linked
    resolution?: string;      // closed
  };
}

// InvestigationRunItem — narrow projection of orchestration_runs.
// Note: this is the orchestration-managed run table, not the ad-hoc
// runs table; investigation_runs only links to orchestration_runs.
export interface InvestigationRunItem {
  ID: number;
  OrchestrationID: number;
  OrchestrationName: { String: string; Valid: boolean };
  Status: string;
  TriggerKind: string;
  StartedAt: string;
  EndedAt: { String: string; Valid: boolean };
  CurrentStepID: { String: string; Valid: boolean };
  Error: { String: string; Valid: boolean };
  LinkedAt: string;
}

// InvestigationIOCItem — IOCs derived from observations on the
// case's linked findings. Aggregations are scoped to the case (not
// the IOC's lifetime totals).
export interface InvestigationIOCItem {
  ID: number;
  Kind: string;
  Value: string;
  Severity: { String: string; Valid: boolean };
  ObservationCount: number;
  HostCount: number;
  FirstSeen: string;
  LastSeen: string;
}

// InvestigationDaimonItem — distinct daimons (= emitting agents)
// that fired the case's findings. Deep-link to daimon detail.
export interface InvestigationDaimonItem {
  Agent: string;
  FindingCount: number;
  LastSeenTs: number;
}

// InvestigationOrchestrationItem — orchestrations that own the
// case's linked runs, grouped with run count + most-recent start.
export interface InvestigationOrchestrationItem {
  OrchestrationID: { Int64: number; Valid: boolean };
  OrchestrationName: string;
  RunCount: number;
  LastStartedAt: string;
}

// SuggestedFinding — server-scored candidate the workspace's
// "Suggested findings" card surfaces. `signals` lists the rule names
// that fired ("dedup_key" | "ioc" | "host_window" | "daimon_sev"); the
// score is the sum of their fixed weights.
export interface SuggestedFinding {
  ID: number;
  Ts: number;
  Agent: { String: string; Valid: boolean };
  Host: { String: string; Valid: boolean };
  Severity: { String: string; Valid: boolean };
  Title: { String: string; Valid: boolean };
  Status: { String: string; Valid: boolean };
  Tags: { String: string; Valid: boolean };
  Subtype: { String: string; Valid: boolean };
  Score: number;
  Signals: SuggestionSignal[];
}

export type SuggestionSignal = 'dedup_key' | 'ioc' | 'host_window' | 'daimon_sev' | 'ioc_cross_cp';

// SuggestionSettings — tunable knobs for the investigation suggestion
// engine, persisted in the meta k/v table. Mirrors the Go-side struct
// — JSON keys are snake_case so the meta blob is operator-readable.
export interface SuggestionSettings {
  threshold: number;
  weights: Partial<Record<SuggestionSignal, number>>;
  host_window_minutes: number;
  daimon_sev_window_hours: number;
  ioc_cross_cp_min_observations: number;
  ioc_cross_cp_window_hours: number;
  /** When > 0, newly-projected findings whose top score against an
   *  active case meets this value are auto-linked. 0 disables. */
  autolink_threshold: number;
}

// BulkLinkResult — per-finding outcome from POST
// /api/investigations/{id}/bulk-link-findings.
export interface BulkLinkResult {
  linked: number;
  failed: number;
  results: { finding_id: number; ok: boolean; error?: string }[];
}

// RelatedCase — server-scored active case that might be related to a
// given finding. Inverse view of SuggestedFinding (workspace card).
// Used by the Findings drawer's "looks related to N cases" banner.
export interface RelatedCase {
  InvestigationID: number;
  Title: string;
  Status: string;
  Score: number;
  Signals: SuggestionSignal[];
}

// InvestigationDetail is the shape of GET /api/investigations/{id}.
// The handler returns enriched lists for every workspace tab; see
// controlplane/api/investigations.go GetInvestigationHandler.
export interface InvestigationDetail {
  investigation: Investigation;
  findings: InvestigationFindingItem[];
  runs: InvestigationRunItem[];
  iocs: InvestigationIOCItem[];
  daimons: InvestigationDaimonItem[];
  orchestrations: InvestigationOrchestrationItem[];
  notes: InvestigationNote[];
  /** True iff any linked finding carries the `war-bridge` tag.
   *  UI flips into red-banner war-room mode + faster auto-refresh. */
  war_room: boolean;
}

export interface AuthConfig {
  oidc_enabled: boolean;
  oidc_label?: string;
}

// Orchestration types — chained agent runs.
export interface Orchestration {
  id: number;
  name: string;
  description?: string;
  spec_yaml: string;
  trigger_kind: 'manual' | 'finding' | 'cron';
  trigger_filter?: string;
  trigger_cron?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  /** Phase B: federation source. Non-null iff this row was fetched
   *  from a federated child CP. Local rows leave this undefined. */
  cp_source?: CPSourceRef;
}

export type OrchestrationRunStatus =
  | 'pending'
  | 'running'
  | 'approval_required'
  | 'completed'
  | 'failed'
  | 'cancelled';

export type OrchestrationStepStatus =
  | 'pending'
  | 'waiting_approval'
  | 'running'
  | 'completed'
  | 'failed'
  | 'skipped';

export interface StepNodeDispatchView {
  host: string;
  status: 'pending' | 'running' | 'completed' | 'failed';
  agent_run_id?: string;
  findings_count: number;
  output_tail?: string;
  error?: string;
  started_at?: string;
  ended_at?: string;
}

export type PromptEntityKind =
  | 'finding'
  | 'ioc'
  | 'node'
  | 'daimon'
  | 'run'
  | 'investigation'
  | 'orchestration'
  | 'cluster'
  | 'agent';

export interface PromptEntityRef {
  cp_instance_id?: string;
  kind: PromptEntityKind;
  id?: number;
  ioc_kind?: string;
  ioc_value?: string;
  snapshot: Record<string, unknown>;
  literal_hash: string;
}

export interface PromptEntities {
  refs: PromptEntityRef[];
}

export interface OrchestrationStepView {
  step_id: string;
  step_idx: number;
  status: OrchestrationStepStatus;
  run_id?: string;
  cp_instance_id?: string;
  rendered_prompt?: string;
  /** Phase 24: typed entity refs for SmartPayload's prompt-mode
   *  rendering. Absent on legacy steps; client falls back to shape
   *  detection in that case. */
  prompt_entities?: PromptEntities;
  result?: Record<string, unknown>;
  output_summary?: string;
  started_at?: string;
  ended_at?: string;
  error?: string;
  approved_at?: string;
  /** Phase 23.x: per-host fan-out dispatches. Present when the step
   *  used `nodes:` (fan-out). Empty for single-node steps and legacy
   *  runs without byNode data. */
  per_node?: StepNodeDispatchView[];
}

export interface OrchestrationRunView {
  id: number;
  orchestration_id: number;
  status: OrchestrationRunStatus;
  trigger_kind: string;
  trigger_payload?: Record<string, unknown>;
  current_step_id?: string;
  started_at: string;
  ended_at?: string;
  error?: string;
  steps?: OrchestrationStepView[];
  /** Phase B: federation source for runs federated from a child CP. */
  cp_source?: CPSourceRef;
}

export const api = {
  authConfig: () => request<AuthConfig>('/api/auth/config'),

  login: (email: string, password: string) =>
    request<User>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ email, password }),
    }),

  logout: () =>
    request<void>('/api/auth/logout', { method: 'POST' }),

  me: () => request<User>('/api/auth/me'),

  events: (limit = 100, beforeTs?: number, filter?: { agent?: string; host?: string }) => {
    const p = new URLSearchParams({ limit: String(limit) });
    if (beforeTs && beforeTs > 0) p.set('before_ts', String(beforeTs));
    if (filter?.agent) p.set('agent', filter.agent);
    if (filter?.host)  p.set('host',  filter.host);
    return request<EventItem[]>(`/api/events?${p.toString()}`);
  },

  // Daimon (registered long-running agent) listing/detail/config-patch.
  // Backend routes still use the legacy "/api/agents" path — UI-only rename.
  daimons: (limit?: number, offset?: number) => {
    const p = new URLSearchParams();
    if (limit) p.set('limit', String(limit));
    if (offset) p.set('offset', String(offset));
    const qs = p.toString();
    return request<DaimonItem[]>(qs ? `/api/agents?${qs}` : '/api/agents');
  },

  daimon: (name: string, host?: string, cp?: string) => {
    const q = new URLSearchParams();
    if (host) q.set('host', host);
    if (cp) q.set('cp', cp);
    const qs = q.toString();
    return request<DaimonItem>(`/api/agents/${encodeURIComponent(name)}${qs ? `?${qs}` : ''}`);
  },

  patchDaimon: (name: string, patch: DaimonConfigPatch) =>
    request<DaimonItem>(`/api/agents/${encodeURIComponent(name)}/config`, {
      method: 'PATCH',
      body: JSON.stringify(patch),
    }),

  // Daimon Library — long-form daimon definitions stored in
  // --daimon-files-dir on the CP host. Operators author/edit these
  // through the UI; deploys read from the same dir.
  daimonLibrary: () => request<DaimonLibraryItem[]>('/api/daimons/library'),
  daimonLibraryGet: (name: string) =>
    request<DaimonLibraryDetail>(`/api/daimons/library/${encodeURIComponent(name)}`),
  daimonLibrarySave: (name: string, content: string) =>
    request<DaimonLibraryDetail>(`/api/daimons/library/${encodeURIComponent(name)}`, {
      method: 'PUT',
      body: JSON.stringify({ content }),
    }),
  daimonLibraryDelete: (name: string) =>
    request<void>(`/api/daimons/library/${encodeURIComponent(name)}`, {
      method: 'DELETE',
    }),
  daimonLibraryRollback: (name: string) =>
    request<DaimonLibraryDetail>(`/api/daimons/library/${encodeURIComponent(name)}/rollback`, {
      method: 'POST',
    }),

  // Agent Library — short-form Claude/Codex agent definitions sourced
  // from ~/.claude/agents, ~/.codex/agents, and any --agent-files-dir
  // overrides the operator passed at startup.
  agentLibrary: () => request<AgentLibraryItem[]>('/api/agent-library'),
  agentLibraryGet: (name: string) =>
    request<AgentLibraryDetail>(`/api/agent-library/${encodeURIComponent(name)}`),
  agentLibrarySave: (name: string, content: string) =>
    request<AgentLibraryDetail>(`/api/agent-library/${encodeURIComponent(name)}`, {
      method: 'PUT',
      body: JSON.stringify({ content }),
    }),
  agentLibraryDelete: (name: string) =>
    request<void>(`/api/agent-library/${encodeURIComponent(name)}`, {
      method: 'DELETE',
    }),

  // Orchestrations — chained agent runs. All endpoints accept a
  // cpInstanceID to route to a federated child CP via ?cp=<id>; on
  // the parent CP, omit it for local-only ops.
  orchestrations: () => request<Orchestration[]>('/api/orchestrations'),
  orchestration: (id: number, cp?: string) =>
    request<Orchestration>(cpQuery(`/api/orchestrations/${id}`, cp)),
  orchestrationCreate: (specYAML: string, targetCPInstanceID?: string) =>
    request<Orchestration>('/api/orchestrations', {
      method: 'POST',
      body: JSON.stringify({
        spec_yaml: specYAML,
        ...(targetCPInstanceID ? { target_cp_instance_id: targetCPInstanceID } : {}),
      }),
    }),
  orchestrationUpdate: (id: number, specYAML: string, cp?: string) =>
    request<Orchestration>(cpQuery(`/api/orchestrations/${id}`, cp), {
      method: 'PUT',
      body: JSON.stringify({ spec_yaml: specYAML }),
    }),
  orchestrationDelete: (id: number, cp?: string) =>
    request<void>(cpQuery(`/api/orchestrations/${id}`, cp), { method: 'DELETE' }),
  orchestrationRun: (id: number, inputs?: Record<string, unknown>, cp?: string) =>
    request<{ run_id: number }>(cpQuery(`/api/orchestrations/${id}/run`, cp), {
      method: 'POST',
      body: JSON.stringify({ inputs: inputs ?? {} }),
    }),
  orchestrationRuns: () => request<OrchestrationRunView[]>('/api/orchestration-runs'),
  /** Bulk-cancel runs scoped to one CP (federated UI partitions per CP). */
  orchestrationRunsBulkCancel: (ids: number[], cp?: string) =>
    request<BulkRunOpResult>(cpQuery('/api/orchestration-runs/bulk-cancel', cp), {
      method: 'POST',
      body: JSON.stringify({ ids }),
    }),
  /** Bulk-retry: spawn fresh runs cloning trigger+orchestration of each input. */
  orchestrationRunsBulkRetry: (ids: number[], cp?: string) =>
    request<BulkRunOpResult>(cpQuery('/api/orchestration-runs/bulk-retry', cp), {
      method: 'POST',
      body: JSON.stringify({ ids }),
    }),
  /** Filter-aware variant. When `withCounts:true`, returns the
   *  wrapper shape `{rows, counts_by_status}` so the runs-tab status
   *  pills can populate from the same call. Filter values that match
   *  the URL params on the server side: status (csv), trigger_kind
   *  (csv), orchestration_id (csv), since ('30m'|'1h'|'24h'|'7d' or
   *  unix-ms), q (free-text), limit, offset. */
  orchestrationRunsFiltered: (
    f: OrchestrationRunsFilter,
    opts: { withCounts?: boolean } = {},
  ) => {
    const qs = new URLSearchParams();
    if (f.status?.length) qs.set('status', f.status.join(','));
    if (f.trigger_kind?.length) qs.set('trigger_kind', f.trigger_kind.join(','));
    if (f.orchestration_id?.length) qs.set('orchestration_id', f.orchestration_id.join(','));
    if (f.since) qs.set('since', f.since);
    if (f.q) qs.set('q', f.q);
    if (f.limit) qs.set('limit', String(f.limit));
    if (f.offset) qs.set('offset', String(f.offset));
    if (opts.withCounts) qs.set('counts', '1');
    const path = `/api/orchestration-runs${qs.size ? '?' + qs.toString() : ''}`;
    if (opts.withCounts) {
      return request<OrchestrationRunsListResponse>(path);
    }
    return request<OrchestrationRunView[]>(path).then((rows) => ({ rows, counts_by_status: {} }));
  },
  orchestrationRunDetail: (id: number, cp?: string) =>
    request<OrchestrationRunView>(cpQuery(`/api/orchestration-runs/${id}`, cp)),
  orchestrationRunCancel: (id: number, cp?: string) =>
    request<void>(cpQuery(`/api/orchestration-runs/${id}/cancel`, cp), { method: 'POST' }),
  orchestrationStepApprove: (runID: number, stepID: string, cp?: string) =>
    request<void>(
      cpQuery(`/api/orchestration-runs/${runID}/steps/${encodeURIComponent(stepID)}/approve`, cp),
      { method: 'POST' },
    ),

  // Investigations attached to a finding — runs whose finding_id == id.
  // `cpInstanceID` routes the lookup to a federated child CP via the
  // ?cp= proxy convention; finding ids are scoped per-CP.
  runsForFinding: (id: number, cpInstanceID?: string) =>
    request<RunListItem[]>(
      cpInstanceID
        ? `/api/findings/${id}/runs?cp=${encodeURIComponent(cpInstanceID)}`
        : `/api/findings/${id}/runs`,
    ),

  // Cases this finding is currently linked to. Drives the
  // InvestigateDialog's "Already in N cases" header so the operator
  // can deep-link instead of accidentally creating a duplicate case.
  findingRelatedCases: (
    id: number,
    opts?: { threshold?: number; limit?: number; cpInstanceID?: string },
  ) => {
    const p = new URLSearchParams();
    if (opts?.threshold != null) p.set('threshold', String(opts.threshold));
    if (opts?.limit != null) p.set('limit', String(opts.limit));
    if (opts?.cpInstanceID) p.set('cp', opts.cpInstanceID);
    const qs = p.toString();
    return request<RelatedCase[]>(`/api/findings/${id}/related-cases${qs ? `?${qs}` : ''}`);
  },
  findingInvestigations: (id: number, cpInstanceID?: string) =>
    request<Investigation[]>(
      cpInstanceID
        ? `/api/findings/${id}/investigations?cp=${encodeURIComponent(cpInstanceID)}`
        : `/api/findings/${id}/investigations`,
    ),

  // IOCs (Phase 22.1+; Catalog UI extends with source/q + per-id endpoints).
  // Without filters returns the most recent 100 by last_seen.
  iocs: (filter: { findingID?: number; kind?: string; source?: string; q?: string } = {}) => {
    const p = new URLSearchParams();
    if (filter.findingID) p.set('finding_id', String(filter.findingID));
    if (filter.kind)      p.set('kind', filter.kind);
    if (filter.source)    p.set('source', filter.source);
    if (filter.q)         p.set('q', filter.q);
    const qs = p.toString();
    return request<FederatedIOCRecord[]>(`/api/iocs${qs ? '?' + qs : ''}`);
  },

  // iocsWithWarning is a variant of iocs() that surfaces the
  // X-Okesu-Federation-Warning response header alongside the rows.
  // Used by the Catalog page to render a yellow banner when one or
  // more federated peers are unreachable.
  iocsWithWarning: async (filter: { findingID?: number; kind?: string; source?: string; q?: string } = {}) => {
    const p = new URLSearchParams();
    if (filter.findingID) p.set('finding_id', String(filter.findingID));
    if (filter.kind)      p.set('kind', filter.kind);
    if (filter.source)    p.set('source', filter.source);
    if (filter.q)         p.set('q', filter.q);
    const qs = p.toString();
    const res = await fetch(`/api/iocs${qs ? '?' + qs : ''}`, {
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
    });
    if (!res.ok) {
      const text = await res.text().catch(() => res.statusText);
      throw new ApiError(res.status, text || res.statusText);
    }
    const rows = (await res.json()) as FederatedIOCRecord[];
    const warning = res.headers.get('X-Okesu-Federation-Warning');
    return { rows: rows ?? [], warning };
  },

  ioc: (id: number) =>
    request<IOCRecord>(`/api/iocs/${id}`),

  iocObservations: (id: number) =>
    request<IOCObservation[]>(`/api/iocs/${id}/observations`),

  iocRelationships: (id: number) =>
    request<IOCRelationship[]>(`/api/iocs/${id}/relationships`),

  iocByKV: (kind: string, value: string) =>
    request<FederatedIOCRecord>(`/api/iocs/by-kv?kind=${encodeURIComponent(kind)}&value=${encodeURIComponent(value)}`),

  iocObservationsByKV: (kind: string, value: string) =>
    request<FederatedIOCObservation[]>(`/api/iocs/by-kv/observations?kind=${encodeURIComponent(kind)}&value=${encodeURIComponent(value)}`),

  iocRelationshipsByKV: (kind: string, value: string) =>
    request<FederatedIOCRelationship[]>(`/api/iocs/by-kv/relationships?kind=${encodeURIComponent(kind)}&value=${encodeURIComponent(value)}`),

  findings: (filter: FindingsFilter = {}) => {
    const p = new URLSearchParams();
    if (filter.severity?.length) p.set('severity', filter.severity.join(','));
    if (filter.agent)    p.set('agent', filter.agent);
    if (filter.host)     p.set('host', filter.host);
    if (filter.category) p.set('category', filter.category);
    if (filter.tag)      p.set('tag', filter.tag);
    if (filter.state)    p.set('state', filter.state);
    if (filter.since)    p.set('since', String(filter.since));
    if (filter.until)    p.set('until', String(filter.until));
    if (filter.limit)    p.set('limit', String(filter.limit));
    if (filter.offset)   p.set('offset', String(filter.offset));
    if (filter.host_selector) p.set('host_selector', filter.host_selector);
    const qs = p.toString();
    return request<Finding[]>(`/api/findings${qs ? '?' + qs : ''}`);
  },

  finding: (id: number, cpInstanceID?: string) =>
    request<Finding>(
      cpInstanceID
        ? `/api/findings/${id}?cp=${encodeURIComponent(cpInstanceID)}`
        : `/api/findings/${id}`,
    ),

  findingsSummary: () =>
    request<FindingsSummary>('/api/findings/summary'),

  findingsGrouped: (filter: FindingsFilter = {}) => {
    const p = new URLSearchParams();
    if (filter.severity?.length) p.set('severity', filter.severity.join(','));
    if (filter.agent)    p.set('agent', filter.agent);
    if (filter.host)     p.set('host', filter.host);
    if (filter.category) p.set('category', filter.category);
    if (filter.tag)      p.set('tag', filter.tag);
    if (filter.since)    p.set('since', String(filter.since));
    if (filter.limit)    p.set('limit', String(filter.limit));
    const qs = p.toString();
    return request<FindingGroup[]>(`/api/findings/grouped${qs ? '?' + qs : ''}`);
  },

  acknowledgeGroup: (req: { dedup_key?: string; title?: string; severity?: string; agent?: string; note?: string }) =>
    request<{ acked: number }>('/api/findings/group/acknowledge', {
      method: 'POST',
      body: JSON.stringify(req),
    }),

  setFindingStatus: (id: number, status: FindingStatus, opts?: { note?: string; cpInstanceID?: string }) =>
    request<Finding>(
      opts?.cpInstanceID
        ? `/api/findings/${id}/status?cp=${encodeURIComponent(opts.cpInstanceID)}`
        : `/api/findings/${id}/status`,
      {
        method: 'POST',
        body: JSON.stringify({ status, note: opts?.note ?? '' }),
      },
    ),

  setGroupStatus: (req: {
    status: FindingStatus;
    dedup_key?: string;
    title?: string;
    severity?: string;
    agent?: string;
    note?: string;
  }) =>
    request<{ changed: number; status: FindingStatus }>('/api/findings/group/status', {
      method: 'POST',
      body: JSON.stringify(req),
    }),

  acknowledgeFinding: (id: number, note?: string) =>
    request<Finding>(`/api/findings/${id}/acknowledge`, {
      method: 'POST',
      body: JSON.stringify({ note: note ?? '' }),
    }),

  unacknowledgeFinding: (id: number) =>
    request<Finding>(`/api/findings/${id}/acknowledge`, {
      method: 'POST',
      body: JSON.stringify({ clear: true }),
    }),

  // Phase 14 — severity overrides. `severity: ""` clears the override.
  // `apply_to_fingerprint=true` also creates a per-fingerprint rule so
  // future findings inherit the override automatically.
  setFindingSeverity: (id: number, req: {
    severity: '' | Severity;
    apply_to_fingerprint?: boolean;
    apply_to_group?: boolean;
    note?: string;
  }) =>
    request<Finding>(`/api/findings/${id}/severity`, {
      method: 'POST',
      body: JSON.stringify(req),
    }),

  severityRules: () =>
    request<SeverityRule[]>('/api/findings/severity-rules'),

  deleteSeverityRule: (fingerprint: string) =>
    request<void>('/api/findings/severity-rules', {
      method: 'DELETE',
      body: JSON.stringify({ fingerprint }),
    }),

  // Phase 22.8 PR γ — credential bindings. All admin-only. Plaintext
  // never crosses the wire; consumers read it directly inside the CP
  // process. The `value` field is write-only (used on POST and on
  // PATCH for rotation; never returned).
  secrets: {
    list: () => request<Secret[]>('/api/secrets'),
    get: (id: number) => request<SecretDetail>(`/api/secrets/${id}`),
    create: (req: { name: string; kind: SecretKind; description?: string; value: string; owner_group_id?: number }) =>
      request<Secret>('/api/secrets', { method: 'POST', body: JSON.stringify(req) }),
    update: (id: number, patch: { description?: string; owner_group_id?: number; value?: string }) =>
      request<void>(`/api/secrets/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),
    delete: (id: number) =>
      request<void>(`/api/secrets/${id}`, { method: 'DELETE' }),
    addBinding: (id: number, selector: string, scope: SecretScope) =>
      request<void>(`/api/secrets/${id}/bindings`, {
        method: 'POST',
        body: JSON.stringify({ selector, scope }),
      }),
    removeBinding: (id: number, bindingID: number) =>
      request<void>(`/api/secrets/${id}/bindings/${bindingID}`, { method: 'DELETE' }),
  },

  // Phase 22.8 PR α — groups + scoped roles. Multi-membership RBAC
  // replacing the single users.role enum. Existing role-gated paths
  // keep working via the migration backfill (default-<role> groups).
  // Selectors are accepted by the API but a no-op until PR β.
  groups: {
    list: () => request<Group[]>('/api/groups'),
    get: (id: number) => request<GroupDetail>(`/api/groups/${id}`),
    create: (req: { name: string; description?: string; external_id?: string }) =>
      request<Group>('/api/groups', { method: 'POST', body: JSON.stringify(req) }),
    update: (id: number, patch: { name: string; description: string; external_id: string }) =>
      request<void>(`/api/groups/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),
    delete: (id: number) =>
      request<void>(`/api/groups/${id}`, { method: 'DELETE' }),
    addRole: (id: number, role: string, selector: string = '') =>
      request<void>(`/api/groups/${id}/roles`, {
        method: 'POST',
        body: JSON.stringify({ role, selector }),
      }),
    removeRole: (id: number, roleID: number) =>
      request<void>(`/api/groups/${id}/roles/${roleID}`, { method: 'DELETE' }),
    addMember: (id: number, userID: number) =>
      request<void>(`/api/groups/${id}/members/${userID}`, { method: 'PUT' }),
    removeMember: (id: number, userID: number) =>
      request<void>(`/api/groups/${id}/members/${userID}`, { method: 'DELETE' }),
    myGroups: () => request<MyGroups>('/api/users/me/groups'),
  },

  // Phase 22.7 — operator-saved searches. Per-(user, scope) named
  // filter sets the Findings page persists so operators don't re-type
  // every filter. is_default is per-(user, scope); creating or
  // updating a row with is_default=true clears the previous default.
  savedSearches: {
    list: (scope: string = 'findings') =>
      request<SavedSearch[]>(`/api/saved-searches?scope=${encodeURIComponent(scope)}`),
    create: (req: { name: string; scope?: string; config: object; is_default?: boolean }) =>
      request<SavedSearch>('/api/saved-searches', {
        method: 'POST',
        body: JSON.stringify({
          name: req.name,
          scope: req.scope ?? 'findings',
          config_json: req.config,
          is_default: req.is_default ?? false,
        }),
      }),
    update: (id: number, patch: { name?: string; config?: object; is_default?: boolean }) =>
      request<void>(`/api/saved-searches/${id}`, {
        method: 'PATCH',
        body: JSON.stringify({
          ...(patch.name !== undefined ? { name: patch.name } : {}),
          ...(patch.config !== undefined ? { config_json: patch.config } : {}),
          ...(patch.is_default !== undefined ? { is_default: patch.is_default } : {}),
        }),
      }),
    delete: (id: number) =>
      request<void>(`/api/saved-searches/${id}`, { method: 'DELETE' }),
  },

  // Phase 22.3 — investigations (T2 case workspace). Operators open
  // a case from a finding, link more findings/runs as the case
  // develops, capture analyst notes, and close with a resolution.
  investigations: {
    list: (status?: string) =>
      request<Investigation[]>(
        `/api/investigations${status ? `?status=${encodeURIComponent(status)}` : ''}`,
      ),
    get: (id: number, cpInstanceID?: string) => {
      const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
      return request<InvestigationDetail>(`/api/investigations/${id}${qs}`);
    },
    create: (
      req: { title: string; summary?: string; from_finding_id?: number; created_by?: string },
      cpInstanceID?: string,
    ) => {
      // Federated finding flow: when the finding lives on a child CP,
      // append ?cp=<id> so the parent forwards the create to that
      // child. Otherwise the FK on investigation_findings.finding_id
      // would fail because the parent doesn't have a row for the
      // federated finding's id.
      const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
      return request<Investigation>(`/api/investigations${qs}`, {
        method: 'POST',
        body: JSON.stringify(req),
      });
    },
    update: (id: number, patch: Partial<{ title: string; status: string; resolution: string; summary: string }>) =>
      request<Investigation>(`/api/investigations/${id}`, {
        method: 'PATCH',
        body: JSON.stringify(patch),
      }),
    addNote: (id: number, author: string, body: string) =>
      request<{ id: number }>(`/api/investigations/${id}/notes`, {
        method: 'POST',
        body: JSON.stringify({ author, body }),
      }),
    linkFinding: (invID: number, findingID: number, cpInstanceID?: string) => {
      const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
      return request<void>(`/api/investigations/${invID}/findings/${findingID}${qs}`, {
        method: 'PUT',
      });
    },
    unlinkFinding: (invID: number, findingID: number) =>
      request<void>(`/api/investigations/${invID}/findings/${findingID}`, {
        method: 'DELETE',
      }),
    linkRun: (invID: number, runID: number, cpInstanceID?: string) => {
      const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
      return request<void>(`/api/investigations/${invID}/runs/${runID}${qs}`, {
        method: 'PUT',
      });
    },
    unlinkRun: (invID: number, runID: number) =>
      request<void>(`/api/investigations/${invID}/runs/${runID}`, {
        method: 'DELETE',
      }),
    suggestFindings: (
      invID: number,
      opts?: { threshold?: number; limit?: number; cpInstanceID?: string },
    ) => {
      const p = new URLSearchParams();
      if (opts?.threshold != null) p.set('threshold', String(opts.threshold));
      if (opts?.limit != null) p.set('limit', String(opts.limit));
      if (opts?.cpInstanceID) p.set('cp', opts.cpInstanceID);
      const qs = p.toString();
      return request<SuggestedFinding[]>(
        `/api/investigations/${invID}/suggested-findings${qs ? `?${qs}` : ''}`,
      );
    },
    dismissSuggestedFinding: (
      invID: number,
      findingID: number,
      opts?: { dismissedBy?: string; cpInstanceID?: string },
    ) => {
      const qs = opts?.cpInstanceID ? `?cp=${encodeURIComponent(opts.cpInstanceID)}` : '';
      return request<void>(
        `/api/investigations/${invID}/dismissed-findings/${findingID}${qs}`,
        {
          method: 'PUT',
          body: JSON.stringify({ dismissed_by: opts?.dismissedBy ?? '' }),
        },
      );
    },
    audit: (invID: number, cpInstanceID?: string) => {
      const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
      return request<InvestigationAuditEvent[]>(`/api/investigations/${invID}/audit${qs}`);
    },
    bulkLinkFindings: (
      invID: number,
      findingIDs: number[],
      cpInstanceID?: string,
    ) => {
      const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
      return request<BulkLinkResult>(
        `/api/investigations/${invID}/bulk-link-findings${qs}`,
        {
          method: 'POST',
          body: JSON.stringify({ finding_ids: findingIDs }),
        },
      );
    },
    settings: {
      get: () => request<SuggestionSettings>('/api/investigations/settings'),
      put: (s: SuggestionSettings) =>
        request<SuggestionSettings>('/api/investigations/settings', {
          method: 'PUT',
          body: JSON.stringify(s),
        }),
    },
  },

  // Phase 5 — nodes & deploy.
  nodes: (limit?: number, offset?: number) => {
    const p = new URLSearchParams();
    if (limit) p.set('limit', String(limit));
    if (offset) p.set('offset', String(offset));
    const qs = p.toString();
    return request<NodeItem[]>(qs ? `/api/nodes?${qs}` : '/api/nodes');
  },
  node: (id: number, cp?: string) =>
    request<NodeItem>(`/api/nodes/${id}${cp ? `?cp=${cp}` : ''}`),
  createNode: (req: NodeCreateReq) =>
    request<NodeItem>('/api/nodes', { method: 'POST', body: JSON.stringify(req) }),
  deleteNode: (id: number) =>
    request<void>(`/api/nodes/${id}`, { method: 'DELETE' }),
  refreshNodeMetadata: (id: number) =>
    request<NodeItem>(`/api/nodes/${id}/refresh-metadata`, { method: 'POST' }),

  // Phase 22.8 PR β — node labels. Read is open to any logged-in
  // user (labels show on node detail); set/delete are admin only.
  nodeLabels: (id: number) =>
    request<Record<string, string>>(`/api/nodes/${id}/labels`),
  setNodeLabel: (id: number, key: string, value: string) =>
    request<void>(`/api/nodes/${id}/labels`, {
      method: 'PUT',
      body: JSON.stringify({ key, value }),
    }),
  deleteNodeLabel: (id: number, key: string) =>
    request<void>(`/api/nodes/${id}/labels/${encodeURIComponent(key)}`, { method: 'DELETE' }),

  // Phase 22.9 — generic labels API. Same shape as the per-node
  // helpers above, parameterised by entity kind. id_or_key is
  // either a numeric id (most kinds) or a string composite key
  // (daimon = "name@host"; federation peer = instance UUID).
  labels: (kind: LabelKind, idOrKey: string | number) =>
    request<Record<string, string>>(`/api/labels/${kind}/${encodeURIComponent(String(idOrKey))}`),
  setLabel: (kind: LabelKind, idOrKey: string | number, key: string, value: string) =>
    request<void>(`/api/labels/${kind}/${encodeURIComponent(String(idOrKey))}`, {
      method: 'PUT',
      body: JSON.stringify({ key, value }),
    }),
  deleteLabel: (kind: LabelKind, idOrKey: string | number, key: string) =>
    request<void>(`/api/labels/${kind}/${encodeURIComponent(String(idOrKey))}/${encodeURIComponent(key)}`, {
      method: 'DELETE',
    }),
  searchLabels: (kind: LabelKind, selector: string) => {
    const qs = new URLSearchParams({ kind, selector });
    return request<LabelTarget[]>(`/api/labels/search?${qs.toString()}`);
  },
  allLabels: (filter: { kind?: LabelKind; key?: string; value?: string; limit?: number } = {}) => {
    const qs = new URLSearchParams();
    if (filter.kind)  qs.set('kind',  filter.kind);
    if (filter.key)   qs.set('key',   filter.key);
    if (filter.value) qs.set('value', filter.value);
    if (filter.limit) qs.set('limit', String(filter.limit));
    const s = qs.toString();
    return request<LabelRow[]>(`/api/labels/all${s ? '?' + s : ''}`);
  },
  labelKeys: (kind: LabelKind) => {
    const qs = new URLSearchParams({ kind });
    return request<string[]>(`/api/labels/keys?${qs.toString()}`);
  },
  labelValues: (kind: LabelKind, key: string) => {
    const qs = new URLSearchParams({ kind, key });
    return request<string[]>(`/api/labels/values?${qs.toString()}`);
  },

  setNodeAutoUpdatePaused: (id: number, paused: boolean) =>
    request<NodeItem>(`/api/nodes/${id}/auto-update`, {
      method: 'PUT',
      body: JSON.stringify({ paused }),
    }),

  // Phase 7c — binary update + rollback. Streams job log via the
  // existing /api/jobs/{id}/log SSE endpoint that deploys already use.
  updateNodeBinary: (id: number, req: { private_key: string; passphrase?: string; sudo_password?: string }) =>
    request<{ job_id: string; node_id: number }>(`/api/nodes/${id}/update-binary`, {
      method: 'POST',
      body: JSON.stringify(req),
    }),

  rollbackNodeBinary: (id: number, req: { private_key: string; passphrase?: string; sudo_password?: string }) =>
    request<{ job_id: string; node_id: number }>(`/api/nodes/${id}/rollback-binary`, {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  deployNode: (id: number, req: NodeDeployReq) =>
    request<{ job_id: string; node_id: number }>(`/api/nodes/${id}/deploy`, {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  /** Phase 4: install the host-side jobs runtime on a node. Same
   *  SSH-credential shape as deployNode. Returns a job id; poll
   *  via api.job(jobId). */
  installJobsRuntime: (id: number, req: {
    private_key: string;
    passphrase?: string;
    sudo_password?: string;
    ssh_user?: string;
    ssh_port?: number;
  }) =>
    request<{ job_id: string; node_id: number }>(`/api/nodes/${id}/install-jobs-runtime`, {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  nodeLibrary: () =>
    request<{ agents: string[]; daemon_binary_path: string; agent_files_dir: string }>(
      '/api/nodes/library',
    ),
  job: (id: string) => request<JobSnapshot>(`/api/jobs/${id}`),

  // Phase 9 — S3 dead-drop transport: bucket-backed deploy that doesn't
  // require any inbound reachability. Operator generates a fleet
  // package, drops it on N hosts, each host self-registers via the
  // shared bucket.
  transportConfigs: () =>
    request<TransportConfigSummary[]>('/api/transport-configs'),
  transportConfigCreate: (req: TransportConfigCreateReq) =>
    request<TransportConfigSummary>('/api/transport-configs', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  bucketCloudProviders: () =>
    request<BucketCloudProvider[]>('/api/buckets/cloud-providers'),

  bucketsDiscover: (cloudCredentialID: number, region: string) =>
    request<BucketInfo[]>(
      `/api/buckets/discover?cloud_credential_id=${cloudCredentialID}&region=${encodeURIComponent(region)}`,
    ),

  bucketsProvision: (req: BucketProvisionReq) =>
    request<TransportConfigSummary>('/api/buckets/provision', {
      method: 'POST',
      body: JSON.stringify(req),
    }),

  transportConfigPatch: (id: number, patch: TransportConfigPatch) =>
    request<TransportConfigSummary>(`/api/transport-configs/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(patch),
    }),

  // Throws ApiError(409) on in-use conflict; the caller catches and
  // decodes the response body via err.body for the referenced_by list.
  transportConfigDelete: (id: number) =>
    request<void>(`/api/transport-configs/${id}`, { method: 'DELETE' }),

  enrollmentPackages: () =>
    request<EnrollmentPackageSummary[]>('/api/enrollment-packages'),
  enrollmentPackageCreate: (req: EnrollmentPackageCreateReq) =>
    request<EnrollmentPackageSummary>('/api/enrollment-packages', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  /** Returns the package URL the operator should open / fetch. We
   *  expose it as a URL rather than fetching here so the browser
   *  drives the download natively (Content-Disposition handles the
   *  filename). */
  enrollmentPackageDownloadURL: (id: number, format: string) =>
    `/api/enrollment-packages/${id}/download?format=${encodeURIComponent(format)}`,
  enrollmentPackageRevoke: (id: number) =>
    request<void>(`/api/enrollment-packages/${id}/revoke`, { method: 'POST' }),

  // Phase 11.4 — finding edit history + run↔finding linkage. Used by
  // the finding-detail History panel and the run-detail "Actions
  // applied" surface.
  findingHistory: (id: number) =>
    request<FindingEditEntry[]>(`/api/findings/${id}/history`),
  findingLinkedRuns: (id: number) =>
    request<FindingRunLinkEntry[]>(`/api/findings/${id}/runs`),
  runLinkedFindings: (id: number) =>
    request<FindingRunLinkEntry[]>(`/api/orchestration-runs/${id}/findings`),

  // Phase 7 — Settings
  about: () => request<AboutInfo>('/api/system/about'),

  // ── Dashboard ────────────────────────────────────────────────────
  dashboard: () => request<DashboardResponse>('/api/dashboard'),
  insightsFindings: (params: { since?: TimeRange; group_by?: 'severity' | 'agent' | 'host'; top?: number }) => {
    const qs = new URLSearchParams();
    if (params.since) qs.set('since', params.since);
    if (params.group_by) qs.set('group_by', params.group_by);
    if (params.top) qs.set('top', String(params.top));
    return request<InsightsFindingsResponse>(`/api/insights/findings?${qs.toString()}`);
  },
  insightsEvents: (params: { since?: TimeRange }) => {
    const qs = new URLSearchParams();
    if (params.since) qs.set('since', params.since);
    return request<InsightsEventsResponse>(`/api/insights/events?${qs.toString()}`);
  },
  insightsTriageOutcomes: (params: { since?: TimeRange }) => {
    const qs = new URLSearchParams();
    if (params.since) qs.set('since', params.since);
    return request<InsightsTriageOutcomesResponse>(`/api/insights/triage-outcomes?${qs.toString()}`);
  },
  insightsOrchestrationsTop: (params: { since?: TimeRange; limit?: number }) => {
    const qs = new URLSearchParams();
    if (params.since) qs.set('since', params.since);
    if (params.limit) qs.set('limit', String(params.limit));
    return request<InsightsOrchestrationsTopResponse>(`/api/insights/orchestrations-top?${qs.toString()}`);
  },
  users: () => request<UserItem[]>('/api/users'),
  user: (id: number) => request<UserItem>(`/api/users/${id}`),
  createUser: (req: { email: string; role: string; password: string }) =>
    request<UserItem>('/api/users', { method: 'POST', body: JSON.stringify(req) }),
  patchUser: (id: number, req: { role?: string; password?: string }) =>
    request<UserItem>(`/api/users/${id}`, { method: 'PATCH', body: JSON.stringify(req) }),
  deleteUser: (id: number) =>
    request<void>(`/api/users/${id}`, { method: 'DELETE' }),
  changeMyPassword: (current: string, next: string) =>
    request<void>('/api/users/me/password', {
      method: 'POST',
      body: JSON.stringify({ current, new: next }),
    }),
  mySessions: () => request<SessionInfo[]>('/api/users/me/sessions'),
  revokeOtherSessions: () =>
    request<void>('/api/users/me/sessions', { method: 'DELETE' }),

  // Phase 8 — deploy hardening
  deployBinaries: () =>
    request<{ dir: string; binaries: BinaryItem[] }>('/api/deploy/binaries'),
  uploadBinary: (osName: string, arch: string, file: File) => {
    const fd = new FormData();
    fd.append('os', osName);
    fd.append('arch', arch);
    fd.append('file', file);
    return fetch('/api/deploy/binaries', { method: 'POST', body: fd, credentials: 'same-origin' })
      .then(async (res) => {
        if (!res.ok) {
          const text = await res.text();
          throw new ApiError(res.status, text);
        }
        return res.json() as Promise<BinaryItem>;
      });
  },
  deleteBinary: (name: string) =>
    request<void>(`/api/deploy/binaries/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  knownHosts: () => request<KnownHostItem[]>('/api/deploy/known-hosts'),
  nodeKnownHost: (id: number) =>
    request<KnownHostItem | null>(`/api/nodes/${id}/known-host`),

  // Stored CP-wide SSH key for deploys (Settings → Deploy → SSH Key).
  // GET returns metadata only — the key bytes never leave the server.
  deployKeyStatus: () => request<DeployKeyStatus>('/api/system/deploy-ssh-key'),
  setDeployKey: (privateKey: string) =>
    request<DeployKeyStatus>('/api/system/deploy-ssh-key', {
      method: 'PUT',
      body: privateKey,
      headers: { 'Content-Type': 'application/x-pem-file' },
    }),
  clearDeployKey: () =>
    request<void>('/api/system/deploy-ssh-key', { method: 'DELETE' }),
  clearNodeKnownHost: (id: number) =>
    request<void>(`/api/nodes/${id}/known-host`, { method: 'DELETE' }),

  audit: (params: AuditQuery = {}) => {
    const p = new URLSearchParams();
    if (params.actor)  p.set('actor', params.actor);
    if (params.action) p.set('action', params.action);
    if (params.target) p.set('target', params.target);
    if (params.result) p.set('result', params.result);
    if (params.limit)  p.set('limit', String(params.limit));
    if (params.offset) p.set('offset', String(params.offset));
    const qs = p.toString();
    return request<AuditEntry[]>(`/api/audit${qs ? '?' + qs : ''}`);
  },

  // Phase 6 — ad-hoc runs over reverse tunnel.
  connectedNodes: () => request<string[]>('/api/nodes/connected'),
  // Phase 22.7 — reachable nodes across all transports (tunnel +
  // HTTPS pull; S3 once the bucket-side writer lands). The Run-Agent
  // dialog uses this so nodes without an attached tunnel still
  // surface when they have a fresh okesu-jobs.service poll.
  reachableNodes: () => request<ReachableNode[]>('/api/nodes/reachable'),
  runs: (limit?: number, offset?: number) => {
    const p = new URLSearchParams();
    if (limit) p.set('limit', String(limit));
    if (offset) p.set('offset', String(offset));
    const qs = p.toString();
    return request<RunListItem[]>(`/api/runs${qs ? '?' + qs : ''}`);
  },
  run: (id: string) => request<RunDetail>(`/api/runs/${id}`),
  createRun: (req: CreateRunReq) =>
    request<{ run_id: string; node: string }>('/api/runs', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  cancelRun: (id: string, cpInstanceID?: string) => {
    const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
    return request<void>(`/api/runs/${id}/cancel${qs}`, { method: 'POST' });
  },

  // Phase 10 — system / database.
  dbStats: () => request<DBStatsResponse>('/api/system/db/stats'),
  dbVacuum: () =>
    request<{ ok: boolean; duration_ms: number }>('/api/system/db/vacuum', { method: 'POST' }),
  dbPruneEvents: (olderThanDays: number) =>
    request<{ deleted: number; older_than_days: number }>(
      `/api/system/db/prune-events?older_than_days=${olderThanDays}`,
      { method: 'POST' },
    ),

  // Phase 22.10 PR β — on-demand stale-finding GC. Closes "open"
  // findings that haven't been re-emitted within `threshold_hours`
  // (default 24). Same logic as the hourly background sweep; this
  // surface lets admins kick the broom on demand for one-shot
  // cleanup after a deploy.
  findingsGCStale: (req: { threshold_hours?: number; limit?: number } = {}) =>
    request<{ closed: number; scanned: number; threshold_ms: number; threshold_str: string }>(
      '/api/findings/gc-stale',
      { method: 'POST', body: JSON.stringify(req) },
    ),

  // Phase 22.10 PR γ — per-label severity ceilings. Match a node's
  // labels via selector, cap any finding emitted on that node at
  // max_severity. CRUD admin-only.
  severityCeilings: () => request<SeverityCeiling[]>('/api/severity-ceilings'),
  createSeverityCeiling: (req: { selector: string; max_severity: string; reason?: string }) =>
    request<{ id: number }>('/api/severity-ceilings', {
      method: 'POST', body: JSON.stringify(req),
    }),
  updateSeverityCeiling: (id: number, req: { max_severity: string; reason?: string }) =>
    request<void>(`/api/severity-ceilings/${id}`, {
      method: 'PATCH', body: JSON.stringify(req),
    }),
  deleteSeverityCeiling: (id: number) =>
    request<void>(`/api/severity-ceilings/${id}`, { method: 'DELETE' }),

  // Phase 9 — notification channels, rules, deliveries.
  channels: () => request<Channel[]>('/api/notifications/channels'),
  createChannel: (req: ChannelCreateReq) =>
    request<Channel>('/api/notifications/channels', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  patchChannel: (id: number, req: ChannelPatchReq) =>
    request<Channel>(`/api/notifications/channels/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(req),
    }),
  deleteChannel: (id: number) =>
    request<void>(`/api/notifications/channels/${id}`, { method: 'DELETE' }),
  testChannel: (id: number) =>
    request<void>(`/api/notifications/channels/${id}/test`, { method: 'POST' }),

  rules: () => request<Rule[]>('/api/notifications/rules'),
  createRule: (req: RuleCreateReq) =>
    request<Rule>('/api/notifications/rules', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  patchRule: (id: number, req: RulePatchReq) =>
    request<Rule>(`/api/notifications/rules/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(req),
    }),
  deleteRule: (id: number) =>
    request<void>(`/api/notifications/rules/${id}`, { method: 'DELETE' }),

  deliveries: (limit = 50) =>
    request<Delivery[]>(`/api/notifications/deliveries?limit=${limit}`),

  // Phase 9 — API tokens.
  tokens: () => request<Token[]>('/api/tokens'),
  createToken: (req: TokenCreateReq) =>
    request<TokenCreateResp>('/api/tokens', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  revokeToken: (id: number) =>
    request<void>(`/api/tokens/${id}`, { method: 'DELETE' }),

  // Phase 9.5 — federation (parent-side aggregator).
  federationPeers: () =>
    request<FederationPeer[]>('/api/federation/peers'),
  addFederationPeer: (req: FederationPeerAddReq) =>
    request<FederationPeer>('/api/federation/peers', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  deleteFederationPeer: (id: number) =>
    request<void>(`/api/federation/peers/${id}`, { method: 'DELETE' }),
  refreshFederationPeer: (id: number) =>
    request<FederationPeer>(`/api/federation/peers/${id}/refresh`, { method: 'POST' }),

  // Phase 21.2 — cloud credentials. Payload-only fields (the cloud-
  // specific secrets) are write-only at this layer: list/get/test
  // never roundtrip the plaintext through the browser. Editing means
  // re-entering the secrets from scratch.
  cloudCredentialsList: (cloud?: string) => {
    const qs = cloud ? `?cloud=${encodeURIComponent(cloud)}` : '';
    return request<CloudCredential[]>(`/api/cloud-credentials${qs}`);
  },
  cloudCredentialCreate: (req: CloudCredentialCreateRequest) =>
    request<CloudCredential>('/api/cloud-credentials', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  /** Partial update. Only fields with a value are persisted; payload
   *  keys with empty strings are dropped server-side so secret
   *  fields the operator didn't re-type stay intact. */
  cloudCredentialUpdate: (id: number, req: CloudCredentialUpdateRequest) =>
    request<CloudCredential>(`/api/cloud-credentials/${id}`, {
      method: 'PUT',
      body: JSON.stringify(req),
    }),
  cloudCredentialDelete: (id: number) =>
    request<void>(`/api/cloud-credentials/${id}`, { method: 'DELETE' }),
  cloudCredentialTest: (id: number) =>
    request<{ ok: boolean; message: string }>(`/api/cloud-credentials/${id}/test`, { method: 'POST' }),
  // Phase 21.5 — set or clear the per-credential monthly USD budget.
  // Pass null to clear; the budget enforcement on cp_provision.create
  // re-reads this value on every submit.
  cloudCredentialBudget: (id: number, monthlyBudgetUSD: number | null) =>
    request<CloudCredential>(`/api/cloud-credentials/${id}/budget`, {
      method: 'PUT',
      body: JSON.stringify({ monthly_budget_usd: monthlyBudgetUSD }),
    }),

  // Fleet-env (Settings → LLM Keys). GET returns the masked summary;
  // PUT applies a partial patch (omit field = leave alone, "" =
  // delete, non-empty = overwrite). On a federated child the
  // override-local / revert-to-parent endpoints flip the source flag
  // without touching the keys themselves.
  fleetEnv: () => request<FleetEnvSummary>('/api/fleet-env'),
  fleetEnvUpdate: (patch: FleetEnvPatch) =>
    request<FleetEnvSummary>('/api/fleet-env', {
      method: 'PUT',
      body: JSON.stringify(patch),
    }),
  fleetEnvOverrideLocal: () =>
    request<FleetEnvSummary>('/api/fleet-env/override-local', { method: 'POST' }),
  fleetEnvRevertToParent: () =>
    request<FleetEnvSummary>('/api/fleet-env/revert-to-parent', { method: 'POST' }),

  // Phase 23 — IOC Feeds. List + registry are viewer-readable;
  // mutation, refresh, validate, and consent are admin-only on the
  // backend.
  feeds: () => request<FeedConfig[]>('/api/feeds'),
  feedsRegistry: () => request<FeedRegistryDef[]>('/api/feeds/registry'),
  feedInstall: (req: FeedInstallRequest) =>
    request<FeedInstallResponse>('/api/feeds', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  feedUpdate: (id: number, patch: FeedConfigPatch) =>
    request<void>(`/api/feeds/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(patch),
    }),
  feedUninstall: (id: number) =>
    request<void>(`/api/feeds/${id}`, { method: 'DELETE' }),
  feedRefresh: (id: number) =>
    request<void>(`/api/feeds/${id}/refresh`, { method: 'POST' }),
  feedValidate: (spec: FeedConfigInsert) =>
    request<FeedValidateResponse>('/api/feeds/validate', {
      method: 'POST',
      body: JSON.stringify(spec),
    }),
  feedsConsent: () =>
    request<FeedsConsentResponse>('/api/feeds/consent', { method: 'POST' }),

  // Phase 21.3 — managed CP provisioning. The registry is empty in
  // 21.3a (the framework PR); per-cloud impls register against it in
  // 21.3b (OCI), 21.3c (AWS), etc. Until then cpProvisionersList()
  // returns {clouds: []} and the +Add CP modal's Managed-deploy tab
  // shows a "no clouds yet" message.
  cpProvisionersList: () =>
    request<{ clouds: string[] }>('/api/federation/cp-provisioners'),
  cpProvisionsList: (limit?: number) => {
    const qs = limit ? `?limit=${limit}` : '';
    return request<CPProvision[]>(`/api/federation/cp-provisions${qs}`);
  },
  cpProvision: (id: number) =>
    request<CPProvision>(`/api/federation/cp-provisions/${id}`),
  cpProvisionCreate: (req: CPProvisionRequest) =>
    request<CPProvision>('/api/federation/cp-provision', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  /** Delete a provision row. When `destroy=true` AND the row has a
   *  cloud_resource_id, the parent first calls Provisioner.Destroy to
   *  terminate the cloud-side instance. Without that flag the cloud
   *  resource is left alone — the safe default for "clean up failed
   *  rows" where there's no live VM to kill. */
  cpProvisionDelete: (id: number, destroy = false) =>
    request<void | { deleted: boolean; destroy_error?: string; cloud_resource?: string }>(
      `/api/federation/cp-provisions/${id}${destroy ? '?destroy=true' : ''}`,
      { method: 'DELETE' },
    ),
  // Phase 21.5 — read-only cost preview for the +Add CP modal.
  cpProvisionEstimate: (req: CPProvisionEstimateRequest) =>
    request<CPProvisionEstimate>('/api/federation/cp-provision/estimate', {
      method: 'POST',
      body: JSON.stringify(req),
    }),

  // Cloud-side discovery: dropdown population for the +Add CP managed
  // deploy form. The server decrypts the credential, calls the OCI
  // SDK, and returns {id, name, attrs}[] so the UI doesn't need to
  // know SDK shapes.
  cloudDiscoveryOCI: {
    compartments: (credId: number, region?: string) =>
      request<DiscoveryItem[]>(`/api/cloud-credentials/${credId}/oci/compartments${region ? `?region=${encodeURIComponent(region)}` : ''}`),
    availabilityDomains: (credId: number, compartmentId: string, region?: string) => {
      const qs = new URLSearchParams({ compartment_id: compartmentId });
      if (region) qs.set('region', region);
      return request<DiscoveryItem[]>(`/api/cloud-credentials/${credId}/oci/availability-domains?${qs}`);
    },
    subnets: (credId: number, compartmentId: string, opts?: { region?: string; vcnId?: string }) => {
      const qs = new URLSearchParams({ compartment_id: compartmentId });
      if (opts?.region) qs.set('region', opts.region);
      if (opts?.vcnId) qs.set('vcn_id', opts.vcnId);
      return request<DiscoveryItem[]>(`/api/cloud-credentials/${credId}/oci/subnets?${qs}`);
    },
    images: (credId: number, compartmentId: string, opts?: { region?: string; os?: string; shape?: string }) => {
      const qs = new URLSearchParams({ compartment_id: compartmentId });
      if (opts?.region) qs.set('region', opts.region);
      if (opts?.os) qs.set('os', opts.os);
      if (opts?.shape) qs.set('shape', opts.shape);
      return request<DiscoveryItem[]>(`/api/cloud-credentials/${credId}/oci/images?${qs}`);
    },
    shapes: (credId: number, compartmentId: string, opts?: { region?: string; availabilityDomain?: string }) => {
      const qs = new URLSearchParams({ compartment_id: compartmentId });
      if (opts?.region) qs.set('region', opts.region);
      if (opts?.availabilityDomain) qs.set('availability_domain', opts.availabilityDomain);
      return request<DiscoveryItem[]>(`/api/cloud-credentials/${credId}/oci/shapes?${qs}`);
    },
  },

  // Phase 21.1 — generate a bootstrap bundle for a fresh child CP.
  // Returns the tar.gz response as a Blob the caller hands to the
  // browser's download flow. The endpoint sends Content-Disposition
  // with the right filename; we read it off the headers so the
  // saved file matches what the server emitted.
  cpBundle: async (req: CPBundleRequest): Promise<{ blob: Blob; filename: string }> => {
    const resp = await fetch('/api/federation/cp-bundle', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
    if (!resp.ok) {
      const text = await resp.text();
      throw new ApiError(resp.status, text || resp.statusText);
    }
    const cd = resp.headers.get('Content-Disposition') || '';
    const m = /filename="([^"]+)"/.exec(cd);
    const filename = m ? m[1] : 'okesu-cp-bundle.tar.gz';
    const blob = await resp.blob();
    return { blob, filename };
  },
};

export interface CPBundleRequest {
  display_name: string;
  region: string;
  format: 'dockerfile-tarball' | 'compose-tarball' | 'terraform' | 's3-dead-drop';
  cloud?: 'oci' | 'aws';
  parent_url?: string;
  child_host?: string;
  child_port?: number;
  mgmt_port?: number;
  with_api_keys?: boolean;
  /** Required for format=s3-dead-drop. Identifies the bucket the
   *  child publishes to + the parent reads from. */
  transport_config_id?: number;
}

// ── Phase 9 — Notifications ────────────────────────────────────────────────

export interface Channel {
  id: number;
  name: string;
  type: 'slack' | 'email' | 'webhook';
  config: Record<string, unknown>;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  created_by_email?: string;
}

export interface ChannelCreateReq {
  name: string;
  type: Channel['type'];
  config: unknown;
}

export interface ChannelPatchReq {
  name?: string;
  config?: unknown;
  enabled?: boolean;
}

export interface Rule {
  id: number;
  name: string;
  channel_id: number;
  min_severity: string;
  agent_substring?: string;
  host_substring?: string;
  host_selector?: string;
  enabled: boolean;
  created_at: string;
}

export interface RuleCreateReq {
  name: string;
  channel_id: number;
  min_severity: string;
  agent_substring?: string;
  host_substring?: string;
  host_selector?: string;
  enabled?: boolean;
}

export interface RulePatchReq {
  name?: string;
  channel_id?: number;
  min_severity?: string;
  agent_substring?: string;
  host_substring?: string;
  host_selector?: string;
  enabled?: boolean;
}

export interface Delivery {
  id: number;
  rule_id?: number;
  channel_id?: number;
  finding_id?: number;
  severity?: string;
  title?: string;
  status: 'succeeded' | 'failed' | 'pending';
  attempt: number;
  error?: string;
  created_at: string;
  finished_at?: string;
}

// ── Phase 9 — API tokens ───────────────────────────────────────────────────

export interface Token {
  id: number;
  name: string;
  prefix: string;
  scopes: string[];
  created_at: string;
  expires_at?: string;
  last_used_at?: string;
  revoked_at?: string;
  created_by_email?: string;
}

export interface TokenCreateReq {
  name: string;
  scopes: string[];
  expires_in_days?: number;
}

export interface TokenCreateResp extends Token {
  token: string; // plaintext, shown once on creation
}

export interface CreateRunReq {
  node: string;
  prompt: string;
  provider?: 'claude' | 'codex' | 'auto';
  model?: string;
  effort?: string;
  agent?: string;
  max_turns?: number;
  /** Optional. Links the run to a finding so the FindingDrawer surfaces
   *  it as an "investigation" attached to that finding. */
  finding_id?: number;
  /** Phase 9.7: when set, the parent forwards the create call to the
   *  named child CP. Set this from the node's `cp_source.instance_id`
   *  when running an agent against a federated node. */
  target_cp_instance_id?: string;
}

export interface RunListItem {
  id: string;
  node: string;
  provider: string;
  agent?: string;
  prompt: string;
  started_at: string;
  finished_at?: string;
  status: 'running' | 'succeeded' | 'failed' | 'cancelled';
  exit_code: number;
  started_by?: string;
  /** Set when the run was launched via "Investigate this finding". */
  finding_id?: number;
  /** Federation tag — present iff the run lives on a federated child CP.
   *  Caller must pass `?cp=<instance_id>` on detail/cancel/log requests
   *  for these rows because run ids are scoped per-CP. */
  cp_source?: CPSourceRef;
}

export interface DBStatsResponse {
  stats: {
    path: string;
    size_bytes: number;
    event_count: number;
    oldest_event_ts?: number;
    finding_count: number;
    run_count: number;
    session_count: number;
    delivery_count: number;
    generated_at: string;
  };
  config: {
    event_ttl_days: number;
  };
}

export interface RunDetail extends RunListItem {
  lines: string[];
  error?: string;
}

export function subscribeRunLog(
  id: string,
  onLine: (line: string) => void,
  onDone: (status: string) => void,
): () => void {
  const es = new EventSource(`/api/runs/${id}/log`);
  es.onmessage = (msg) => onLine(msg.data);
  es.addEventListener('done', (msg) => {
    onDone((msg as MessageEvent).data);
    es.close();
  });
  return () => es.close();
}

export interface NodeItem {
  id: number;
  name: string;
  hostname: string;
  /** Hostname captured from inside the guest at deploy time. Daemon
   *  events carry this value in their `host` field (it usually differs
   *  from `hostname`, which is the SSH target). Empty until the first
   *  successful deploy after this column was added. */
  daemon_hostname?: string;
  ssh_user: string;
  ssh_port: number;
  status: 'pending' | 'deploying' | 'ready' | 'failed';
  status_message?: string;
  last_status_at?: string;
  last_deployed_at?: string;
  agents_installed: string[];
  notes?: string;
  created_at: string;

  // Phase 7a — refreshable telemetry. Populated by the "Refresh
  // metadata" probe over the tunnel.
  kernel_release?: string;
  os_release?: string;
  arch?: string;
  cpu_count?: number;
  /** When true, daimons running on this node will NOT hot-reload new
   *  definitions. Operators set this for compliance windows or before
   *  manual rollouts. */
  auto_update_paused?: boolean;
  /** Phase 4 — pull-mode jobs runtime liveness. Set every time the
   *  daemon polls /api/v1/agents/jobs. The Runtimes panel reads this
   *  to show "polled Ns ago" or "not installed". */
  jobs_runtime_seen_at?: string;
  /** True iff the host's jobs runtime currently has a managed
   *  okesu node child running (i.e. tunnel is up via a start_tunnel
   *  job). For static reverse-tunnels this stays false; the UI
   *  cross-references the live tunnel registry separately. */
  tunnel_running?: boolean;
  /** "" (auto) | "tunnel" | "jobs". Operator-set per-node hint that
   *  layers under spec defaults when an orchestration step uses
   *  dispatch=auto. */
  preferred_dispatch?: '' | 'tunnel' | 'jobs';
  memory_mb?: number;
  disk_free_mb?: number;
  okesu_version?: string;
  metadata_at?: string;
  /** Phase 9.6: federation source. */
  cp_source?: CPSourceRef;
}

export interface NodeCreateReq {
  name: string;
  hostname: string;
  ssh_user?: string;
  ssh_port?: number;
  notes?: string;
  /** Phase 9.7: when set, the parent CP forwards this request to the
   *  named child CP instead of creating the node locally. The child's
   *  response is streamed back unchanged so the dialog still gets a
   *  NodeItem on success. */
  target_cp_instance_id?: string;
}

export interface NodeDeployReq {
  agents: string[];
  private_key: string;
  passphrase?: string;
  /** Forwarded to `sudo -S` on the target. Empty/omitted ⇒ try sudo
   *  without password (NOPASSWD or root login) and fall through to
   *  plain bash. Mac developer machines and other non-root SSH targets
   *  need this. Never persisted server-side. */
  sudo_password?: string;
  anthropic_api_key?: string;
  openai_api_key?: string;
  include_webhook?: boolean;
  include_mgmt_cert?: boolean;
  /** Phase 9.7: when set, the parent forwards the deploy call to the
   *  named child CP. Set this from the target node's
   *  `cp_source.instance_id` when deploying to a federated node. */
  target_cp_instance_id?: string;
}

// ── Runs-tab filter shape (Phase 12.1) ──────────────────────────────────────

export interface OrchestrationRunsFilter {
  /** OR'd status pills — server: any-of. */
  status?: OrchestrationRunStatus[];
  trigger_kind?: ('manual' | 'finding' | 'cron')[];
  orchestration_id?: number[];
  /** Relative alias ('30m'|'1h'|'24h'|'7d') or raw unix-ms. */
  since?: string;
  /** Free-text against run id, current step id, host / finding_id in payload. */
  q?: string;
  limit?: number;
  offset?: number;
}

export interface OrchestrationRunsListResponse {
  rows: OrchestrationRunView[];
  counts_by_status: Partial<Record<OrchestrationRunStatus, number>>;
}

export interface BulkRunOpResult {
  /** Run ids that successfully transitioned (cancel) or that the new
   *  spawned run ids (retry). UI surfaces the total count. */
  affected: number[];
  /** id → reason for runs the server skipped (already terminal,
   *  orchestration disabled, deleted). */
  skipped?: Record<number, string>;
}

// ── Finding history + run linkage (Phase 11.4) ──────────────────────────────

export interface FindingEditEntry {
  id: number;
  finding_id: number;
  /** status | severity_override | tag_add | tag_remove | linked_run */
  field: string;
  old_value?: string;
  new_value?: string;
  reason?: string;
  edited_by_user_id?: number;
  edited_by_email?: string;
  orchestration_run_id?: number;
  orchestration_step_id?: string;
  edited_at: string;
}

export interface FindingRunLinkEntry {
  finding_id: number;
  orchestration_run_id: number;
  step_id?: string;
  linked_at: string;
}

// ── S3 transport / enrollment packages ──────────────────────────────────────

export interface TransportConfigSummary {
  id: number;
  name: string;
  kind: string;            // 's3'
  bucket: string;
  /** Public/external endpoint nodes embed in their bootstrap.json. */
  endpoint: string;
  /** Optional private endpoint the CP scanner dials; empty falls back to `endpoint`. */
  endpoint_internal?: string;
  region?: string;
  use_ssl: boolean;
  access_key?: string;
  has_secret_key: boolean;
  has_fleet_pubkey: boolean;
  has_fleet_privkey: boolean;
  scanner_interval_ms: number;
  cp_id?: string;
  created_at?: string;
  updated_at?: string;
}

export interface TransportConfigCreateReq {
  name: string;
  kind: string;            // 's3'
  bucket: string;
  endpoint: string;
  endpoint_internal?: string;
  region?: string;
  use_ssl: boolean;
  access_key?: string;
  secret_key?: string;
  generate_fleet_keys?: boolean;
  scanner_interval_ms?: number;
  cp_id?: string;
}

// Bucket provisioning (Settings → Add Bucket wizard, post-CRUD-gap fill).
export interface BucketCloudProvider {
  id: number;
  cloud: string;          // 'aws' | 'oci' | 'minio'
  display_name: string;
  region?: string;
}

export interface BucketInfo {
  name: string;
  region: string;
  endpoint: string;
}

export interface BucketProvisionReq {
  cloud_credential_id: number;
  region: string;
  bucket_name: string;
  mode: 'discover' | 'create';
  display_name: string;
  generate_fleet_keys: boolean;
  scanner_interval_ms: number;
  /** OCI only: overrides the credential's default compartment for
   *  ListBuckets / EnsureBucket. Omit (or pass empty string) to fall
   *  back to the credential's stored compartment, then the tenancy root.
   *  Ignored by AWS and MinIO provisioners. */
  compartment_id?: string;
}

export interface TransportConfigPatch {
  name?: string;
  scanner_interval_ms?: number;
}

// NamedRef mirrors db.NamedRef (json tags lowercase).
export interface NamedRef {
  id: number;
  name: string;
}

// TransportConfigReferences mirrors db.TransportConfigReferences which
// has no json tags — fields marshal as PascalCase.
export interface TransportConfigReferences {
  Nodes: NamedRef[];
  EnrollmentPackages: NamedRef[];
  FederationPeers: NamedRef[];
  CPProvisions: NamedRef[];
}

// TransportConfigDeleteConflict is the body returned by DELETE on 409.
export interface TransportConfigDeleteConflict {
  message: string;
  referenced_by: TransportConfigReferences;
}

export interface EnrollmentPackageSummary {
  id: number;
  display_name: string;
  transport_config_id: number;
  cp_id: string;
  created_at?: string;
  revoked_at?: string;
}

export interface PackageDefaults {
  agents?: string[];
  poll_interval_ms?: number;
  heartbeat_interval_ms?: number;
  cp_id?: string;
}

export interface EnrollmentPackageCreateReq {
  display_name: string;
  transport_config_id: number;
  defaults: PackageDefaults;
}

/** Time-range shared across the two dashboard timeline charts. */
export type TimeRange = '30m' | '1h' | '24h' | '7d' | '30d';

export interface DashboardResponse {
  daimons: { total: number; healthy: number; unhealthy: number; suspended: number };
  nodes:   { total: number; heartbeating: number };
  tunnels: { live: number };
  findings: { open: number; critical: number; high: number };
  drift: {
    total: number;
    items: Array<{
      name: string;
      host: string;
      current_hash?: string;
      canonical_hash?: string;
      definition_version?: string;
      binary_version?: string;
    }>;
  };
  top_hosts: Array<{ host: string; open: number; critical: number }>;
  os_distribution: Array<{ os: string; count: number }>;
  fleet_status: {
    healthy: number;
    needs_patching: number;
    offline: number;
    frozen: number;
    total: number;
  };
  // Phase 9.5 — Always present, but children=0 when this CP federates
  // from no one. When children > 0, the headline daimons/nodes/findings
  // numbers above ALREADY include the federated rollup; local_only
  // exposes the pre-rollup figures so the UI can show a "Local only"
  // toggle.
  federation: {
    children: number;
    healthy_children: number;
    local_only: {
      daimons_total: number;
      nodes_total: number;
      open_findings: number;
    };
  };

  // Phase 22.7 — investigations rollup. Active count, last-24h
  // throughput, autolink activity, and a top-5 most-recently-
  // updated active list for the "Recent investigations" card.
  // Local-only in v1 (federated rollup is a future follow-up).
  investigations: {
    active: number;
    closed_24h: number;
    autolinked_findings_24h: number;
    recent_active: Array<{
      id: number;
      title: string;
      finding_count: number;
      updated_at: string;
    }>;
  } | null;
}

export interface InsightsFindingsResponse {
  bucket_ms: number;
  group_by: 'severity' | 'agent' | 'host';
  series: string[];
  buckets: Array<{
    ts: number;
    by: Record<string, number>;
  }>;
}

export interface InsightsEventsResponse {
  bucket_ms: number;
  buckets: Array<{ ts: number; count: number }>;
}

export interface InsightsTriageOutcomesResponse {
  bucket_ms: number;
  buckets: Array<{
    ts: number;
    incoming: number;
    t0_superseded: number;
    t1_resolved: number;
    t1_tagged: number;
  }>;
  totals: {
    incoming: number;
    auto_handled: number;
    triage_rate: number; // 0..1
  };
}

export interface InsightsOrchestrationsTopResponse {
  since: string;
  rows: Array<{
    orchestration_id: number;
    name: string;
    total: number;
    completed: number;
    failed: number;
    running: number;
    cancelled: number;
    pending: number;
    approval_required: number;
    avg_duration_ms?: number;
  }>;
}

export interface AboutInfo {
  /** okesu-cp binary version. */
  version: string;
  /** okesu daemon binary version the CP would push on a deploy or update.
   *  When this differs from a node's reported binary version, the UI
   *  surfaces an "update available" cue. */
  daemon_version?: string;
  go_version: string;
  os: string;
  arch: string;
  features: {
    oidc: boolean;
    mgmt_plane: boolean;
    tunnel: boolean;
    deploy: boolean;
    webhook_ingest: boolean;
  };
}

export interface UserItem {
  id: number;
  email: string;
  role: 'admin' | 'operator' | 'viewer';
  has_password: boolean;
  created_at: string;
}

export interface SessionInfo {
  id: string;
  is_current: boolean;
  created_at: string;
  expires_at: string;
}

export interface AuditEntry {
  id: number;
  ts: string;
  actor_id?: number;
  actor_email?: string;
  actor_role?: string;
  actor_ip?: string;
  action: string;
  target?: string;
  result: 'ok' | 'denied' | 'error';
  metadata?: Record<string, unknown>;
}

export interface AuditQuery {
  actor?: string;
  action?: string;
  target?: string;
  result?: string;
  limit?: number;
  offset?: number;
}

export interface BinaryItem {
  name: string;
  os: string;
  arch: string;
  path: string;
  sha256: string;
  size_bytes: number;
  uploaded_at: string;
  uploaded_by_email?: string;
}

/** Status of the CP-wide stored SSH key used as a fallback when a deploy
 *  request omits a per-deploy private_key. The key bytes themselves
 *  never leave the server — only fingerprint + metadata. */
export interface DeployKeyStatus {
  configured: boolean;
  fingerprint?: string; // SHA256:<base64> (OpenSSH style)
  key_type?: string;    // ssh-ed25519, ssh-rsa, …
  comment?: string;
  /** When false, the configured Secrets adapter is read-only and the
   *  Save button should be disabled. */
  adapter_writable: boolean;
}

// ReachableNode — server-decided dispatchable node (tunnel or pull).
// `method` is what dispatch would use *right now*; `transport` is the
// node's persistent transport configuration.
export interface ReachableNode {
  name: string;
  transport: string;            // 'https' | 's3' | '' for legacy
  method: 'tunnel' | 'pull';
  last_seen?: string;           // ISO-8601
}

export interface KnownHostItem {
  node_id: number;
  node_name?: string;
  hostname?: string;
  ssh_port?: number;
  key_type: string;
  fingerprint: string;
  accepted_at: string;
  accepted_by_email?: string;
}

export interface JobSnapshot {
  id: string;
  node_id: number;
  type: string;
  status: 'running' | 'succeeded' | 'failed';
  error?: string;
  started_at: string;
  finished_at?: string;
  lines: string[];
}

export function subscribeJobLog(
  id: string,
  onLine: (line: string) => void,
  onDone: (status: string) => void,
): () => void {
  const es = new EventSource(`/api/jobs/${id}/log`);
  es.onmessage = (msg) => onLine(msg.data);
  es.addEventListener('done', (msg) => {
    onDone((msg as MessageEvent).data);
    es.close();
  });
  return () => es.close();
}

// Subscribe to live events via SSE.
// Returns an unsubscribe function.
export function subscribeEvents(onEvent: (e: EventItem) => void): () => void {
  const es = new EventSource('/api/events/stream');
  es.onmessage = (msg) => {
    try {
      const raw = JSON.parse(msg.data);
      const item: EventItem = {
        id: 0,
        ts: raw.ts ?? Date.now(),
        type: raw.type ?? 'unknown',
        agent: raw.agent,
        host: raw.host,
        severity: raw.severity,
        title: raw.title,
        raw,
        // Federation source — injected by the parent CP's SSE
        // multiplexer (controlplane/api/federation_reads.go's
        // injectCPSource); local events leave this undefined.
        cp_source: raw.cp_source as CPSourceRef | undefined,
      };
      onEvent(item);
    } catch {
      // ignore parse errors
    }
  };
  return () => es.close();
}

// ── Phase 9.5 — federation peers (parent-side) ─────────────────────────────

// FederationPeer is one child CP this CP aggregates from. The
// `introspect` blob mirrors the IntrospectResponse shape from the
// child's /api/v1/cp/introspect; we keep it loose-typed here so adding
// a new field on the wire doesn't require a UI rebuild.
export interface FederationPeer {
  id: number;
  url: string;
  display_name: string;
  added_at: string;
  last_polled_at?: string;
  last_seen_at?: string;
  last_error?: string;
  heartbeat_age_sec?: number;
  healthy: boolean;
  /** 'https_pull' (parent polls /api/v1/cp/introspect) or 's3_dead_drop'
   *  (parent reads {bucket_prefix}/introspect.json). Used to chip the
   *  transport per row + drive transport-specific drawer fields. */
  transport: string;
  /** S3-only: bucket-relative prefix where the child publishes its
   *  outbound objects ('cp/<child-id>/outbound/<this-cp-id>/'). */
  bucket_prefix?: string;
  /** S3-only: which transport_config row supplies the bucket creds. */
  transport_config_id?: number;
  introspect?: {
    instance_id?: string;
    region?: string;
    display_name?: string;
    role?: string;
    version?: string;
    daemon_version?: string;
    counts?: {
      daimons?: number;
      daimons_healthy?: number;
      nodes?: number;
      open_findings?: number;
    };
    features?: Record<string, boolean>;
    webhook_public_url?: string;
    mgmt_public_url?: string;
  };
}

export interface FederationPeerAddReq {
  url: string;
  token: string;
  display_name?: string;
}

// Phase 21.2 — cloud credentials.
export type CloudKind = 'oci' | 'aws' | 'gcp' | 'azure' | 'digitalocean' | 'minio';

export interface CloudCredential {
  id: number;
  cloud: CloudKind;
  name: string;
  region?: string;
  monthly_budget_usd?: number;
  created_at: string;
  created_by_email?: string;
  last_used_at?: string;
  last_test_at?: string;
  last_test_ok?: boolean;
  last_test_error?: string;
}

export interface CloudCredentialCreateRequest {
  cloud: CloudKind;
  name: string;
  region?: string;
  payload: Record<string, string>;
}

/** Partial update: only fields the operator actually changed. Payload
 *  keys with empty/undefined values are dropped server-side, so secret
 *  fields stay intact unless re-typed. */
export interface CloudCredentialUpdateRequest {
  name?: string;
  region?: string;
  payload?: Record<string, string>;
}

// Fleet-env (Settings → LLM Keys). Holds Anthropic + OpenAI keys
// distributed to nodes (mTLS) and federated child CPs (federation
// token). The summary never carries plaintext — only `*_set` and
// `*_last4`. PUT semantics mirror cloud credentials: omitted = leave
// alone, "" = delete, non-empty = overwrite.
export interface FleetEnvSummary {
  anthropic_set: boolean;
  anthropic_last4: string;
  openai_set: boolean;
  openai_last4: string;
  version: number;
  source: 'local' | 'federated_from_parent';
  parent_cp_id: string | null;
  updated_at: string;
  updated_by_user_email: string | null;
}

export interface FleetEnvPatch {
  anthropic_api_key?: string;
  openai_api_key?: string;
}

// Phase 21.3 — managed CP provisioning.
export type CPProvisionStatus =
  | 'queued'
  | 'starting'
  | 'cloud_init_running'
  | 'bootstrap_pending'
  | 'ready'
  | 'failed'
  | 'cancelled';

export interface CPProvision {
  id: number;
  display_name: string;
  region: string;
  cloud: CloudKind;
  credential_name?: string;
  cloud_params?: Record<string, unknown>;
  status: CPProvisionStatus;
  cloud_resource_id?: string;
  cloud_resource_url?: string;
  peer_id?: number;
  log?: string;
  error?: string;
  est_cost_per_hour_usd?: number;
  instance_shape?: string;
  created_at: string;
  started_at?: string;
  ended_at?: string;
  created_by_email?: string;
}

export interface CPProvisionRequest {
  display_name: string;
  region: string;
  cloud: CloudKind;
  credential_id: number;
  cloud_params?: Record<string, unknown>;
  parent_url?: string;
  /** Phase 21.6 — federation transport. Empty / 'https' uses the
   *  existing mTLS bootstrap callback. 's3_dead_drop' uses the bucket
   *  pipe; transport_config_id must also be set. */
  transport?: 'https' | 's3_dead_drop';
  /** Required when transport === 's3_dead_drop'. */
  transport_config_id?: number;
  /** Phase 21.5 — bypasses the budget check; submit returns 409 otherwise. */
  force_over_budget?: boolean;
}

// Phase 21.5 — cost estimate preview body + response.
export interface CPProvisionEstimateRequest {
  cloud: CloudKind;
  credential_id?: number;
  cloud_params?: Record<string, unknown>;
}

export interface CPProvisionEstimate {
  hourly_usd?: number;
  monthly_usd?: number;
  instance_shape?: string;
  catalog_version: string;
  note?: string;
  monthly_budget_usd?: number;
  current_monthly_usd?: number;
  projected_monthly_usd?: number;
  unknown_active_count?: number;
  would_exceed_budget?: boolean;
}

// Cloud-discovery list item — every /oci/{resource} handler returns this.
export interface DiscoveryItem {
  id: string;
  name: string;
  attrs?: Record<string, unknown>;
}

// Phase 21.5 — structured 409 from cp-provision when over budget.
export interface CPProvisionBudgetExceeded {
  error: string;
  reason: 'budget_exceeded';
  credential_id: number;
  monthly_budget_usd: number;
  current_monthly_usd: number;
  projected_monthly_usd: number;
  new_monthly_usd: number;
  unknown_active_count: number;
}

// Phase 23 — IOC Feeds (Settings → Feeds + Catalog → Feeds tab).

// FeedConfig is the read shape returned by GET /api/feeds.
// Wire shape uses Go's default-casing (CamelCase field names) since
// db.FeedConfig has no JSON struct tags.
export interface FeedConfig {
  ID: number;
  Slug: string;
  Name: string;
  Kind: 'single_file' | 'git';
  URL: string;
  Subpath: string;
  Parser: 'yara' | 'sigma' | 'urlhaus_csv' | 'threatfox_csv' | 'cisa_kev_json';
  AuthCredentialID: { Int64: number; Valid: boolean };
  RefreshIntervalSeconds: number;
  Enabled: boolean;
  InstalledFromRegistry: boolean;
  LastRefreshAt: { Time: string; Valid: boolean };
  LastRefreshStatus: string; // "" | "ok" | "error"
  LastRefreshError: string;
  LastRefreshEntryCount: number;
  CreatedAt: string;
  UpdatedAt: string;
  Source: 'local' | 'federated_from_parent';
  ParentCPID: { String: string; Valid: boolean };
}

// FeedRegistryDef is the read shape returned by GET /api/feeds/registry.
// Mirrors feeds.FeedDef in Go (no JSON tags → CamelCase).
export interface FeedRegistryDef {
  Slug: string;
  Name: string;
  Kind: 'single_file' | 'git';
  URL: string;
  Subpath: string;
  Parser: string;
  License: string;
  Description: string;
  DefaultIntervalSeconds: number;
  DefaultInstalled: boolean;
}

// FeedInstallRequest is the POST /api/feeds body. Exactly one of
// from_registry_slug or custom must be set.
export interface FeedInstallRequest {
  from_registry_slug?: string;
  custom?: FeedConfigInsert;
}

// FeedConfigInsert mirrors db.FeedConfigInsert. The Go struct uses
// CamelCase field names (no JSON tags). Source defaults to "local"
// when omitted.
export interface FeedConfigInsert {
  Slug: string;
  Name: string;
  Kind: 'single_file' | 'git';
  URL: string;
  Subpath?: string;
  Parser: 'yara' | 'sigma' | 'urlhaus_csv' | 'threatfox_csv' | 'cisa_kev_json';
  AuthCredentialID?: number;
  RefreshIntervalSeconds: number;
  Enabled: boolean;
  InstalledFromRegistry?: boolean;
  Source?: 'local' | 'federated_from_parent';
  ParentCPID?: string;
}

// FeedConfigPatch mirrors db.FeedConfigPatch. All fields optional.
// AuthCredentialID is wire-shaped as a Go sql.NullInt64 to support
// "set", "clear", and "leave alone" — pass `{Int64: id, Valid: true}`
// to set, `{Int64: 0, Valid: false}` to clear, or omit the key to
// leave alone.
export interface FeedConfigPatch {
  Name?: string;
  URL?: string;
  Subpath?: string;
  Parser?: string;
  AuthCredentialID?: { Int64: number; Valid: boolean };
  RefreshIntervalSeconds?: number;
  Enabled?: boolean;
}

export interface FeedInstallResponse {
  id: number;
  slug: string;
}

export interface FeedValidateResponse {
  entry_count: number;
}

export interface FeedsConsentResponse {
  granted_at: string;
}
