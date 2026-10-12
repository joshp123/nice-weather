import { describe, expect, it } from 'vitest';
import { WorldwideClimateClient } from './climateClient';

const sha = (letter: string) => letter.repeat(64);
const recordVector = hexBytes('0148514044537d00320064009600fa002c019600363a77016400c800fa00fa0096003200c9ffb6003d01640008014e00a1007b005400380144114f2a3d6c0200');
const fields = ['comfort_good', 'dry_good', 'clear_sky_good', 'nice_good', 'nice_calm_good', 'cycling_good', 'cycling_calm_good'];

describe('WorldwideClimateClient ERA5 contract', () => {
  it('loads pointer v2, an exact ERA5 index and its checksum-bound catalog', async () => {
    const fixture = await publication();
    const client = new WorldwideClimateClient({ fetcher: fixture.fetcher });
    const index = await client.loadIndex();
    expect(index.version).toBe(2);
    expect(index.source.dataset).toBe('ERA5');
    expect(index.geometry).toMatchObject({ rows: 721, columns: 1440, cellResolutionDegrees: 0.25, cellRegistration: 'exact-era5-grid-point-centres' });
    expect(index.probability.fields).toContain('clear_sky_good');
    expect(index.probability.fields).not.toContain('bright_good');
    const catalog = await client.loadCatalog('month-11');
    expect(catalog?.source.localDay).toContain('IANA civil local daytime');
    expect(catalog?.pointRecordBytes).toBe(64);
    expect(fixture.calls.filter((call) => call === '/climate/current.json')).toHaveLength(5);
  });

  it('reads the complete year at one coastal source cell with two pointer checks and bounded concurrency', async () => {
    const fixture = await publication();
    let activeRequests = 0;
    let maximumRequests = 0;
    const fetcher: typeof fetch = async (input, init) => {
      activeRequests++;
      maximumRequests = Math.max(maximumRequests, activeRequests);
      try {
        await new Promise((resolve) => setTimeout(resolve, 1));
        return await fixture.fetcher(input, init);
      } finally { activeRequests--; }
    };
    const months = await new WorldwideClimateClient({ fetcher }).loadYearPoint(-179.75, 0, undefined, true);
    expect(months?.map((month) => month.period)).toEqual(Array.from({ length: 12 }, (_, month) => `month-${String(month + 1).padStart(2, '0')}`));
    expect(months?.every((month) => month.sourceCell.latitude === 0 && month.sourceCell.longitude === -180 && month.usedAdjacentCell)).toBe(true);
    expect(fixture.calls.filter((call) => call === '/climate/current.json')).toHaveLength(2);
    expect(fixture.calls.filter((call) => call.endsWith('/catalog.json'))).toHaveLength(12);
    expect(fixture.rangeHeaders).toEqual(Array(12).fill('bytes=0-32831'));
    expect(maximumRequests).toBe(3);
  });

  it('discards the whole year if its publication changes during the read', async () => {
    const fixture = await publication({ changePointerAfter: 1 });
    await expect(new WorldwideClimateClient({ fetcher: fixture.fetcher }).loadYearPoint(-180, 0)).rejects.toMatchObject({ code: 'stale' });
    expect(fixture.calls.filter((call) => call.endsWith('.nwc'))).toHaveLength(12);
  });

  it('preserves caller cancellation while reading a year', async () => {
    const fixture = await publication();
    const controller = new AbortController();
    const reason = new DOMException('Selection changed', 'AbortError');
    const fetcher: typeof fetch = async (input, init) => {
      if (String(input).endsWith('/era5-02/catalog.json')) controller.abort(reason);
      return fixture.fetcher(input, init);
    };
    await expect(new WorldwideClimateClient({ fetcher }).loadYearPoint(-180, 0, controller.signal)).rejects.toBe(reason);
  });

  it('decodes the established distribution prefix and all ERA5 physical evidence', async () => {
    const fixture = await publication();
    const point = await new WorldwideClimateClient({ fetcher: fixture.fetcher }).loadPoint('month-11', -180, 0);
    expect(point).toMatchObject({
      sourceCell: { latitude: 0, longitude: -180 }, usedAdjacentCell: false,
      comfortGoodPercent: 72, dryGoodPercent: 81, clearSkyGoodPercent: 64,
      physical: {
        daytimeTemperatureP10C: -5.5, daytimeTemperatureP50C: 18.2, daytimeTemperatureP90C: 31.7,
        daytimeMinimumP50C: 10, daytimeMaximumP50C: 26.4,
        dewPointP50C: 7.8, dewPointP90C: 16.1,
        relativeHumidityP50Percent: 68, muggyDayPercent: 17,
        rainFreeDayPercent: 79, rainP90MM: 12.3,
        cloudCoverP50Percent: 42, clearSkyDayPercent: 61,
        windP50KPH: 8.4, windP90KPH: 31.2, days: 620,
      },
    });
    expect(point?.nice.ratingShares).toEqual({ awful: 50, poor: 100, mixed: 150, good: 250, veryGood: 300, excellent: 150 });
    expect(point?.nice.goodOrBetterPercent).toBe(70);
    expect(point?.cycling.calmGoodPercent).toBeLessThanOrEqual(point!.nice.calmGoodPercent);
    expect(fixture.rangeHeaders).toEqual(['bytes=0-32831']);
  });

  it('keeps the positive dateline independent from the canonical -180 degree cell', async () => {
    const positive = Uint8Array.from(recordVector);
    positive[1] = 12;
    const fixture = await publication({ datelineRecord: positive });
    const client = new WorldwideClimateClient({ fetcher: fixture.fetcher });
    const west = await client.loadPoint('month-11', -180, 0);
    const east = await client.loadPoint('month-11', 180, 0);
    expect(west?.sourceCell).toEqual({ latitude: 0, longitude: -180 });
    expect(east?.sourceCell).toEqual({ latitude: 0, longitude: 180 });
    expect(west?.comfortGoodPercent).toBe(72);
    expect(east?.comfortGoodPercent).toBe(12);
    expect(fixture.rangeHeaders).toEqual(['bytes=0-32831', 'bytes=0-1087']);
    expect(fixture.calls).toContain('/climate/releases/era5-nov/points/month-11/main/022/000.nwc');
    expect(fixture.calls).toContain('/climate/releases/era5-nov/points/month-11/dateline/022.nwd');
  });

  it('uses a bounded whole-block Range request, verifies the asset, and reuses its cache', async () => {
    const fixture = await publication();
    const client = new WorldwideClimateClient({ fetcher: fixture.fetcher });
    await client.loadPoint('month-11', -180, 0);
    await client.loadPoint('month-11', -180, 0);
    expect(fixture.rangeHeaders).toEqual(['bytes=0-32831']);
    expect(fixture.calls.filter((call) => call.endsWith('.nwc'))).toHaveLength(1);
  });

  it.each(['sha', 'payload-sha', 'content-range', 'media-type'] as const)('fails closed for invalid point transport: %s', async (transportFault) => {
    const fixture = await publication({ transportFault });
    await expect(new WorldwideClimateClient({ fetcher: fixture.fetcher }).loadPoint('month-11', -180, 0)).rejects.toMatchObject({ code: 'invalid' });
  });

  it('finds scalar tiles only from the declared clear-sky field and release', async () => {
    const fixture = await publication();
    const client = new WorldwideClimateClient({ fetcher: fixture.fetcher });
    const catalog = (await client.loadCatalog('month-11'))!;
    const tile = client.findScalarTile(catalog, 'clear_sky_good', 4, 0, 0);
    expect(tile?.path).toBe('scores/month-11/clear_sky_good/4/0/0.nws');
    expect(client.scalarTileUrl(catalog, tile!)).toBe('/climate/releases/era5-nov/scores/month-11/clear_sky_good/4/0/0.nws');
  });

  it.each([
    ['AgERA5 index v1', (index: any) => { index.version = 1; }],
    ['old provenance object', (index: any) => { delete index.source; index.provenance = { derivationContract: 'agera5-v2.0' }; }],
    ['bright_good field', (index: any) => { index.probability.fields[2] = 'bright_good'; }],
    ['old 0.1 degree geometry', (index: any) => { index.geometry.rows = 1701; index.geometry.columns = 3599; index.geometry.cellResolutionDegrees = 0.1; }],
  ])('fails closed for %s', async (_name, mutate) => {
    const fixture = await publication({ mutateIndex: mutate });
    await expect(new WorldwideClimateClient({ fetcher: fixture.fetcher }).loadIndex()).rejects.toMatchObject({ code: 'invalid' });
  });

  it.each([
    ['old main media type', (catalog: any) => { catalog.pointBlocks[0].mediaType = 'application/vnd.nice-weather.climate-block'; }],
    ['old point path', (catalog: any) => { catalog.pointBlocks[0].path = 'points/month-11/022/000.nwc'; }],
    ['36-byte record declaration', (catalog: any) => { catalog.pointRecordBytes = 36; }],
    ['retired source', (catalog: any) => { catalog.source.dataset = 'AgERA5'; }],
  ])('rejects catalog contract: %s', async (_name, mutate) => {
    const fixture = await publication({ mutateCatalog: mutate });
    await expect(new WorldwideClimateClient({ fetcher: fixture.fetcher }).loadCatalog('month-11')).rejects.toMatchObject({ code: 'invalid' });
  });

  it('rejects the old AgERA5 point magic even when the declared checksums match', async () => {
    const fixture = await publication({ mainMagic: 'NWAG5CLM' });
    await expect(new WorldwideClimateClient({ fetcher: fixture.fetcher }).loadPoint('month-11', -180, 0)).rejects.toMatchObject({ code: 'invalid' });
  });

  it('rejects invalid physical ordering and non-zero reserved bytes', async () => {
    const invalid = Uint8Array.from(recordVector);
    new DataView(invalid.buffer).setInt16(36, 400, true);
    invalid[63] = 1;
    const fixture = await publication({ mainRecord: invalid });
    await expect(new WorldwideClimateClient({ fetcher: fixture.fetcher }).loadPoint('month-11', -180, 0)).rejects.toMatchObject({ code: 'invalid' });
  });

  it('detects a pointer change during one read', async () => {
    const fixture = await publication({ changePointerAfter: 1 });
    await expect(new WorldwideClimateClient({ fetcher: fixture.fetcher }).loadIndex()).rejects.toMatchObject({ code: 'stale' });
  });

  it('cancels an older point lookup when a newer lookup starts', async () => {
    const fixture = await publication({ delayPoint: true });
    const client = new WorldwideClimateClient({ fetcher: fixture.fetcher });
    const first = client.loadPoint('month-11', -180, 0);
    await Promise.resolve();
    const second = client.loadPoint('month-11', 180, 0);
    await expect(first).rejects.toMatchObject({ code: 'stale' });
    await expect(second).resolves.toBeDefined();
  });
});

type Options = {
  mainRecord?: Uint8Array;
  datelineRecord?: Uint8Array;
  mainMagic?: string;
  mutateIndex?: (index: any) => void;
  mutateCatalog?: (catalog: any) => void;
  changePointerAfter?: number;
  delayPoint?: boolean;
  transportFault?: 'sha' | 'payload-sha' | 'content-range' | 'media-type';
};

async function publication(options: Options = {}) {
  let main = await pointAsset(options.mainMagic ?? 'NWE5CPT1', 22, 0, 16, 32, options.mainRecord ?? recordVector, 8 * 32);
  if (options.transportFault === 'payload-sha') {
    const bytes = Uint8Array.from(main.bytes);
    bytes[32] ^= 1;
    main = { ...main, bytes, sha256: await digest(bytes) };
  }
  const dateline = await pointAsset('NWE5CDL1', 22, 0, 16, 1, options.datelineRecord ?? recordVector, 8);
  const scalarTiles = fields.flatMap((field) => Array.from({ length: 5 }, (_, zoom) => ({
    field, zoom, x: 0, y: 0, path: `scores/month-11/${field}/${zoom}/0/0.nws`, bytes: 66564,
    sha256: sha('a'), mediaType: 'application/vnd.nice-weather.score-tile', dataTexels: 1, noDataTexels: 66563,
  })));
  const sourceCommon = {
    dataset: 'ERA5', kind: 'hourly-reanalysis-reduced-to-iana-civil-local-days', gridDegrees: 0.25,
    localDay: 'IANA civil local daytime 10:00–20:00, exact valid dates, 2006–2025',
    landAuthority: 'Natural Earth 5.1.1 source-cell-footprint intersection', landSourceSha256: sha('1'),
    publicationAuthorityKind: 'checksum-trusted-publication-authority-v1',
    logicalCellCount: 2, datelineCellCount: 1,
    displayMaskAuthority: 'Natural Earth 5.1.1 centre-on-land at exact ERA5 0.25 degree grid points',
    displayMaskDerivation: 'centre points from the checksum-bound publication authority', displayMaskSha256: sha('2'), displayMaskBytes: 1038240,
  };
  const geometry = { projection: 'EPSG:4326', west: -180, east: 180, south: -90, north: 90, rows: 721, columns: 1440, cellResolutionDegrees: 0.25, cellRegistration: 'exact-era5-grid-point-centres' };
  const positiveDateline = { longitude: 180, meaning: 'independent +180 degree civil-day records; never alias to -180 degree records', canonicalColumns: 1440, overrideRecordBytes: 64 };
  const probability = { unit: 'whole-percent', denominator: 'complete-real-days-in-reference-month', threshold: 'canonical-rating-good-or-better', goodMinimumScore: 55, noDataValue: 255, fields: [...fields] };
  const scalarSemantics = { maximumZoomMeaning: 'exact evaluated point value sampled only where the ERA5 grid-point centre is land', parentAggregation: 'rounded arithmetic mean of available four child texels; no-data excluded' };
  const catalog: any = {
    version: 2, releaseId: 'era5-nov', period: 'month-11', geometry,
    source: {
      ...sourceCommon, referenceFirstYear: 2006, referenceLastYear: 2025,
      acquisitionPlanSha256: sha('3'), logicalPlanSha256: sha('4'), publicationAuthoritySha256: sha('5'),
      displayMaskPath: 'display-mask.bin',
    },
    positiveDateline, probability, scalarSemantics, maximumZoom: 4, pointRecordBytes: 64,
    pointBlocks: [{ latitudeBlock: 22, longitudeBlock: 0, latitudeCount: 16, longitudeCount: 32, path: 'points/month-11/main/022/000.nwc', bytes: main.bytes.length, sha256: main.sha256, payloadSha256: main.payloadSha256, mediaType: 'application/vnd.nice-weather.era5-climate-point-block', dataRecords: 1, noDataRecords: 511 }],
    datelineOverrides: [{ latitudeBlock: 22, latitudeCount: 16, path: 'points/month-11/dateline/022.nwd', bytes: dateline.bytes.length, sha256: dateline.sha256, payloadSha256: dateline.payloadSha256, mediaType: 'application/vnd.nice-weather.era5-climate-dateline-block', dataRecords: 1, noDataRecords: 15 }],
    scalarTiles,
  };
  options.mutateCatalog?.(catalog);
  const monthlyCatalogs = new Map<string, Uint8Array>();
  const months = await Promise.all(Array.from({ length: 12 }, async (_, month) => {
    const period = `month-${String(month + 1).padStart(2, '0')}`;
    const releaseId = month === 10 ? 'era5-nov' : `era5-${String(month + 1).padStart(2, '0')}`;
    const monthly = JSON.parse(JSON.stringify(catalog).replaceAll('month-11', period));
    monthly.releaseId = releaseId;
    const bytes = jsonBytes(monthly);
    monthlyCatalogs.set(`/climate/releases/${releaseId}/catalog.json`, bytes);
    return { period, releaseId, catalogSha256: await digest(bytes) };
  }));
  const index: any = {
    version: 2, indexId: 'era5-index', state: 'complete', referencePeriod: { firstYear: 2006, lastYear: 2025 },
    geometry, source: sourceCommon, positiveDateline, probability, scalarSemantics, maximumZoom: 4, pointRecordBytes: 64,
    months,
  };
  options.mutateIndex?.(index);
  const indexBytes = jsonBytes(index);
  const indexSha = await digest(indexBytes);
  const pointer = jsonBytes({ version: 2, indexId: 'era5-index', indexSha256: indexSha });
  const changedPointer = jsonBytes({ version: 2, indexId: 'era5-index-next', indexSha256: sha('f') });
  const calls: string[] = [];
  const rangeHeaders: string[] = [];
  let pointerReads = 0;
  const fetcher: typeof fetch = async (input, init) => {
    const path = String(input); calls.push(path);
    if (init?.signal?.aborted) throw init.signal.reason;
    if (path === '/climate/current.json') {
      pointerReads++;
      const body = options.changePointerAfter && pointerReads > options.changePointerAfter ? changedPointer : pointer;
      return jsonResponse(body);
    }
    if (path === '/climate/indexes/era5-index.json') return jsonResponse(indexBytes);
    const monthlyCatalog = monthlyCatalogs.get(path);
    if (monthlyCatalog) return jsonResponse(monthlyCatalog);
    if (path.endsWith('.nwc') || path.endsWith('.nwd')) {
      if (options.delayPoint) await new Promise<void>((resolve, reject) => {
        const timer = setTimeout(resolve, path.endsWith('.nwc') ? 10 : 0);
        init?.signal?.addEventListener('abort', () => { clearTimeout(timer); reject(init.signal!.reason); }, { once: true });
      });
      let value = path.endsWith('.nwd') ? dateline.bytes : main.bytes;
      if (path.endsWith('.nwc') && options.transportFault === 'sha') {
        value = Uint8Array.from(value);
        value[value.length - 1] ^= 1;
      }
      let media = path.endsWith('.nwd') ? 'application/vnd.nice-weather.era5-climate-dateline-block' : 'application/vnd.nice-weather.era5-climate-point-block';
      if (path.endsWith('.nwc') && options.transportFault === 'media-type') media = 'application/octet-stream';
      const range = new Headers(init?.headers).get('Range') ?? ''; rangeHeaders.push(range);
      const contentRange = path.endsWith('.nwc') && options.transportFault === 'content-range' ? `bytes 1-${value.length - 1}/${value.length}` : `bytes 0-${value.length - 1}/${value.length}`;
      return new Response(value.slice().buffer, { status: 206, headers: { 'Content-Type': media, 'Content-Range': contentRange } });
    }
    return new Response('', { status: 404 });
  };
  return { fetcher, calls, rangeHeaders };
}

async function pointAsset(magic: string, latitudeBlock: number, longitudeBlock: number, rows: number, columns: number, record: Uint8Array, recordIndex: number) {
  const payload = new Uint8Array(rows * columns * 64);
  payload.set(record, recordIndex * 64);
  const payloadSha256 = await digest(payload);
  const header = new Uint8Array(64);
  header.set(new TextEncoder().encode(magic), 0);
  const view = new DataView(header.buffer);
  view.setUint16(8, 1, true); view.setUint16(10, 64, true); view.setUint16(12, 64, true);
  view.setUint16(14, rows, true); view.setUint16(16, columns, true);
  view.setUint32(20, latitudeBlock, true); view.setUint32(24, longitudeBlock, true);
  header.set(hexBytes(payloadSha256), 32);
  const bytes = new Uint8Array(header.length + payload.length); bytes.set(header); bytes.set(payload, header.length);
  return { bytes, sha256: await digest(bytes), payloadSha256 };
}
function jsonBytes(value: unknown): Uint8Array { return new TextEncoder().encode(JSON.stringify(value)); }
function jsonResponse(body: Uint8Array): Response { return new Response(body.slice().buffer, { status: 200, headers: { 'Content-Type': 'application/json', 'Content-Encoding': 'br' } }); }
async function digest(bytes: Uint8Array): Promise<string> { return Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes as BufferSource)), (value) => value.toString(16).padStart(2, '0')).join(''); }
function hexBytes(value: string): Uint8Array { return Uint8Array.from(value.match(/../g) ?? [], (byte) => Number.parseInt(byte, 16)); }
