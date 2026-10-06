#!/usr/bin/env node
// scripts/screenshot-diff.mjs — Compare two screenshot directories pixel-by-pixel.
//
// Why this exists: visual regression testing. The baseline directory
// holds the "known good" screenshots; the actual directory holds the
// current run. The diff directory gets one PNG per viewport showing
// the pixel-level differences, plus a side-by-side composite (before
// | diff | after) for human review, plus a JSON summary.
//
// Output per matching pair of files:
//   <diff-dir>/<viewport>-diff.png          — red pixels where they differ
//   <diff-dir>/<viewport>-side-by-side.png — before | diff | after, stacked
//   <diff-dir>/summary.json                — { viewport, diffPx, totalPx, diffPct }
//
// Usage:
//   node scripts/screenshot-diff.mjs <before-dir> <after-dir> <diff-dir>
//   node scripts/screenshot-diff.mjs screenshots/baseline screenshots/actual screenshots/diff
//
// Pairs are matched by filename (ignoring extension). Files that
// exist in only one side are reported as "missing" but don't fail
// the script — partial diffs are still useful (e.g. when you add a
// new viewport to the capture list and only have actuals for it).

import { readFile, readdir, mkdir, writeFile } from 'node:fs/promises';
import { resolve, join, basename, extname } from 'node:path';
import { PNG } from 'pngjs';
import pixelmatch from 'pixelmatch';

const PIXEL_TOLERANCE = 0.1; // 0..1, antialiasing noise threshold
const DIFF_RED = [255, 0, 0, 255]; // colour of the diff overlay

async function loadPng(path) {
  const buf = await readFile(path);
  return PNG.sync.read(buf);
}

function pairFiles(beforeFiles, afterFiles) {
  // Match by basename (without extension) so .png/.jpg don't matter
  // and capture-script naming changes don't break the diff.
  const strip = (f) => basename(f, extname(f));
  const beforeMap = new Map(beforeFiles.map((f) => [strip(f), f]));
  const afterMap = new Map(afterFiles.map((f) => [strip(f), f]));
  const names = new Set([...beforeMap.keys(), ...afterMap.keys()]);
  return [...names].map((name) => ({
    name,
    before: beforeMap.get(name) || null,
    after: afterMap.get(name) || null,
  }));
}

// Compose a vertical stack: before (top), diff (middle), after (bottom).
// Width is the max of the three; each is centered horizontally on a
// neutral grey gutter. Heights stack — output is the sum.
//
// Each panel has a 4px separator strip above it (and below the
// first/last) so the boundaries are visually obvious, and a
// per-panel label band at the top ("BEFORE" / "DIFF" / "AFTER").
// The label band is 32px tall with a dark background and white
// text, rendered by hand using only pngjs (no canvas / font
// dependency) — this keeps the tool dependency-light. The label
// characters are a tiny 5x7 bitmap font (see BITMAP_FONT below)
// which is just enough to read "BEFORE", "DIFF", "AFTER" and
// the viewport size.
const BITMAP_FONT = {
  // Each character is a 5x7 bitmap stored as 7 rows of 5 bits,
  // top to bottom, MSB on the left. Spaces are implicit (use
  // a blank character).
  A: [0b01110, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001],
  B: [0b11110, 0b10001, 0b10001, 0b11110, 0b10001, 0b10001, 0b11110],
  C: [0b01110, 0b10001, 0b10000, 0b10000, 0b10000, 0b10001, 0b01110],
  D: [0b11110, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b11110],
  E: [0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b11111],
  F: [0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b10000],
  G: [0b01110, 0b10001, 0b10000, 0b10111, 0b10001, 0b10001, 0b01110],
  H: [0b10001, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001],
  I: [0b01110, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b01110],
  L: [0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b11111],
  M: [0b10001, 0b11011, 0b10101, 0b10001, 0b10001, 0b10001, 0b10001],
  N: [0b10001, 0b10001, 0b10011, 0b10101, 0b11001, 0b10001, 0b10001],
  O: [0b01110, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01110],
  P: [0b11110, 0b10001, 0b10001, 0b11110, 0b10000, 0b10000, 0b10000],
  R: [0b11110, 0b10001, 0b10001, 0b11110, 0b10100, 0b10010, 0b10001],
  S: [0b01111, 0b10000, 0b10000, 0b01110, 0b00001, 0b00001, 0b11110],
  T: [0b11111, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100],
  U: [0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01110],
  V: [0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01010, 0b00100],
  W: [0b10001, 0b10001, 0b10001, 0b10001, 0b10101, 0b11011, 0b10001],
  X: [0b10001, 0b10001, 0b01010, 0b00100, 0b01010, 0b10001, 0b10001],
  Y: [0b10001, 0b10001, 0b10001, 0b01010, 0b00100, 0b00100, 0b00100],
  '0': [0b01110, 0b10001, 0b10011, 0b10101, 0b11001, 0b10001, 0b01110],
  '1': [0b00100, 0b01100, 0b00100, 0b00100, 0b00100, 0b00100, 0b01110],
  '2': [0b01110, 0b10001, 0b00001, 0b00010, 0b00100, 0b01000, 0b11111],
  '3': [0b11111, 0b00010, 0b00100, 0b00010, 0b00001, 0b10001, 0b01110],
  '4': [0b00010, 0b00110, 0b01010, 0b10010, 0b11111, 0b00010, 0b00010],
  '5': [0b11111, 0b10000, 0b11110, 0b00001, 0b00001, 0b10001, 0b01110],
  '6': [0b00110, 0b01000, 0b10000, 0b11110, 0b10001, 0b10001, 0b01110],
  '7': [0b11111, 0b00001, 0b00010, 0b00100, 0b01000, 0b01000, 0b01000],
  '8': [0b01110, 0b10001, 0b10001, 0b01110, 0b10001, 0b10001, 0b01110],
  '9': [0b01110, 0b10001, 0b10001, 0b01111, 0b00001, 0b00010, 0b01100],
  ' ': [0, 0, 0, 0, 0, 0, 0],
  '-': [0b00000, 0b00000, 0b00000, 0b01110, 0b00000, 0b00000, 0b00000],
  'x': [0b00000, 0b00000, 0b10001, 0b01010, 0b00100, 0b01010, 0b10001],
  '(': [0b00010, 0b00100, 0b01000, 0b01000, 0b01000, 0b00100, 0b00010],
  ')': [0b01000, 0b00100, 0b00010, 0b00010, 0b00010, 0b00100, 0b01000],
  '.': [0b00000, 0b00000, 0b00000, 0b00000, 0b00000, 0b01100, 0b01100],
  '%': [0b11000, 0b11001, 0b00010, 0b00100, 0b01000, 0b10011, 0b00011],
  '|': [0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100],
  '/': [0b00001, 0b00010, 0b00010, 0b00100, 0b01000, 0b01000, 0b10000],
};

// Draw text into a PNG buffer. Returns the width consumed. Uses
// a 5x7 font at 1x scale, so each character is 5px wide with
// 1px kerning (6px stride). Callers control vertical position
// via the yOff parameter. Color is RGBA.
function drawText(png, text, xStart, yStart, color) {
  const charW = 5;
  const charH = 7;
  const kerning = 1; // px between characters
  let x = xStart;
  for (const ch of text.toUpperCase()) {
    const glyph = BITMAP_FONT[ch] || BITMAP_FONT[' '];
    for (let row = 0; row < charH; row++) {
      const bits = glyph[row];
      for (let col = 0; col < charW; col++) {
        if (bits & (1 << (charW - 1 - col))) {
          const idx = ((yStart + row) * png.width + (x + col)) << 2;
          png.data[idx] = color[0];
          png.data[idx + 1] = color[1];
          png.data[idx + 2] = color[2];
          png.data[idx + 3] = color[3];
        }
      }
    }
    x += charW + kerning;
  }
  return x - xStart; // total width
}

// Text width helper (for centering calculations).
function textWidth(text) {
  const charW = 5;
  const kerning = 1;
  return text.length * (charW + kerning);
}

async function composeSideBySide(before, diff, after, viewportLabel, diffPct) {
  const w = Math.max(before.width, diff.width, after.width);
  const labelH = 32; // text band height
  const sepH = 6; // separator strip between panels
  const panelGap = labelH + sepH;
  const h =
    before.height + diff.height + after.height + panelGap * 4;
  const out = new PNG({ width: w, height: h });

  // Light grey background so the panel boundaries read clearly.
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const idx = (y * w + x) << 2;
      out.data[idx] = 240; // R
      out.data[idx + 1] = 240; // G
      out.data[idx + 2] = 240; // B
      out.data[idx + 3] = 255; // A
    }
  }

  // Paste a panel centered horizontally at the given y offset.
  function paste(src, yOffset) {
    const xOff = Math.floor((w - src.width) / 2);
    for (let y = 0; y < src.height; y++) {
      for (let x = 0; x < src.width; x++) {
        const srcIdx = (y * src.width + x) << 2;
        const dstIdx = ((y + yOffset) * w + (x + xOff)) << 2;
        out.data[dstIdx] = src.data[srcIdx];
        out.data[dstIdx + 1] = src.data[srcIdx + 1];
        out.data[dstIdx + 2] = src.data[srcIdx + 2];
        out.data[dstIdx + 3] = src.data[srcIdx + 3];
      }
    }
  }

  // Draw a label band at the given y. The band is the full width
  // of the composite, dark grey background, with the label text
  // centered horizontally.
  function drawLabel(yOff, text) {
    // Dark grey background
    for (let y = yOff; y < yOff + labelH; y++) {
      for (let x = 0; x < w; x++) {
        const idx = (y * w + x) << 2;
        out.data[idx] = 50;
        out.data[idx + 1] = 50;
        out.data[idx + 2] = 50;
        out.data[idx + 3] = 255;
      }
    }
    // White text, centered. The text uses the 5x7 bitmap font so
    // yOff + 12 is roughly the vertical center of a 32px band.
    const tw = textWidth(text);
    const tx = Math.floor((w - tw) / 2);
    drawText(out, text, tx, yOff + 12, [255, 255, 255, 255]);
  }

  // Stack: BEFORE → DIFF → AFTER with label bands and
  // separator strips between.
  let y = 0;
  // Top label band
  drawLabel(y, 'BEFORE - ' + viewportLabel);
  y += labelH;
  paste(before, y);
  y += before.height;
  // Separator + label
  drawLabel(y, 'DIFF (' + diffPct.toFixed(2) + '% PIXELS DIFFER)');
  y += labelH;
  paste(diff, y);
  y += diff.height;
  // Separator + label
  drawLabel(y, 'AFTER - ' + viewportLabel);
  y += labelH;
  paste(after, y);

  return out;
}

async function diffPair(pair, diffDir) {
  const { name, before, after } = pair;
  if (!before) {
    return { name, status: 'missing-before', diffPx: 0, totalPx: 0, diffPct: 0 };
  }
  if (!after) {
    return { name, status: 'missing-after', diffPx: 0, totalPx: 0, diffPct: 0 };
  }
  const a = await loadPng(before);
  const b = await loadPng(after);
  // pixelmatch requires equal dimensions. If the viewports changed
  // (e.g. body padding or navbar height), the actuals can be a
  // different size. Report it; don't crash.
  if (a.width !== b.width || a.height !== b.height) {
    return {
      name,
      status: 'size-mismatch',
      before: { width: a.width, height: a.height },
      after: { width: b.width, height: b.height },
      diffPx: 0,
      totalPx: a.width * a.height,
      diffPct: 0,
    };
  }
  // alpha: 0 — unchanged pixels are transparent so the diff
  // overlay sits cleanly on the side-by-side composite. With
  // the default alpha (0.1-0.4), unchanged pixels are dimmed
  // which can make the diff panel look half-empty when only
  // a small region changed.
  const diff = new PNG({ width: a.width, height: a.height });
  const diffPx = pixelmatch(
    a.data,
    b.data,
    diff.data,
    a.width,
    a.height,
    { threshold: PIXEL_TOLERANCE, alpha: 0 }
  );
  const totalPx = a.width * a.height;
  const diffPct = totalPx > 0 ? (diffPx / totalPx) * 100 : 0;
  // The diff PNG itself is the per-pixel diff (red where they differ).
  // The side-by-side adds the before/after panels for human review
  // with viewport + diff stats labels.
  await writeFile(join(diffDir, `${name}-diff.png`), PNG.sync.write(diff));
  // Use the viewport from the filename (e.g. "desktop-1920x1080"
  // → "1920X1080" for the label). Falls back to the bare name.
  const viewportLabel = (name.match(/\d+x\d+/) || [name])[0].toUpperCase();
  const composite = await composeSideBySide(a, diff, b, viewportLabel, diffPct);
  await writeFile(join(diffDir, `${name}-side-by-side.png`), PNG.sync.write(composite));
  return {
    name,
    status: 'ok',
    diffPx,
    totalPx,
    diffPct: Number(diffPct.toFixed(4)),
  };
}

async function main() {
  const beforeDir = resolve(process.argv[2]);
  const afterDir = resolve(process.argv[3]);
  const diffDir = resolve(process.argv[4]);
  if (!beforeDir || !afterDir || !diffDir) {
    console.error(
      'Usage: node scripts/screenshot-diff.mjs <before-dir> <after-dir> <diff-dir>'
    );
    process.exit(2);
  }
  await mkdir(diffDir, { recursive: true });

  const [beforeFiles, afterFiles] = await Promise.all([
    readdir(beforeDir).then((f) => f.filter((x) => /\.(png|jpe?g)$/i.test(x))),
    readdir(afterDir).then((f) => f.filter((x) => /\.(png|jpe?g)$/i.test(x))),
  ]);

  // pairFiles returns just the basenames. We rejoin with the
  // directory paths here so the per-pair helpers can stay
  // directory-agnostic.
  const pairs = pairFiles(beforeFiles, afterFiles).map((p) => ({
    ...p,
    before: p.before ? join(beforeDir, p.before) : null,
    after: p.after ? join(afterDir, p.after) : null,
  }));

  const results = [];
  for (const p of pairs) {
    const r = await diffPair(p, diffDir);
    results.push(r);
    if (r.status === 'ok') {
      console.log(
        `[diff] ${r.name}: ${r.diffPx}/${r.totalPx} px (${r.diffPct}%) differ`
      );
    } else if (r.status === 'size-mismatch') {
      console.log(
        `[diff] ${r.name}: SIZE MISMATCH before=${r.before.width}x${r.before.height} after=${r.after.width}x${r.after.height}`
      );
    } else {
      console.log(`[diff] ${r.name}: ${r.status}`);
    }
  }
  await writeFile(
    join(diffDir, 'summary.json'),
    JSON.stringify({ at: new Date().toISOString(), pairs: results }, null, 2)
  );

  // Exit code: 0 if every paired viewport is byte-identical (perfect
  // match), 1 if any differs or is missing. CI / `make` callers can
  // gate on this.
  const hasDiffs = results.some(
    (r) => r.status === 'ok'
      ? r.diffPx > 0
      : r.status === 'size-mismatch' || r.status === 'missing-after'
  );
  process.exit(hasDiffs ? 1 : 0);
}

main().catch((err) => {
  console.error('[diff] FAILED:', err.stack || err.message);
  process.exit(1);
});
