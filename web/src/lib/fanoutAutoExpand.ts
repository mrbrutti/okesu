import type { OrchestrationStepStatus, StepNodeDispatchView } from '../api';

/**
 * Decide whether a fan-out card should auto-expand its per-host rows
 * on first render, before any user override.
 *
 * Rules (decided in spec §"Auto-expand rule"):
 *
 *   - n ≤ 4         → always expanded
 *   - 5 ≤ n ≤ 20   → expanded while the step is running/failed OR any
 *                    host is running/failed; collapsed otherwise
 *   - n > 20        → never expands per-host rows on the canvas;
 *                    drill-in is via the host-list drawer
 *
 * Returns true when host rows should be visible inline.
 */
export function fanoutAutoExpand(
  hostCount: number,
  stepStatus: OrchestrationStepStatus,
  hosts: StepNodeDispatchView[],
): boolean {
  if (hostCount > 20) return false;
  if (hostCount <= 4) return true;
  if (stepStatus === 'running' || stepStatus === 'failed') return true;
  return hosts.some((h) => h.status === 'running' || h.status === 'failed');
}
