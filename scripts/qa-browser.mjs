import { chromium } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import { PNG } from 'pngjs';

const baseURL = process.argv[2] ?? 'http://127.0.0.1:5173/';
const screenshotDir = path.resolve('output/screenshots');
await mkdir(screenshotDir, { recursive: true });

const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, deviceScaleFactor: 1 });
const errors = [];
const tileURLs = new Set();

page.on('console', (message) => {
  if (message.type() === 'error') errors.push(message.text());
});
page.on('pageerror', (error) => errors.push(error.message));
page.on('response', (response) => {
  if (response.url().includes('/artifacts/tiles/forecast/') && response.status() === 200) {
    tileURLs.add(response.url());
  }
});

try {
  await page.goto(baseURL, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.maplibregl-canvas', { timeout: 15_000 });
  await page.waitForFunction(() => document.querySelector('.freshness')?.textContent?.includes('DWD run'), null, { timeout: 15_000 });
  await page.waitForTimeout(1_500);

  const freshness = await page.locator('.freshness').innerText();
  assertIncludes(freshness, 'DWD run');
  assertIncludes(freshness, 'Built');
  assertIncludes(freshness, 'Age');
  assertIncludes(freshness, '10:00-20:00');
  if (!/\d{4}-\d{2}-\d{2}/.test(freshness)) {
    throw new Error(`freshness block lacks an exact local date: ${freshness}`);
  }

  const horizons = await page.locator('.horizonGrid button').allTextContents();
  if (horizons.length !== 5) {
    throw new Error(`expected five forecast horizon buttons, saw ${horizons.length}: ${horizons.join(', ')}`);
  }
  if (!horizons.includes('Sat 9 May')) {
    throw new Error(`expected concrete Sat 9 May horizon label, saw: ${horizons.join(', ')}`);
  }

  const beforeDetail = await page.locator('.anchorDetail').innerText();
  await captureAndCheckMap(page, 'nice-weather-main.png');

  await page.getByRole('button', { name: 'Sat 9 May' }).click();
  await page.waitForTimeout(800);
  const afterDetail = await page.locator('.anchorDetail').innerText();
  if (afterDetail === beforeDetail) {
    throw new Error('anchor detail did not change after selecting Sat 9 May');
  }
  await captureAndCheckMap(page, 'nice-weather-sat-9-may.png');

  await page.getByRole('button', { name: 'cycling' }).click();
  await page.waitForTimeout(800);
  await captureAndCheckMap(page, 'nice-weather-cycling.png');

  if (tileURLs.size < 3) {
    throw new Error(`expected forecast tile requests for initial/horizon/profile states, saw ${tileURLs.size}`);
  }

  await page.goto('about:blank');
  await page.goto(`${baseURL.replace(/#.*$/, '')}#design-system`, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.maplibregl-canvas', { timeout: 15_000 });
  await page.waitForFunction(() => document.querySelector('.freshness')?.textContent?.includes('DWD run'), null, { timeout: 15_000 });
  await page.waitForTimeout(1_000);
  const designAnchorCount = await page.locator('.anchorRow').count();
  if (designAnchorCount !== 1) {
    throw new Error(`design-system route did not render the fixture state; expected 1 anchor row, saw ${designAnchorCount}`);
  }
  await captureAndCheckMap(page, 'nice-weather-design-system.png');

  if (errors.length > 0) {
    throw new Error(`browser console errors:\n${errors.join('\n')}`);
  }

  console.log(`qa-browser ok: ${tileURLs.size} forecast tile responses; screenshots in ${screenshotDir}`);
} finally {
  await browser.close();
}

function assertIncludes(value, expected) {
  if (!value.includes(expected)) {
    throw new Error(`expected "${expected}" in:\n${value}`);
  }
}

async function captureAndCheckMap(page, name) {
  const screenshotPath = path.join(screenshotDir, name);
  const deadline = Date.now() + 12_000;
  let lastError;
  while (Date.now() < deadline) {
    await page.screenshot({ path: screenshotPath, fullPage: true });
    try {
      await assertMapPixels(screenshotPath);
      return;
    } catch (error) {
      lastError = error;
      await page.waitForTimeout(500);
    }
  }
  throw lastError;
}

async function assertMapPixels(screenshotPath) {
  const png = PNG.sync.read(await readFile(screenshotPath));
  const maxY = Math.floor(png.height * 0.45);
  let sampled = 0;
  let colored = 0;
  const unique = new Set();
  for (let y = 0; y < maxY; y += 4) {
    for (let x = 0; x < png.width; x += 4) {
      const offset = (png.width * y + x) * 4;
      const r = png.data[offset];
      const g = png.data[offset + 1];
      const b = png.data[offset + 2];
      const a = png.data[offset + 3];
      if (a < 16) continue;
      sampled += 1;
      unique.add(`${Math.round(r / 16)},${Math.round(g / 16)},${Math.round(b / 16)}`);
      if (Math.max(r, g, b) - Math.min(r, g, b) > 24) {
        colored += 1;
      }
    }
  }
  const coloredRatio = sampled === 0 ? 0 : colored / sampled;
  if (unique.size < 24 || coloredRatio < 0.03) {
    throw new Error(`map screenshot looks blank: ${screenshotPath}, unique=${unique.size}, coloredRatio=${coloredRatio.toFixed(3)}`);
  }
}
