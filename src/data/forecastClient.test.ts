import { createHash } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it, vi } from 'vitest';
import {
  WorldwideForecastClient,
  forecastTierForPoint,
  type DailyForecastCatalog,
  type DayForecastProduct,
  type ForecastField,
  type ForecastScalarTile,
} from './forecastClient';

const fields: readonly ForecastField[] = ['thermal', 'dryness', 'sun', 'nice_score', 'nice_wind', 'cycling_score', 'cycling_wind'];
const zeroSHA = '0'.repeat(64);
const land = {
  authority: 'Natural Earth' as const, dataset: 'ne_10m_land' as const, version: '5.1.1' as const,
  sourceArchiveSha256: 'a'.repeat(64), maskSha256: 'b'.repeat(64), derivationContract: 'web-mercator-all-touched-v1' as const,
};

describe('WorldwideForecastClient', () => {
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
    if (!currentIndexPath || !existsSync(currentIndexPath) || JSON.parse(readFileSync(currentIndexPath, 'utf8')).productContract !== 'nice-weather-forecast-run-v3') return;
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

  it('chooses one declared tier before colour and never falls through a broken detail field', () => {
    const global = catalog('global', 3, [{ zoom: 3, x: 0, y: 0, omitted: false }]);
    const detail = catalog('europe-detail', 5, [{ zoom: 5, x: 16, y: 10, omitted: false }]);
    const product: DayForecastProduct = { indexId: 'run-test', date: '2026-07-22', tiers: [detail, global] };
    const client = new WorldwideForecastClient();

    expect(forecastTierForPoint(product, 10, 50).tier).toBe('europe-detail');
    expect(forecastTierForPoint(product, 10, 50, false).tier).toBe('global');
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
});

function catalog(tier: 'global' | 'europe-detail', zoom: 3 | 5, scalarCoverage: DailyForecastCatalog['scalarCoverage']): DailyForecastCatalog {
  const x = tier === 'global' ? 0 : 16;
  const y = tier === 'global' ? 0 : 10;
  const scalarTiles: ForecastScalarTile[] = fields.map((field) => ({
    field, zoom, x, y,
    path: `${field}/${zoom}/${x}/${y}.nws`, bytes: 258 * 258, sha256: zeroSHA,
  }));
  return {
    version: 2,
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
    pointBlocks: [], displayPoints: [], scalarCoverage, scalarTiles,
    root: `/forecast/${tier}`,
  };
}

async function snapshotFixture(landIdentity: Record<string, unknown> = land): Promise<Map<string, Uint8Array>> {
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
  const daily = {
    version: 2, releaseId: 'run-test', localDate: dates[0], provider: 'ecmwf-ifs', tier: 'global',
    grid: { projection: 'EPSG:4326', west: -180, east: 180, south: -90, north: 90, rows: 720, columns: 1440, cellDegrees: .25, cellRegistration: 'cell-centre' },
    evidence: { product: 'ifs-control', runUtc: '2026-07-31T00:00:00Z', nativeResolutionKm: 28, sourceEvidenceSha256: zeroSHA },
    land: landIdentity, scalarMaxZoom: 3, scores: dailyScores, coverage: {}, pointBlocks: [],
    displayPoints: [{ zoom: 3, x: 0, y: 0, path: 'display/3/0/0.nwt', bytes: 192 + 256 * 256 * 8, sha256: zeroSHA, payloadSha256: zeroSHA, mediaType: 'application/vnd.nice-weather.display-point-tile' }],
    scalarCoverage: [{ zoom: 3, x: 0, y: 0, omitted: false, dataRecords: 1, noDataRecords: 0 }],
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
    productContract: 'nice-weather-forecast-range-v1',
    releaseId: 'run-test', period: 'next-14-days', dates,
    source: { provider: 'ecmwf-ifs', tier: 'global', product: 'ifs-control', runUtc: '2026-07-31T00:00:00Z', nativeResolutionKm: 28, staleAfterHours: 18 },
    grid: { projection: 'EPSG:3857-XYZ', zoom: 3, tileSize: 256, rows: 2048, columns: 2048 },
    scores: { ...dailyScores, mediaType: 'application/vnd.nice-weather.score-tile' },
    pointBlockLayout: { rows: 64, columns: 64, headerBytes: 256, recordBytes: 44, mediaType: 'application/vnd.nice-weather.forecast-range-block' },
    inputs: tiers.map(({ date, catalogPath, catalogBytes: bytes, catalogSha256: sha256 }) => ({ date, path: catalogPath.replace('runs/run-test/', ''), bytes, sha256 })),
    coverage: {}, pointBlocks: [],
    scalarCoverage: [{ zoom: 3, x: 0, y: 0, omitted: false, dataRecords: 1, noDataRecords: 0 }],
    scalarTiles: fields.map((field) => ({ field, zoom: 3, x: 0, y: 0, path: `${field}/3/0/0.nws`, bytes: 258 * 258, sha256: zeroSHA, dataTexels: 1, noDataTexels: 0 })),
  };
  const rangeBytes = json(range);
  const index = {
    version: 2, productContract: 'nice-weather-forecast-run-v3', indexId: 'run-test', firstDate: dates[0], lastDate: dates[13],
    tierSelection: { method: 'choose-one-complete-tier-before-colour', priority: ['europe-detail', 'global'], blend: false },
    days: dates.map((date, index) => ({ date, tiers: [tiers[index]] })),
    range: { period: 'next-14-days', provider: 'ecmwf-ifs', tier: 'global', product: 'ifs-control', runUtc: '2026-07-31T00:00:00Z', nativeResolutionKm: 28, staleAfterHours: 18, catalogPath: 'runs/run-test/forecast-ranges/next-14-days/global/catalog.json', catalogBytes: rangeBytes.byteLength, catalogSha256: sha(rangeBytes) },
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

function json(value: unknown): Uint8Array { return new TextEncoder().encode(`${JSON.stringify(value)}\n`); }
function sha(bytes: Uint8Array): string { return createHash('sha256').update(bytes).digest('hex'); }
function arrayBuffer(bytes: Uint8Array): ArrayBuffer { return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer; }
