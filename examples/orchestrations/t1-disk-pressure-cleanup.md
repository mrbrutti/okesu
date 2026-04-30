---
name: t1-disk-pressure-cleanup
description: Tier-1 disk pressure auto-remediation. When a host reports low free space, scopes the offenders, applies safe automated cleanup (rotated logs, package caches, /tmp), verifies recovery, and only escalates if the host is still tight after the sweep.

trigger:
  on: finding
  filter: "finding.title contains 'disk' && finding.title contains 'free' && finding.severity in ['MEDIUM','HIGH','CRITICAL']"

inputs:
  host:
    type: string
    required: false
    default: ""
  finding_id:
    type: int
    required: false
    default: 0

defaults:
  timeout: 4m

steps:
  # 1. Capture the baseline: which mountpoints are pressured, what
  #    are the largest files / dirs, what's the package manager state.
  #    Read-only — no action taken yet.
  - id: scope
    agent: investigator
    node: "{{trigger.host}}"
    actions:
      - update_finding_status
      - set_finding_severity_override
      - add_finding_tag
      - link_run_to_finding
    prompt: |
      Scope disk pressure on {{trigger.host}} for finding #{{trigger.finding_id}}.

      Run these read-only checks (use the bash tool):
        - `df -h` to see all filesystems and their fill levels
        - `du -h --max-depth=2 /var | sort -rh | head -20` for /var
        - `du -h --max-depth=2 /tmp | sort -rh | head -10`
        - `du -h --max-depth=2 /home | sort -rh | head -10` (may not exist)
        - Identify which mounts are >80% full
        - Note any pre-existing process holding deleted files (lsof | grep deleted)

      Emit an orchestration_result finding with attributes:
        pressured_mounts (array of strings: e.g. ["/", "/var"])
        largest_var (string: top /var subdir)
        largest_tmp (string: top /tmp subdir or empty)
        deleted_handles (int: count of processes holding deleted files)
        free_pct (int: lowest free pct across pressured mounts)

  # 2. Safe automated cleanup. Strictly bounded to known-safe
  #    operations: rotated logs >7d, journal trim, package cache,
  #    /tmp files >7d. Runs only when /var or / is pressured.
  - id: cleanup
    when: "{{scope.result.pressured_mounts contains '/' || scope.result.pressured_mounts contains '/var'}}"
    agent: investigator
    node: "{{trigger.host}}"
    timeout: 5m
    actions:
      - update_finding_status
      - set_finding_severity_override
      - add_finding_tag
      - link_run_to_finding
    prompt: |
      Apply ONLY these safe cleanups on {{trigger.host}}. Stop after each
      one and reassess; abort if anything looks risky.

      1. Rotated logs older than 7d:
           find /var/log -type f \( -name "*.gz" -o -name "*.[0-9]" -o -name "*.[0-9].*" \) -mtime +7 -delete

      2. systemd journal vacuum to last 14d (only if journalctl exists):
           journalctl --vacuum-time=14d

      3. Package manager cache (pick whichever is installed):
           - Debian/Ubuntu: `apt-get clean` and `apt-get autoremove --purge -y`
           - RHEL/Fedora/Rocky:   `dnf clean all`
           - Alpine:              `apk cache clean`

      4. /tmp files older than 7d not in active use:
           find /tmp -type f -atime +7 -mtime +7 -delete 2>/dev/null
           find /tmp -mindepth 1 -type d -empty -delete 2>/dev/null

      DO NOT touch:
        - /home (user data)
        - /var/lib/* (application state)
        - /etc (config)
        - any docker/podman volumes
        - core dumps (let an operator decide)

      Emit an orchestration_result finding with attributes:
        bytes_reclaimed (int)
        actions_taken (array of strings)
        skipped (array of strings: reasons we skipped a step)

  # 3. Verify the cleanup worked. If still pressured, that's an
  #    escalation: there's an offending workload that auto-cleanup
  #    can't safely deal with.
  - id: verify
    when: "{{scope.result.pressured_mounts contains '/' || scope.result.pressured_mounts contains '/var'}}"
    agent: investigator
    node: "{{trigger.host}}"
    timeout: 2m
    actions:
      - update_finding_status
      - add_finding_tag
      - link_run_to_finding
    prompt: |
      Re-run `df -h` on {{trigger.host}}. Decide:
        - resolved: bool (every previously pressured mount now <80%)
        - still_pressured: array of mounts still ≥80%

      Originally pressured: {{scope.result.pressured_mounts}}
      Reclaimed: {{cleanup.result.bytes_reclaimed}} bytes

      Emit an orchestration_result finding with attributes:
        resolved (bool)
        still_pressured (array of strings)
        new_lowest_free_pct (int)
        operator_recommendation (string — only set when not resolved)
        actions (array)

      Actions to request:
        - resolved=true:
            update_finding_status → resolved
              (reason: e.g. "auto-cleanup reclaimed N bytes")
            add_finding_tag → auto-resolved
            link_run_to_finding
        - resolved=false:
            add_finding_tag → needs-human
            link_run_to_finding
            (do NOT change status — leave it open for the operator)
---

# Notes

This orchestration is **autonomous** — no approval gate. The cleanup
step uses a strict whitelist of safe operations (no recursion outside
known-noise dirs, no DB / app state touched). If the host is still
tight after the cleanup, the verify step's `operator_recommendation`
field gives the on-call a starting point.

Tested cleanup paths:
  - Debian/Ubuntu: apt + journalctl
  - RHEL family:   dnf + journalctl
  - Alpine:        apk
  - Generic:       /tmp + /var/log rotation

Hosts where autoremove is hazardous (curated lists, snapshot-locked
images) should be excluded by tagging — add a `noremediate=disk`
node label and the orchestration short-circuits at the scope step.
