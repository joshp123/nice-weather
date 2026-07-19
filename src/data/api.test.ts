import { afterEach, describe, expect, it, vi } from 'vitest';
import { forecastRunStatus, freshnessLabel, imageAssetUrl, isStale, loadManifest, loadPoint, StaleReleaseError } from './api';
import pointVector from './testdata/point-asset-vector.json';

afterEach(() => vi.unstubAllGlobals());

describe('weather delivery client', () => {
  it('parses manifest v8 with dense outlook score planes and window lenses', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(manifestFixture()), { status: 200 })));
    const manifest = await loadManifest();
    expect(manifest.productContract).toBe('nice-weather-v17');
    expect(manifest.forecast.window).toMatchObject({ id: 'next-14-days', dayCount: 14, startDate: '2026-07-18', endDate: '2026-07-31', resolutionKm: 28 });
    expect(Object.keys(manifest.forecast.window.lenses)).toEqual(['comfort', 'dry', 'bright', 'wind-nice', 'wind-cycling']);
    expect(manifest.forecast.days).toHaveLength(14);
    expect(manifest.climate?.months).toHaveLength(12);
  });

  it('accepts strictly increasing mixed wind source times', async () => {
    const fixture = manifestFixture();
    fixture.forecast.days[0].windFrames = [0, 1, 2, 5, 8, 10].map((index) => fixture.forecast.days[0].windFrames[index]);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture), { status: 200 })));
    const manifest = await loadManifest();
    expect(manifest.forecast.days[0].windFrames?.map((frame) => frame.validAt)).toEqual([
      '2026-07-18T10:00:00Z', '2026-07-18T11:00:00Z', '2026-07-18T12:00:00Z',
      '2026-07-18T15:00:00Z', '2026-07-18T18:00:00Z', '2026-07-18T20:00:00Z',
    ]);
  });

  it('rejects duplicate and out-of-order wind source times', async () => {
    for (const times of [
      ['2026-07-18T10:00:00Z', '2026-07-18T10:00:00Z'],
      ['2026-07-18T11:00:00Z', '2026-07-18T10:00:00Z'],
    ]) {
      const fixture = manifestFixture();
      fixture.forecast.days[0].windFrames[0].validAt = times[0];
      fixture.forecast.days[0].windFrames[1].validAt = times[1];
      vi.spyOn(console, 'error').mockImplementation(() => undefined);
      vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture), { status: 200 })));
      await expect(loadManifest()).rejects.toThrow('forecast could not be read');
      vi.restoreAllMocks();
    }
  });

  it('rejects old contracts behind one human-facing error and logs the technical cause', async () => {
    const fixture = manifestFixture();
    fixture.productContract = 'nice-weather-v12';
    const error = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture), { status: 200 })));
    await expect(loadManifest()).rejects.toThrow('forecast could not be read');
    expect(error).toHaveBeenCalledWith('[nice-weather] Invalid weather manifest.', expect.any(Error));
  });

  it('rejects a live release that substitutes fixture-sized display geometry', async () => {
    const fixture = manifestFixture(); fixture.dataStatus = 'live';
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture), { status: 200 })));
    await expect(loadManifest()).rejects.toThrow('forecast could not be read');
  });

  it('rejects unknown manifest fields rather than silently drifting', async () => {
    const fixture = { ...manifestFixture(), surprise: true };
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture), { status: 200 })));
    await expect(loadManifest()).rejects.toThrow('forecast could not be read');
  });

  it('rejects manifest point geometry that differs from its images or region', async () => {
    for (const mutate of [
      (fixture: any) => { fixture.forecast.days[0].images.nice.width = 2; },
      (fixture: any) => { fixture.forecast.window.lenses.comfort.width = 2; },
      (fixture: any) => { fixture.forecast.days[0].point.geometry.bounds.west = -24; },
      (fixture: any) => { fixture.forecast.window.point.geometry.quad.west = 0.1; },
      (fixture: any) => {
        const points = [fixture.forecast.window.point, ...fixture.forecast.days.map((day: any) => day.point), ...fixture.climate.months.map((month: any) => month.point)];
        points.forEach((point: any) => { point.geometry.quad.west += 1 / 8192; });
      },
    ]) {
      const fixture = manifestFixture(); mutate(fixture);
      vi.spyOn(console, 'error').mockImplementation(() => undefined);
      vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture), { status: 200 })));
      await expect(loadManifest()).rejects.toThrow('forecast could not be read');
      vi.restoreAllMocks();
    }
  });

  it('rejects forecast-window lenses that alias another map field', async () => {
    const fixture = manifestFixture();
    fixture.forecast.window.lenses.comfort = fixture.forecast.window.profiles.nice.image;
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture), { status: 200 })));
    await expect(loadManifest()).rejects.toThrow('forecast could not be read');
  });

  it('rejects duplicate or gapped forecast dates', async () => {
    const fixture = manifestFixture();
    fixture.forecast.days[1].date = fixture.forecast.days[0].date;
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture), { status: 200 })));
    await expect(loadManifest()).rejects.toThrow('forecast could not be read');
  });

  it('rejects duplicate or missing climate months', async () => {
    const fixture = manifestFixture();
    fixture.climate.months[1].month = 1;
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture), { status: 200 })));
    await expect(loadManifest()).rejects.toThrow('forecast could not be read');
  });

  it('rejects duplicate forecast or climate period IDs', async () => {
    const fixture = manifestFixture();
    fixture.forecast.days[1].id = fixture.forecast.days[0].id;
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture), { status: 200 })));
    await expect(loadManifest()).rejects.toThrow('forecast could not be read');

    const climateFixture = manifestFixture();
    climateFixture.climate.months[1].id = climateFixture.climate.months[0].id;
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(climateFixture), { status: 200 })));
    await expect(loadManifest()).rejects.toThrow('forecast could not be read');
  });

  it('constructs immutable release image URLs with the checksum', () => {
    expect(imageAssetUrl('release-1', image('score'))).toBe(`/releases/release-1/images/score.png?v=${digest('score')}`);
    expect(() => imageAssetUrl('../bad', image('score'))).toThrow('Invalid releaseId');
  });

  it('reads daily point bytes with exact ranges and caches the immutable header', async () => {
    const manifest = await parsedManifest('daily-range-test');
    const asset = manifest.forecast.days[0].point;
    const file = joinPointFile(pointHeader(asset, 1), dailyRecord());
    const fetch = vi.fn(rangeFetch(file));
    vi.stubGlobal('fetch', fetch);
    const request = { releaseId: manifest.releaseId, lat: 39.47, lon: -0.38, period: manifest.forecast.days[0].id, profile: 'cycling' as const };
    const point = await loadPoint(request, manifest);
    expect(point).toMatchObject({ kind: 'daily', score: 88, summary: '25°C · dry · bright · light wind', metrics: { maxTempC: 29, rainMm: 0 }, source: { basis: 'forecast' } });
    const nice = await loadPoint({ ...request, profile: 'nice' }, manifest);
    expect(nice).toMatchObject({ kind: 'daily', score: 87 });
    expect(fetch).toHaveBeenCalledTimes(3);
    expect(fetch.mock.calls.map((call) => new Headers(call[1]?.headers).get('Range'))).toEqual(['bytes=0-127', 'bytes=128-157', 'bytes=128-157']);
  });

  it('decodes the canonical thermal notice without evaluating weather in the browser', async () => {
    const manifest = await parsedManifest('thermal-summary-test');
    const asset = manifest.forecast.days[0].point;
    const record = dailyRecord();
    const view = new DataView(record.buffer);
    view.setInt16(0, 350, true); view.setInt16(2, 280, true); view.setInt16(4, 380, true); view.setInt16(6, 50, true);
    record[22] = 36; record[25] = 36; record[27] = 36;
    record[29] = 92; // Go code: too hot + dry + sunny + light wind.
    vi.stubGlobal('fetch', vi.fn(rangeFetch(joinPointFile(pointHeader(asset, 1), record))));
    const point = await loadPoint({ releaseId: manifest.releaseId, lat: 39.47, lon: -0.38, period: manifest.forecast.days[0].id, profile: 'nice' }, manifest);
    expect(point).toMatchObject({ kind: 'daily', score: 36, summary: '35°C · too hot · dry · sunny · light wind' });
  });

  it('decodes every summary-code boundary and rejects the reserved tail', async () => {
    const cases: [number, string][] = [
      [79, '25°C · wet · sunny · severe gusts'],
      [80, '25°C · too hot · dry · overcast · light wind'],
      [159, '25°C · too hot · wet · sunny · severe gusts'],
      [160, '25°C · too cold · dry · overcast · light wind'],
      [239, '25°C · too cold · wet · sunny · severe gusts'],
    ];
    for (const [code, summary] of cases) {
      const manifest = await parsedManifest(`summary-code-${code}`);
      const asset = manifest.forecast.days[0].point;
      const record = dailyRecord(); record[29] = code;
      vi.stubGlobal('fetch', vi.fn(rangeFetch(joinPointFile(pointHeader(asset, 1), record))));
      const point = await loadPoint({ releaseId: manifest.releaseId, lat: 39.47, lon: -0.38, period: manifest.forecast.days[0].id, profile: 'nice' }, manifest);
      expect(point).toMatchObject({ kind: 'daily', summary });
    }
    for (const code of [240, 255]) {
      const manifest = await parsedManifest(`invalid-summary-code-${code}`);
      const asset = manifest.forecast.days[0].point;
      const record = dailyRecord(); record[29] = code;
      const error = vi.spyOn(console, 'error').mockImplementation(() => undefined);
      vi.stubGlobal('fetch', vi.fn(rangeFetch(joinPointFile(pointHeader(asset, 1), record))));
      await expect(loadPoint({ releaseId: manifest.releaseId, lat: 39.47, lon: -0.38, period: manifest.forecast.days[0].id, profile: 'nice' }, manifest)).rejects.toThrow('Weather details could not be read');
      expect(error).toHaveBeenCalledWith('[nice-weather] Invalid static point asset.', expect.objectContaining({ message: 'Invalid point summary code.' }));
      vi.restoreAllMocks();
    }
  });

  it('decodes the literal Go binary vector including negative half-degree rounding', async () => {
    const manifest = await parsedManifest('shared-vector-test');
    const asset = manifest.forecast.days[0].point;
    asset.payloadSha256 = '11'.repeat(32);
    const file = joinPointFile(hexBytes(pointVector.headerHex), hexBytes(pointVector.dailyRecordHex));
    vi.stubGlobal('fetch', vi.fn(rangeFetch(file)));
    const point = await loadPoint({ releaseId: manifest.releaseId, lat: 45, lon: 0, period: manifest.forecast.days[0].id, profile: 'nice' }, manifest);
    expect(point).toMatchObject({ kind: 'daily', score: 19, summary: '-1°C · too cold · brief rain · bright · light wind', metrics: { temperatureC: -0.5, rainMm: 0.2 } });
  });

  it('keeps native outlook facts while using the dense displayed score', async () => {
    const manifest = await parsedManifest('outlook-display-test');
    const period = manifest.forecast.days[5];
    const nativeFile = joinPointFile(pointHeader(period.point, 1), dailyRecord());
    const display = new Uint8Array([10, 20, 30, 91, 40, 92, 41, 1]);
    const displayFile = joinPointFile(pointHeader(period.displayPoint!, 5), display);
    const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const file = String(input).includes(period.displayPoint!.url) ? displayFile : nativeFile;
      return rangeFetch(file)(input, init);
    });
    vi.stubGlobal('fetch', fetch);
    const point = await loadPoint({ releaseId: manifest.releaseId, lat: 39.47, lon: -0.38, period: period.id, profile: 'nice' }, manifest);
    expect(point).toMatchObject({ kind: 'daily', score: 91, components: { comfort: { value: 10 }, dryness: { value: 20 }, sun: { value: 30 }, wind: { value: 40 } }, metrics: { temperatureC: 25 }, source: { resolutionKm: 28 } });
    expect(fetch).toHaveBeenCalledTimes(4);
  });

  it('reads the one 14-day record and rejects a forged aggregate', async () => {
    const manifest = await parsedManifest('range-fingerprint-test');
    const asset = manifest.forecast.window.point;
    const record = rangeRecord();
    vi.stubGlobal('fetch', vi.fn(rangeFetch(joinPointFile(pointHeader(asset, 2), record))));
    const request = { releaseId: manifest.releaseId, lat: 39.47, lon: -0.38, period: 'next-14-days', profile: 'nice' as const };
    const point = await loadPoint(request, manifest);
    expect(point).toMatchObject({ kind: 'range', score: 70, window: { niceDays: 14, awfulDays: 0, longestPoorRun: 0 } });
    expect('metrics' in point).toBe(false);

    const forgedManifest = await parsedManifest('range-forged-test');
    const forged = rangeRecord(); forged[6] = 13;
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.stubGlobal('fetch', vi.fn(rangeFetch(joinPointFile(pointHeader(forgedManifest.forecast.window.point, 2), forged))));
    await expect(loadPoint({ ...request, releaseId: forgedManifest.releaseId }, forgedManifest)).rejects.toThrow('could not be read');
  });

  it('fails closed on 200, encoded, truncated, and stale range responses', async () => {
    for (const [release, response] of [
      ['point-200-test', () => new Response(new Uint8Array(128), { status: 200, headers: { 'Content-Type': 'application/octet-stream' } })],
      ['point-encoded-test', () => new Response(new Uint8Array(128), { status: 206, headers: { 'Content-Type': 'application/octet-stream', 'Content-Encoding': 'gzip', 'Content-Range': 'bytes 0-127/158' } })],
      ['point-parameter-content-type-test', () => new Response(new Uint8Array(128), { status: 206, headers: { 'Content-Type': 'application/octet-stream; charset=binary', 'Content-Range': 'bytes 0-127/158', 'Content-Length': '128' } })],
      ['point-truncated-test', () => new Response(new Uint8Array(127), { status: 206, headers: { 'Content-Type': 'application/octet-stream', 'Content-Range': 'bytes 0-127/158' } })],
      ['point-wildcard-total-test', () => new Response(new Uint8Array(128), { status: 206, headers: { 'Content-Type': 'application/octet-stream', 'Content-Range': 'bytes 0-127/*' } })],
      ['point-wrong-total-test', () => new Response(new Uint8Array(128), { status: 206, headers: { 'Content-Type': 'application/octet-stream', 'Content-Range': 'bytes 0-127/159' } })],
    ] as const) {
      const manifest = await parsedManifest(release);
      vi.spyOn(console, 'error').mockImplementation(() => undefined);
      vi.stubGlobal('fetch', vi.fn(response));
      await expect(loadPoint({ releaseId: release, lat: 39, lon: 0, period: manifest.forecast.days[0].id, profile: 'nice' }, manifest)).rejects.toThrow('could not be read');
      vi.restoreAllMocks();
    }
    for (const status of [404, 409, 410]) {
      const manifest = await parsedManifest(`point-stale-${status}-test`);
      vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status })));
      await expect(loadPoint({ releaseId: manifest.releaseId, lat: 39, lon: 0, period: manifest.forecast.days[0].id, profile: 'nice' }, manifest)).rejects.toBeInstanceOf(StaleReleaseError);
    }
  });

  it('rejects reserved header bytes and unsafe manifest dimensions', async () => {
    const manifest = await parsedManifest('point-reserved-test');
    const header = pointHeader(manifest.forecast.days[0].point, 1); header[120] = 1;
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.stubGlobal('fetch', vi.fn(rangeFetch(joinPointFile(header, dailyRecord()))));
    await expect(loadPoint({ releaseId: manifest.releaseId, lat: 39, lon: 0, period: manifest.forecast.days[0].id, profile: 'nice' }, manifest)).rejects.toThrow('could not be read');

    const fixture = manifestFixture('unsafe-point-size-test');
    fixture.forecast.days[0].point.geometry.rows = Number.MAX_SAFE_INTEGER;
    fixture.forecast.days[0].point.bytes = Number.MAX_SAFE_INTEGER;
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture), { status: 200 })));
    await expect(loadManifest()).rejects.toThrow('forecast could not be read');
  });

  it('uses the rendered texel plane for coastline no-data and legacy climate', async () => {
    const manifest = await parsedManifest('coast-texel-test');
    const asset = manifest.forecast.days[0].point;
    asset.geometry.cols = 2; asset.bytes = 128 + 2 * 30;
    const header = pointHeader(asset, 1);
    const file = new Uint8Array(asset.bytes); file.set(header); file.set(dailyRecord(), 128 + 30);
    vi.stubGlobal('fetch', vi.fn(rangeFetch(file)));
    await expect(loadPoint({ releaseId: manifest.releaseId, lat: 45, lon: -10, period: manifest.forecast.days[0].id, profile: 'nice' }, manifest)).rejects.toThrow('no complete weather record');
    const land = await loadPoint({ releaseId: manifest.releaseId, lat: 45, lon: 30, period: manifest.forecast.days[0].id, profile: 'cycling' }, manifest);
    expect(land).toMatchObject({ kind: 'daily', score: 88 });

    const climateManifest = await parsedManifest('legacy-climate-test');
    const month = climateManifest.climate!.months[10];
    vi.stubGlobal('fetch', vi.fn(rangeFetch(joinPointFile(pointHeader(month.point, 3), dailyRecord()))));
    const climate = await loadPoint({ releaseId: climateManifest.releaseId, lat: 36.72, lon: -4.42, period: month.id, profile: 'nice' }, climateManifest);
    expect(climate).toMatchObject({ kind: 'daily', source: { basis: 'climate', referencePeriod: '1981–2010' } });
  });
});

describe('forecast freshness copy', () => {
  const now = new Date('2026-07-18T12:00:00Z');
  it('uses compact human time', () => {
    expect(freshnessLabel('2026-07-18T11:30:00Z', now)).toBe('Updated 30 minutes ago');
    expect(freshnessLabel('2026-07-17T12:00:00Z', now)).toBe('Updated 1 day ago');
  });
  it('separates stale judgement from the label', () => {
    expect(isStale('2026-07-18T06:00:00Z', 12, now)).toBe(false);
    expect(forecastRunStatus('2026-07-17T06:00:00Z', 12, now)).toEqual({ stale: true, label: 'Forecast is out of date' });
  });
});

function manifestFixture(releaseId = 'release-20260718'): Record<string, any> {
  const period = (id: string, extra: Record<string, unknown>) => ({
    id, label: id,
    source: { providerId: 'test', providerLabel: 'Test forecast', runAt: '2026-07-18T06:00:00Z', validFrom: '2026-07-18T10:00:00Z', validTo: '2026-07-18T20:00:00Z' },
    nativeResolutionKm: extra.kind === 'outlook' ? 28 : 7, temporalResolutionHours: 1,
    images: { nice: image(`${id}-nice`), cycling: image(`${id}-cycling`) },
    lenses: { comfort: image(`${id}-comfort`), dry: image(`${id}-dry`), bright: image(`${id}-bright`), 'wind-nice': image(`${id}-wind-nice`), 'wind-cycling': image(`${id}-wind-cycling`) },
    point: pointAsset(id, 'forecast-day'), ...extra,
  });
  const days = Array.from({ length: 14 }, (_, index) => {
    const date = `2026-07-${String(18 + index).padStart(2, '0')}`;
    const windFrames = Array.from({ length: 11 }, (_, hour) => ({
      validAt: `2026-07-18T${String(10 + hour).padStart(2, '0')}:00:00Z`,
      url: `wind/${date}-${String(hour).padStart(2, '0')}-${digest(`${date}-${hour}`)}.nww.gz`,
      sha256: digest(`${date}-${hour}`),
      clippedComponents: 0,
    }));
    const kind = index < 5 ? 'detailed' : 'outlook';
    return period(date, { date, kind, ...(kind === 'outlook' ? { displayPoint: pointAsset(`${date}-display`, 'display-score') } : {}), windFrames });
  });
  return {
    version: 8, releaseId, productContract: 'nice-weather-v17', dataStatus: 'sample', generatedAt: '2026-07-18T10:00:00Z',
    region: { id: 'europe', label: 'Europe', bounds: { west: -25, east: 45, south: 30, north: 72 } },
    profiles: [{ id: 'nice', label: 'Nice' }, { id: 'cycling', label: 'Cycling' }],
    legend: POINT_LEGEND,
    palette: Array.from({ length: 12 }, (_, index) => ({ score: index === 11 ? 100 : index * 9, color: `#${String(index + 1).repeat(6).slice(0, 6)}` })),
    forecast: {
      updatedAt: '2026-07-18T09:30:00Z', staleAfterHours: 12,
      window: { id: 'next-14-days', label: 'Next 14 days', startDate: '2026-07-18', endDate: '2026-07-31', dayCount: 14, gridResolutionKm: 28, point: pointAsset('window', 'forecast-window'), profiles: {
        nice: { image: image('window-nice') },
        cycling: { image: image('window-cycling') },
      }, lenses: { comfort: image('window-comfort'), dry: image('window-dry'), bright: image('window-bright'), 'wind-nice': image('window-wind-nice'), 'wind-cycling': image('window-wind-cycling') } },
      days,
    },
    climate: { label: 'Typical year', referencePeriod: '1981–2010', months: Array.from({ length: 12 }, (_, index) => period(`month-${index + 1}`, { label: new Date(2026, index, 1).toLocaleString('en-GB', { month: 'long' }), month: index + 1, point: pointAsset(`month-${index + 1}`, 'legacy-climate-month') })) },
  };
}

function image(id: string) { return { url: `images/${id}.png`, sha256: digest(id), width: 1, height: 1, projection: 'web-mercator' as const }; }
function digest(value: string) { return Array.from(value).reduce((total, char) => (total * 31 + char.charCodeAt(0)) >>> 0, 0).toString(16).padStart(8, '0').repeat(8); }

function pointAsset(id: string, kind: 'forecast-day' | 'forecast-window' | 'legacy-climate-month' | 'display-score') {
  const sha256 = digest(`point-${id}-${kind}`);
  const recordBytes = kind === 'forecast-window' ? 44 : kind === 'display-score' ? 8 : 30;
  const bounds = { west: -25, east: 45, south: 30, north: 72 };
  return {
    url: `points/${id}-${sha256}.nwp`, sha256, payloadSha256: digest(`payload-${id}-${kind}`), bytes: 128 + recordBytes,
    kind, headerBytes: 128, recordBytes, projection: 'web-mercator-image',
    geometry: { bounds, quad: canonicalTestQuad(bounds), rows: 1, cols: 1 },
  };
}

function canonicalTestQuad(bounds: { west: number; east: number; south: number; north: number }) {
  const x = (longitude: number) => (longitude + 180) / 360;
  const y = (latitude: number) => 0.5 - Math.asinh(Math.tan(latitude * Math.PI / 180)) / (2 * Math.PI);
  const west = x(bounds.west); const east = x(bounds.east); const north = y(bounds.north); const south = y(bounds.south);
  const scale = 2 ** Math.max(0, Math.floor(-Math.log2(Math.max(east - west, south - north)))) * 8192;
  const snap = (value: number) => Math.round(value * scale) / scale;
  return { west: snap(west), east: snap(east), north: snap(north), south: snap(south) };
}

async function parsedManifest(release: string) {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(manifestFixture(release)), { status: 200 })));
  return loadManifest();
}

function pointHeader(asset: any, kindCode: 1 | 2 | 3 | 5): Uint8Array {
  const bytes = new Uint8Array(128); const view = new DataView(bytes.buffer);
  bytes.set(new TextEncoder().encode('NWFPOINT'));
  view.setUint16(8, 1, true); view.setUint16(10, 128, true); view.setUint16(12, asset.recordBytes, true);
  view.setUint8(14, kindCode); view.setUint8(15, 1); view.setUint32(16, asset.geometry.rows, true); view.setUint32(20, asset.geometry.cols, true);
  const values = [asset.geometry.bounds.west, asset.geometry.bounds.east, asset.geometry.bounds.south, asset.geometry.bounds.north, asset.geometry.quad.west, asset.geometry.quad.east, asset.geometry.quad.north, asset.geometry.quad.south];
  values.forEach((value, index) => view.setFloat64(24 + index * 8, value, true));
  bytes.set(hexBytes(asset.payloadSha256), 88);
  return bytes;
}

function dailyRecord(): Uint8Array {
  const bytes = new Uint8Array(30); const view = new DataView(bytes.buffer);
  view.setInt16(0, 250, true); view.setInt16(2, 200, true); view.setInt16(4, 290, true); view.setInt16(6, 150, true);
  view.setUint16(8, 0, true); view.setUint16(10, 110, true); view.setUint16(12, 180, true); view.setUint16(14, 1920, true); view.setUint16(16, 0, true);
  bytes[18] = 18; bytes[19] = 50; bytes[20] = 0x7f;
  bytes.set([91, 100, 82, 87, 85, 88, 79, 8], 22);
  return bytes;
}

function rangeRecord(): Uint8Array {
  const bytes = new Uint8Array(44);
  bytes.set([80, 90, 75, 70, 70, 85, 14, 0, 0, 70, 70, 78, 14, 0, 0], 0);
  bytes.fill(70, 15, 43); bytes[43] = 1;
  return bytes;
}

function joinPointFile(header: Uint8Array, record: Uint8Array): Uint8Array {
  const result = new Uint8Array(header.length + record.length); result.set(header); result.set(record, header.length); return result;
}

function rangeFetch(file: Uint8Array) {
  return async (_input: RequestInfo | URL, init?: RequestInit) => {
    const range = new Headers(init?.headers).get('Range');
    const match = /^bytes=(\d+)-(\d+)$/.exec(range ?? '');
    if (!match) return new Response(null, { status: 416 });
    const start = Number(match[1]); const end = Number(match[2]); const body = file.slice(start, end + 1);
    return new Response(body, { status: 206, headers: {
      'Content-Type': 'application/octet-stream', 'Content-Range': `bytes ${start}-${end}/${file.length}`, 'Content-Length': String(body.length),
    } });
  };
}

function hexBytes(value: string): Uint8Array {
  return Uint8Array.from(value.match(/../g) ?? [], (byte) => Number.parseInt(byte, 16));
}

const POINT_LEGEND = [
  { label: 'Awful', color: '#343B53', min: 0, max: 24 }, { label: 'Poor', color: '#5E6382', min: 25, max: 39 },
  { label: 'Mixed', color: '#97949C', min: 40, max: 54 }, { label: 'Good', color: '#C9A47F', min: 55, max: 69 },
  { label: 'Very good', color: '#E18952', min: 70, max: 84 }, { label: 'Excellent', color: '#F06031', min: 85, max: 100 },
];
