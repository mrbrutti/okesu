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
  constructor(public status: number, message: string) {
    super(message);
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    throw new ApiError(res.status, text || res.statusText);
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
  | 'wontfix';

export const ALL_FINDING_STATUSES: FindingStatus[] = [
  'open', 'acknowledged', 'investigating', 'resolved', 'false_positive', 'wontfix',
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
  state?: 'open' | 'acked' | 'all';
  since?: number;        // unix ms
  until?: number;        // unix ms
  limit?: number;
  offset?: number;
}

export interface AuthConfig {
  oidc_enabled: boolean;
  oidc_label?: string;
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

  events: (limit = 100, beforeTs?: number) => {
    const p = new URLSearchParams({ limit: String(limit) });
    if (beforeTs && beforeTs > 0) p.set('before_ts', String(beforeTs));
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

  daimon: (name: string, cp?: string) =>
    request<DaimonItem>(`/api/agents/${encodeURIComponent(name)}${cp ? `?cp=${cp}` : ''}`),

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

  // Investigations attached to a finding — runs whose finding_id == id.
  runsForFinding: (id: number) =>
    request<RunListItem[]>(`/api/findings/${id}/runs`),

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
    const qs = p.toString();
    return request<Finding[]>(`/api/findings${qs ? '?' + qs : ''}`);
  },

  finding: (id: number) =>
    request<Finding>(`/api/findings/${id}`),

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

  setFindingStatus: (id: number, status: FindingStatus, note?: string) =>
    request<Finding>(`/api/findings/${id}/status`, {
      method: 'POST',
      body: JSON.stringify({ status, note: note ?? '' }),
    }),

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
  nodeLibrary: () =>
    request<{ agents: string[]; daemon_binary_path: string; agent_files_dir: string }>(
      '/api/nodes/library',
    ),
  job: (id: string) => request<JobSnapshot>(`/api/jobs/${id}`),

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
  cancelRun: (id: string) =>
    request<void>(`/api/runs/${id}/cancel`, { method: 'POST' }),

  // Phase 10 — system / database.
  dbStats: () => request<DBStatsResponse>('/api/system/db/stats'),
  dbVacuum: () =>
    request<{ ok: boolean; duration_ms: number }>('/api/system/db/vacuum', { method: 'POST' }),
  dbPruneEvents: (olderThanDays: number) =>
    request<{ deleted: number; older_than_days: number }>(
      `/api/system/db/prune-events?older_than_days=${olderThanDays}`,
      { method: 'POST' },
    ),

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
};

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
  enabled: boolean;
  created_at: string;
}

export interface RuleCreateReq {
  name: string;
  channel_id: number;
  min_severity: string;
  agent_substring?: string;
  host_substring?: string;
  enabled?: boolean;
}

export interface RulePatchReq {
  name?: string;
  channel_id?: number;
  min_severity?: string;
  agent_substring?: string;
  host_substring?: string;
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
