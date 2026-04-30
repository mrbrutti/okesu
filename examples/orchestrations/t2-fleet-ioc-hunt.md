---
name: t2-fleet-ioc-hunt
description: Tier-2 fleet-wide IOC hunt. Triggered when any finding surfaces a usable indicator (sha256, IP, domain, key fingerprint). Fans out to every reachable host of the same OS family, hunts the IOC, builds a heatmap, and pauses for operator approval before any containment action.

trigger:
  on: finding
  filter: "(finding.attributes.sha256 != '') || (finding.attributes.ioc != '')"

inputs:
  ioc:
    type: string
    required: false
    default: ""
  ioc_kind:
    type: string
    required: false
    default: "sha256"   # one of sha256|ipv4|domain|ssh_pubkey
  source_host:
    type: string
    required: false
    default: ""

defaults:
  timeout: 12m

steps:
  # 1. Confirm the IOC and pick the hunt scope. Different IOC kinds
  #    point at different host populations — a sha256 spreads via
  #    package/payload (so all hosts of the same OS), an SSH pubkey
  #    spreads via provisioning (so all hosts using the same key
  #    template), an IP/domain via outbound calls (any host).
  - id: scope
    agent: investigator
    node: "{{trigger.source_host}}"
    data:
      ioc:
        query: iocs.lookup
        params: { kind: "{{trigger.ioc_kind}}", value: "{{trigger.ioc}}" }
    prompt: |
      Confirm IOC and define hunt scope.

      Source finding's host: {{trigger.source_host}}
      IOC: `{{data.ioc.normalized_value}}` (kind={{data.ioc.kind}})
      Catalog metadata: source={{data.ioc.source}} attribution={{data.ioc.attribution}} severity_floor={{data.ioc.severity_floor}}
      Attributes from triggering finding: {{trigger.attributes | json}}

      Decide hunt scope:
        - target_population: one of `same_os`, `all`, `web_tier`, `db_tier`
        - explanation: one sentence why
        - max_hosts: cap parallel fan-out (default 20)

      Emit an orchestration_result finding with attributes:
        valid (bool, copy from {{data.ioc.valid}})
        normalized_ioc (string, copy from {{data.ioc.normalized_value}})
        target_population (string)
        explanation (string)
        max_hosts (int)

  # 2. Fan out to representative hosts. The operator's lab uses
  #    these names; a real deployment would either inject node
  #    selectors via the inputs block or read them from a tag query
  #    (when the engine adds tag selectors).
  - id: hunt
    when: "{{scope.result.valid == true}}"
    agent: threat-hunter
    nodes:
      - threat-rocky-1
      - threat-rocky-2
      - threat-fedora-1
      - threat-fedora-2
      - threat-debian-1
      - threat-debian-2
      - edr-rocky-1
      - edr-fedora-1
      - edr-debian-1
      - fim-debian-1
      - fim-rocky-1
      - fim-fedora-1
      - sre-debian-1
      - sre-rocky-1
      - sre-fedora-1
      - mixed-east-1
      - mixed-west-1
    timeout: 8m
    continue_on_error: true
    prompt: |
      Hunt for IOC `{{scope.result.normalized_ioc}}` ({{trigger.ioc_kind}}) on this host.

      For sha256:
        - find / -type f -size +1k -exec sha256sum {} + 2>/dev/null | grep -F "{{scope.result.normalized_ioc}}"
        - rpm -qa | xargs rpm -ql 2>/dev/null  (or dpkg -L for Debian) and verify integrity
        - Check process memory of long-running daemons

      For ipv4 / domain:
        - `ss -tunap | grep {{scope.result.normalized_ioc}}` (live connections)
        - `journalctl --since "24 hours ago" | grep {{scope.result.normalized_ioc}}` (logs)
        - `ip route get {{scope.result.normalized_ioc}}` if v4

      For ssh_pubkey:
        - Search ~/.ssh/authorized_keys, /root/.ssh/authorized_keys
        - Check /etc/ssh/sshd_config TrustedUserCAKeys
        - grep -rF "{{scope.result.normalized_ioc}}" /etc/ssh /root/.ssh 2>/dev/null

      Emit an orchestration_result finding with attributes:
        host_match (bool)
        evidence (array of strings — paths / connections / lines)
        confidence (string: low|medium|high)
        first_seen (string — RFC3339 from filesystem mtime / log timestamp)

  # 3. Heatmap + recommendation. Aggregate the hunt results into a
  #    spread map and write the on-call brief.
  - id: heatmap
    when: "{{scope.result.valid == true}}"
    agent: incident-responder
    timeout: 5m
    actions:
      - add_finding_tag
      - link_run_to_finding
      - escalate
    prompt: |
      Build the fleet heatmap for IOC `{{scope.result.normalized_ioc}}`.

      Per-host hunt results:
        {{hunt.byNode | json}}

      Aggregate:
        - matched_hosts (array of names)
        - clean_hosts (array of names)
        - errored_hosts (array of names)
        - earliest_first_seen across matches
        - most_common_evidence_type

      Recommend a containment plan, scoped by confidence:
        - confidence=high  → quarantine matched hosts (firewall isolate, snapshot)
        - confidence=medium → snapshot + monitor, no isolation yet
        - confidence=low    → keep watching, ask the operator if they recognise it

      Emit an orchestration_result finding with attributes:
        matched_count (int)
        clean_count (int)
        errored_count (int)
        recommended_action (string: quarantine|snapshot|monitor|noop)
        recommended_severity (string: SEV-1|SEV-2|SEV-3)
        plan (string — multi-line markdown, ≤500 words)
        actions (array)

      Actions to request (always):
        add_finding_tag → ioc-hunted (on the source finding)
        link_run_to_finding (on the source finding)

      If matched_count > 0 AND recommended_severity in (SEV-1, SEV-2):
        escalate (reason: short summary, severity matches recommended_severity)

  # 4. Operator-gated containment. Approve to actually isolate the
  #    matched hosts. The action is intentionally explicit — even
  #    inside an automated orchestration, hard isolation needs a
  #    human "go".
  - id: contain
    approval: required
    when: "{{heatmap.result.recommended_action == 'quarantine'}}"
    agent: incident-responder
    timeout: 10m
    prompt: |
      Containment for IOC `{{scope.result.normalized_ioc}}`. Operator approved.

      Targets ({{heatmap.result.matched_count}} hosts): {{heatmap.result.matched_hosts | json}}

      For each target:
        1. Snapshot key state if a `snapshot.sh` is present at /usr/local/bin
        2. Apply network isolation:
           - iptables/nftables: DROP egress except to {{trigger.source_host}}'s management plane
           - macOS: pf rule via `pfctl -e`
        3. Pause auto-update on the host so the next deploy can't sneak in
        4. Note the action in /etc/okesu/incident-trail.log

      Emit an orchestration_result finding with attributes:
        contained_hosts (array of strings)
        failed_hosts (array of strings)
        actions_per_host (object: hostname → array of actions taken)
---

# Notes

This is the T2 spread-detection orchestration. The flow:

  trigger → scope → fan-out hunt → heatmap → [GATE] → contain

The gate matters: a fleet-wide quarantine triggered by a noisy IOC
is its own incident. Operator approves only when the heatmap's
confidence + recommended_action match what they're willing to
authorise.

Tagging a host `noisolate=yes` would let it skip the contain step
even after approval — useful for the CP itself (which usually
shouldn't be isolated by its own automation). Add the tag check
to the contain prompt's "for each target" loop.
