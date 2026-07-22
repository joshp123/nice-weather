import { afterEach, describe, expect, it, vi } from 'vitest';
import { loadBestWeather } from './bestWeather';
import { createHash, webcrypto } from 'node:crypto';

const results = Array.from({ length: 6 }, (_, index) => ({
  geonameId: 2514256 + index, name: `Place ${index + 1}`, admin1: 'Andalusia', country: 'Spain', countryCode: 'ES',
  latitude: 36.72016 + index, longitude: -4.42034, score: 95 - index, niceDays: 14,
}));
const valid = {
  schema: 'nice-best-weather-world-v1', forecastIndexId: 'forecast-1', placeReleaseId: 'places-1', startDate: '2026-07-19', endDate: '2026-08-01', forecastRunUtc: '2026-07-19T12:00:00Z',
  profiles: { nice: results, cycling: results },
};
const forecast = { indexId: 'forecast-1', firstDate: valid.startDate, lastDate: valid.endDate, runUtc: valid.forecastRunUtc };

afterEach(() => vi.unstubAllGlobals());

describe('loadBestWeather', () => {
  it('accepts release-bound canonical weather evidence', async () => {
    vi.stubGlobal('crypto', webcrypto);
    const payload = JSON.stringify(valid);
    const digest = createHash('sha256').update(payload).digest('hex');
    const fetcher = vi.fn(async (path: string) => path === '/best-weather/current.json'
      ? new Response(JSON.stringify({ schema: 'nice-best-weather-world-v1', forecastIndexId: 'forecast-1', placeReleaseId: 'places-1', url: '/best-weather/releases/forecast-1-world-a.json', sha256: digest }), { status: 200 })
      : path === '/places/current.json' ? new Response(JSON.stringify({ releaseId: 'places-1', manifestSha256: 'a'.repeat(64) }), { status: 200 })
      : new Response(payload, { status: 200 }));
    vi.stubGlobal('fetch', fetcher);
    await expect(loadBestWeather(forecast, 'nice')).resolves.toEqual({ forecastIndexId: 'forecast-1', placeReleaseId: 'places-1', profile: 'nice', startDate: valid.startDate, endDate: valid.endDate, forecastRunUtc: valid.forecastRunUtc, results });
    expect(fetcher).toHaveBeenNthCalledWith(1, '/best-weather/current.json', { signal: undefined, cache: 'no-cache' });
  });

  it('rejects stale, corrupt and impossible responses', async () => {
    vi.stubGlobal('crypto', webcrypto);
    for (const broken of [
      { ...valid, forecastIndexId: 'other' },
      { ...valid, profiles: { ...valid.profiles, nice: results.map((item, index) => index ? item : { ...item, score: 101 }) } },
      { ...valid, profiles: { ...valid.profiles, cycling: undefined } },
      { ...valid, profiles: { ...valid.profiles, nice: [] } },
      { ...valid, profiles: { ...valid.profiles, nice: results.slice(0, 5) } },
      { ...valid, forecastRunUtc: '19 July 2026' },
      { ...valid, endDate: '2026-07-31' },
      { ...valid, profiles: { ...valid.profiles, extra: results } },
      { ...valid, profiles: { ...valid.profiles, nice: results.map((item, index) => index ? item : { ...item, population: 500_000 }) } },
      { ...valid, profiles: { ...valid.profiles, nice: results.map((item, index) => index === 1 ? results[0] : item) } },
      { ...valid, profiles: { ...valid.profiles, nice: results.map((item, index) => index === 1 ? { ...item, score: 99 } : item) } },
    ]) {
      const payload = JSON.stringify(broken);
      const digest = createHash('sha256').update(payload).digest('hex');
      vi.stubGlobal('fetch', vi.fn(async (path: string) => path === '/best-weather/current.json'
        ? new Response(JSON.stringify({ schema: 'nice-best-weather-world-v1', forecastIndexId: 'forecast-1', placeReleaseId: 'places-1', url: '/best-weather/releases/forecast-1-world-a.json', sha256: digest }), { status: 200 })
        : path === '/places/current.json' ? new Response(JSON.stringify({ releaseId: 'places-1', manifestSha256: 'a'.repeat(64) }), { status: 200 })
        : new Response(payload, { status: 200 })));
      await expect(loadBestWeather(forecast, 'nice')).rejects.toThrow('could not be read');
    }
  });

  it('rejects a recommendation built from a stale place release', async () => {
    vi.stubGlobal('crypto', webcrypto);
    const payload = JSON.stringify(valid);
    const digest = createHash('sha256').update(payload).digest('hex');
    vi.stubGlobal('fetch', vi.fn(async (path: string) => path === '/best-weather/current.json'
      ? new Response(JSON.stringify({ schema: 'nice-best-weather-world-v1', forecastIndexId: 'forecast-1', placeReleaseId: 'places-1', url: '/best-weather/releases/forecast-1-world-a.json', sha256: digest }), { status: 200 })
      : path === '/places/current.json'
        ? new Response(JSON.stringify({ releaseId: 'places-2', manifestSha256: 'a'.repeat(64) }), { status: 200 })
        : new Response(payload, { status: 200 })));

    await expect(loadBestWeather(forecast, 'nice')).rejects.toThrow('could not be read');
  });
});
