import { createHash } from 'node:crypto';
import { describe, expect, it, vi } from 'vitest';
import {
  ClimateDataError, WorldwideClimateClient,
  type ClimateCatalog, type ClimateField,
} from './climateClient';

const RELEASE = 'climate-2026-07';
const PERIOD = 'month-07';
const BLOCK_TYPE = 'application/vnd.nice-weather.climate-block';
const SCALAR_TYPE = 'application/vnd.nice-weather.score-tile';
const FIELDS: ClimateField[] = ['comfort_good', 'dry_good', 'bright_good', 'nice_good', 'nice_calm_good', 'cycling_good', 'cycling_calm_good'];

describe('WorldwideClimateClient', () => {
  it('does not make a request until a catalogue or point is called', () => {
    const fetcher = vi.fn();
    new WorldwideClimateClient({ fetcher });
    expect(fetcher).not.toHaveBeenCalled();
  });

  it('loads Málaga and Amsterdam from verified blocks at the exact native record offsets', async () => {
    const harness = await climateHarness();

    const malaga = await harness.client.loadPoint(PERIOD, -4.42034, 36.72016);
    const amsterdam = await harness.client.loadPoint(PERIOD, 4.9041, 52.3676);

    expect(malaga).toEqual({
      releaseId: RELEASE,
      period: PERIOD,
      sourceCell: { latitude: 36.7, longitude: -4.4 },
      comfortGoodPercent: 74,
      dryGoodPercent: 82,
      brightGoodPercent: 68,
      nice: expect.objectContaining({ medianScore: 72, calmGoodPercent: 80, poorWeekPermille: 120, goodOrBetterPermille: 700, goodOrBetterPercent: 70, awfulPermille: 50, awfulPercent: 5 }),
      cycling: expect.objectContaining({ medianScore: 65, calmGoodPercent: 70, poorWeekPermille: 210, goodOrBetterPermille: 550, goodOrBetterPercent: 55, awfulPermille: 100, awfulPercent: 10 }),
    });
    expect(amsterdam?.sourceCell).toEqual({ latitude: 52.4, longitude: 4.9 });
    expect(malaga?.nice.ratingShares).toEqual({ awful: 50, poor: 100, mixed: 150, good: 250, veryGood: 300, excellent: 150 });

    expect(harness.blocksFor(79, 54)).toBe(1);
    expect(harness.blocksFor(89, 57)).toBe(1);
  });

  it('wraps the dateline once and honours the deliberate cropped edge', async () => {
    const harness = await climateHarness();

    expect((await harness.client.loadPoint(PERIOD, -179.94, 0))?.sourceCell).toEqual({ latitude: 0, longitude: -179.9 });
    expect((await harness.client.loadPoint(PERIOD, 179.94, 0))?.sourceCell).toEqual({ latitude: 0, longitude: 179.9 });
    expect((await harness.client.loadPoint(PERIOD, 539.94, 0))?.sourceCell).toEqual({ latitude: 0, longitude: 179.9 });
    await expect(harness.client.loadPoint(PERIOD, 180, 0)).resolves.toBeUndefined();
    await expect(harness.client.loadPoint(PERIOD, -180, 0)).resolves.toBeUndefined();
    await expect(harness.client.loadPoint(PERIOD, 0, 85.06)).resolves.toBeUndefined();
    await expect(harness.client.loadPoint(PERIOD, 0, -85.06)).resolves.toBeUndefined();
  });

  it('maps both exact latitude crop edges to their one native source row', async () => {
    const harness = await climateHarness();
    expect((await harness.client.loadPoint(PERIOD, 0, 85.05))?.sourceCell).toEqual({ latitude: 85, longitude: 0 });
    expect((await harness.client.loadPoint(PERIOD, 0, -85.05))?.sourceCell).toEqual({ latitude: -85, longitude: 0 });
  });

  it('returns no data for an omitted block or an exact zero record', async () => {
    const harness = await climateHarness();

    await expect(harness.client.loadPoint(PERIOD, 30, 30)).resolves.toBeUndefined();
    await expect(harness.client.loadPoint(PERIOD, -4.31, 36.71)).resolves.toBeUndefined();
    expect(harness.requests.filter((request) => request.includes('/points/')).length).toBe(1);
  });

  it('caches the immutable catalogue and exposes checked scalar lookup without fetching a tile', async () => {
    const harness = await climateHarness();

    const first = await harness.client.loadCatalog();
    const second = await harness.client.loadCatalog();
    const tile = harness.client.findScalarTile(first, 'nice_good', 4, 0, 0)!;

    expect(first).toBe(second);
    expect(Object.isFrozen(first)).toBe(true);
    expect(Object.isFrozen(first.pointBlocks)).toBe(true);
    expect(harness.client.scalarTileUrl(first, tile)).toBe(`/climate/releases/${RELEASE}/scores/${PERIOD}/nice_good/4/0/0.nws`);
    expect(harness.client.findScalarTile(first, 'nice_good', 5, 0, 0)).toBeUndefined();
    expect(harness.count('/catalog.json')).toBe(1);
    expect(harness.requests.some((request) => request.endsWith('.nws'))).toBe(false);
  });

  it.each([
    ['unknown pointer field', (fixture: Fixture) => { (fixture.pointer as Record<string, unknown>).extra = true; }],
    ['unsafe release', (fixture: Fixture) => { fixture.pointer.releaseId = '../private'; }],
    ['unknown catalogue version', (fixture: Fixture) => { fixture.catalog.version = 2; }],
    ['unknown catalogue field', (fixture: Fixture) => { fixture.catalog.secret = 'do not show'; }],
    ['wrong release', (fixture: Fixture) => { fixture.catalog.releaseId = 'other-release'; }],
    ['wrong source', (fixture: Fixture) => { fixture.catalog.source.kind = 'native-atmosphere'; }],
    ['wrong geometry', (fixture: Fixture) => { fixture.catalog.geometry.west = -180; }],
    ['wrong probability contract', (fixture: Fixture) => { fixture.catalog.probability.goodMinimumScore = 50; }],
    ['wrong field order', (fixture: Fixture) => { fixture.catalog.probability.fields.reverse(); }],
    ['zero provenance', (fixture: Fixture) => { fixture.catalog.provenance.sourceEvidenceSha256 = '0'.repeat(64); }],
    ['malicious point path', (fixture: Fixture) => { fixture.catalog.pointBlocks[0].path = '../private.nwc'; }],
    ['wrong block bytes', (fixture: Fixture) => { fixture.catalog.pointBlocks[0].bytes++; }],
    ['block outside crop', (fixture: Fixture) => { const block = fixture.catalog.pointBlocks[0]; block.latitudeBlock = 0; block.latitudeCount = 16; block.path = `points/${PERIOD}/000/${String(block.longitudeBlock).padStart(3, '0')}.nwc`; }],
    ['duplicate block', (fixture: Fixture) => { fixture.catalog.pointBlocks.splice(1, 0, { ...fixture.catalog.pointBlocks[0] }); }],
    ['wrong scalar path', (fixture: Fixture) => { fixture.catalog.scalarTiles[0].path = '/private.nws'; }],
    ['missing scalar field', (fixture: Fixture) => { fixture.catalog.scalarTiles = fixture.catalog.scalarTiles.filter((tile: any) => tile.field !== 'cycling_good'); }],
    ['missing scalar parent', (fixture: Fixture) => { fixture.catalog.scalarTiles = fixture.catalog.scalarTiles.filter((tile: any) => !(tile.field === 'comfort_good' && tile.zoom === 3)); }],
  ])('rejects %s', async (_name, mutate) => {
    const fixture = makeFixture();
    mutate(fixture);
    bindCatalog(fixture);
    const harness = await climateHarness(fixture);

    await expect(harness.client.loadCatalog()).rejects.toMatchObject({ code: expect.stringMatching(/invalid|stale/) });
  });

  it('rejects a catalogue whose bytes do not match the pointer digest', async () => {
    const fixture = makeFixture();
    fixture.pointer.catalogSha256 = '0'.repeat(64);
    const harness = await climateHarness(fixture);
    await expect(harness.client.loadCatalog()).rejects.toMatchObject({ code: 'invalid' });
  });

  it('bounds pointer and catalogue responses before parsing', async () => {
    const cancelPointer = vi.fn();
    const pointerBody = new ReadableStream<Uint8Array>({ cancel: cancelPointer });
    const pointerClient = new WorldwideClimateClient({ fetcher: async () => new Response(pointerBody, { headers: { 'Content-Type': 'application/json', 'Content-Length': '1025' } }) });
    await expect(pointerClient.loadCatalog()).rejects.toMatchObject({ code: 'invalid' });
    expect(cancelPointer).toHaveBeenCalledOnce();

    const fixture = makeFixture();
    const catalogClient = new WorldwideClimateClient({ fetcher: async (input) => {
      if (String(input) === '/climate/current.json') return jsonResponse(fixture.pointer);
      const body = new ReadableStream<Uint8Array>();
      return new Response(body, { headers: { 'Content-Type': 'application/json', 'Content-Length': String(32 * 1024 * 1024 + 1) } });
    } });
    await expect(catalogClient.loadCatalog()).rejects.toMatchObject({ code: 'invalid' });
  });

  it('requires JSON media types before parsing pointer and catalogue bodies', async () => {
    const fixture = makeFixture();
    const pointerClient = new WorldwideClimateClient({ fetcher: async () => new Response(JSON.stringify(fixture.pointer), { headers: { 'Content-Type': 'text/plain' } }) });
    await expect(pointerClient.loadCatalog()).rejects.toMatchObject({ code: 'invalid' });

    const catalogClient = new WorldwideClimateClient({ fetcher: async (input) => String(input) === '/climate/current.json'
      ? jsonResponse(fixture.pointer)
      : new Response(fixture.catalogBytes, { headers: { 'Content-Type': 'text/plain' } }) });
    await expect(catalogClient.loadCatalog()).rejects.toMatchObject({ code: 'invalid' });
  });

  it.each([
    ['HTTP 200', (response: BlockResponse) => { response.status = 200; }],
    ['wrong media type', (response: BlockResponse) => { response.headers['Content-Type'] = 'application/octet-stream'; }],
    ['content encoding', (response: BlockResponse) => { response.headers['Content-Encoding'] = 'gzip'; }],
    ['wrong content range', (response: BlockResponse) => { response.headers['Content-Range'] = 'bytes 1-2/3'; }],
    ['wrong declared length', (response: BlockResponse) => { response.headers['Content-Length'] = String(response.body.byteLength - 1); }],
    ['truncated body', (response: BlockResponse) => { response.body = response.body.subarray(0, response.body.byteLength - 1); }],
    ['oversized body', (response: BlockResponse) => { response.body = new Uint8Array([...response.body, 0]); }],
  ])('rejects hostile full-block response: %s', async (_name, mutate) => {
    const harness = await climateHarness(undefined, { mutateBlock: mutate });
    await expect(harness.client.loadPoint(PERIOD, -4.42, 36.72)).rejects.toMatchObject({ code: 'invalid' });
  });

  it.each([
    ['magic', (header: Uint8Array) => { header[0] = 0; }],
    ['version', (header: Uint8Array) => { header[8] = 2; }],
    ['release', (header: Uint8Array) => { header[32] ^= 1; }],
    ['period', (header: Uint8Array) => { header[96] ^= 1; }],
    ['block', (header: Uint8Array) => { header[18] ^= 1; }],
    ['reserved byte', (header: Uint8Array) => { header[26] = 1; }],
    ['identity padding', (header: Uint8Array) => { header[64] = 1; }],
    ['payload digest', (header: Uint8Array) => { header[159] ^= 1; }],
  ])('rejects a hostile block header: %s', async (_name, mutate) => {
    const harness = await climateHarness(undefined, { mutateBlock: (block) => mutate(block.body.subarray(0, 160)) });
    await expect(harness.client.loadPoint(PERIOD, -4.42, 36.72)).rejects.toMatchObject({ code: 'invalid' });
  });

  it('rejects valid-looking evidence changed after publication', async () => {
    const start = recordOffset(79, 54, 1267, 1756);
    const harness = await climateHarness(undefined, { mutateBlock: (block) => { block.body[start + 4] = 73; } });
    await expect(harness.client.loadPoint(PERIOD, -4.42, 36.72)).rejects.toMatchObject({ code: 'invalid' });
  });

  it.each([
    ['unknown flags', (record: Uint8Array) => { record[0] = 3; }],
    ['nonzero no-data', (record: Uint8Array) => { record.fill(0); record[1] = 1; }],
    ['shared probability', (record: Uint8Array) => { record[1] = 101; }],
    ['median score', (record: Uint8Array) => { record[4] = 101; }],
    ['poor-week probability', (record: Uint8Array) => { new DataView(record.buffer, record.byteOffset, record.byteLength).setUint16(6, 1001, true); }],
    ['rating share', (record: Uint8Array) => { new DataView(record.buffer, record.byteOffset, record.byteLength).setUint16(8, 1001, true); }],
    ['rating total', (record: Uint8Array) => { new DataView(record.buffer, record.byteOffset, record.byteLength).setUint16(8, 49, true); }],
    ['Cycling calm exceeds Nice', (record: Uint8Array) => { record[21] = 81; }],
  ])('rejects a hostile 36-byte record: %s', async (_name, mutate) => {
    const fixture = makeFixture();
    authenticateRecordMutation(fixture, mutate);
    const harness = await climateHarness(fixture);
    await expect(harness.client.loadPoint(PERIOD, -4.42, 36.72)).rejects.toMatchObject({ code: 'invalid' });
  });

  it('rejects a changed pointer before returning point evidence', async () => {
    const fixture = makeFixture();
    let pointerReads = 0;
    const harness = await climateHarness(fixture, { pointer: () => {
      pointerReads++;
      return pointerReads === 2 ? { releaseId: 'next-release', catalogSha256: 'b'.repeat(64) } : fixture.pointer;
    } });
    await expect(harness.client.loadPoint(PERIOD, -4.42, 36.72)).rejects.toMatchObject({ code: 'stale' });
  });

  it('rejects the same release ID being rebound to changed catalogue content', async () => {
    const first = makeFixture();
    const second = makeFixture();
    second.catalog.provenance.landMaskSetSha256 = 'c'.repeat(64);
    bindCatalog(second);
    let pointer = first.pointer;
    const harness = await climateHarness(first, { pointer: () => pointer });
    await harness.client.loadCatalog();
    pointer = second.pointer;
    await expect(harness.client.loadCatalog()).rejects.toMatchObject({ code: 'stale' });
  });

  it('cancels stale point reads even when the fetch implementation ignores abort', async () => {
    const fixture = makeFixture();
    const base = await climateHarness(fixture);
    let releaseFirst!: (value: Response) => void;
    let delayed = true;
    let firstSignal: AbortSignal | undefined;
    const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      if (delayed && String(input).includes('/points/')) {
        delayed = false;
        firstSignal = init?.signal as AbortSignal;
        return new Promise((resolve) => { releaseFirst = resolve; });
      }
      return base.fetcher(input, init);
    });
    const client = new WorldwideClimateClient({ fetcher });

    const stale = client.loadPoint(PERIOD, -4.42, 36.72);
    await vi.waitFor(() => expect(firstSignal).toBeDefined());
    const current = client.loadPoint(PERIOD, 4.9, 52.37);
    releaseFirst(new Response('', { status: 500 }));

    await expect(stale).rejects.toMatchObject({ code: 'stale' });
    await expect(current).resolves.toMatchObject({ sourceCell: { latitude: 52.4, longitude: 4.9 } });
    expect(firstSignal?.aborted).toBe(true);
  });

  it('honours catalogue cancellation when a fetch implementation ignores abort', async () => {
    const fixture = makeFixture();
    let resolvePointer!: (response: Response) => void;
    let started = false;
    const fetcher = vi.fn(async (): Promise<Response> => {
      started = true;
      return new Promise((resolve) => { resolvePointer = resolve; });
    });
    const controller = new AbortController();
    const client = new WorldwideClimateClient({ fetcher });

    const pending = client.loadCatalog(controller.signal);
    await vi.waitFor(() => expect(started).toBe(true));
    const reason = new DOMException('Cancelled', 'AbortError');
    controller.abort(reason);
    resolvePointer(jsonResponse(fixture.pointer));

    await expect(pending).rejects.toBe(reason);
    expect(fetcher).toHaveBeenCalledOnce();
  });

  it('keeps the verified-block cache bounded and refetches its least recent entry', async () => {
    const harness = await climateHarness(undefined, { blockCacheSize: 1 });
    await harness.client.loadPoint(PERIOD, -4.42, 36.72);
    await harness.client.loadPoint(PERIOD, 4.9, 52.37);
    await harness.client.loadPoint(PERIOD, -4.42, 36.72);
    expect(harness.blocksFor(79, 54)).toBe(2);
  });

  it('remembers immutable release identity after its catalogue leaves the LRU cache', async () => {
    const first = makeFixture();
    const other = makeFixture();
    other.catalog.releaseId = 'climate-2026-08';
    other.pointer.releaseId = 'climate-2026-08';
    bindCatalog(other);
    const rebound = makeFixture();
    rebound.catalog.provenance.landMaskSetSha256 = 'c'.repeat(64);
    bindCatalog(rebound);
    let active = first;
    const fetcher = vi.fn(async (input: RequestInfo | URL): Promise<Response> => {
      const url = String(input);
      if (url === '/climate/current.json') return jsonResponse(active.pointer);
      if (url === `/climate/releases/${active.pointer.releaseId}/catalog.json`) return new Response(active.catalogBytes, { headers: { 'Content-Type': 'application/json' } });
      return new Response('', { status: 404 });
    });
    const client = new WorldwideClimateClient({ fetcher, catalogCacheSize: 1 });

    await client.loadCatalog();
    active = other;
    await client.loadCatalog();
    active = rebound;
    await expect(client.loadCatalog()).rejects.toMatchObject({ code: 'stale' });
  });

  it('uses short product-safe errors without paths, headers, or response content', async () => {
    const client = new WorldwideClimateClient({ fetcher: async () => new Response('private response detail', { status: 500 }) });
    const error = await client.loadCatalog().catch((reason: unknown) => reason);
    expect(error).toBeInstanceOf(ClimateDataError);
    expect(String((error as Error).message)).toBe('Climate data is not available right now.');
    expect(String((error as Error).message)).not.toMatch(/private|header|\/climate\//i);
  });
});

type Fixture = ReturnType<typeof makeFixture>;
type BlockResponse = { status: number; headers: Record<string, string>; body: Uint8Array };

async function climateHarness(
  fixture = makeFixture(),
  options: {
    mutateBlock?: (response: BlockResponse) => void;
    pointer?: () => Record<string, any>;
    blockCacheSize?: number;
  } = {},
) {
  const requests: string[] = [];
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = String(input);
    const range = new Headers(init?.headers).get('Range');
    requests.push(range ? `${url}#${range}` : url);
    if (url === '/climate/current.json') return jsonResponse(options.pointer?.() ?? fixture.pointer);
    if (url === `/climate/releases/${fixture.pointer.releaseId}/catalog.json`) return new Response(fixture.catalogBytes, { headers: { 'Content-Type': 'application/json; charset=utf-8' } });
    const match = /\/points\/month-07\/(\d{3})\/(\d{3})\.nwc$/.exec(url);
    if (match) {
      const latitudeBlock = Number(match[1]);
      const longitudeBlock = Number(match[2]);
      const file = fixture.files.get(`${latitudeBlock}/${longitudeBlock}`);
      if (!file) return new Response('', { status: 404 });
      const response: BlockResponse = {
        status: 206,
        headers: {
          'Content-Type': BLOCK_TYPE,
          'Content-Length': String(file.byteLength),
          'Content-Range': `bytes 0-${file.byteLength - 1}/${file.byteLength}`,
        },
        body: file.slice(),
      };
      options.mutateBlock?.(response);
      return new Response(response.body as Uint8Array<ArrayBuffer>, { status: response.status, headers: response.headers });
    }
    return new Response('', { status: 404 });
  });
  const client = new WorldwideClimateClient({ fetcher, blockCacheSize: options.blockCacheSize });
  return {
    client, fetcher, requests,
    count: (suffix: string) => requests.filter((request) => request.includes(suffix)).length,
    blocksFor: (latitude: number, longitude: number) => {
      const path = `/${String(latitude).padStart(3, '0')}/${String(longitude).padStart(3, '0')}.nwc`;
      const bytes = fixture.files.get(`${latitude}/${longitude}`)!.byteLength;
      return requests.filter((request) => request.endsWith(`${path}#bytes=0-${bytes - 1}`)).length;
    },
    blockResponse: (latitude: number, longitude: number) => {
      const file = fixture.files.get(`${latitude}/${longitude}`)!;
      return new Response(file as Uint8Array<ArrayBuffer>, { status: 206, headers: { 'Content-Type': BLOCK_TYPE, 'Content-Length': String(file.byteLength), 'Content-Range': `bytes 0-${file.byteLength - 1}/${file.byteLength}` } });
    },
  };
}

function makeFixture() {
  const coordinates: Array<[number, number, number, number]> = [
    [56, 0, 900, 1],
    [56, 112, 900, 3599],
    [79, 54, 1267, 1756],
    [89, 57, 1424, 1849],
    [3, 56, 50, 1800],
    [109, 56, 1750, 1800],
  ];
  coordinates.sort((left, right) => left[0] - right[0] || left[1] - right[1]);
  const files = new Map<string, Uint8Array>();
  const pointBlocks: any[] = [];
  for (const [latitudeBlock, longitudeBlock, latitudeIndex, longitudeIndex] of coordinates) {
    const latitudeCount = latitudeBlock === 112 ? 9 : 16;
    const longitudeCount = longitudeBlock === 112 ? 16 : 32;
    const payload = new Uint8Array(latitudeCount * longitudeCount * 36);
    const index = (latitudeIndex - latitudeBlock * 16) * longitudeCount + longitudeIndex - longitudeBlock * 32;
    payload.set(validRecord(), index * 36);
    const payloadSha256 = digest(payload);
    const header = blockHeader(latitudeBlock, longitudeBlock, latitudeCount, longitudeCount, payloadSha256);
    const file = new Uint8Array(header.length + payload.length);
    file.set(header);
    file.set(payload, header.length);
    files.set(`${latitudeBlock}/${longitudeBlock}`, file);
    pointBlocks.push({
      latitudeBlock, longitudeBlock, latitudeCount, longitudeCount,
      path: `points/${PERIOD}/${String(latitudeBlock).padStart(3, '0')}/${String(longitudeBlock).padStart(3, '0')}.nwc`,
      bytes: file.byteLength, sha256: digest(file), payloadSha256, mediaType: BLOCK_TYPE,
      dataRecords: 1, noDataRecords: latitudeCount * longitudeCount - 1,
    });
  }
  const scalarTiles = FIELDS.flatMap((field) => Array.from({ length: 5 }, (_, zoom) => ({
    field, zoom, x: 0, y: 0,
    path: `scores/${PERIOD}/${field}/${zoom}/0/0.nws`,
    bytes: 258 * 258, sha256: 'd'.repeat(64), mediaType: SCALAR_TYPE,
    dataTexels: 1, noDataTexels: 258 * 258 - 1,
  })));
  const catalog: any = {
    version: 1,
    releaseId: RELEASE,
    period: PERIOD,
    geometry: { projection: 'EPSG:4326', west: -179.95, east: 179.95, south: -85.05, north: 85.05, rows: 1701, columns: 3599, cellResolutionDegrees: 0.1, cellRegistration: 'centre' },
    source: {
      dataset: 'AgERA5', version: '2.0', kind: 'topography-corrected-interpolated-era5',
      referencePeriod: { firstYear: 2006, lastYear: 2025 }, deliveryGridDegrees: 0.1,
      atmosphericEvidenceKm: 30, precipitationEvidenceDegrees: 0.25,
      localDay: 'agera5-eight-longitude-zones-at-three-hour-offsets-not-civil-time', landMask: 'natural-earth-5.1.1-centre-on-land',
    },
    provenance: {
      sourceEvidenceSha256: 'a'.repeat(64), landMaskSetSha256: 'b'.repeat(64),
      derivationContract: 'agera5-v2.0|daily-metrics-v1|extrema-order-v1|weather-score-v1|climate-distribution-v1|natural-earth-5.1.1-centre-land|2006-2025',
    },
    probability: {
      unit: 'whole-percent', denominator: 'complete-real-days-in-reference-month', threshold: 'canonical-rating-good-or-better',
      goodMinimumScore: 55, noDataValue: 255, fields: [...FIELDS],
    },
    maximumZoom: 4,
    pointBlocks,
    scalarTiles,
  };
  const fixture = { catalog, catalogBytes: new Uint8Array(), pointer: { releaseId: RELEASE, catalogSha256: '' }, files };
  bindCatalog(fixture);
  return fixture;
}

function bindCatalog(fixture: { catalog: any; catalogBytes: Uint8Array; pointer: Record<string, any> }): void {
  fixture.catalogBytes = new TextEncoder().encode(`${JSON.stringify(fixture.catalog)}\n`);
  fixture.pointer = { ...fixture.pointer, releaseId: fixture.pointer.releaseId ?? fixture.catalog.releaseId, catalogSha256: digest(fixture.catalogBytes) };
}

function blockHeader(latitudeBlock: number, longitudeBlock: number, latitudeCount: number, longitudeCount: number, payloadSha256: string): Uint8Array {
  const header = new Uint8Array(160);
  header.set(new TextEncoder().encode('NWAG5CLM'));
  const view = new DataView(header.buffer);
  view.setUint16(8, 1, true);
  view.setUint16(10, 160, true);
  view.setUint16(12, 36, true);
  view.setUint16(14, latitudeCount, true);
  view.setUint16(16, longitudeCount, true);
  view.setUint16(18, latitudeBlock, true);
  view.setUint16(20, longitudeBlock, true);
  header[22] = RELEASE.length;
  header[23] = PERIOD.length;
  header[24] = 1;
  header[25] = 1;
  header.set(new TextEncoder().encode(RELEASE), 32);
  header.set(new TextEncoder().encode(PERIOD), 96);
  header.set(hexBytes(payloadSha256), 128);
  return header;
}

function authenticateRecordMutation(fixture: Fixture, mutate: (record: Uint8Array) => void): void {
  const file = fixture.files.get('79/54')!;
  const start = recordOffset(79, 54, 1267, 1756);
  mutate(file.subarray(start, start + 36));
  const payloadSha256 = digest(file.subarray(160));
  file.set(hexBytes(payloadSha256), 128);
  const asset = fixture.catalog.pointBlocks.find((block: any) => block.latitudeBlock === 79 && block.longitudeBlock === 54)!;
  asset.payloadSha256 = payloadSha256;
  asset.sha256 = digest(file);
  bindCatalog(fixture);
}

function validRecord(): Uint8Array {
  const record = new Uint8Array(36);
  record[0] = 1;
  record[1] = 74;
  record[2] = 82;
  record[3] = 68;
  encodeProfile(record.subarray(4, 20), 72, 80, 120, [50, 100, 150, 250, 300, 150]);
  encodeProfile(record.subarray(20, 36), 65, 70, 210, [100, 150, 200, 250, 200, 100]);
  return record;
}

function encodeProfile(target: Uint8Array, median: number, calm: number, poorWeek: number, shares: number[]): void {
  const view = new DataView(target.buffer, target.byteOffset, target.byteLength);
  target[0] = median;
  target[1] = calm;
  view.setUint16(2, poorWeek, true);
  shares.forEach((share, index) => view.setUint16(4 + index * 2, share, true));
}

function recordOffset(latitudeBlock: number, longitudeBlock: number, latitudeIndex: number, longitudeIndex: number): number {
  const longitudeCount = longitudeBlock === 112 ? 16 : 32;
  return 160 + ((latitudeIndex - latitudeBlock * 16) * longitudeCount + longitudeIndex - longitudeBlock * 32) * 36;
}

function digest(value: Uint8Array): string { return createHash('sha256').update(value).digest('hex'); }
function hexBytes(value: string): Uint8Array { return Uint8Array.from(value.match(/../g)!, (item) => Number.parseInt(item, 16)); }
function jsonResponse(value: unknown): Response { return new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } }); }
