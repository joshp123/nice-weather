import { createHash } from 'node:crypto';
import { describe, expect, it, vi } from 'vitest';
import {
  ClimateDataError, WorldwideClimateClient,
  type ClimateCatalog, type ClimateField,
} from './climateClient';

const INDEX_ID = 'typical-2006-2025-preview-07';
const RELEASE = 'climate-2026-07';
const PERIOD = 'month-07';
const BLOCK_TYPE = 'application/vnd.nice-weather.climate-block';
const SCALAR_TYPE = 'application/vnd.nice-weather.score-tile';
const FIELDS: ClimateField[] = ['comfort_good', 'dry_good', 'bright_good', 'nice_good', 'nice_calm_good', 'cycling_good', 'cycling_calm_good'];
const DERIVATION = 'agera5-v2.0|daily-metrics-v1|extrema-order-v1|weather-score-v2|climate-distribution-v1|natural-earth-5.1.1-centre-land|2006-2025';

describe('WorldwideClimateClient year index', () => {
  it('calls the default browser fetch with the global receiver', async () => {
    const harness = climateHarness();
    const browserFetch = vi.fn(async function (this: typeof globalThis, input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
      if (this !== globalThis) throw new TypeError('Illegal invocation');
      return harness.fetcher(input, init);
    });
    vi.stubGlobal('fetch', browserFetch);
    try {
      await expect(new WorldwideClimateClient().loadIndex()).resolves.toMatchObject({ indexId: INDEX_ID });
      expect(browserFetch).toHaveBeenCalledTimes(3);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('is lazy and loads the exact immutable index before any month catalogue', async () => {
    const harness = climateHarness();
    expect(harness.fetcher).not.toHaveBeenCalled();

    const index = await harness.client.loadIndex();

    expect(index).toMatchObject({ version: 1, indexId: INDEX_ID, state: 'preview', months: [{ period: PERIOD, releaseId: RELEASE }] });
    expect(Object.isFrozen(index)).toBe(true);
    expect(Object.isFrozen(index.months)).toBe(true);
    expect(harness.count('/climate/current.json')).toBe(2);
    expect(harness.count(`/climate/indexes/${INDEX_ID}.json`)).toBe(1);
    expect(harness.count('/catalog.json')).toBe(0);
  });

  it('returns undefined for a missing preview month without requesting a catalogue or asset', async () => {
    const harness = climateHarness();

    await expect(harness.client.loadCatalog('month-11')).resolves.toBeUndefined();
    await expect(harness.client.loadPoint('month-11', -4.42, 36.72)).resolves.toBeUndefined();

    expect(harness.count('/catalog.json')).toBe(0);
    expect(harness.count('/points/')).toBe(0);
    expect(harness.count('/scores/')).toBe(0);
  });

  it('loads only the selected month catalogue and exposes scalar metadata without fetching a tile', async () => {
    const fixture = makeFixture(['month-03', PERIOD]);
    const harness = climateHarness(fixture);

    const catalog = await harness.client.loadCatalog(PERIOD) as ClimateCatalog;
    const again = await harness.client.loadCatalog(PERIOD);
    const tile = harness.client.findScalarTile(catalog, 'nice_good', 4, 0, 0)!;

    expect(again).toBe(catalog);
    expect(catalog).toMatchObject({
      version: 2,
      sourceQuality: {
        referenceWindowMaterialDaytimeTemperatureRepairs: 7,
        maximumReferenceWindowDaytimeAdjustmentC: 5.237,
      },
    });
    expect(Object.isFrozen(catalog.sourceQuality)).toBe(true);
    expect(harness.count(`/climate/releases/${RELEASE}/catalog.json`)).toBe(1);
    expect(harness.count('/climate/releases/climate-2026-03/catalog.json')).toBe(0);
    expect(harness.client.scalarTileUrl(catalog, tile)).toBe(`/climate/releases/${RELEASE}/scores/${PERIOD}/nice_good/4/0/0.nws`);
    expect(harness.requests.some((request) => request.includes('.nws'))).toBe(false);
  });

  it('loads exact native records from one fully verified whole-block Range', async () => {
    const harness = climateHarness();

    const malaga = await harness.client.loadPoint(PERIOD, -4.42034, 36.72016);

    expect(malaga).toEqual({
      releaseId: RELEASE, period: PERIOD, sourceCell: { latitude: 36.7, longitude: -4.4 },
      usedAdjacentCell: false,
      comfortGoodPercent: 74, dryGoodPercent: 82, brightGoodPercent: 68,
      nice: expect.objectContaining({ medianScore: 72, calmGoodPercent: 80, poorWeekPermille: 120, goodOrBetterPercent: 70, awfulPercent: 5 }),
      cycling: expect.objectContaining({ medianScore: 65, calmGoodPercent: 70, poorWeekPermille: 210, goodOrBetterPercent: 55, awfulPercent: 10 }),
    });
    expect(malaga?.nice.ratingShares).toEqual({ awful: 50, poor: 100, mixed: 150, good: 250, veryGood: 300, excellent: 150 });
    expect(harness.blocksFor(79, 54)).toBe(1);
  });

  it.each([
    ['Isla Cristina', -7.31667, 37.2, -7.4, 37.2],
    ['Islantilla', -7.23742, 37.20572, -7.2, 37.3],
    ['Venice', 12.334, 45.437, 12.3, 45.5],
    ['Faro', -7.93, 37.019, -7.9, 37.1],
    ['Brighton', -0.14, 50.828, -0.1, 50.9],
  ])('uses the nearest real adjacent cell for coastal %s', async (_name, longitude, latitude, sourceLongitude, sourceLatitude) => {
    const harness = climateHarness();

    const evidence = await harness.client.loadPoint(PERIOD, longitude, latitude, undefined, true);

    expect(evidence).toMatchObject({
      sourceCell: { longitude: sourceLongitude, latitude: sourceLatitude },
      usedAdjacentCell: true,
    });
  });

  it('prefers exact data, breaks equal-distance ties deterministically and never searches beyond the adjacent ring', async () => {
    const harness = climateHarness();

    await expect(harness.client.loadPoint(PERIOD, -4.42034, 36.72016, undefined, true)).resolves.toMatchObject({
      sourceCell: { latitude: 36.7, longitude: -4.4 }, usedAdjacentCell: false,
    });
    await expect(harness.client.loadPoint(PERIOD, 10, 10, undefined, true)).resolves.toMatchObject({
      sourceCell: { latitude: 9.9, longitude: 10 }, usedAdjacentCell: true,
    });
    await expect(harness.client.loadPoint(PERIOD, -7.31667, 37.2)).resolves.toBeUndefined();
    await expect(harness.client.loadPoint(PERIOD, 20, 0, undefined, true)).resolves.toBeUndefined();
  });

  it('keeps the deliberate global crop and exact edge mapping', async () => {
    const harness = climateHarness();

    expect((await harness.client.loadPoint(PERIOD, -179.94, 0))?.sourceCell).toEqual({ latitude: 0, longitude: -179.9 });
    expect((await harness.client.loadPoint(PERIOD, 179.94, 0))?.sourceCell).toEqual({ latitude: 0, longitude: 179.9 });
    expect((await harness.client.loadPoint(PERIOD, 539.94, 0))?.sourceCell).toEqual({ latitude: 0, longitude: 179.9 });
    await expect(harness.client.loadPoint(PERIOD, 180, 0)).resolves.toBeUndefined();
    await expect(harness.client.loadPoint(PERIOD, -180, 0)).resolves.toBeUndefined();
    expect((await harness.client.loadPoint(PERIOD, 0, 85.05))?.sourceCell).toEqual({ latitude: 85, longitude: 0 });
    expect((await harness.client.loadPoint(PERIOD, 0, -85.05))?.sourceCell).toEqual({ latitude: -85, longitude: 0 });
    await expect(harness.client.loadPoint(PERIOD, 0, 85.06)).resolves.toBeUndefined();
  });

  it('accepts a complete index only when all 12 canonical months are present', async () => {
    const complete = climateHarness(makeFixture(Array.from({ length: 12 }, (_, index) => `month-${String(index + 1).padStart(2, '0')}`), 'complete'));
    await expect(complete.client.loadIndex()).resolves.toMatchObject({ state: 'complete', months: { length: 12 } });

    const short = makeFixture([PERIOD]);
    short.index.state = 'complete';
    bindIndex(short);
    await expect(climateHarness(short).client.loadIndex()).rejects.toMatchObject({ code: 'invalid' });
  });

  it.each([
    ['legacy pointer', (fixture: Fixture) => { fixture.pointer = { releaseId: RELEASE, catalogSha256: 'a'.repeat(64) }; }],
    ['unknown pointer field', (fixture: Fixture) => { fixture.pointer.extra = true; }],
    ['unsafe index ID', (fixture: Fixture) => { fixture.pointer.indexId = '../private'; }],
    ['wrong index digest', (fixture: Fixture) => { fixture.pointer.indexSha256 = '0'.repeat(64); }],
    ['unknown index field', (fixture: Fixture) => { fixture.index.extra = true; bindIndex(fixture); }],
    ['wrong index identity', (fixture: Fixture) => { fixture.index.indexId = 'other-index'; bindIndex(fixture, false); }],
    ['unordered preview months', (fixture: Fixture) => { fixture.index.months = [fixture.index.months[0], { ...fixture.index.months[0], period: 'month-03' }]; bindIndex(fixture); }],
    ['duplicate preview month', (fixture: Fixture) => { fixture.index.months.push({ ...fixture.index.months[0] }); bindIndex(fixture); }],
    ['zero index provenance', (fixture: Fixture) => { fixture.index.provenance.sourceEvidenceSha256 = '0'.repeat(64); bindIndex(fixture); }],
  ])('rejects %s', async (_name, mutate) => {
    const fixture = makeFixture();
    mutate(fixture);
    await expect(climateHarness(fixture).client.loadIndex()).rejects.toMatchObject({ code: expect.stringMatching(/invalid|stale/) });
  });

  it.each([
    ['pointer media type', { target: 'pointer', type: 'text/plain' }],
    ['index media type', { target: 'index', type: 'text/plain' }],
    ['unsupported index encoding', { target: 'index', encoding: 'br' }],
    ['index missing length', { target: 'index', omitLength: true }],
    ['index wrong length', { target: 'index', lengthDelta: 1 }],
  ])('requires exact JSON transport metadata: %s', async (_name, mutation) => {
    const harness = climateHarness(undefined, { jsonMutation: mutation });
    await expect(harness.client.loadIndex()).rejects.toMatchObject({ code: 'invalid' });
  });

  it.each(['index', 'catalog'])('accepts browser-decoded gzip for immutable %s JSON', async (target) => {
    const harness = climateHarness(undefined, { jsonMutation: { target, encoding: ' GZip ', lengthDelta: -1 } });
    if (target === 'index') {
      await expect(harness.client.loadIndex()).resolves.toMatchObject({ indexId: INDEX_ID });
    } else {
      await expect(harness.client.loadCatalog(PERIOD)).resolves.toMatchObject({ version: 2, releaseId: RELEASE });
    }
  });

  it('accepts decoded gzip JSON without an encoded Content-Length', async () => {
    const harness = climateHarness(undefined, { jsonMutation: { target: 'index', encoding: 'gzip', omitLength: true } });
    await expect(harness.client.loadIndex()).resolves.toMatchObject({ indexId: INDEX_ID });
  });

  it('bounds the immutable index before parsing', async () => {
    const cancel = vi.fn();
    const body = new ReadableStream<Uint8Array>({ cancel });
    const fixture = makeFixture();
    const fetcher = vi.fn(async (input: RequestInfo | URL) => String(input) === '/climate/current.json'
      ? exactJSONResponse(fixture.pointer)
      : new Response(body, { headers: { 'Content-Type': 'application/json', 'Content-Length': String(64 * 1024 + 1) } }));
    await expect(new WorldwideClimateClient({ fetcher }).loadIndex()).rejects.toMatchObject({ code: 'invalid' });
    expect(cancel).toHaveBeenCalledOnce();
  });

  it('bounds the decoded body of gzip JSON independently of its encoded length', async () => {
    const fixture = makeFixture();
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === '/climate/current.json') return exactJSONResponse(fixture.pointer);
      const body = new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(new Uint8Array(64 * 1024 + 1));
          controller.close();
        },
      });
      return new Response(body, { headers: { 'Content-Type': 'application/json', 'Content-Encoding': 'gzip', 'Content-Length': '128' } });
    });
    await expect(new WorldwideClimateClient({ fetcher }).loadIndex()).rejects.toMatchObject({ code: 'invalid' });
  });

  it('rejects catalogue content not bound by the selected month reference', async () => {
    const fixture = makeFixture();
    fixture.index.months[0].catalogSha256 = '0'.repeat(64);
    bindIndex(fixture);
    await expect(climateHarness(fixture).client.loadCatalog(PERIOD)).rejects.toMatchObject({ code: 'invalid' });
  });

  it('rejects a catalogue that disagrees with its year-level provenance', async () => {
    const fixture = makeFixture();
    const month = fixture.months.get(PERIOD)!;
    month.catalog.provenance.landMaskSetSha256 = 'c'.repeat(64);
    bindCatalog(month);
    fixture.index.months[0].catalogSha256 = digest(month.catalogBytes);
    bindIndex(fixture);
    await expect(climateHarness(fixture).client.loadCatalog(PERIOD)).rejects.toMatchObject({ code: 'invalid' });
  });

  it.each([
    ['legacy catalogue version', (catalog: any) => { catalog.version = 1; }],
    ['unknown catalogue field', (catalog: any) => { catalog.extra = true; }],
    ['wrong source', (catalog: any) => { catalog.source.kind = 'native-atmosphere'; }],
    ['wrong geometry', (catalog: any) => { catalog.geometry.west = -180; }],
    ['wrong probability contract', (catalog: any) => { catalog.probability.goodMinimumScore = 50; }],
    ['malicious point path', (catalog: any) => { catalog.pointBlocks[0].path = '../private.nwc'; }],
    ['duplicate point block', (catalog: any) => { catalog.pointBlocks.splice(1, 0, { ...catalog.pointBlocks[0] }); }],
    ['missing scalar field', (catalog: any) => { catalog.scalarTiles = catalog.scalarTiles.filter((tile: any) => tile.field !== 'cycling_good'); }],
    ['missing scalar parent', (catalog: any) => { catalog.scalarTiles = catalog.scalarTiles.filter((tile: any) => !(tile.field === 'comfort_good' && tile.zoom === 3)); }],
  ])('rejects hostile selected catalogue metadata: %s', async (_name, mutate) => {
    const fixture = makeFixture();
    const month = fixture.months.get(PERIOD)!;
    mutate(month.catalog);
    bindCatalog(month);
    fixture.index.months[0].catalogSha256 = digest(month.catalogBytes);
    bindIndex(fixture);
    await expect(climateHarness(fixture).client.loadCatalog(PERIOD)).rejects.toMatchObject({ code: 'invalid' });
  });

  it.each([
    ['missing source quality', (catalog: any) => { delete catalog.sourceQuality; }],
    ['unknown source quality field', (catalog: any) => { catalog.sourceQuality.extra = true; }],
    ['fractional repair count', (catalog: any) => { catalog.sourceQuality.referenceWindowMaterialDaytimeTemperatureRepairs = 0.5; }],
    ['unsafe repair count', (catalog: any) => { catalog.sourceQuality.referenceWindowMaterialDaytimeTemperatureRepairs = Number.MAX_SAFE_INTEGER + 1; }],
    ['maximum without repairs', (catalog: any) => { catalog.sourceQuality.referenceWindowMaterialDaytimeTemperatureRepairs = 0; }],
    ['repairs without a material maximum', (catalog: any) => { catalog.sourceQuality.maximumReferenceWindowDaytimeAdjustmentC = 5; }],
    ['impossible maximum', (catalog: any) => { catalog.sourceQuality.maximumReferenceWindowDaytimeAdjustmentC = 171; }],
  ])('rejects invalid source quality: %s', async (_name, mutate) => {
    const fixture = makeFixture();
    const month = fixture.months.get(PERIOD)!;
    mutate(month.catalog);
    bindCatalog(month);
    fixture.index.months[0].catalogSha256 = digest(month.catalogBytes);
    bindIndex(fixture);
    await expect(climateHarness(fixture).client.loadCatalog(PERIOD)).rejects.toMatchObject({ code: 'invalid' });
  });

  it('accepts the exact zero-repair source quality state', async () => {
    const fixture = makeFixture();
    const month = fixture.months.get(PERIOD)!;
    month.catalog.sourceQuality = {
      referenceWindowMaterialDaytimeTemperatureRepairs: 0,
      maximumReferenceWindowDaytimeAdjustmentC: 0,
    };
    bindCatalog(month);
    fixture.index.months[0].catalogSha256 = digest(month.catalogBytes);
    bindIndex(fixture);
    await expect(climateHarness(fixture).client.loadCatalog(PERIOD)).resolves.toMatchObject({ sourceQuality: month.catalog.sourceQuality });
  });

  it('rejects a negative-zero unsigned repair count on the wire', async () => {
    const fixture = makeFixture();
    const month = fixture.months.get(PERIOD)!;
    const json = new TextDecoder().decode(month.catalogBytes).replace('"referenceWindowMaterialDaytimeTemperatureRepairs":7', '"referenceWindowMaterialDaytimeTemperatureRepairs":-0');
    month.catalogBytes = new TextEncoder().encode(json);
    fixture.index.months[0].catalogSha256 = digest(month.catalogBytes);
    bindIndex(fixture);
    await expect(climateHarness(fixture).client.loadCatalog(PERIOD)).rejects.toMatchObject({ code: 'invalid' });
  });

  it.each([
    ['HTTP 200', (response: BlockResponse) => { response.status = 200; }],
    ['wrong media type', (response: BlockResponse) => { response.headers['Content-Type'] = 'application/octet-stream'; }],
    ['content encoding', (response: BlockResponse) => { response.headers['Content-Encoding'] = 'gzip'; }],
    ['wrong content range', (response: BlockResponse) => { response.headers['Content-Range'] = 'bytes 1-2/3'; }],
    ['wrong declared length', (response: BlockResponse) => { response.headers['Content-Length'] = String(response.body.byteLength - 1); }],
    ['truncated body', (response: BlockResponse) => { response.body = response.body.subarray(0, response.body.byteLength - 1); }],
  ])('rejects hostile whole-block transport: %s', async (_name, mutate) => {
    const harness = climateHarness(undefined, { mutateBlock: mutate });
    await expect(harness.client.loadPoint(PERIOD, -4.42, 36.72)).rejects.toMatchObject({ code: 'invalid' });
  });

  it('rejects valid-looking point data changed after catalogue publication', async () => {
    const harness = climateHarness(undefined, { mutateBlock: (response) => { response.body[recordOffset(79, 54, 1267, 1756) + 4] = 73; } });
    await expect(harness.client.loadPoint(PERIOD, -4.42, 36.72)).rejects.toMatchObject({ code: 'invalid' });
  });

  it('fails closed when the pointer rotates during an index read', async () => {
    const fixture = makeFixture();
    let reads = 0;
    const harness = climateHarness(fixture, { pointer: () => {
      reads++;
      return reads === 1 ? fixture.pointer : { version: 2, indexId: 'next-index', indexSha256: 'b'.repeat(64) };
    } });
    await expect(harness.client.loadIndex()).rejects.toMatchObject({ code: 'stale' });
  });

  it('rejects the same release ID rebound through a later year index', async () => {
    const first = makeFixture();
    const second = makeFixture();
    second.index.indexId = 'typical-2006-2025-preview-next';
    const month = second.months.get(PERIOD)!;
    month.catalog.scalarTiles[0].sha256 = 'e'.repeat(64);
    bindCatalog(month);
    second.index.months[0].catalogSha256 = digest(month.catalogBytes);
    bindIndex(second);
    let active = first;
    const fetcher = vi.fn(async (input: RequestInfo | URL): Promise<Response> => fixtureResponse(active, String(input)));
    const client = new WorldwideClimateClient({ fetcher, catalogCacheSize: 1 });

    await client.loadCatalog(PERIOD);
    active = second;
    await expect(client.loadCatalog(PERIOD)).rejects.toMatchObject({ code: 'stale' });
  });

  it('rechecks a cached catalogue against a later index common contract', async () => {
    const first = makeFixture();
    const second = makeFixture();
    second.index.indexId = 'typical-2006-2025-inconsistent';
    second.index.provenance.landMaskSetSha256 = 'c'.repeat(64);
    bindIndex(second);
    let active = first;
    const fetcher = vi.fn(async (input: RequestInfo | URL): Promise<Response> => fixtureResponse(active, String(input)));
    const client = new WorldwideClimateClient({ fetcher });

    await client.loadCatalog(PERIOD);
    active = second;
    await expect(client.loadCatalog(PERIOD)).rejects.toMatchObject({ code: 'invalid' });
  });

  it('does not let two month references alias one cached release', async () => {
    const fixture = makeFixture();
    fixture.index.months = [
      { ...fixture.index.months[0], period: 'month-03' },
      fixture.index.months[0],
    ];
    bindIndex(fixture);
    const client = climateHarness(fixture).client;

    await client.loadCatalog(PERIOD);
    await expect(client.loadCatalog('month-03')).rejects.toMatchObject({ code: 'invalid' });
  });

  it('honours cancellation when the fetch implementation ignores abort', async () => {
    const fixture = makeFixture();
    let resolvePointer!: (response: Response) => void;
    let started = false;
    const fetcher = vi.fn(async (): Promise<Response> => {
      started = true;
      return new Promise((resolve) => { resolvePointer = resolve; });
    });
    const controller = new AbortController();
    const client = new WorldwideClimateClient({ fetcher });
    const pending = client.loadIndex(controller.signal);
    await vi.waitFor(() => expect(started).toBe(true));
    const reason = new DOMException('Cancelled', 'AbortError');
    controller.abort(reason);
    resolvePointer(exactJSONResponse(fixture.pointer));
    await expect(pending).rejects.toBe(reason);
    expect(fetcher).toHaveBeenCalledOnce();
  });

  it('cancels a superseded adjacent-cell read even when its fetch ignores abort', async () => {
    const base = climateHarness();
    let releaseFirst!: (value: Response) => void;
    let delayed = true;
    let firstSignal: AbortSignal | undefined;
    const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      if (delayed && String(input).includes('/points/month-07/081/053.nwc')) {
        delayed = false;
        firstSignal = init?.signal as AbortSignal;
        return new Promise((resolve) => { releaseFirst = resolve; });
      }
      return base.fetcher(input, init);
    });
    const client = new WorldwideClimateClient({ fetcher });
    const stale = client.loadPoint(PERIOD, -7.2, 40, undefined, true);
    await vi.waitFor(() => expect(firstSignal).toBeDefined());
    const current = client.loadPoint(PERIOD, 179.94, 0);
    releaseFirst(new Response('', { status: 500 }));
    await expect(stale).rejects.toMatchObject({ code: 'stale' });
    await expect(current).resolves.toMatchObject({ sourceCell: { latitude: 0, longitude: 179.9 } });
    expect(firstSignal?.aborted).toBe(true);
  });

  it('fails closed when the pointer rotates after an adjacent-cell read', async () => {
    const fixture = makeFixture();
    let reads = 0;
    const harness = climateHarness(fixture, { pointer: () => {
      reads++;
      return reads <= 2 ? fixture.pointer : { version: 2, indexId: 'next-index', indexSha256: 'b'.repeat(64) };
    } });

    await expect(harness.client.loadPoint(PERIOD, -7.31667, 37.2, undefined, true)).rejects.toMatchObject({ code: 'stale' });
  });

  it('keeps whole-block caching bounded', async () => {
    const harness = climateHarness(undefined, { blockCacheSize: 1 });
    await harness.client.loadPoint(PERIOD, -4.42, 36.72);
    await harness.client.loadPoint(PERIOD, 179.94, 0);
    await harness.client.loadPoint(PERIOD, -4.42, 36.72);
    expect(harness.blocksFor(79, 54)).toBe(2);
  });

  it('uses short product-safe errors', async () => {
    const client = new WorldwideClimateClient({ fetcher: async () => new Response('private response detail', { status: 500 }) });
    const error = await client.loadIndex().catch((reason: unknown) => reason);
    expect(error).toBeInstanceOf(ClimateDataError);
    expect(String((error as Error).message)).toBe('Climate data is not available right now.');
    expect(String((error as Error).message)).not.toMatch(/private|header|\/climate\//i);
  });
});

type MonthFixture = { releaseId: string; period: string; catalog: any; catalogBytes: Uint8Array; files: Map<string, Uint8Array> };
type Fixture = { pointer: any; index: any; indexBytes: Uint8Array; months: Map<string, MonthFixture> };
type BlockResponse = { status: number; headers: Record<string, string>; body: Uint8Array };

function climateHarness(
  fixture = makeFixture(),
  options: {
    pointer?: () => Record<string, any>;
    mutateBlock?: (response: BlockResponse) => void;
    jsonMutation?: { target: string; type?: string; encoding?: string; omitLength?: boolean; lengthDelta?: number };
    blockCacheSize?: number;
  } = {},
) {
  const requests: string[] = [];
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = String(input);
    const range = new Headers(init?.headers).get('Range');
    requests.push(range ? `${url}#${range}` : url);
    if (url === '/climate/current.json') return mutatedJSONResponse(options.pointer?.() ?? fixture.pointer, options.jsonMutation?.target === 'pointer' ? options.jsonMutation : undefined);
    if (url === `/climate/indexes/${fixture.index.indexId}.json`) return mutatedBytesResponse(fixture.indexBytes, options.jsonMutation?.target === 'index' ? options.jsonMutation : undefined);
    for (const month of fixture.months.values()) {
      if (url === `/climate/releases/${month.releaseId}/catalog.json`) return mutatedBytesResponse(month.catalogBytes, options.jsonMutation?.target === 'catalog' ? options.jsonMutation : undefined);
      const match = new RegExp(`/climate/releases/${month.releaseId}/points/${month.period}/(\\d{3})/(\\d{3})\\.nwc$`).exec(url);
      if (match) {
        const file = month.files.get(`${Number(match[1])}/${Number(match[2])}`);
        if (!file) return new Response('', { status: 404 });
        const response: BlockResponse = { status: 206, headers: { 'Content-Type': BLOCK_TYPE, 'Content-Length': String(file.byteLength), 'Content-Range': `bytes 0-${file.byteLength - 1}/${file.byteLength}` }, body: file.slice() };
        options.mutateBlock?.(response);
        return new Response(response.body as Uint8Array<ArrayBuffer>, { status: response.status, headers: response.headers });
      }
    }
    return new Response('', { status: 404 });
  });
  const client = new WorldwideClimateClient({ fetcher, blockCacheSize: options.blockCacheSize });
  return {
    client, fetcher, requests,
    count: (part: string) => requests.filter((request) => request.includes(part)).length,
    blocksFor: (latitude: number, longitude: number) => requests.filter((request) => request.includes(`/${String(latitude).padStart(3, '0')}/${String(longitude).padStart(3, '0')}.nwc#bytes=`)).length,
  };
}

function makeFixture(periods: string[] = [PERIOD], state: 'preview' | 'complete' = 'preview'): Fixture {
  const months = new Map<string, MonthFixture>();
  for (const period of periods) months.set(period, makeMonth(period));
  const index: any = {
    version: 1, indexId: state === 'complete' ? 'typical-2006-2025-complete' : INDEX_ID, state,
    referencePeriod: { firstYear: 2006, lastYear: 2025 },
    provenance: { sourceEvidenceSha256: 'a'.repeat(64), landMaskSetSha256: 'b'.repeat(64), derivationContract: DERIVATION },
    months: periods.map((period) => {
      const month = months.get(period)!;
      return { period, releaseId: month.releaseId, catalogSha256: digest(month.catalogBytes) };
    }),
  };
  const fixture: Fixture = { pointer: {}, index, indexBytes: new Uint8Array(), months };
  bindIndex(fixture);
  return fixture;
}

function makeMonth(period: string): MonthFixture {
  const monthNumber = period.slice(-2);
  const releaseId = `climate-2026-${monthNumber}`;
  const coordinates: Array<[number, number, number, number]> = [
    [56, 0, 900, 1], [56, 112, 900, 3599], [79, 54, 1267, 1756], [3, 56, 50, 1800], [109, 56, 1750, 1800],
    [79, 53, 1272, 1726], [79, 54, 1273, 1728], [84, 60, 1355, 1923], [79, 53, 1271, 1721], [88, 56, 1409, 1799],
    [62, 59, 999, 1900], [62, 59, 1001, 1900], [81, 53, 1300, 1727],
  ];
  const coordinatesByBlock = new Map<string, Array<[number, number]>>();
  for (const [latitudeBlock, longitudeBlock, latitudeIndex, longitudeIndex] of coordinates) {
    const key = `${latitudeBlock}/${longitudeBlock}`;
    const values = coordinatesByBlock.get(key) ?? [];
    values.push([latitudeIndex, longitudeIndex]);
    coordinatesByBlock.set(key, values);
  }
  const blocks = [...coordinatesByBlock.entries()].map(([key, values]) => {
    const [latitudeBlock, longitudeBlock] = key.split('/').map(Number);
    return { latitudeBlock, longitudeBlock, values };
  }).sort((left, right) => left.latitudeBlock - right.latitudeBlock || left.longitudeBlock - right.longitudeBlock);
  const files = new Map<string, Uint8Array>();
  const pointBlocks: any[] = [];
  for (const { latitudeBlock, longitudeBlock, values } of blocks) {
    const latitudeCount = latitudeBlock === 112 ? 9 : 16;
    const longitudeCount = longitudeBlock === 112 ? 16 : 32;
    const payload = new Uint8Array(latitudeCount * longitudeCount * 36);
    for (const [latitudeIndex, longitudeIndex] of values) {
      const index = (latitudeIndex - latitudeBlock * 16) * longitudeCount + longitudeIndex - longitudeBlock * 32;
      payload.set(validRecord(), index * 36);
    }
    const payloadSha256 = digest(payload);
    const header = blockHeader(releaseId, period, latitudeBlock, longitudeBlock, latitudeCount, longitudeCount, payloadSha256);
    const file = new Uint8Array(header.length + payload.length);
    file.set(header); file.set(payload, header.length);
    files.set(`${latitudeBlock}/${longitudeBlock}`, file);
    pointBlocks.push({ latitudeBlock, longitudeBlock, latitudeCount, longitudeCount, path: `points/${period}/${String(latitudeBlock).padStart(3, '0')}/${String(longitudeBlock).padStart(3, '0')}.nwc`, bytes: file.byteLength, sha256: digest(file), payloadSha256, mediaType: BLOCK_TYPE, dataRecords: values.length, noDataRecords: latitudeCount * longitudeCount - values.length });
  }
  const scalarTiles = FIELDS.flatMap((field) => Array.from({ length: 5 }, (_, zoom) => ({ field, zoom, x: 0, y: 0, path: `scores/${period}/${field}/${zoom}/0/0.nws`, bytes: 258 * 258, sha256: 'd'.repeat(64), mediaType: SCALAR_TYPE, dataTexels: 1, noDataTexels: 258 * 258 - 1 })));
  const catalog: any = {
    version: 2, releaseId, period,
    geometry: { projection: 'EPSG:4326', west: -179.95, east: 179.95, south: -85.05, north: 85.05, rows: 1701, columns: 3599, cellResolutionDegrees: 0.1, cellRegistration: 'centre' },
    source: { dataset: 'AgERA5', version: '2.0', kind: 'topography-corrected-interpolated-era5', referencePeriod: { firstYear: 2006, lastYear: 2025 }, deliveryGridDegrees: 0.1, atmosphericEvidenceKm: 30, precipitationEvidenceDegrees: 0.25, localDay: 'agera5-eight-longitude-zones-at-three-hour-offsets-not-civil-time', landMask: 'natural-earth-5.1.1-centre-on-land' },
    provenance: { sourceEvidenceSha256: 'a'.repeat(64), landMaskSetSha256: 'b'.repeat(64), derivationContract: DERIVATION },
    sourceQuality: { referenceWindowMaterialDaytimeTemperatureRepairs: 7, maximumReferenceWindowDaytimeAdjustmentC: 5.237 },
    probability: { unit: 'whole-percent', denominator: 'complete-real-days-in-reference-month', threshold: 'canonical-rating-good-or-better', goodMinimumScore: 55, noDataValue: 255, fields: [...FIELDS] },
    maximumZoom: 4, pointBlocks, scalarTiles,
  };
  const month = { releaseId, period, catalog, catalogBytes: new Uint8Array(), files };
  bindCatalog(month);
  return month;
}

function bindCatalog(month: MonthFixture): void { month.catalogBytes = new TextEncoder().encode(`${JSON.stringify(month.catalog)}\n`); }
function bindIndex(fixture: Fixture, bindPointer = true): void {
  fixture.indexBytes = new TextEncoder().encode(`${JSON.stringify(fixture.index)}\n`);
  if (bindPointer) fixture.pointer = { version: 2, indexId: fixture.index.indexId, indexSha256: digest(fixture.indexBytes) };
}

function blockHeader(releaseId: string, period: string, latitudeBlock: number, longitudeBlock: number, latitudeCount: number, longitudeCount: number, payloadSha256: string): Uint8Array {
  const header = new Uint8Array(160);
  header.set(new TextEncoder().encode('NWAG5CLM'));
  const view = new DataView(header.buffer);
  view.setUint16(8, 1, true); view.setUint16(10, 160, true); view.setUint16(12, 36, true);
  view.setUint16(14, latitudeCount, true); view.setUint16(16, longitudeCount, true); view.setUint16(18, latitudeBlock, true); view.setUint16(20, longitudeBlock, true);
  header[22] = releaseId.length; header[23] = period.length; header[24] = 1; header[25] = 1;
  header.set(new TextEncoder().encode(releaseId), 32); header.set(new TextEncoder().encode(period), 96); header.set(hexBytes(payloadSha256), 128);
  return header;
}

function validRecord(): Uint8Array {
  const record = new Uint8Array(36);
  record[0] = 1; record[1] = 74; record[2] = 82; record[3] = 68;
  encodeProfile(record.subarray(4, 20), 72, 80, 120, [50, 100, 150, 250, 300, 150]);
  encodeProfile(record.subarray(20, 36), 65, 70, 210, [100, 150, 200, 250, 200, 100]);
  return record;
}

function encodeProfile(target: Uint8Array, median: number, calm: number, poorWeek: number, shares: number[]): void {
  const view = new DataView(target.buffer, target.byteOffset, target.byteLength);
  target[0] = median; target[1] = calm; view.setUint16(2, poorWeek, true);
  shares.forEach((share, index) => view.setUint16(4 + index * 2, share, true));
}

function recordOffset(latitudeBlock: number, longitudeBlock: number, latitudeIndex: number, longitudeIndex: number): number {
  const longitudeCount = longitudeBlock === 112 ? 16 : 32;
  return 160 + ((latitudeIndex - latitudeBlock * 16) * longitudeCount + longitudeIndex - longitudeBlock * 32) * 36;
}

function fixtureResponse(fixture: Fixture, url: string): Response {
  if (url === '/climate/current.json') return exactJSONResponse(fixture.pointer);
  if (url === `/climate/indexes/${fixture.index.indexId}.json`) return exactBytesResponse(fixture.indexBytes);
  for (const month of fixture.months.values()) if (url === `/climate/releases/${month.releaseId}/catalog.json`) return exactBytesResponse(month.catalogBytes);
  return new Response('', { status: 404 });
}

function exactJSONResponse(value: unknown): Response { return exactBytesResponse(new TextEncoder().encode(JSON.stringify(value))); }
function exactBytesResponse(bytes: Uint8Array): Response { return new Response(bytes as Uint8Array<ArrayBuffer>, { headers: { 'Content-Type': 'application/json; charset=utf-8', 'Content-Length': String(bytes.byteLength) } }); }
function mutatedJSONResponse(value: unknown, mutation?: { type?: string; encoding?: string; omitLength?: boolean; lengthDelta?: number }): Response { return mutatedBytesResponse(new TextEncoder().encode(JSON.stringify(value)), mutation); }
function mutatedBytesResponse(bytes: Uint8Array, mutation?: { type?: string; encoding?: string; omitLength?: boolean; lengthDelta?: number }): Response {
  const headers: Record<string, string> = { 'Content-Type': mutation?.type ?? 'application/json', 'Content-Length': String(bytes.byteLength + (mutation?.lengthDelta ?? 0)) };
  if (mutation?.omitLength) delete headers['Content-Length'];
  if (mutation?.encoding) headers['Content-Encoding'] = mutation.encoding;
  return new Response(bytes as Uint8Array<ArrayBuffer>, { headers });
}
function digest(value: Uint8Array): string { return createHash('sha256').update(value).digest('hex'); }
function hexBytes(value: string): Uint8Array { return Uint8Array.from(value.match(/../g)!, (item) => Number.parseInt(item, 16)); }
