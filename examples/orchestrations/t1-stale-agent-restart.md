---
name: t1-stale-agent-restart
description: Tier-1 stale-daimon recovery. When a daimon misses heartbeats, attempts a `systemctl restart` (or flavour-equivalent), waits for liveness to recover, and only escalates if the restart fails or the unit goes back into a crash loop.

trigger:
  on: finding
  filter: "finding.title contains 'stale' && finding.agent == 'okesu-system'"

inputs:
  host:
    type: string
    required: false
    default: ""
  agent_name:
    type: string
    required: false
    default: ""

defaults:
  timeout: 3m

steps:
  # 1. Probe: is the unit actually broken, or did the heartbeat
  #    just lag for a non-recoverable reason (host down, network
  #    partition, sysadmin paused it intentionally)?
  - id: probe
    agent: investigator
    node: "{{trigger.host}}"
    actions:
      - update_finding_status
      - set_finding_severity_override
      - add_finding_tag
      - link_run_to_finding
    prompt: |
      Probe daimon `{{trigger.agent}}` on {{trigger.host}}.

      Run, in order:
        1. `systemctl is-active okesu-agent-{{trigger.agent}}.service` (or launchctl/rcctl/svcs equivalent)
        2. `systemctl status okesu-agent-{{trigger.agent}}.service --no-pager | head -40`
        3. `journalctl -u okesu-agent-{{trigger.agent}}.service -n 50 --no-pager`
           (or the OS equivalent — see /var/log on BSD, /var/svc/log on illumos)
        4. Inspect for crashloop patterns: repeated "Started" / "Failed" toggling,
           OOM killer messages, exit code 137/143.

      Decide:
        - state: one of `running` | `failed` | `crash_loop` | `paused` | `missing`
        - safe_to_restart: bool — false when paused or crash_loop with non-recoverable error
        - reason: short sentence

      Emit an orchestration_result finding with attributes:
        state, safe_to_restart, reason, last_log_line (string)

  # 2. Restart when safe. The recovery step is intentionally simple:
  #    one restart, then wait for the next heartbeat to land.
  - id: restart
    when: "{{probe.result.safe_to_restart == true}}"
    agent: investigator
    node: "{{trigger.host}}"
    timeout: 1m
    actions:
      - update_finding_status
      - set_finding_severity_override
      - add_finding_tag
      - link_run_to_finding
    prompt: |
      Restart `okesu-agent-{{trigger.agent}}` on {{trigger.host}}.

      Detect the service manager and use the right command:
        - systemd:  `systemctl restart okesu-agent-{{trigger.agent}}.service`
        - launchd:  `launchctl kickstart -k system/com.okesu.agent-{{trigger.agent}}`
        - rc.d:     `service okesu_agent_{{trigger.agent}} restart` (FreeBSD) or
                    `rcctl restart okesu_agent_{{trigger.agent}}` (OpenBSD)
        - SMF:      `svcadm restart svc:/site/okesu-agent-{{trigger.agent}}:default`

      After the restart returns, check `is-active` again and capture the result.

      Emit an orchestration_result finding with attributes:
        restart_ok (bool)
        post_state (string)
        cmd_used (string)

  # 3. Verify liveness. Wait for the next heartbeat object in the
  #    bucket / the next mgmt-plane heartbeat call. Read-only.
  - id: verify
    when: "{{probe.result.safe_to_restart == true}}"
    agent: investigator
    node: "{{trigger.host}}"
    timeout: 4m
    actions:
      - update_finding_status
      - add_finding_tag
      - link_run_to_finding
    prompt: |
      Verify `{{trigger.agent}}` on {{trigger.host}} is healthy after the
      restart.

      Wait up to 90s for either:
        - `systemctl is-active` to report `active (running)`
        - The unit's stdout to emit a fresh `tick_done` event
        - The host's heartbeat.json to update (S3 transport only)

      Sample every 10s. Report:
        - recovered (bool)
        - elapsed_seconds (int)
        - operator_recommendation (string — only when not recovered)

      Emit an orchestration_result finding with those attributes plus
      `actions` (array).

      Actions to request:
        - recovered=true:
            update_finding_status → resolved
              (reason: "daimon restart succeeded, heartbeat caught up in Ns")
            add_finding_tag → auto-recovered
            link_run_to_finding
        - recovered=false:
            add_finding_tag → flapping
            link_run_to_finding
            (status stays open; flapping needs human eyes on the unit logs)
---

# Notes

Failure modes routed to escalation:
  - state=paused        — operator intentionally stopped the daimon
  - state=crash_loop    — non-recoverable; needs a config / binary fix
  - state=missing       — unit file gone; needs a redeploy
  - restart succeeded but verify failed — flapping; needs investigation

Successes are silent — no notification, just an audit trail in the
orchestration runs page. The whole point is the operator only hears
about the daimons that actually need attention.
