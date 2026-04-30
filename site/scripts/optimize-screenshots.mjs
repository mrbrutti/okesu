#!/usr/bin/env node
// Convert PNG screenshots in site/public/screenshots/ to WebP at 85%
// quality for ~50% size reduction. The capture script writes PNGs;
// this script post-processes them. Run:
//
//   cd site
//   node scripts/optimize-screenshots.mjs
//
// PNG originals are deleted after WebP is written. Re-running the
// capture overwrites the PNGs which then need re-optimizing.

import sharp from 'sharp';
import { readdir, unlink } from 'node:fs/promises';
import { join, dirname, basename } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const DIR = join(__dirname, '..', 'public', 'screenshots');

const files = (await readdir(DIR)).filter(f => f.endsWith('.png'));
console.log(`> Optimizing ${files.length} PNGs in ${DIR}`);

for (const f of files) {
  const src = join(DIR, f);
  const dst = join(DIR, basename(f, '.png') + '.webp');
  await sharp(src).webp({ quality: 85, effort: 6 }).toFile(dst);
  await unlink(src);
  console.log(`  ✓ ${f} → ${basename(dst)}`);
}
console.log('> Done.');
