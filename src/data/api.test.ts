import { afterEach, describe, expect, it, vi } from 'vitest';
import { forecastRunStatus, freshnessLabel, imageAssetUrl, isStale, loadManifest, loadPoint, StaleReleaseError } from './api';

afterEach(() => vi.unstubAllGlobals());

describe('weather delivery client', () => {
  it('parses manifest v5 with the canonical 14-day window', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(manifestFixture()), { status: 200 })));
    const manifest = await loadManifest();
    expect(manifest.productContract).toBe('nice-weather-v10');
    expect(manifest.forecast.window).toMatchObject({ id: 'next-14-days', dayCount: 14, startDate: '2026-07-18', endDate: '2026-07-31', resolutionKm: 28 });
    expect(manifest.forecast.days).toHaveLength(14);
    expect(manifest.climate?.months).toHaveLength(12);
  });

  it('rejects old contracts behind one human-facing error and logs the technical cause', async () => {
    const fixture = manifestFixture();
    fixture.productContract = 'nice-weather-v9';
    const error = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture), { status: 200 })));
    await expect(loadManifest()).rejects.toThrow('forecast could not be read');
    expect(error).toHaveBeenCalledWith('[nice-weather] Invalid weather manifest.', expect.any(Error));
  });

  it('rejects unknown manifest fields rather than silently drifting', async () => {
    const fixture = { ...manifestFixture(), surprise: true };
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

  it('parses a range point without inventing daily weather', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      releaseId: 'release-20260718', periodId: 'next-14-days', periodLabel: 'Next 14 days', profile: 'nice',
      location: { lat: 39.47, lon: -0.38 },
      score: { value: 82, category: 'Very good', components: componentFixture(84, 91, 77, 74) },
      window: { startDate: '2026-07-18', endDate: '2026-07-31', dayCount: 14, niceDays: 10, awfulDays: 1, medianScore: 85, longestPoorRun: 2 },
      source: { basis: 'forecast-window', detailLabel: '14-day forecast', resolutionKm: 28, artifactGeneratedAt: '2026-07-18T10:00:00Z' },
    }), { status: 200 })));
    const point = await loadPoint({ releaseId: 'release-20260718', lat: 39.47, lon: -0.38, period: 'next-14-days', profile: 'nice' });
    expect(point).toMatchObject({ kind: 'range', score: 82, window: { niceDays: 10, awfulDays: 1, longestPoorRun: 2 } });
    expect('metrics' in point).toBe(false);
  });

  it('rejects a range point whose dates do not match the loaded manifest', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      releaseId: 'release-20260718', periodId: 'next-14-days', periodLabel: 'Next 14 days', profile: 'nice',
      location: { lat: 39.47, lon: -0.38 },
      score: { value: 82, category: 'Very good', components: componentFixture(84, 91, 77, 74) },
      window: { startDate: '2026-07-19', endDate: '2026-08-01', dayCount: 14, niceDays: 10, awfulDays: 1, medianScore: 85, longestPoorRun: 2 },
      source: { basis: 'forecast-window', detailLabel: '14-day forecast', resolutionKm: 28, artifactGeneratedAt: '2026-07-18T10:00:00Z' },
    }), { status: 200 })));
    const expected = { startDate: '2026-07-18', endDate: '2026-07-31', dayCount: 14 } as const;
    await expect(loadPoint({ releaseId: 'release-20260718', lat: 39.47, lon: -0.38, period: 'next-14-days', profile: 'nice' }, undefined, expected)).rejects.toThrow('could not be read');
  });

  it('parses daily physical weather and source evidence', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      releaseId: 'release-20260718', periodId: '2026-07-18', periodLabel: 'Saturday 18 July', profile: 'cycling',
      location: { lat: 39.47, lon: -0.38 }, summary: 'Dry, bright and calm.',
      score: { value: 88, category: 'Excellent', components: componentFixture(91, 100, 82, 79) },
      weather: { temperatureC: 25, minTempC: 20, maxTempC: 29, rainMm: 0, wetTimeFraction: 0, cloudPct: 18, solarEnergyMJm2: 19.2, windKmh: 11, gustKmh: 18 },
      source: { basis: 'forecast', detailLabel: 'Detailed forecast', providerLabel: 'ICON-EU', resolutionKm: 7, temporalResolutionHours: 1, artifactGeneratedAt: '2026-07-18T10:00:00Z', runAt: '2026-07-18T06:00:00Z', validFrom: '2026-07-18T10:00:00Z', validTo: '2026-07-18T20:00:00Z' },
    }), { status: 200 })));
    const point = await loadPoint({ releaseId: 'release-20260718', lat: 39.47, lon: -0.38, period: '2026-07-18', profile: 'cycling' });
    expect(point).toMatchObject({ kind: 'daily', score: 88, metrics: { maxTempC: 29, rainMm: 0 }, source: { basis: 'forecast', providerLabel: 'ICON-EU' } });
  });

  it('keeps the climate reference period supplied by the artifact', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      releaseId: 'release-20260718', periodId: 'month-11', periodLabel: 'November', profile: 'nice',
      location: { lat: 36.72, lon: -4.42 }, summary: 'Warm and dry.',
      score: { value: 82, category: 'Very good', components: componentFixture(84, 91, 77, 74) },
      weather: { temperatureC: 22, minTempC: 17, maxTempC: 25, rainMm: 1.2, cloudPct: 24, windKmh: 10 },
      source: { basis: 'climate', detailLabel: 'Typical month', providerLabel: 'CHELSA 2.1', resolutionKm: 11, temporalResolutionHours: 744, artifactGeneratedAt: '2026-07-18T10:00:00Z', referencePeriod: '1981–2010' },
    }), { status: 200 })));

    const point = await loadPoint({ releaseId: 'release-20260718', lat: 36.72, lon: -4.42, period: 'month-11', profile: 'nice' });
    expect(point).toMatchObject({ kind: 'daily', source: { basis: 'climate', referencePeriod: '1981–2010' } });
  });

  it('returns a typed stale release error for a conflict', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ error: { code: 'stale_release', message: 'changed' } }), { status: 409 })));
    await expect(loadPoint({ releaseId: 'release-old', lat: 52.37, lon: 4.9, period: 'next-14-days', profile: 'nice' })).rejects.toBeInstanceOf(StaleReleaseError);
  });

  it('rejects a point response for a different period or profile', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      releaseId: 'release-20260718', periodId: 'next-14-days', periodLabel: 'Next 14 days', profile: 'cycling',
      location: { lat: 39.47, lon: -0.38 },
      score: { value: 82, category: 'Very good', components: componentFixture(84, 91, 77, 74) },
      window: { startDate: '2026-07-18', endDate: '2026-07-31', dayCount: 14, niceDays: 10, awfulDays: 1, medianScore: 85, longestPoorRun: 2 },
      source: { basis: 'forecast-window', detailLabel: '14-day forecast', resolutionKm: 28, artifactGeneratedAt: '2026-07-18T10:00:00Z' },
    }), { status: 200 })));

    await expect(loadPoint({ releaseId: 'release-20260718', lat: 39.47, lon: -0.38, period: 'next-14-days', profile: 'nice' })).rejects.toThrow('could not be read');
  });

  it('rejects an impossible split of awful days into a shorter poor run', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      releaseId: 'release-20260718', periodId: 'next-14-days', periodLabel: 'Next 14 days', profile: 'nice',
      location: { lat: 39.47, lon: -0.38 },
      score: { value: 22, category: 'Awful', components: componentFixture(20, 20, 20, 20) },
      window: { startDate: '2026-07-18', endDate: '2026-07-31', dayCount: 14, niceDays: 0, awfulDays: 14, medianScore: 20, longestPoorRun: 1 },
      source: { basis: 'forecast-window', detailLabel: '14-day forecast', resolutionKm: 28, artifactGeneratedAt: '2026-07-18T10:00:00Z' },
    }), { status: 200 })));

    await expect(loadPoint({ releaseId: 'release-20260718', lat: 39.47, lon: -0.38, period: 'next-14-days', profile: 'nice' })).rejects.toThrow('could not be read');
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

function manifestFixture(): Record<string, any> {
  const period = (id: string, extra: Record<string, unknown>) => ({
    id, label: id,
    source: { providerId: 'test', providerLabel: 'Test forecast', runAt: '2026-07-18T06:00:00Z', validFrom: '2026-07-18T10:00:00Z', validTo: '2026-07-18T20:00:00Z' },
    nativeResolutionKm: 7, temporalResolutionHours: 1,
    images: { nice: image(`${id}-nice`), cycling: image(`${id}-cycling`) },
    lenses: { comfort: image(`${id}-comfort`), dry: image(`${id}-dry`), bright: image(`${id}-bright`), 'wind-nice': image(`${id}-wind-nice`), 'wind-cycling': image(`${id}-wind-cycling`) },
    gridUrl: `grids/${id}.nwg`, gridSha256: digest(`${id}-grid`), ...extra,
  });
  const days = Array.from({ length: 14 }, (_, index) => {
    const date = `2026-07-${String(18 + index).padStart(2, '0')}`;
    return period(date, { date, kind: index < 5 ? 'detailed' : 'outlook' });
  });
  return {
    version: 5, releaseId: 'release-20260718', productContract: 'nice-weather-v10', dataStatus: 'sample', generatedAt: '2026-07-18T10:00:00Z',
    region: { id: 'europe', label: 'Europe', bounds: { west: -25, east: 45, south: 30, north: 72 } },
    profiles: [{ id: 'nice', label: 'Nice' }, { id: 'cycling', label: 'Cycling' }],
    legend: [
      { label: 'Awful', color: '#343B53', min: 0, max: 24 }, { label: 'Poor', color: '#5E6382', min: 25, max: 39 },
      { label: 'Mixed', color: '#97949C', min: 40, max: 54 }, { label: 'Good', color: '#C9A47F', min: 55, max: 69 },
      { label: 'Very good', color: '#E18952', min: 70, max: 84 }, { label: 'Excellent', color: '#F06031', min: 85, max: 100 },
    ],
    palette: Array.from({ length: 12 }, (_, index) => ({ score: index === 11 ? 100 : index * 9, color: `#${String(index + 1).repeat(6).slice(0, 6)}` })),
    forecast: {
      updatedAt: '2026-07-18T09:30:00Z', staleAfterHours: 12,
      window: { id: 'next-14-days', label: 'Next 14 days', startDate: '2026-07-18', endDate: '2026-07-31', dayCount: 14, gridResolutionKm: 28, profiles: {
        nice: { gridUrl: 'grids/window-nice.nwr', gridSha256: digest('window-nice-grid'), image: image('window-nice') },
        cycling: { gridUrl: 'grids/window-cycling.nwr', gridSha256: digest('window-cycling-grid'), image: image('window-cycling') },
      } },
      days,
    },
    climate: { label: 'Typical year', referencePeriod: '1981–2010', months: Array.from({ length: 12 }, (_, index) => period(`month-${index + 1}`, { label: new Date(2026, index, 1).toLocaleString('en-GB', { month: 'long' }), month: index + 1 })) },
  };
}

function image(id: string) { return { url: `images/${id}.png`, sha256: digest(id), width: 900, height: 600, projection: 'web-mercator' as const }; }
function digest(value: string) { return Array.from(value).reduce((total, char) => (total * 31 + char.charCodeAt(0)) >>> 0, 0).toString(16).padStart(8, '0').repeat(8); }
function componentFixture(comfort: number, dryness: number, sun: number, wind: number) {
  const score = (value: number) => ({ value, category: value >= 85 ? 'Excellent' : value >= 70 ? 'Very good' : value >= 55 ? 'Good' : value >= 40 ? 'Mixed' : value >= 25 ? 'Poor' : 'Awful' });
  return { comfort: score(comfort), dryness: score(dryness), sun: score(sun), wind: score(wind) };
}
