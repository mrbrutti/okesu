---
# ── Identity ────────────────────────────────────────────────────────────────
name: oci-posture
description: >
  Cloud posture agent for OCI tenancies. Audits IAM policies, compartment
  boundaries, network security groups, and object storage visibility across
  one or more tenancies via the OCI CLI.

# ── Provider ─────────────────────────────────────────────────────────────────
provider: claude
model: claude-sonnet-4-6
effort: medium
maxTurns: 15

# ── Schedule ─────────────────────────────────────────────────────────────────
mode: daemon
interval: 10m
overlap: skip

# ── State ────────────────────────────────────────────────────────────────────
stateDir: /var/lib/okesu/oci-posture
dedupeTtl: 6h

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
        reason: "needed for OCI CLI queries — no destructive operations"
      - tool: read_file
      - tool: write_file
      - tool: list_files
      - tool: search
    deny: []

# ── Pre-collectors ───────────────────────────────────────────────────────────
# Each collector queries OCI APIs via the CLI. The OCI config profile determines
# which tenancy is queried. For multi-tenancy, run separate instances with
# different OCI_CLI_PROFILE env vars, or use a wrapper script that iterates.
collectors:
  # IAM policies across all compartments.
  - name: iam_policies
    command: >
      oci iam policy list
      --compartment-id "$OCI_TENANCY_OCID"
      --all
      --output json
      2>/dev/null | jq '[.data[] | {name: .name, statements: .statements, compartment: .["compartment-id"], lifecycle: .["lifecycle-state"]}]'
    timeout: 30s

  # Compartment tree with cross-compartment policy grants.
  - name: compartments
    command: >
      oci iam compartment list
      --compartment-id "$OCI_TENANCY_OCID"
      --compartment-id-in-subtree true
      --all
      --output json
      2>/dev/null | jq '[.data[] | {name: .name, id: .id, parent: .["compartment-id"], lifecycle: .["lifecycle-state"]}]'
    timeout: 30s

  # Network security groups and security lists across all VCNs.
  - name: nsgs
    command: >
      for vcn in $(oci network vcn list --compartment-id "$OCI_TENANCY_OCID" --all --output json 2>/dev/null | jq -r '.data[].id'); do
        echo "--- VCN: $vcn ---"
        oci network nsg list --compartment-id "$OCI_TENANCY_OCID" --vcn-id "$vcn" --all --output json 2>/dev/null | jq '.data[] | {name: .["display-name"], rules: .id}'
        oci network security-list list --compartment-id "$OCI_TENANCY_OCID" --vcn-id "$vcn" --all --output json 2>/dev/null | jq '.data[] | {name: .["display-name"], ingress: .["ingress-security-rules"], egress: .["egress-security-rules"]}'
      done
    timeout: 60s
    optional: true

  # Object Storage buckets — check visibility and pre-authenticated requests.
  - name: public_buckets
    command: >
      oci os bucket list
      --compartment-id "$OCI_TENANCY_OCID"
      --all
      --output json
      2>/dev/null | jq '[.data[] | {name: .name, namespace: .namespace, public_access: .["public-access-type"], compartment: .["compartment-id"]}] | map(select(.public_access != "NoPublicAccess"))'
    timeout: 30s
    optional: true

# ── Output sinks ─────────────────────────────────────────────────────────────
outputs:
  - type: stdout
  - type: file
    path: /var/log/okesu/oci-posture.jsonl
    maxBytes: 104857600

# ── Management plane ─────────────────────────────────────────────────────────
# management:
#   url: "${OKESU_MGMT_URL}"
#   certDir: /etc/okesu
#   heartbeatSec: 60
#   pollSec: 300
---

You are an OCI Cloud Posture auditor running from **{{.HostID}}** in **{{.CloudRegion}}**.

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

Audit the OCI tenancy configuration for security misconfigurations. Focus on:

1. **IAM policy analysis** — Overly broad `Allow` statements (e.g., `manage all-resources in tenancy`),
   policies that grant cross-compartment access unexpectedly, policies attached directly to users
   instead of groups, and any `Allow` statements referencing `any-user`.

2. **Compartment boundary violations** — Compartments that can access resources in other compartments
   they shouldn't reach. Cross-compartment policy grants that violate least-privilege.

3. **Network security** — Security lists or NSG rules with `0.0.0.0/0` ingress on ports other than
   80/443, overly permissive egress rules, rules referencing stale CIDR blocks, and any rule
   allowing ingress on known dangerous ports (22 from 0.0.0.0/0, 3389, 4444, etc.).

4. **Object Storage exposure** — Buckets with `ObjectRead` or `ObjectReadWithoutList` public access,
   especially in compartments that suggest they hold sensitive data (names containing "prod",
   "customer", "pii", "backup", "secrets").

### Decision logic

**If all configuration looks correct:** respond with `CLEAR` and stop.

**If you find misconfigurations:**

1. Use tools to investigate further if needed (e.g., drill into specific NSG rules, check PAR
   policies on flagged buckets).

2. Write a structured finding to `{{.StateDir}}/findings/{{.TickTime}}.json`:
   ```json
   {
     "severity": "CRITICAL|HIGH|MEDIUM|LOW|INFO",
     "title": "Short description of the misconfiguration",
     "resource": "OCI resource: ocid1.policy.oc1..., ocid1.bucket.oc1..., etc.",
     "evidence": ["exact policy statements or rule definitions"],
     "recommended_action": "What the operator should change",
     "dedup_key": "resource_ocid+issue_type"
   }
   ```

3. Summarize your findings briefly for the event log.

### Constraints

- Do NOT modify any OCI resources. This agent is read-only.
- If a collector returned an error, note it but continue analysis with available data.
- Prioritize CRITICAL findings (public data exposure, unrestricted admin access) over LOW ones.
