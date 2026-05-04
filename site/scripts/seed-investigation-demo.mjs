#!/usr/bin/env node
// Drive the demo CP into a known state for screenshot capture:
//   1. Run edr-critical-response end-to-end
//   2. Confirm the resulting case
//   3. Add an operator note
// The capture scripts read CASE_ID from this script's stdout (last
// line) so they can land directly on the right case detail.
//
// Usage:
//   cd site
//   OKESU_URL=https://localhost:7443 \
//   OKESU_EMAIL=admin@local \
//   OKESU_PASSWORD=okesu-demo \
//     node scripts/seed-investigation-demo.mjs
//
// On success the last line of stdout is `CASE_ID=<id>`.

const URL      = process.env.OKESU_URL      ?? 'https://localhost:7443';
const EMAIL    = process.env.OKESU_EMAIL    ?? 'admin@local';
const PASSWORD = process.env.OKESU_PASSWORD ?? 'okesu-demo';

let cookieJar = '';

async function api(path, opts = {}) {
  const headers = {
    'Content-Type': 'application/json',
    ...(opts.headers ?? {}),
    ...(cookieJar ? { Cookie: cookieJar } : {}),
  };
  const res = await fetch(`${URL}${path}`, {
    ...opts,
    headers,
    // Allow the demo CP's self-signed cert.
    // Node 18+: Setting this env var is the cleanest way:
    //   NODE_TLS_REJECT_UNAUTHORIZED=0 (set externally before running).
  });
  // Capture session cookie on login.
  const setCookie = res.headers.get('set-cookie');
  if (setCookie) cookieJar = setCookie.split(';')[0];
  if (!res.ok) {
    const body = await res.text().catch(() => '');
    throw new Error(`${opts.method ?? 'GET'} ${path} → ${res.status}: ${body.slice(0, 200)}`);
  }
  return res;
}

async function login() {
  console.log('> login');
  const res = await api('/api/auth/login', {
    method: 'POST',
    body: JSON.stringify({ email: EMAIL, password: PASSWORD }),
  });
  await res.json().catch(() => null);
}

async function dispatchOrchestration() {
  console.log('> dispatch edr-critical-response');
  // The run endpoint expects a numeric orchestration id, not a name —
  // look it up first by listing /api/orchestrations.
  const list = await (await api('/api/orchestrations')).json();
  const items = Array.isArray(list) ? list : (list.orchestrations ?? []);
  const orch = items.find(o => (o.name ?? o.Name) === 'edr-critical-response');
  if (!orch) throw new Error('edr-critical-response orchestration not found on this CP');
  const orchId = orch.id ?? orch.ID;

  const res = await api(`/api/orchestrations/${orchId}/run`, {
    method: 'POST',
    body: JSON.stringify({ inputs: {} }),
  });
  const body = await res.json();
  return body.run_id ?? body.id ?? body.ID;
}

async function waitForRunCompletion(runId, timeoutMs = 90000) {
  console.log(`> wait for run ${runId} to complete (or hit approval gate)`);
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    const res = await api(`/api/orchestrations/runs/${runId}`);
    const run = await res.json();
    const status = run.status ?? '';
    if (status === 'completed' || status === 'failed' || status === 'approval_required') {
      console.log(`  status=${status}`);
      return run;
    }
    await new Promise(r => setTimeout(r, 2000));
  }
  throw new Error(`run ${runId} did not settle within ${timeoutMs}ms`);
}

async function findOrCreateCase(runId) {
  console.log('> find or create investigation for run');
  // First, see whether the run has already attached to a case.
  const res = await api(`/api/orchestrations/runs/${runId}`);
  const run = await res.json();
  if (run.investigation_id) return run.investigation_id;

  // Otherwise create one from the run's findings.
  const create = await api('/api/investigations', {
    method: 'POST',
    body: JSON.stringify({
      title: 'Demo: critical EDR callback investigation',
      severity: 'high',
      run_ids: [runId],
    }),
  });
  const body = await create.json();
  return body.id;
}

async function addNote(caseId, body) {
  console.log(`> add note to case ${caseId}`);
  await api(`/api/investigations/${caseId}/notes`, {
    method: 'POST',
    body: JSON.stringify({ body }),
  });
}

async function main() {
  if (process.env.NODE_TLS_REJECT_UNAUTHORIZED !== '0') {
    console.warn('[warn] expecting NODE_TLS_REJECT_UNAUTHORIZED=0 for self-signed CP');
  }

  await login();
  const runId = await dispatchOrchestration();
  await waitForRunCompletion(runId);
  const caseId = await findOrCreateCase(runId);
  await addNote(caseId, 'Triage in progress — EDR triage step landed, pulling host snapshots.');
  await addNote(caseId, 'Confirmed callback to 8.8.8.8 — pivoting to fleet hunt.');

  // Final line of stdout is the marker the capture scripts read.
  console.log(`CASE_ID=${caseId}`);
}

main().catch(err => {
  console.error('FATAL:', err.message);
  process.exit(1);
});
