#!/usr/bin/env node
// Capture the tour screenshots — narrative-driven shots that the
// /tour page stitches together. Reuses the same login + viewport
// setup as capture-screenshots.mjs but adds tour-specific flows
// (open a finding drawer, click into a run, capture mid-run).
//
// Run AFTER kicking off some orchestrations via the API so there are
// fresh "running" runs to land on. The script is forgiving — if a
// state isn't available it just skips and logs.

import { chromium } from 'playwright';
import { mkdir } from 'node:fs/promises';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const URL      = process.env.OKESU_URL      ?? 'https://localhost:7443';
const EMAIL    = process.env.OKESU_EMAIL    ?? 'admin@local';
const PASSWORD = process.env.OKESU_PASSWORD ?? 'okesu-demo';
const RUN_ID   = process.env.OKESU_RUN_ID;       // optional: a specific run-detail page id
const FANOUT_RUN_ID = process.env.OKESU_FANOUT_RUN_ID; // optional: pure-fanout run
const CASE_ID = process.env.OKESU_CASE_ID;

const __dirname = dirname(fileURLToPath(import.meta.url));
const OUT_DIR   = join(__dirname, '..', 'public', 'screenshots');

async function login(page) {
  console.log('> login');
  await page.goto(`${URL}/login`, { waitUntil: 'networkidle' });
  await page.fill('input[type="email"]', EMAIL);
  await page.fill('input[type="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await page.waitForFunction(() => !location.pathname.startsWith('/login'), { timeout: 10000 });
  await page.waitForTimeout(1500);
}

async function shoot(page, name) {
  await page.screenshot({ path: join(OUT_DIR, `tour-${name}.png`), fullPage: false });
  console.log(`  ✓ tour-${name}.png`);
}

async function main() {
  await mkdir(OUT_DIR, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    ignoreHTTPSErrors: true,
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
  });
  const page = await context.newPage();

  await login(page);

  // ── 1. Findings list — the hero shot for "a finding worth your attention".
  console.log('> findings (severity-filtered)');
  await page.goto(`${URL}/findings`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(2000);
  await shoot(page, 'findings-arrived');

  // ── 2. Click into the first CRITICAL finding to capture its drawer.
  console.log('> finding drawer');
  try {
    // Click the first row that looks clickable.
    const firstFinding = page.locator('button, a').filter({ hasText: /CRITICAL/i }).first();
    if (await firstFinding.count() > 0) {
      await firstFinding.click({ position: { x: 200, y: 12 } });
      await page.waitForTimeout(1500);
      await shoot(page, 'finding-drawer');
    } else {
      console.log('  (no CRITICAL row found — skipping drawer)');
    }
  } catch (err) {
    console.error(`  ✗ ${err.message}`);
  }

  // ── 3. Orchestrations library — show the menu of playbooks.
  console.log('> orchestrations library');
  await page.goto(`${URL}/orchestrations`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(2000);
  await shoot(page, 'orchestrations-library');

  // ── 4. Run detail — navigate via Orchestrations → Runs → click row.
  console.log('> run-detail (via Runs tab)');
  await page.goto(`${URL}/orchestrations?tab=runs`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(2000);
  // Click the first running row if available, else the first row at all.
  let row = page.locator('button, a').filter({ hasText: /^Run #\d+/ }).first();
  if (await row.count() === 0) {
    row = page.locator('text=/^Run #\\d+/').first();
  }
  if (await row.count() > 0) {
    await row.click();
    await page.waitForTimeout(2500); // let canvas + animations settle
    await shoot(page, 'run-detail');
  } else {
    console.log('  (no run rows visible)');
  }

  // ── 5. Investigations list + detail.
  console.log('> investigations list');
  await page.goto(`${URL}/investigations`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);
  await shoot(page, 'investigations-list');

  console.log('> investigation detail');
  try {
    const firstInv = page.locator('a[href*="/investigations/"]').first();
    if (await firstInv.count() > 0) {
      await firstInv.click();
      await page.waitForTimeout(2000);
      await shoot(page, 'investigation-detail');
    }
  } catch (err) {
    console.error(`  ✗ ${err.message}`);
  }

  if (!CASE_ID) {
    console.log('  (OKESU_CASE_ID not set — skipping tour investigation tail)');
  } else {
    const base = `${URL}/investigations/${CASE_ID}`;

    // ── 7. Timeline.
    console.log('> tour: investigation timeline');
    await page.goto(`${base}?tab=timeline`, { waitUntil: 'networkidle' });
    await page.waitForTimeout(2500);
    await shoot(page, 'investigation-timeline');

    // ── 8. Graph.
    console.log('> tour: investigation graph');
    await page.goto(`${base}?tab=graph`, { waitUntil: 'networkidle' });
    await page.waitForTimeout(2500);
    await shoot(page, 'investigation-graph');

    // ── 10. PDF page-1 (handled before war-room because war-room
    //       opens a second context that we want to tear down last).
    console.log('> tour: investigation pdf');
    try {
      const res = await page.goto(`${URL}/api/investigations/${CASE_ID}/report.pdf`, { waitUntil: 'networkidle' });
      const ctype = (res?.headers() ?? {})['content-type'] ?? '';
      if (ctype.includes('application/pdf')) {
        await page.waitForTimeout(2000);
        await shoot(page, 'investigation-pdf');
      } else {
        console.log('  (PDF endpoint did not return application/pdf — skipping)');
      }
    } catch (err) {
      console.log(`  (PDF capture failed: ${err.message} — skipping)`);
    }
  }

  if (CASE_ID) {
    console.log('> tour: investigation war-room (two contexts)');
    let altContext;
    try {
      // Open a second browser context as a different operator.
      altContext = await browser.newContext({
        ignoreHTTPSErrors: true,
        viewport: { width: 1440, height: 900 },
        deviceScaleFactor: 2,
      });
      const altPage = await altContext.newPage();

      // Login on the alt context (uses the same admin user — distinct
      // session yields a distinct presence chip via session id).
      await altPage.goto(`${URL}/login`, { waitUntil: 'networkidle' });
      await altPage.fill('input[type="email"]', EMAIL);
      await altPage.fill('input[type="password"]', PASSWORD);
      await altPage.click('button[type="submit"]');
      await altPage.waitForFunction(() => !location.pathname.startsWith('/login'), { timeout: 10000 });

      // Both contexts navigate to the war room.
      const url = `${URL}/investigations/${CASE_ID}?tab=notes`;
      await page.goto(url, { waitUntil: 'networkidle' });
      await altPage.goto(url, { waitUntil: 'networkidle' });
      await page.waitForTimeout(1500);

      // Open the war room on each (button text may be "Start war room"
      // or similar — match anything containing "war").
      for (const p of [page, altPage]) {
        try {
          const btn = p.locator('button').filter({ hasText: /war/i }).first();
          if (await btn.count() > 0) await btn.click();
        } catch (e) { /* skip */ }
      }
      await page.waitForTimeout(1500);

      // Type into both — produces real cursor positions + presence.
      const textareaA = page.locator('textarea').first();
      const textareaB = altPage.locator('textarea').first();
      if (await textareaA.count() > 0) {
        await textareaA.fill('callback to 8.8.8.8 from web-prod-01 — netflow confirms');
      }
      if (await textareaB.count() > 0) {
        await textareaB.fill('EDR shows pid 4421 spawned by sshd — pulling memory snapshot now');
      }
      await page.waitForTimeout(1200);

      await shoot(page, 'investigation-war-room');
    } catch (err) {
      console.log(`  (war-room two-context capture failed: ${err.message} — skipping)`);
    } finally {
      if (altContext) await altContext.close();
    }
  }

  await browser.close();
  console.log('> Done.');
}

main().catch(err => {
  console.error('FATAL:', err);
  process.exit(1);
});
