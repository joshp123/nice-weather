import { createHash } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it, vi } from 'vitest';
import {
  WorldwideForecastClient,
  dailySummary,
  forecastTierForPoint,
  forecastRunStatus,
  freshnessLabel,
  isStale,
  type DailyForecastCatalog,
  type DayForecastProduct,
  type ForecastDailyPointBlock,
  type ForecastField,
  type ForecastPointBlock,
  type ForecastScalarTile,
} from './forecastClient';
import { WEATHER_PRESENTATION } from '../weatherPresentation';

const fields: readonly ForecastField[] = ['thermal', 'dryness', 'sun', 'nice_score', 'nice_wind', 'cycling_score', 'cycling_wind'];
const zeroSHA = '0'.repeat(64);
const land = {
  authority: 'Natural Earth' as const, dataset: 'ne_10m_land' as const, version: '5.1.1' as const,
  sourceArchiveSha256: 'a'.repeat(64), maskSha256: 'b'.repeat(64), derivationContract: 'web-mercator-all-touched-v1' as const,
};

describe('WorldwideForecastClient', () => {
  it('reports forecast freshness from the active run timestamp', () => {
    const now = new Date('2026-07-18T12:00:00Z');
    expect(freshnessLabel('2026-07-18T11:30:00Z', now)).toBe('Updated 30 minutes ago');
    expect(freshnessLabel('2026-07-17T12:00:00Z', now)).toBe('Updated 1 day ago');
    expect(isStale('2026-07-18T06:00:00Z', 12, now)).toBe(false);
    expect(forecastRunStatus('2026-07-17T06:00:00Z', 12, now)).toEqual({ stale: true, label: 'Forecast is out of date' });
  });

  it('decodes the canonical sky phrase from the native daily summary', () => {
    expect(dailySummary(25, 0)).toBe('25°C · dry · overcast · light wind');
    expect(dailySummary(25, 4)).toBe('25°C · dry · mixed cloud · light wind');
    expect(dailySummary(12.4, 8)).toBe('12°C · dry · bright · light wind');
    expect(dailySummary(25, 12)).toBe('25°C · dry · sunny · light wind');
    expect(dailySummary(27, 3)).toBe('27°C · wet · overcast · light wind');
    expect(dailySummary(-0.5, 0)).toBe('-1°C · dry · overcast · light wind');
  });

  it('calls the default browser fetch with the global receiver', async () => {
    const fixture = await snapshotFixture();
    const browserFetch = vi.fn(async function (this: typeof globalThis, input: RequestInfo | URL): Promise<Response> {
      if (this !== globalThis) throw new TypeError('Illegal invocation');
      const bytes = fixture.get(String(input));
      if (!bytes) return new Response(null, { status: 404 });
      return new Response(arrayBuffer(bytes), { status: 200, headers: { 'Content-Type': 'application/json', 'Content-Length': String(bytes.byteLength) } });
    });
    vi.stubGlobal('fetch', browserFetch);
    try {
      await expect(new WorldwideForecastClient().loadSnapshot()).resolves.toMatchObject({ index: { indexId: 'run-test' } });
      expect(browserFetch).toHaveBeenCalledTimes(4);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('loads the real immutable pointer, index and range catalogue without eager payload reads', async () => {
    if (!existsSync('artifacts/forecast/current.json')) return;
    const pointer = JSON.parse(readFileSync('artifacts/forecast/current.json', 'utf8')) as { current?: { path?: string } };
    const currentIndexPath = pointer.current?.path && resolve('artifacts/forecast', pointer.current.path);
    if (!currentIndexPath || !existsSync(currentIndexPath)) return;
    const currentIndex = JSON.parse(readFileSync(currentIndexPath, 'utf8'));
    if (currentIndex.productContract !== 'nice-weather-forecast-run-v4') return;
    const currentDailyPath = resolve('artifacts/forecast', currentIndex.days?.[0]?.tiers?.at(-1)?.catalogPath ?? '');
    if (!existsSync(currentDailyPath) || JSON.parse(readFileSync(currentDailyPath, 'utf8')).version !== 3) return;
    const paths: string[] = [];
    const client = new WorldwideForecastClient(async (input) => {
      const url = String(input); paths.push(url);
      const path = resolve('artifacts', url.replace(/^\//, ''));
      const bytes = readFileSync(path);
      return new Response(arrayBuffer(bytes), { status: 200, headers: { 'Content-Type': 'application/json', 'Content-Length': String(bytes.byteLength) } });
    });

    const snapshot = await client.loadSnapshot();
    const firstDay = await client.loadDayCatalog(snapshot, snapshot.index.firstDate);

    expect(snapshot.index.days).toHaveLength(14);
    expect(snapshot.index.days.filter(({ tiers }) => tiers.some(({ tier }) => tier === 'europe-detail'))).toHaveLength(5);
    expect(snapshot.range.dates).toEqual(snapshot.index.days.map(({ date }) => date));
    expect(firstDay.tiers.map(({ tier }) => tier)).toEqual(['europe-detail', 'global']);
    expect(firstDay.tiers.every(({ dailyPointBlocks }) => dailyPointBlocks.length > 0)).toBe(true);
    expect(paths.filter((path) => path === '/forecast/current.json')).toHaveLength(3);
    expect(paths.every((path) => path.endsWith('.json'))).toBe(true);
  });

  it('accepts the browser live shape for gzip JSON and loads no eager payloads', async () => {
    const fixture = await snapshotFixture();
    const calls: string[] = [];
    const client = new WorldwideForecastClient(async (input) => {
      const path = String(input); calls.push(path);
      const bytes = fixture.get(path);
      if (!bytes) return new Response(null, { status: 404 });
      const immutable = path !== '/forecast/current.json';
      return new Response(arrayBuffer(bytes), { status: 200, headers: immutable
        ? { 'Content-Type': 'application/json', 'Content-Encoding': 'gzip' }
        : { 'Content-Type': 'application/json', 'Content-Length': String(bytes.byteLength) } });
    });

    const snapshot = await client.loadSnapshot();

    expect(snapshot.index.days).toHaveLength(14);
    expect(calls).toEqual([
      '/forecast/current.json',
      '/forecast/runs/run-test/index.json',
      '/forecast/runs/run-test/forecast-ranges/next-14-days/global/catalog.json',
      '/forecast/current.json',
    ]);
  });

  it('reads the previous immutable freshness contract but rejects mixed metadata', async () => {
    await expect(new WorldwideForecastClient(fetchFixture(await snapshotFixture(land, 18, 18))).loadSnapshot()).resolves.toMatchObject({
      index: { range: { staleAfterHours: 18 } },
      range: { source: { staleAfterHours: 36 } },
    });
    await expect(new WorldwideForecastClient(fetchFixture(await snapshotFixture(land, 18, 36))).loadSnapshot()).rejects.toMatchObject({ code: 'invalid' });
    await expect(new WorldwideForecastClient(fetchFixture(await snapshotFixture(land, 36, 18))).loadSnapshot()).rejects.toMatchObject({ code: 'invalid' });
  });

  it('requires exact Natural Earth source and mask provenance on every daily catalogue', async () => {
    const load = async (identity = land) => {
      const fixture = await snapshotFixture(identity);
      const client = new WorldwideForecastClient(async (input) => {
        const bytes = fixture.get(String(input));
        return bytes ? new Response(arrayBuffer(bytes), { status: 200, headers: { 'Content-Type': 'application/json', 'Content-Length': String(bytes.byteLength) } }) : new Response(null, { status: 404 });
      });
      const snapshot = await client.loadSnapshot();
      return client.loadDayCatalog(snapshot, snapshot.index.firstDate);
    };

    await expect(load()).resolves.toMatchObject({ tiers: [{ land }] });
    await expect(load({ ...land, maskSha256: 'A'.repeat(64) })).rejects.toMatchObject({ code: 'invalid' });
    await expect(load({ ...land, derivationContract: 'nearest-land-v1' as typeof land.derivationContract })).rejects.toMatchObject({ code: 'invalid' });
  });

  it('rejects daily blocks whose record coverage does not exactly match the scalar plane', async () => {
    const load = async (options: Readonly<{ pointDataRecords?: number; extraPointParent?: boolean }>) => {
      const client = new WorldwideForecastClient(fetchFixture(await snapshotFixture(land, 36, 36, options)));
      const snapshot = await client.loadSnapshot();
      return client.loadDayCatalog(snapshot, snapshot.index.firstDate);
    };

    await expect(load({ pointDataRecords: 2 })).rejects.toMatchObject({ code: 'invalid' });
    await expect(load({ extraPointParent: true })).rejects.toMatchObject({ code: 'invalid' });
  });

  it('decodes facts and both profile judgements from one dense daily block', async () => {
    const point = denseDailyPointBlock(16, 16);
    const global = {
      ...catalog('global', 3, [{ zoom: 3, x: 4, y: 4, omitted: false, dataRecords: 1, noDataRecords: 65_535 }], [pointBlock(5, 11)]),
      dailyPointBlocks: [point.asset],
    };
    const product: DayForecastProduct = { indexId: 'run-test', date: '2026-07-22', tiers: [global] };
    const pointer = json({ version: 1, current: { indexId: 'run-test', path: 'runs/run-test/index.json', sha256: zeroSHA } });
    const calls: { url: string; range?: string }[] = [];
    const client = new WorldwideForecastClient(async (input, init) => {
      const url = String(input); calls.push({ url, range: new Headers(init?.headers).get('Range') ?? undefined });
      if (url === '/forecast/current.json') return new Response(arrayBuffer(pointer), { status: 200, headers: { 'Content-Type': 'application/json', 'Content-Length': String(pointer.byteLength) } });
      if (url === `/forecast/global/${point.asset.path}`) return new Response(arrayBuffer(point.bytes), { status: 206, headers: {
        'Content-Type': point.asset.mediaType,
        'Content-Length': String(point.bytes.byteLength),
        'Content-Range': `bytes 0-${point.bytes.byteLength - 1}/${point.bytes.byteLength}`,
      } });
      return new Response(null, { status: 404 });
    });
    const presentation = { legend: WEATHER_PRESENTATION.legend, generatedAt: '2026-07-22T06:00:00Z' };

    const nice = await client.loadDailyPoint(product, 0, 0, 'nice', presentation);
    expect(nice).toMatchObject({
      score: 88, category: 'Excellent', summary: '24°C · dry · sunny · light wind',
      components: { comfort: { value: 86 }, dryness: { value: 94 }, sun: { value: 91 }, wind: { value: 72 } },
      metrics: { temperatureC: 24, minTempC: 18, maxTempC: 28, rainMm: 0, windKmh: 12, dewPointC: 13, gustKmh: 20, solarEnergyMJm2: 18, cloudPct: 20, humidityPct: 48, wetTimeFraction: 0 },
    });
    const cycling = await client.loadDailyPoint(product, 0, 0, 'cycling', presentation);
    expect(cycling).toMatchObject({ score: 76, category: 'Very good', components: { wind: { value: 64 } } });
    expect(calls.filter(({ url }) => url.endsWith('.nwt'))).toEqual([{ url: `/forecast/global/${point.asset.path}`, range: `bytes=0-${point.bytes.byteLength - 1}` }]);
    expect(calls.some(({ url }) => url.endsWith('.nwf'))).toBe(false);
  });

  it('chooses one declared tier before colour and never falls through a broken detail field', () => {
    const global = catalog('global', 3, [{ zoom: 3, x: 0, y: 0, omitted: false, dataRecords: 1, noDataRecords: 65_535 }]);
    const detail = catalog('europe-detail', 5, [{ zoom: 5, x: 16, y: 10, omitted: false, dataRecords: 1, noDataRecords: 65_535 }], [pointBlock(5, 8)]);
    const product: DayForecastProduct = { indexId: 'run-test', date: '2026-07-22', tiers: [detail, global] };
    const client = new WorldwideForecastClient();

    expect(forecastTierForPoint(product, 10, 50).tier).toBe('europe-detail');
    expect(forecastTierForPoint(product, -74, 40.7).tier).toBe('global');
    expect(client.sourceZoomFor(product, 1.35)).toBe(3);
    expect(client.sourceZoomFor(product, 4)).toBe(5);
    expect(client.scalarSourceForOutputTile(product, 'nice_score', { z: 5, x: 16, y: 10 })?.url).toContain('/europe-detail/');
    expect(client.scalarSourceForOutputTile(product, 'nice_score', { z: 5, x: 0, y: 0 })).toMatchObject({
      url: '/forecast/global/nice_score/3/0/0.nws',
      textureBounds: [0, 0, 0.25, 0.25],
    });

    const incompleteDetail = { ...detail, scalarTiles: detail.scalarTiles.filter(({ field }) => field !== 'nice_score') };
    const incomplete: DayForecastProduct = { ...product, tiers: [incompleteDetail, global] };
    expect(client.scalarSourceForOutputTile(incomplete, 'nice_score', { z: 5, x: 16, y: 10 })).toBeUndefined();
  });

  it('does not select detail for coordinate bleed in a boundary tile', () => {
    const global = catalog('global', 3, [{ zoom: 3, x: 0, y: 0, omitted: false, dataRecords: 1, noDataRecords: 65_535 }]);
    const detail = catalog('europe-detail', 5, [{ zoom: 5, x: 13, y: 10, omitted: false, dataRecords: 1, noDataRecords: 65_535 }], [pointBlock(5, 0)]);
    const product: DayForecastProduct = { indexId: 'run-test', date: '2026-07-22', tiers: [detail, global] };

    expect(forecastTierForPoint(product, -23.99, 50).tier).toBe('europe-detail');
    expect(forecastTierForPoint(product, -24.01, 50).tier).toBe('global');
  });

  it('falls back to global when the detail native point block is missing or omitted', () => {
    const global = catalog('global', 3, [{ zoom: 3, x: 0, y: 0, omitted: false, dataRecords: 1, noDataRecords: 65_535 }]);
    const coverage = [{ zoom: 5, x: 16, y: 10, omitted: false, dataRecords: 1, noDataRecords: 65_535 }] as const;
    const missing = catalog('europe-detail', 5, coverage, []);
    const omitted = catalog('europe-detail', 5, coverage, [pointBlock(5, 8, true)]);

    expect(forecastTierForPoint({ indexId: 'run-test', date: '2026-07-22', tiers: [missing, global] }, 10, 50).tier).toBe('global');
    expect(forecastTierForPoint({ indexId: 'run-test', date: '2026-07-22', tiers: [omitted, global] }, 10, 50).tier).toBe('global');
  });
});

function catalog(tier: 'global' | 'europe-detail', zoom: 3 | 5, scalarCoverage: DailyForecastCatalog['scalarCoverage'], pointBlocks: readonly ForecastPointBlock[] = []): DailyForecastCatalog {
  const x = scalarCoverage[0]?.x ?? 0;
  const y = scalarCoverage[0]?.y ?? 0;
  const scalarTiles: ForecastScalarTile[] = fields.map((field) => ({
    field, zoom, x, y,
    path: `${field}/${zoom}/${x}/${y}.nws`, bytes: 258 * 258, sha256: zeroSHA,
  }));
  return {
    version: 3,
    releaseId: 'run-test',
    localDate: '2026-07-22',
    provider: tier === 'global' ? 'ecmwf-ifs' : 'dwd-icon-eu',
    tier,
    grid: tier === 'global'
      ? { west: -180, east: 180, south: -90, north: 90, rows: 720, columns: 1440 }
      : { west: -24, east: 45, south: 29, north: 71.5, rows: 680, columns: 1104 },
    evidence: { product: 'test', runUtc: '2026-07-22T00:00:00Z', nativeResolutionKm: tier === 'global' ? 28 : 7, sourceEvidenceSha256: zeroSHA },
    land,
    scalarMaxZoom: zoom,
    pointBlocks, dailyPointBlocks: [], scalarCoverage, scalarTiles,
    root: `/forecast/${tier}`,
  };
}

function pointBlock(row: number, column: number, omitted = false): ForecastPointBlock {
  return {
    row, column, rows: 64, columns: 64, omitted, mediaType: 'application/vnd.nice-weather.forecast-block',
    ...(omitted ? {} : { path: `blocks/${row}-${column}.nwf`, bytes: 123_200, sha256: zeroSHA, payloadSha256: zeroSHA }),
  };
}

function denseDailyPointBlock(x: number, y: number): { asset: ForecastDailyPointBlock; bytes: Uint8Array } {
  const payload = new Uint8Array(64 * 64 * 30);
  const record = payload.subarray(0, 30);
  const view = new DataView(record.buffer, record.byteOffset, record.byteLength);
  view.setInt16(0, 240, true);
  view.setInt16(2, 180, true);
  view.setInt16(4, 280, true);
  view.setInt16(6, 130, true);
  view.setUint16(8, 0, true);
  view.setUint16(10, 120, true);
  view.setUint16(12, 200, true);
  view.setUint16(14, 1800, true);
  view.setUint16(16, 0, true);
  record[18] = 20;
  record[19] = 48;
  record[20] = 0x7f;
  record.set([86, 94, 91, 88, 72, 76, 64, 12], 22);
  const payloadSHA = sha(payload);
  const header = new Uint8Array(192);
  const headerView = new DataView(header.buffer);
  header.set(new TextEncoder().encode('NWFXYZPT'), 0);
  headerView.setUint16(8, 2, true);
  headerView.setUint16(10, 192, true);
  headerView.setUint16(12, 30, true);
  headerView.setUint16(14, 64, true);
  header[16] = 3;
  header[17] = 1;
  headerView.setUint32(18, x, true);
  headerView.setUint32(22, y, true);
  const release = new TextEncoder().encode('run-test'), period = new TextEncoder().encode('2026-07-22'), tier = new TextEncoder().encode('global');
  header[26] = release.byteLength;
  header[27] = period.byteLength;
  header[28] = tier.byteLength;
  header.set(release, 32);
  header.set(period, 96);
  header.set(tier, 128);
  header.set(Buffer.from(payloadSHA, 'hex'), 160);
  const bytes = new Uint8Array(header.byteLength + payload.byteLength);
  bytes.set(header);
  bytes.set(payload, header.byteLength);
  const asset = {
    zoom: 3, x, y, path: `points/2026-07-22/global/3/${x}/${y}.nwt`, bytes: bytes.byteLength,
    sha256: sha(bytes), payloadSha256: payloadSHA, mediaType: 'application/vnd.nice-weather.daily-point-block',
    dataRecords: 1, noDataRecords: 4095,
  } satisfies ForecastDailyPointBlock;
  return { asset, bytes };
}

async function snapshotFixture(landIdentity: Record<string, unknown> = land, indexStaleAfterHours: 18 | 36 = 36, rangeStaleAfterHours: 18 | 36 = indexStaleAfterHours, options: Readonly<{ pointDataRecords?: number; extraPointParent?: boolean }> = {}): Promise<Map<string, Uint8Array>> {
  const dates = Array.from({ length: 14 }, (_, index) => `2026-08-${String(index + 1).padStart(2, '0')}`);
  const dailyScores = {
    unit: 'canonical-score-0-100', noDataValue: 255,
    profiles: ['nice', 'cycling'],
    fields: [
      { id: 'thermal', profile: 'shared' }, { id: 'dryness', profile: 'shared' }, { id: 'sun', profile: 'shared' },
      { id: 'nice_score', profile: 'nice' }, { id: 'nice_wind', profile: 'nice' },
      { id: 'cycling_score', profile: 'cycling' }, { id: 'cycling_wind', profile: 'cycling' },
    ],
  };
  const pointDataRecords = options.pointDataRecords ?? 1;
  const dailyPointBlocks = [{ zoom: 3, x: 0, y: 0, path: `points/${dates[0]}/global/3/0/0.nwt`, bytes: 192 + 64 * 64 * 30, sha256: zeroSHA, payloadSha256: zeroSHA, mediaType: 'application/vnd.nice-weather.daily-point-block', dataRecords: pointDataRecords, noDataRecords: 4096 - pointDataRecords }];
  if (options.extraPointParent) dailyPointBlocks.push({ ...dailyPointBlocks[0], x: 4, path: `points/${dates[0]}/global/3/4/0.nwt` });
  const daily = {
    version: 3, releaseId: 'run-test', localDate: dates[0], provider: 'ecmwf-ifs', tier: 'global',
    grid: { projection: 'EPSG:4326', west: -180, east: 180, south: -90, north: 90, rows: 720, columns: 1440, cellDegrees: .25, cellRegistration: 'cell-centre' },
    evidence: { product: 'ifs-control', runUtc: '2026-07-31T00:00:00Z', nativeResolutionKm: 28, sourceEvidenceSha256: zeroSHA },
    land: landIdentity, scalarMaxZoom: 3, scores: dailyScores, coverage: {}, pointBlocks: [],
    dailyPointBlocks,
    scalarCoverage: [{ zoom: 3, x: 0, y: 0, omitted: false, dataRecords: 1, noDataRecords: 65_535 }],
    scalarTiles: fields.map((field) => ({ field, zoom: 3, x: 0, y: 0, path: `${field}/3/0/0.nws`, bytes: 258 * 258, sha256: zeroSHA, dataTexels: 1, noDataTexels: 0 })),
  };
  const dailyBytes = json(daily);
  const tiers = dates.map((date) => ({
    date,
    provider: 'ecmwf-ifs',
    tier: 'global',
    product: 'ifs-control',
    runUtc: '2026-07-31T00:00:00Z',
    nativeResolutionKm: 28,
    sourceEvidenceSha256: zeroSHA,
    catalogPath: `runs/run-test/forecast-days/${date}/global/catalog.json`,
    catalogBytes: date === dates[0] ? dailyBytes.byteLength : 100,
    catalogSha256: date === dates[0] ? sha(dailyBytes) : zeroSHA,
  }));
  const range = {
    version: 1,
    productContract: 'nice-weather-forecast-range-v2',
    releaseId: 'run-test', period: 'next-14-days', dates,
    source: { provider: 'ecmwf-ifs', tier: 'global', product: 'ifs-control', runUtc: '2026-07-31T00:00:00Z', nativeResolutionKm: 28, staleAfterHours: rangeStaleAfterHours },
    grid: { projection: 'EPSG:3857-XYZ', zoom: 3, tileSize: 256, rows: 2048, columns: 2048 },
    scores: { ...dailyScores, mediaType: 'application/vnd.nice-weather.score-tile' },
    pointBlockLayout: { rows: 64, columns: 64, headerBytes: 256, recordBytes: 44, mediaType: 'application/vnd.nice-weather.forecast-range-block' },
    inputs: tiers.map(({ date, catalogPath, catalogBytes: bytes, catalogSha256: sha256 }) => ({ date, path: catalogPath.replace('runs/run-test/', ''), bytes, sha256 })),
    coverage: {}, pointBlocks: [],
    scalarCoverage: [{ zoom: 3, x: 0, y: 0, omitted: false, dataRecords: 1, noDataRecords: 65_535 }],
    scalarTiles: fields.map((field) => ({ field, zoom: 3, x: 0, y: 0, path: `${field}/3/0/0.nws`, bytes: 258 * 258, sha256: zeroSHA, dataTexels: 1, noDataTexels: 0 })),
  };
  const rangeBytes = json(range);
  const index = {
    version: 2, productContract: 'nice-weather-forecast-run-v4', indexId: 'run-test', firstDate: dates[0], lastDate: dates[13],
    tierSelection: { method: 'choose-one-complete-tier-before-colour', priority: ['europe-detail', 'global'], blend: false },
    days: dates.map((date, index) => ({ date, tiers: [tiers[index]] })),
    range: { period: 'next-14-days', provider: 'ecmwf-ifs', tier: 'global', product: 'ifs-control', runUtc: '2026-07-31T00:00:00Z', nativeResolutionKm: 28, staleAfterHours: indexStaleAfterHours, catalogPath: 'runs/run-test/forecast-ranges/next-14-days/global/catalog.json', catalogBytes: rangeBytes.byteLength, catalogSha256: sha(rangeBytes) },
  };
  const indexBytes = json(index);
  const pointerBytes = json({ version: 1, current: { indexId: 'run-test', path: 'runs/run-test/index.json', sha256: sha(indexBytes) } });
  return new Map([
    ['/forecast/current.json', pointerBytes],
    ['/forecast/runs/run-test/index.json', indexBytes],
    ['/forecast/runs/run-test/forecast-ranges/next-14-days/global/catalog.json', rangeBytes],
    [`/forecast/runs/run-test/forecast-days/${dates[0]}/global/catalog.json`, dailyBytes],
  ]);
}

function fetchFixture(fixture: Map<string, Uint8Array>): typeof fetch {
  return async (input) => {
    const bytes = fixture.get(String(input));
    if (!bytes) return new Response(null, { status: 404 });
    return new Response(arrayBuffer(bytes), { status: 200, headers: { 'Content-Type': 'application/json', 'Content-Length': String(bytes.byteLength) } });
  };
}

function json(value: unknown): Uint8Array { return new TextEncoder().encode(`${JSON.stringify(value)}\n`); }
function sha(bytes: Uint8Array): string { return createHash('sha256').update(bytes).digest('hex'); }
function arrayBuffer(bytes: Uint8Array): ArrayBuffer { return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer; }
