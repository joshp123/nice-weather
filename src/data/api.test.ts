import { afterEach, describe, expect, it, vi } from 'vitest';
import { forecastRunStatus, freshnessLabel, imageAssetUrl, isStale, loadManifest, loadPoint, StaleReleaseError } from './api';

afterEach(() => vi.unstubAllGlobals());

describe('forecast status copy', () => {
  const now = new Date('2026-07-18T12:00:00Z');

  it('uses plain relative update times', () => {
    expect(freshnessLabel('2026-07-18T11:43:00Z', now)).toBe('Updated 17 minutes ago');
    expect(freshnessLabel('2026-07-18T09:45:00Z', now)).toBe('Updated 2 hours ago');
  });

  it('marks forecasts stale at the manifest threshold', () => {
    expect(isStale('2026-07-18T05:00:00Z', 8, now)).toBe(false);
    expect(isStale('2026-07-18T03:00:00Z', 8, now)).toBe(true);
  });

  it('does not let a fresh outlook hide an old detailed run', async () => {
    const fixture = manifestFixture();
    fixture.forecast.updatedAt = '2026-07-18T11:45:00Z';
    fixture.forecast.days[0].source.runAt = '2026-07-17T18:00:00Z';
    fixture.forecast.days[5].source.runAt = '2026-07-18T11:00:00Z';
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture), { status: 200 })));

    const manifest = await loadManifest();
    expect(forecastRunStatus(manifest.forecast.days[0].sourceRunAt, manifest.forecast.staleAfterHours, now))
      .toEqual({ stale: true, label: 'Forecast is out of date' });
    expect(forecastRunStatus(manifest.forecast.days[5].sourceRunAt, manifest.forecast.staleAfterHours, now))
      .toEqual({ stale: false, label: 'Updated 1 hour ago' });
  });
});

describe('image asset URLs', () => {
  it('keys immutable images by their stable manifest checksum', () => {
    const image = { url: 'images/day.png', sha256: 'abc123', width: 280, height: 168, projection: 'web-mercator' as const };
    expect(imageAssetUrl('release-20260718', image)).toBe('/releases/release-20260718/images/day.png?v=abc123');
    expect(imageAssetUrl('release-20260718', image)).toBe(imageAssetUrl('release-20260718', image));
  });

  it('preserves query parameters and fragments while replacing an old version', () => {
    expect(imageAssetUrl('release-20260718', { url: '/images/day.png?palette=nice&v=old#map', sha256: 'new/value', width: 280, height: 168, projection: 'web-mercator' }))
      .toBe('/releases/release-20260718/images/day.png?palette=nice&v=new%2Fvalue#map');
  });

  it('rejects an unsafe release or image path', () => {
    const image = { url: '../images/day.png', sha256: 'abc123', width: 280, height: 168, projection: 'web-mercator' as const };
    expect(() => imageAssetUrl('../current', { ...image, url: 'images/day.png' })).toThrow('Invalid releaseId');
    expect(() => imageAssetUrl('release-20260718', image)).toThrow('Invalid image URL');
  });
});

describe('manifest contract', () => {
  it('accepts only the complete version 4 contract', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(manifestFixture()), { status: 200 })));

    const manifest = await loadManifest();
    expect(manifest).toMatchObject({ version: 4, releaseId: 'release-20260718', productContract: 'nice-weather-v7', dataStatus: 'sample', bounds: [-25, 30, 45, 72] });
    expect(manifest.forecast.days).toHaveLength(14);
    expect(manifest.climate.months).toHaveLength(12);
    expect(manifest.forecast.days[0]).toMatchObject({ shortLabel: 'Sat 18', kind: 'detailed', providerLabel: 'Test forecast' });
  });

  it('rejects an old manifest instead of guessing defaults', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ version: 2 }), { status: 200 })));
    await expect(loadManifest()).rejects.toThrow('unsupported data format');
  });

  it('rejects an unsafe release identity', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ ...manifestFixture(), releaseId: '../current' }), { status: 200 })));
    await expect(loadManifest()).rejects.toThrow('Invalid releaseId');
  });

  it('rejects a different product contract', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ ...manifestFixture(), productContract: 'nice-weather-v6' }), { status: 200 })));
    await expect(loadManifest()).rejects.toThrow('unsupported product contract');
  });

  it('rejects a geographic image that MapLibre would distort', async () => {
    const fixture = manifestFixture();
    fixture.forecast.days[0].images.nice.projection = 'epsg-4326';
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture), { status: 200 })));
    await expect(loadManifest()).rejects.toThrow('unsupported projection');
  });
});

describe('point contract', () => {
  it('preserves range, wet time, components and source details', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL) => new Response(JSON.stringify({
      releaseId: 'release-20260718',
      periodId: '2026-07-18', periodLabel: 'Saturday 18 July', profile: 'cycling',
      location: { lat: 39.47, lon: -0.38 }, summary: 'Warm, dry, sunny, light wind',
      score: { value: 88, category: 'Excellent', components: {
        comfort: { value: 91, category: 'Excellent' }, dryness: { value: 100, category: 'Excellent' },
        sun: { value: 85, category: 'Excellent' }, wind: { value: 82, category: 'Very good' },
      } },
      weather: { temperatureC: 25, minTempC: 20, maxTempC: 29, rainMm: 0, wetTimeFraction: 0, solarEnergyMJm2: 19.2, windKmh: 11, gustKmh: 18 },
      source: {
        basis: 'forecast', detailLabel: 'Detailed forecast', providerLabel: 'ICON-EU', resolutionKm: 7, temporalResolutionHours: 1,
        artifactGeneratedAt: '2026-07-18T10:00:00Z', runAt: '2026-07-18T06:00:00Z', validFrom: '2026-07-18T10:00:00Z', validTo: '2026-07-18T20:00:00Z',
      },
    }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    const point = await loadPoint({ releaseId: 'release-20260718', lat: 39.47, lon: -0.38, period: '2026-07-18', profile: 'cycling' });
    expect(point).toMatchObject({ score: 88, category: 'Excellent', source: { basis: 'forecast', detailLabel: 'Detailed forecast', providerLabel: 'ICON-EU' }, components: { comfort: { value: 91, category: 'Excellent' }, dryness: { value: 100, category: 'Excellent' } } });
    expect(point.metrics).toMatchObject({ minTempC: 20, maxTempC: 29, wetTimeFraction: 0, solarEnergyMJm2: 19.2, gustKmh: 18 });
    expect(String(fetchMock.mock.calls[0][0])).toContain('release=release-20260718');
  });

  it('returns a typed stale release error for the server conflict contract', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      error: { code: 'stale_release', message: 'Weather data changed. Refresh the forecast and try again.' },
    }), { status: 409, headers: { 'Content-Type': 'application/json' } })));

    await expect(loadPoint({ releaseId: 'release-old', lat: 52.37, lon: 4.9, period: '2026-07-18', profile: 'nice' }))
      .rejects.toBeInstanceOf(StaleReleaseError);
  });
});

function manifestFixture() {
  const period = (id: string, extra: Record<string, unknown>) => ({
    id, label: id,
    source: { providerId: 'test', providerLabel: 'Test forecast', runAt: '2026-07-18T06:00:00Z', validFrom: '2026-07-18T10:00:00Z', validTo: '2026-07-18T20:00:00Z' },
    nativeResolutionKm: 7, temporalResolutionHours: 1,
    images: { nice: image(`${id}-nice`), cycling: image(`${id}-cycling`) },
    lenses: { comfort: image(`${id}-comfort`), dry: image(`${id}-dry`), bright: image(`${id}-bright`), 'wind-nice': image(`${id}-wind-nice`), 'wind-cycling': image(`${id}-wind-cycling`) },
    gridUrl: `/weather/${id}.nwg`, gridSha256: `grid-${id}`, ...extra,
  });
  return {
    version: 4, releaseId: 'release-20260718', productContract: 'nice-weather-v7', dataStatus: 'sample', generatedAt: '2026-07-18T10:00:00Z',
    region: { id: 'europe', label: 'Europe', bounds: { west: -25, east: 45, south: 30, north: 72 } },
    profiles: [{ id: 'nice', label: 'Nice' }, { id: 'cycling', label: 'Cycling' }],
    legend: [
      { label: 'Awful', color: '#3F505A', min: 0, max: 24 }, { label: 'Poor', color: '#73858D', min: 25, max: 39 },
      { label: 'Mixed', color: '#A5B2AA', min: 40, max: 54 }, { label: 'Good', color: '#68AE8A', min: 55, max: 69 },
      { label: 'Very good', color: '#D7BD62', min: 70, max: 84 }, { label: 'Excellent', color: '#E5A073', min: 85, max: 100 },
    ],
    palette: [
      { score: 0, color: '#314047' }, { score: 24, color: '#52636B' },
      { score: 25, color: '#73858D' }, { score: 39, color: '#87969B' },
      { score: 40, color: '#9CAAA3' }, { score: 54, color: '#B5BDB3' },
      { score: 55, color: '#5B9B7A' }, { score: 69, color: '#78BA95' },
      { score: 70, color: '#C7AD55' }, { score: 84, color: '#E0C873' },
      { score: 85, color: '#DE956A' }, { score: 100, color: '#F0B085' },
    ],
    forecast: { updatedAt: '2026-07-18T09:30:00Z', staleAfterHours: 12, days: Array.from({ length: 14 }, (_, index) => {
      const date = `2026-07-${String(18 + index).padStart(2, '0')}`;
      return period(date, { date, kind: index < 5 ? 'detailed' : 'outlook' });
    }) },
    climate: { label: 'Typical year', referencePeriod: '1991–2020', months: Array.from({ length: 12 }, (_, index) => period(`month-${index + 1}`, { label: new Date(2026, index, 1).toLocaleString('en-GB', { month: 'long' }), month: index + 1 })) },
  };
}

function image(id: string) {
  return { url: `/images/${id}.png`, sha256: id, width: 900, height: 600, projection: 'web-mercator' };
}
