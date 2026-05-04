#!/usr/bin/env node
// Capture real screenshots from a running Okesu CP at localhost:7443
// (or whatever URL/credentials are in env vars) for use on okesu.to.
//
// Usage:
//   cd site
//   OKESU_URL=https://localhost:7443 \
//   OKESU_EMAIL=admin@local \
//   OKESU_PASSWORD=okesu-demo \
//     node scripts/capture-screenshots.mjs
//
// Output goes to site/public/screenshots/. Each screenshot is captured
// at 2x device pixel ratio for retina sharpness.
//
// /events is skipped because its long-lived stream never resolves
// `networkidle`; if needed in the future, swap to `domcontentloaded`.

import { chromium } from 'playwright';
import { mkdir } from 'node:fs/promises';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const URL      = process.env.OKESU_URL      ?? 'https://localhost:7443';
const EMAIL    = process.env.OKESU_EMAIL    ?? 'admin@local';
const PASSWORD = process.env.OKESU_PASSWORD ?? 'okesu-demo';
const CASE_ID  = process.env.OKESU_CASE_ID; // produced by seed-investigation-demo.mjs

const __dirname = dirname(fileURLToPath(import.meta.url));
const OUT_DIR   = join(__dirname, '..', 'public', 'screenshots');

const targets = [
  { name: 'dashboard',        path: '/dashboard',      settle: 1500 },
  { name: 'findings-list',    path: '/findings',       settle: 1500 },
  { name: 'investigations',   path: '/investigations', settle: 1500 },
  { name: 'orchestrations',   path: '/orchestrations', settle: 1500 },
  { name: 'agents-library',   path: '/agents',         settle: 1500 },
  { name: 'daimons-library',  path: '/daimons',        settle: 1500 },
  { name: 'nodes-list',       path: '/nodes',          settle: 1500 },
  { name: 'federation',       path: '/federation',     settle: 1500 },
];

async function captureRunsTabAndFirstRun(page) {
  // Visit the orchestrations Runs tab (signature view: live DAG).
  await page.goto(`${URL}/orchestrations?tab=runs`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);
  await page.screenshot({
    path: join(OUT_DIR, 'orchestration-runs-list.png'),
    fullPage: false,
  });
  console.log('  ✓ orchestration-runs-list.png');

  // Drill into the first run row (the platform shows a "Run #N" card).
  // We look for any clickable run-id element.
  const firstRow = page.locator('text=/^Run #\\d+/').first();
  if (await firstRow.count() === 0) {
    console.log('  (no runs visible — skipping run-detail)');
    return;
  }
  await firstRow.click();
  await page.waitForTimeout(2500); // let the run-canvas render + animations settle
  await page.screenshot({
    path: join(OUT_DIR, 'orchestration-run-detail.png'),
    fullPage: false,
  });
  console.log('  ✓ orchestration-run-detail.png');
}

async function captureFirstOrchestrationEditor(page) {
  // Visit the orchestrations Library tab and open the first
  // orchestration to land on the editor canvas.
  await page.goto(`${URL}/orchestrations`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);

  // The list rows have 'Run' buttons; the row body itself navigates
  // to the editor when clicked. Find the first row and click its
  // text body (not the Run button).
  const firstOrchName = page.locator('div').filter({ hasText: /^t1-|^t2-|^edr-/ }).first();
  if (await firstOrchName.count() === 0) {
    console.log('  (no orchestrations visible — skipping editor)');
    return;
  }
  // Click anywhere on the row but not the Run button on the right.
  await firstOrchName.click({ position: { x: 100, y: 10 } });
  await page.waitForTimeout(2500);
  await page.screenshot({
    path: join(OUT_DIR, 'orchestration-editor.png'),
    fullPage: false,
  });
  console.log('  ✓ orchestration-editor.png');
}

async function captureFirstInvestigation(page) {
  await page.goto(`${URL}/investigations`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);
  const firstRow = page.locator('a[href*="/investigations/"]').first();
  if (await firstRow.count() === 0) {
    console.log('  (no investigations visible — skipping detail)');
    return;
  }
  await firstRow.click();
  await page.waitForTimeout(2000);
  await page.screenshot({
    path: join(OUT_DIR, 'investigation-detail.png'),
    fullPage: false,
  });
  console.log('  ✓ investigation-detail.png');
}

async function captureInvestigationSurfaces(page) {
  if (!CASE_ID) {
    console.log('  (OKESU_CASE_ID not set — skipping investigation surfaces)');
    return;
  }
  const base = `${URL}/investigations/${CASE_ID}`;

  // Overview
  console.log('> investigation overview');
  await page.goto(base, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);
  await page.screenshot({ path: join(OUT_DIR, 'investigation-overview.png'), fullPage: false });
  console.log('  ✓ investigation-overview.png');

  // The SPA tracks the active tab in component state, not URL — we
  // click the tab button to switch. Tab buttons may have a count
  // badge appended ("Notes 0") so we match by substring rather than
  // exact text.
  async function clickTab(label) {
    const btn = page.locator('button').filter({ hasText: new RegExp(label, 'i') }).first();
    if (await btn.count() > 0) {
      await btn.click();
      await page.waitForTimeout(1500);
    }
  }

  // Timeline view lives on the Overview tab (the case timeline panel
  // with severity/host/agent filter chips). The investigation-timeline
  // asset captures the same surface — just framed differently for
  // the concept page's "timeline" section. We re-screenshot here to
  // make the asset name explicit.
  console.log('> investigation timeline (Overview tab)');
  await page.screenshot({ path: join(OUT_DIR, 'investigation-timeline.png'), fullPage: false });
  console.log('  ✓ investigation-timeline.png');

  // Graph tab
  console.log('> investigation graph');
  await clickTab('Graph');
  await page.waitForTimeout(1500); // let react-flow settle
  await page.screenshot({ path: join(OUT_DIR, 'investigation-graph.png'), fullPage: false });
  console.log('  ✓ investigation-graph.png');

  // War-room thumb (single-context — the multi-presence shot lives in capture-tour.mjs).
  console.log('> investigation war-room thumb');
  await clickTab('Notes');
  // Fire-and-forget click; if the war-room button isn't there, we just
  // capture the notes panel as-is and skip the dedicated thumb.
  try {
    const btn = page.locator('button').filter({ hasText: /war.?room/i }).first();
    if (await btn.count() > 0) {
      await btn.click();
      await page.waitForTimeout(1500);
    }
  } catch (err) { /* skip */ }
  await page.screenshot({ path: join(OUT_DIR, 'investigation-war-room-thumb.png'), fullPage: false });
  console.log('  ✓ investigation-war-room-thumb.png');

  // PDF page-1 — best-effort. If the endpoint serves an inline preview
  // we capture it; otherwise we log + skip and the page falls back to a
  // placeholder.
  console.log('> investigation pdf page-1');
  try {
    const res = await page.goto(`${URL}/api/investigations/${CASE_ID}/report.pdf`, { waitUntil: 'networkidle' });
    const ctype = (res?.headers() ?? {})['content-type'] ?? '';
    if (ctype.includes('application/pdf')) {
      // Browsers may render PDFs inline; if so the screenshot captures it.
      await page.waitForTimeout(2000);
      await page.screenshot({ path: join(OUT_DIR, 'investigation-pdf-page1.png'), fullPage: false });
      console.log('  ✓ investigation-pdf-page1.png');
    } else {
      console.log('  (PDF endpoint did not return application/pdf — skipping)');
    }
  } catch (err) {
    console.log(`  (PDF capture failed: ${err.message} — skipping)`);
  }
}

async function main() {
  await mkdir(OUT_DIR, { recursive: true });
  console.log(`> Capturing screenshots from ${URL}`);
  console.log(`> Output: ${OUT_DIR}`);

  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    ignoreHTTPSErrors: true,
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
  });
  const page = await context.newPage();

  console.log('> Logging in…');
  await page.goto(`${URL}/login`, { waitUntil: 'networkidle' });
  await page.fill('input[type="email"]', EMAIL);
  await page.fill('input[type="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  // Wait until any non-login URL becomes the current URL.
  await page.waitForFunction(() => !location.pathname.startsWith('/login'), {
    timeout: 10000,
  });
  await page.waitForTimeout(1500);
  console.log('  ✓ logged in');

  for (const t of targets) {
    try {
      console.log(`> ${t.name}`);
      await page.goto(`${URL}${t.path}`, { waitUntil: 'networkidle' });
      await page.waitForTimeout(t.settle);
      await page.screenshot({
        path: join(OUT_DIR, `${t.name}.png`),
        fullPage: false,
      });
      console.log(`  ✓ ${t.name}.png`);
    } catch (err) {
      console.error(`  ✗ ${t.name}: ${err.message}`);
    }
  }

  try {
    console.log('> orchestration runs + run detail');
    await captureRunsTabAndFirstRun(page);
  } catch (err) {
    console.error(`  ✗ runs/run-detail: ${err.message}`);
  }

  try {
    console.log('> orchestration editor');
    await captureFirstOrchestrationEditor(page);
  } catch (err) {
    console.error(`  ✗ editor: ${err.message}`);
  }

  try {
    console.log('> investigation detail');
    await captureFirstInvestigation(page);
  } catch (err) {
    console.error(`  ✗ investigation-detail: ${err.message}`);
  }

  await captureInvestigationSurfaces(page);

  await browser.close();
  console.log('> Done.');
}

main().catch(err => {
  console.error('FATAL:', err);
  process.exit(1);
});
