import { chromium } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import { PNG } from 'pngjs';

const baseURL = process.argv[2] ?? 'http://127.0.0.1:5173/';
const screenshotDir = path.resolve('output/screenshots');
await mkdir(screenshotDir, { recursive: true });

const browser = await chromium.launch({ channel: 'chrome', headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const errors = [];
const telemetryEvents = new Set();

page.on('console', (message) => {
  const match = message.text().match(/^nice-weather:([a-z-]+)/);
  if (match) telemetryEvents.add(match[1]);
  if (message.type() === 'error') errors.push(message.text());
});
page.on('pageerror', (error) => errors.push(error.message));

try {
  await page.goto(baseURL, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.controlDock .periodRail button', { timeout: 15_000 });
  await page.waitForTimeout(1_000);

  const manifest = await page.evaluate(async () => (await fetch('/manifest.json')).json());
  if (manifest.version !== 4 || manifest.productContract !== 'nice-weather-v6' || !manifest.releaseId || manifest.forecast?.days?.length !== 14 || manifest.climate?.months?.length !== 12) {
    throw new Error(`unexpected production manifest: v${manifest.version}, ${manifest.forecast?.days?.length} days, ${manifest.climate?.months?.length} months`);
  }
  const sampleWarnings = await page.locator('.sampleWarning').count();
  if ((manifest.dataStatus === 'sample') !== (sampleWarnings === 1)) {
    throw new Error(`sample warning disagrees with dataStatus=${manifest.dataStatus}`);
  }
  if (await page.locator('.periodRail button').count() !== 14) throw new Error('forecast rail must contain 14 days');
  await assertMapLibreAsset(page, manifest.releaseId, manifest.forecast.days[0].id, 'nice', 'overall', manifest.forecast.days[0].images.nice);
  await page.locator('.legend').focus();
  await page.keyboard.press('Enter');
  if (await page.locator('.lensMenu').count() !== 1) throw new Error('lens menu did not open');
  await page.getByRole('button', { name: 'Search featured cities or coordinates' }).click();
  if (await page.locator('.lensMenu').count() !== 0) throw new Error('search and lens menu must be mutually exclusive');
  await page.getByRole('button', { name: 'Close search' }).click();
  await page.locator('.legend').click();
  await page.getByRole('radio', { name: /Comfort/i }).click();
  await assertMapLibreAsset(page, manifest.releaseId, manifest.forecast.days[0].id, 'nice', 'comfort', manifest.forecast.days[0].lenses.comfort);
  await capture(page, 'nice-weather-desktop.png');

  await page.locator('.periodRail button').nth(1).click();
  await page.getByRole('button', { name: /Cycling/i }).click();
  await assertMapLibreAsset(page, manifest.releaseId, manifest.forecast.days[1].id, 'cycling', 'comfort', manifest.forecast.days[1].lenses.comfort);
  await page.getByRole('region', { name: 'Weather score map' }).focus();
  await page.keyboard.press('Enter');
  await page.waitForSelector('.pointPanel .score', { timeout: 10_000 });
  await assertText(page, '.pointPanel', 'Why this score');
  await page.locator('.scoreWhy summary').click();
  for (const label of ['Temperature', 'Minimum', 'Maximum', 'Rain', 'Wet time', 'Sun energy', 'Cloud', 'Wind', 'Gusts', 'Humidity', 'Dew point']) {
    await assertText(page, '.scoreWhy', label);
  }
  await page.locator('.legend').click();
  if (await page.locator('.pointPanel').count() !== 0 || await page.locator('.lensMenu').count() !== 1) throw new Error('lens menu must replace point details');
  await page.getByRole('button', { name: 'Search featured cities or coordinates' }).click();
  if (await page.locator('.pointPanel').count() !== 0 || await page.locator('.lensMenu').count() !== 0) throw new Error('search must replace lens and point details');
  await page.getByRole('button', { name: 'Close search' }).click();
  if (await page.locator('.pointPanel').count() !== 1) throw new Error('point details did not return after closing search');
  await page.getByRole('button', { name: 'Explain the weather score' }).click();
  if (await page.locator('.pointPanel').count() !== 0 || await page.locator('.scoringGuide').count() !== 1) throw new Error('guide must replace point details');
  await page.locator('.legend').focus();
  await page.keyboard.press('Enter');
  if (await page.locator('.scoringGuide').count() !== 0 || await page.locator('.lensMenu').count() !== 1) throw new Error('lens menu must replace the guide');
  await page.getByRole('radio', { name: /Comfort/i }).click();
  if (await page.locator('.pointPanel').count() !== 1) throw new Error('point details did not return after closing lens menu');
  await capture(page, 'nice-weather-point.png');

  await page.getByRole('button', { name: /Typical year|Typical$/i }).click();
  if (await page.locator('.periodRail button').count() !== 12) throw new Error('climate rail must contain 12 months');
  await page.locator('.periodRail button').nth(6).click();
  await assertText(page, '.forecastStatus', manifest.climate.referencePeriod);
  await assertMapLibreAsset(page, manifest.releaseId, manifest.climate.months[6].id, 'cycling', 'comfort', manifest.climate.months[6].lenses.comfort);
  await assertText(page, '.pointPanel .dataNote', `Typical month · ${manifest.climate.referencePeriod}`);
  const climateSource = await page.locator('.pointPanel .dataNote').innerText();
  if (climateSource.includes('Updated')) throw new Error(`climate source must not claim forecast freshness: ${climateSource}`);
  await capture(page, 'nice-weather-typical.png');
  await assertMapLibreCanvas(page);

  await page.setViewportSize({ width: 393, height: 852 });
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.controlDock .periodRail button', { timeout: 15_000 });
  await capture(page, 'nice-weather-phone.png');
  await page.getByRole('button', { name: 'Search featured cities or coordinates' }).click();
  await page.getByLabel('Featured cities or coordinates').fill('36.72, -4.42');
  await page.getByLabel('Featured cities or coordinates').press('Enter');
  await page.waitForSelector('.pointPanel .score', { timeout: 10_000 });
  if (await page.locator('.controlDock:visible').count() !== 0 || await page.locator('.legendCluster:visible').count() !== 0) {
    throw new Error('mobile point details must own the chrome; dock and legend remain visible');
  }
  const mobileLayout = await page.evaluate(() => ({
    bodyWidth: document.body.scrollWidth,
    viewportWidth: window.innerWidth,
    panel: document.querySelector('.pointPanel')?.getBoundingClientRect().toJSON(),
    panelScrollHeight: document.querySelector('.pointPanel')?.scrollHeight,
    panelClientHeight: document.querySelector('.pointPanel')?.clientHeight,
  }));
  if (mobileLayout.bodyWidth > mobileLayout.viewportWidth || !mobileLayout.panel || mobileLayout.panel.x < 0 || mobileLayout.panel.right > mobileLayout.viewportWidth) {
    throw new Error(`mobile point details overflow: ${JSON.stringify(mobileLayout)}`);
  }
  await page.locator('.pointPanel').evaluate((panel) => { panel.scrollTop = panel.scrollHeight; });
  await page.locator('.scoreWhy summary').click();
  await page.locator('.scoreWhy summary').scrollIntoViewIfNeeded();
  if (!await page.getByText('Weather used', { exact: true }).isVisible()) throw new Error('mobile provenance is not reachable inside point details');
  await capture(page, 'nice-weather-phone-point.png');
  await page.getByRole('button', { name: 'Close weather details' }).click();
  if (await page.locator('.controlDock:visible').count() !== 1 || await page.locator('.legendCluster:visible').count() !== 1) {
    throw new Error('mobile map controls did not return after closing details');
  }

  const unavailable = await browser.newPage({ viewport: { width: 393, height: 852 } });
  await unavailable.addInitScript(() => {
    const original = HTMLCanvasElement.prototype.getContext;
    HTMLCanvasElement.prototype.getContext = function getContext(type, ...args) {
      return typeof type === 'string' && type.startsWith('webgl') ? null : original.call(this, type, ...args);
    };
  });
  await unavailable.goto(baseURL, { waitUntil: 'domcontentloaded' });
  await unavailable.getByRole('alert').filter({ hasText: 'The map is not available' }).waitFor({ timeout: 15_000 });
  if (await unavailable.locator('.fallbackMap').count() !== 0) throw new Error('WebGL failure must not show invented fallback geography');
  await unavailable.screenshot({ path: path.join(screenshotDir, 'nice-weather-no-webgl.png') });
  await unavailable.close();

  for (const event of ['manifest-ready', 'map-ready', 'overlay-ready', 'resource-summary', 'period-changed', 'profile-changed', 'mode-changed', 'map-keyboard']) {
    if (!telemetryEvents.has(event)) throw new Error(`missing telemetry event ${event}; saw ${[...telemetryEvents].sort().join(', ')}`);
  }
  if (errors.length) throw new Error(`browser console errors:\n${errors.join('\n')}`);

  console.log(`qa-browser ok: selected MapLibre assets and UI states verified; screenshots in ${screenshotDir}`);
} finally {
  await browser.close();
}

async function assertText(page, selector, expected) {
  await page.waitForFunction(({ selector, expected }) => document.querySelector(selector)?.textContent?.includes(expected), { selector, expected }, { timeout: 10_000 });
  const value = await page.locator(selector).innerText();
  if (!value.includes(expected)) throw new Error(`expected "${expected}" in:\n${value}`);
}

async function assertMapLibreAsset(page, releaseId, period, profile, lens, image) {
  const expected = withChecksum(releaseId, image);
  try {
    await page.waitForFunction(({ assetUrl, period, lens }) => window.__niceWeatherTelemetry?.some((event) =>
      event.event === 'overlay-ready' && event.renderer === 'maplibre' && event.assetUrl === assetUrl && event.period === period && event.lens === lens
    ), { assetUrl: expected, period, lens }, { timeout: 20_000 });
  } catch (error) {
    const recent = await page.evaluate(() => window.__niceWeatherTelemetry?.slice(-20));
    throw new Error(`selected overlay did not become ready: ${JSON.stringify({ expected, period, profile, lens, recent })}`, { cause: error });
  }
  const event = await page.evaluate(({ assetUrl, period, lens }) => window.__niceWeatherTelemetry.findLast((candidate) =>
    candidate.event === 'overlay-ready' && candidate.renderer === 'maplibre' && candidate.assetUrl === assetUrl && candidate.period === period && candidate.lens === lens
  ), { assetUrl: expected, period, lens });
  if (!Number.isFinite(event?.elapsedMs) || event.elapsedMs < 0) throw new Error(`invalid selected MapLibre readiness event: ${JSON.stringify(event)}`);
}

function withChecksum(releaseId, image) {
  const hashAt = image.url.indexOf('#');
  const fragment = hashAt >= 0 ? image.url.slice(hashAt) : '';
  const withoutFragment = hashAt >= 0 ? image.url.slice(0, hashAt) : image.url;
  const queryAt = withoutFragment.indexOf('?');
  const assetPath = (queryAt >= 0 ? withoutFragment.slice(0, queryAt) : withoutFragment).replace(/^\//, '');
  const query = new URLSearchParams(queryAt >= 0 ? withoutFragment.slice(queryAt + 1) : '');
  query.set('v', image.sha256);
  return `/releases/${encodeURIComponent(releaseId)}/${assetPath}?${query.toString()}${fragment}`;
}

async function assertMapLibreCanvas(page) {
  const canvas = page.locator('.maplibregl-canvas');
  if (!await canvas.isVisible()) throw new Error('MapLibre canvas is not visible after selected overlay readiness');
  const pixels = PNG.sync.read(await canvas.screenshot());
  const colours = new Set();
  for (let y = 0; y < pixels.height; y += 12) {
    for (let x = 0; x < pixels.width; x += 12) {
      const offset = (pixels.width * y + x) * 4;
      colours.add(`${pixels.data[offset]},${pixels.data[offset + 1]},${pixels.data[offset + 2]}`);
    }
  }
  if (colours.size < 24) throw new Error(`MapLibre canvas lacks rendered pixel variation: ${colours.size} colours`);
}

async function capture(page, name) {
  const screenshotPath = path.join(screenshotDir, name);
  await page.screenshot({ path: screenshotPath, fullPage: true });
  const png = PNG.sync.read(await readFile(screenshotPath));
  const colours = new Set();
  for (let y = 0; y < png.height; y += 8) {
    for (let x = 0; x < png.width; x += 8) {
      const offset = (png.width * y + x) * 4;
      colours.add(`${png.data[offset]},${png.data[offset + 1]},${png.data[offset + 2]}`);
    }
  }
  if (colours.size < 32) throw new Error(`screenshot looks blank: ${screenshotPath}`);
}
