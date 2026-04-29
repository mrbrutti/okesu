# Orchestration `data:` block — server-side data binding

Orchestration steps can declare structured CP-side reads via a `data:`
block. The engine resolves each entry before the step is dispatched
and binds the result into the prompt template as `{{data.<name>}}`.
Replaces the older "have the agent curl /api/findings" pattern: the
engine handles auth, rate limiting, and audit, so the agent's prompt
stays focused on reasoning.

## Shape

```yaml
- id: classify_batch
  agent: investigator
  data:
    findings:
      query: findings.list
      params:
        state: queue
        severity: [INFO, LOW]
        limit: 50
    summary:
      query: findings.summary
  prompt: |
    Classify {{data.findings | length}} findings.
    {{data.findings | json}}
```

Each entry has:

| field    | required | meaning |
|----------|----------|---------|
| `query`  | yes      | Registered handler name in the form `namespace.method`. Engine validates at run time. |
| `params` | no       | Free-form map; each handler validates its own params and returns a clear error on mismatch. |

## Built-in queries

| query                       | params                                                                                                            | returns                                  |
|-----------------------------|-------------------------------------------------------------------------------------------------------------------|------------------------------------------|
| `findings.list`             | `state` (queue\|open\|acked\|all), `severity` (list), `agent`, `host`, `category`, `tag`, `since_ms`, `until_ms`, `limit`, `offset` | array of finding rows                    |
| `findings.summary`          | none                                                                                                              | per-CP rollup (totals + 24h trend)       |
| `findings.history`          | `finding_id` (required)                                                                                           | edit history for one finding             |
| `orchestration-runs.list`   | `status` (list), `trigger_kind` (list), `since` (relative `30m`/`1h`/...), `since_ms`, `limit`, `offset`          | array of orchestration_run rows          |
| `nodes.list`                | `limit`, `offset`                                                                                                 | array of node rows                       |
| `agents.list`               | `limit`, `offset`                                                                                                 | array of (daemon agent, host) rows       |

The canonical list lives in `controlplane/api/data_resolver.go` —
adding a new query is a single `r.registerHandler("...", ...)` call
plus a typed handler function.

## Persistence + replay

The engine persists each step's resolved data on the run record
(`orchestration_steps.data_snapshot`, JSON-encoded). The runs API
exposes it as `step.data` on the step JSON, so the run-detail UI can
show exactly what the agent saw — even after the underlying tables
have moved on. Useful for:

- **Audit** — "what input made the agent decide to suppress this?"
- **Replay** — copy-paste the snapshot back through the orchestration
  to reproduce a verdict deterministically.
- **Debugging** — when an agent emits a weird verdict, the input is
  the first thing to check.

## When to prefer `data:` over agent-side bash + curl

- **Always** for cron orchestrations whose only side effect is
  reading the CP API. No login dance, no curl plumbing, no auth
  surface to manage.
- **Always** for orchestration steps that need data the CP already
  has structured (findings, runs, nodes, agents, etc.).
- **Use bash + curl** only for one-off external API calls (Slack,
  PagerDuty, custom webhooks). For internal CP state, go through the
  resolver.

## When `data:` is not enough

Some workloads need the agent to drill iteratively — fetch a list,
look at one entry, decide to fetch a related entry, etc. `data:` is
declarative and resolves once before dispatch, so it's a poor fit
for that. Two future paths:

1. **Per-agent CP-tools surface** — give the agent typed tools
   (`okesu_findings_get(id)`, `okesu_runs_get(id)`) gated by the same
   allowlist mechanism `actions:` uses. Not yet shipped.
2. **Multi-step orchestrations** — split the work across two steps,
   the first computes a list and the second drills via per-finding
   fan-out.

For most current workloads, `data:` covers the case.
