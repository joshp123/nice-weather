import { describe, expect, it } from 'vitest';
import { findLayer, forecastFreshness, forecastHorizonDetail, horizonsFor, validHorizon } from './manifest';
import type { Manifest } from './types';

const manifest: Manifest = {
  version: 1,
  generatedAt: 'test',
  sources: {
    forecast: {
      name: 'DWD ICON-EU',
      run: 'test',
      runStartedAt: '2026-05-05T06:00:00Z',
      staleAfterHours: 12,
      horizons: ['2026-05-05'],
      horizonDetails: [{ id: '2026-05-05', label: 'Tue 5 May', localDate: '2026-05-05', windowStart: '10:00', windowEnd: '20:00' }],
    },
    climatology: { name: 'WorldClim 2.1', period: '1970-2000', horizons: ['jan'] },
  },
  layers: [
    { basis: 'forecast', profile: 'general', horizon: '2026-05-05', tileUrl: '/forecast-today.png' },
    { basis: 'climatology', profile: 'cycling', horizon: 'jan', tileUrl: '/climate-jan.png' },
  ],
  anchors: [],
  legend: { min: 0, max: 100, categories: [] },
};

describe('manifest helpers', () => {
  it('finds layers from manifest state only', () => {
    expect(findLayer(manifest, 'forecast', 'general', '2026-05-05').tileUrl).toBe('/forecast-today.png');
  });

  it('returns basis-specific horizons', () => {
    expect(horizonsFor(manifest, 'climatology')).toEqual(['jan']);
  });

  it('normalizes stale horizons when basis changes', () => {
    expect(validHorizon(['jan', 'feb'], '2026-05-05')).toBe('jan');
    expect(validHorizon(['jan', 'feb'], 'feb')).toBe('feb');
  });

  it('returns forecast horizon detail by id', () => {
    expect(forecastHorizonDetail(manifest, '2026-05-05')?.windowStart).toBe('10:00');
  });

  it('marks old forecast runs as stale', () => {
    expect(forecastFreshness(manifest, new Date('2026-05-05T12:00:00Z')).state).toBe('fresh');
    expect(forecastFreshness(manifest, new Date('2026-05-06T00:01:00Z')).state).toBe('stale');
  });
});
