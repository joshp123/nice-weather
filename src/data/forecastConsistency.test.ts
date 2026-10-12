import { readFileSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { WorldwideForecastClient } from './forecastClient';
import { WEATHER_PRESENTATION } from '../weatherPresentation';

const artifacts = resolve('artifacts');
const pointerPath = resolve(artifacts, 'forecast/current.json');
const hasCurrentForecast = existsSync(pointerPath);

function servedArtifact(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const url = String(input);
  const bytes = new Uint8Array(readFileSync(resolve(artifacts, url.slice(1))));
  const binary = url.endsWith('.nwt') || url.endsWith('.nwr');
  const media = url.endsWith('.nwt') ? 'application/vnd.nice-weather.daily-point-block' : 'application/vnd.nice-weather.forecast-range-block';
  const headers = { 'Content-Type': binary ? media : 'application/json', ...(binary ? { 'Content-Range': `bytes 0-${bytes.length - 1}/${bytes.length}` } : {}) };
  if (binary) expect(new Headers(init?.headers).get('Range')).toBe(`bytes=0-${bytes.length - 1}`);
  return Promise.resolve(new Response(bytes, { status: binary ? 206 : 200, headers }));
}

describe.skipIf(!hasCurrentForecast)('Choosing days from the published fortnight', () => {
  it('loads Rotterdam physical evidence once and reuses it when the place is selected again', async () => {
    const paths: string[] = [];
    const client = new WorldwideForecastClient((input, init) => {
      paths.push(String(input));
      return servedArtifact(input, init);
    });
    const snapshot = await client.loadSnapshot();
    const presentation = { legend: WEATHER_PRESENTATION.legend, generatedAt: snapshot.range.source.runUtc };
    paths.length = 0;
    const physical = await client.loadPeriodEvidence(snapshot, 4.4792, 51.9225, 'nice', presentation);
    expect(physical).toHaveLength(14);
    expect(paths.filter((path) => path === '/forecast/current.json')).toHaveLength(1);
    expect(paths.filter((path) => path.endsWith('/catalog.json'))).toHaveLength(14);
    expect(paths.filter((path) => path.endsWith('.nwt'))).toHaveLength(14);
    expect(physical.map((point) => point.source.resolutionKm)).toEqual(snapshot.index.days.map((day) => day.tiers.some((tier) => tier.tier === 'europe-detail') ? 7 : 28));
    paths.length = 0;
    await expect(client.loadPeriodEvidence(snapshot, 4.4792, 51.9225, 'nice', presentation)).resolves.toEqual(physical);
    expect(paths).toEqual(['/forecast/current.json']);
  }, 30_000);

  it('rejects the entire physical summary when the published release changes during its reads', async () => {
    let changed = false;
    const client = new WorldwideForecastClient(async (input, init) => {
      const response = await servedArtifact(input, init);
      if (String(input).endsWith('.nwt')) changed = true;
      if (changed && String(input) === '/forecast/current.json') {
        const pointer = await response.json();
        pointer.current.indexId = 'replacement-release';
        pointer.current.path = 'runs/replacement-release/index.json';
        const body = JSON.stringify(pointer);
        return new Response(body, { headers: { 'Content-Type': 'application/json', 'Content-Length': String(new TextEncoder().encode(body).length) } });
      }
      return response;
    });
    const snapshot = await client.loadSnapshot();
    await expect(client.loadPeriodEvidence(snapshot, 4.4792, 51.9225, 'nice', { legend: WEATHER_PRESENTATION.legend, generatedAt: snapshot.range.source.runUtc })).rejects.toMatchObject({ code: 'stale' });
  }, 30_000);

  for (const place of [
    { name: 'Rotterdam', longitude: 4.4792, latitude: 51.9225, regional: true },
    { name: 'Barcelona', longitude: 2.1734, latitude: 41.3851, regional: true },
    { name: 'Cape Town', longitude: 18.4241, latitude: -33.9249, regional: false },
    { name: 'Málaga', longitude: -4.4214, latitude: 36.7213, regional: true },
    { name: 'Cádiz', longitude: -6.2994, latitude: 36.5298, regional: false },
  ]) {
    it(`${place.name}: each opened Nice and Cycling day agrees with its fortnight strip`, async () => {
      const client = new WorldwideForecastClient(servedArtifact);
      const snapshot = await client.loadSnapshot();
      const presentation = { legend: WEATHER_PRESENTATION.legend, generatedAt: snapshot.range.source.runUtc };
      for (const profile of ['nice', 'cycling'] as const) {
        const fortnight = await client.loadRangePoint(snapshot, place.longitude, place.latitude, profile, presentation,undefined,true);
        const longitude=fortnight.sourceCell?.longitude ?? place.longitude, latitude=fortnight.sourceCell?.latitude ?? place.latitude;
        const physical = await client.loadPeriodEvidence(snapshot, longitude, latitude, profile, presentation);
        for (const [dayIndex, entry] of fortnight.days.entries()) {
          expect(physical[dayIndex].score).toBe(entry.score);
          expect(physical[dayIndex].releaseId).toBe(fortnight.releaseId);
          const product = await client.loadDayCatalog(snapshot, entry.date);
          const opened = await client.loadDailyPoint(product, longitude, latitude, profile, presentation);
          expect(opened.score, `${place.name} ${profile} ${entry.date}`).toBe(entry.score);
          expect(opened.category).toBe(entry.category);
          expect(opened.releaseId).toBe(fortnight.releaseId);
          const hasRegionalDay = product.tiers.some(({ tier }) => tier === 'europe-detail');
          if (place.name !== 'Cádiz') expect(opened.source.resolutionKm).toBe(place.regional && hasRegionalDay ? 7 : 28);
          if (fortnight.sourceCell) expect(fortnight.sourceCell.distanceKm).toBeLessThanOrEqual(30);
        }
      }
    }, 30_000);
  }
});
