import { describe, expect, it } from 'vitest';
import { findLayer, horizonsFor, validHorizon } from './manifest';
import type { Manifest } from './types';

const manifest: Manifest = {
  version: 1,
  generatedAt: 'test',
  sources: {
    forecast: { name: 'DWD ICON-EU', run: 'test', horizons: ['d1'] },
    climatology: { name: 'WorldClim 2.1', period: '1970-2000', horizons: ['jan'] },
  },
  layers: [
    { basis: 'forecast', profile: 'general', horizon: 'd1', tileUrl: '/forecast-d1.png' },
    { basis: 'climatology', profile: 'cycling', horizon: 'jan', tileUrl: '/climate-jan.png' },
  ],
  anchors: [],
  legend: { min: 0, max: 100, categories: [] },
};

describe('manifest helpers', () => {
  it('finds layers from manifest state only', () => {
    expect(findLayer(manifest, 'forecast', 'general', 'd1').tileUrl).toBe('/forecast-d1.png');
  });

  it('returns basis-specific horizons', () => {
    expect(horizonsFor(manifest, 'climatology')).toEqual(['jan']);
  });

  it('normalizes stale horizons when basis changes', () => {
    expect(validHorizon(['jan', 'feb'], 'd1')).toBe('jan');
    expect(validHorizon(['jan', 'feb'], 'feb')).toBe('feb');
  });
});
