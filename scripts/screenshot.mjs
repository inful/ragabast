#!/usr/bin/env node
// scripts/screenshot.mjs — Capture screenshots of a URL at standard viewports.
//
// Why this exists: layout/UX changes need before/after comparison, and
// "I changed the CSS and ran the tests" is not a verification that
// the layout actually looks the way it should. This script drives the
// system Chrome via puppeteer-core (no extra browser download) and
// captures each page at the three viewports we care about:
//
//   - 1920x1080 (desktop)
//   - 1280x800  (laptop)
//   - 768x1024  (tablet portrait)
//
// Output: <output-dir>/<viewport>.png for each viewport, e.g.
// screenshots/actual/desktop-1920x1080.png.
//
// Usage:
//   node scripts/screenshot.mjs <url> <output-dir>
//   node scripts/screenshot.mjs http://127.0.0.1:8080/ screenshots/actual
//
// The script is intentionally minimal — the Makefile wraps it with the
// server start, the css-check guard, and the directory creation. Run
// it directly when you want to skip the Make wrapper (e.g. when the
// server is already up and you just want a quick capture).

import { mkdir } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { resolve, join } from 'node:path';
import puppeteer from 'puppeteer-core';

const CHROME_PATHS = [
  // macOS — user-installed Google Chrome
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  // macOS — Chromium (homebrew keg-only)
  '/Applications/Chromium.app/Contents/MacOS/Chromium',
  // Linux — distro package
  '/usr/bin/google-chrome',
  '/usr/bin/chromium-browser',
  '/usr/bin/chromium',
];

const VIEWPORTS = [
  { name: 'desktop-1920x1080', width: 1920, height: 1080 },
  { name: 'laptop-1280x800', width: 1280, height: 800 },
  { name: 'tablet-768x1024', width: 768, height: 1024 },
];

// Find the first existing Chrome binary. The list is OS-agnostic —
// each platform picks its own canonical path.
function findChrome() {
  for (const p of CHROME_PATHS) {
    if (existsSync(p)) return p;
  }
  throw new Error(
    'No Chrome/Chromium binary found. Tried:\n  ' +
      CHROME_PATHS.join('\n  ') +
      '\n\nInstall Chrome (https://google.com/chrome) or Chromium and re-run.'
  );
}

async function main() {
  const url = process.argv[2];
  const outDirRaw = process.argv[3];
  if (!url || !outDirRaw) {
    console.error('Usage: node scripts/screenshot.mjs <url> <output-dir>');
    process.exit(2);
  }

  const outDir = resolve(outDirRaw);
  await mkdir(outDir, { recursive: true });

  const chromePath = findChrome();
  console.log(`[screenshot] launching ${chromePath}`);
  const browser = await puppeteer.launch({
    executablePath: chromePath,
    headless: true,
    args: [
      '--no-sandbox',
      '--disable-setuid-sandbox',
      // Disable GPU paths that crash in headless on macOS.
      '--disable-gpu',
      // Stable window size across viewports.
      '--force-device-scale-factor=1',
    ],
  });

  try {
    for (const vp of VIEWPORTS) {
      const page = await browser.newPage();
      await page.setViewport({ width: vp.width, height: vp.height, deviceScaleFactor: 1 });
      console.log(`[screenshot] ${vp.name} -> ${url}`);
      // networkidle0: wait for no more than 0 network connections for
      // at least 500ms. The chat page is server-rendered, so this
      // resolves quickly. For pages that stream responses (HTMX
      // swappable) we add a small post-load settle window below.
      await page.goto(url, { waitUntil: 'networkidle0', timeout: 15000 });
      // 500ms settle — gives CSS transitions, font loading, and
      // late layout shifts (e.g. web fonts swapping in) a chance
      // to finish before the screenshot. Empirically enough for
      // the chat page; raise if you see mid-animation captures.
      await new Promise((r) => setTimeout(r, 500));
      const outPath = join(outDir, `${vp.name}.png`);
      await page.screenshot({ path: outPath, fullPage: true });
      console.log(`[screenshot]   wrote ${outPath}`);
      await page.close();
    }
  } finally {
    await browser.close();
  }
}

main().catch((err) => {
  console.error('[screenshot] FAILED:', err.message);
  process.exit(1);
});
