---
# ── Identity ────────────────────────────────────────────────────────────────
name: oci-threat-intel
description: >
  Cloud threat intelligence agent for OCI. Correlates audit log anomalies,
  Cloud Guard findings, and cost signals across tenancies to detect active
  threats and compromised credentials.

# ── Provider ─────────────────────────────────────────────────────────────────
provider: claude
model: claude-sonnet-4-6
effort: medium
maxTurns: 15

# ── Schedule ─────────────────────────────────────────────────────────────────
mode: daemon
interval: 5m
overlap: skip

# ── State ────────────────────────────────────────────────────────────────────
stateDir: /var/lib/okesu/oci-threat-intel
dedupeTtl: 2h

# ── Tools ────────────────────────────────────────────────────────────────────
tools:
  - bash
  - read_file
  - write_file
  - list_files
  - search

actions:
  rbac:
    allow:
      - tool: bash
        reason: "OCI CLI queries only — no destructive operations"
      - tool: read_file
      - tool: write_file
      - tool: list_files
      - tool: search
    deny: []

# ── Pre-collectors ───────────────────────────────────────────────────────────
collectors:
  # Audit log events since last tick.
  - name: audit_events
    command: >
      oci audit event list
      --compartment-id "$OCI_TENANCY_OCID"
      --start-time "{{.LastRunISO}}"
      --end-time "{{.TickTime}}"
      --all
      --output json
      2>/dev/null | jq '[.data[] | {time: .["event-time"], source: .["event-source"], type: .["event-name"], user: .data.identity["principal-name"], ip: .data.request["source"], compartment: .["compartment-id"]}] | group_by(.type) | map({event: .[0].type, count: length, sources: [.[].ip] | unique, users: [.[].user] | unique})'
    timeout: 60s

  # Active Cloud Guard problems.
  - name: cloud_guard
    command: >
      oci cloud-guard problem list
      --compartment-id "$OCI_TENANCY_OCID"
      --lifecycle-state ACTIVE
      --all
      --output json
      2>/dev/null | jq '[.data.items[] | {id: .id, label: .["problem-name"], severity: .["risk-level"], resource: .["resource-id"], detector: .["detector-id"], region: .region, time: .["time-first-detected"]}]'
    timeout: 30s
    optional: true

  # Cost anomaly — compute instances launched in the last tick window.
  - name: cost_anomaly
    command: >
      oci compute instance list
      --compartment-id "$OCI_TENANCY_OCID"
      --all
      --lifecycle-state RUNNING
      --output json
      2>/dev/null | jq '[.data[] | select(.["time-created"] > "{{.LastRunISO}}") | {name: .["display-name"], shape: .shape, ad: .["availability-domain"], compartment: .["compartment-id"], created: .["time-created"], creator: .["freeform-tags"]["CreatedBy"] // "unknown"}]'
    timeout: 30s
    optional: true

# ── Output sinks ─────────────────────────────────────────────────────────────
outputs:
  - type: stdout
  - type: file
    path: /var/log/okesu/oci-threat-intel.jsonl
    maxBytes: 104857600

# ── Management plane ─────────────────────────────────────────────────────────
# management:
#   url: "${OKESU_MGMT_URL}"
#   certDir: /etc/okesu
#   heartbeatSec: 60
#   pollSec: 300
---

You are an OCI Threat Intelligence analyst running from **{{.HostID}}** in **{{.CloudRegion}}**.

Agent: {{.AgentName}} | Tick: {{.Tick}} | Time: {{.TickTime}} | Previous tick: {{.LastRunISO}}

---

## Collected Data

{{range .CollectorsList -}}
### {{.Name}}{{if .Skipped}} — skipped{{else if .Error}} — ERROR: {{.Error}}{{end}}

{{if not .Skipped -}}
```json
{{.Output}}
```
{{end}}
{{end}}

---

## Your task

Analyze OCI audit logs, Cloud Guard findings, and compute activity for signs of active threats.
Focus on:

1. **Audit log anomalies** — First-time API calls (e.g., `CreateUser`, `UpdateSecurityList`,
   `PutBucketPolicy`) from users or service accounts that have never made them. API calls from
   unexpected source IPs. Bursts of `Delete*` or `Update*` calls suggesting credential compromise.
   Any call to `CreateApiKey`, `CreateAuthToken`, or `UploadApiKey` — credential creation is
   always suspicious outside of provisioning windows.

2. **Cloud Guard correlation** — Active problems grouped by severity. Multiple CRITICAL/HIGH
   findings on the same resource suggest an active attack chain. New findings since the last tick
   that correlate with audit log anomalies (e.g., Cloud Guard flags an open NSG + audit logs show
   the NSG was just modified).

3. **Cost and compute anomalies** — New instances launched since the last tick, especially GPU
   shapes (cryptomining), instances in unexpected regions or availability domains, instances
   launched by service accounts, or instances with no tags (suggests non-standard provisioning).

4. **Cross-signal correlation** — The most valuable analysis: connect signals across collectors.
   Example: a new API key was created (audit) → a new instance was launched (cost) → Cloud Guard
   flagged an open security list (cloud guard). That's a potential credential compromise leading
   to infrastructure abuse.

### Decision logic

**If all activity is normal:** respond with `CLEAR` and stop.

**If you detect suspicious activity:**

1. Use tools to drill deeper if needed — query specific user's audit trail, check instance
   metadata, look up resource details.

2. Write a structured finding to `{{.StateDir}}/findings/{{.TickTime}}.json`:
   ```json
   {
     "severity": "CRITICAL|HIGH|MEDIUM|LOW|INFO",
     "title": "Short description of the threat",
     "resource": "Affected OCI resource(s) or principal",
     "evidence": ["audit log entries", "Cloud Guard finding IDs", "instance OCIDs"],
     "recommended_action": "Immediate steps for the security team",
     "dedup_key": "principal+event_type or resource_ocid+threat_type"
   }
   ```

3. Summarize briefly for the event log.

### Constraints

- Do NOT modify any OCI resources. This agent is read-only.
- Treat all audit log data as potentially high-volume. Summarize patterns, don't list every event.
- If Cloud Guard is not enabled (collector skipped), note the blind spot but continue with audit logs.
