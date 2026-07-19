import { describe, expect, it } from 'vitest';
import type { WindGrid } from '../data/wind';
import { baseOverlayBefore, scoreOverlayBefore, windFeatures, windLayerLayout } from './windLayer';

const grid: WindGrid = {
  validAt: '2026-07-18T12:00:00Z',
  bounds: [0, 0, 2, 2],
  rows: 2,
  cols: 2,
  vectors: new Int8Array([2, 0, 0, 2, -128, -128, 1, 0]),
};

describe('wind direction map features', () => {
  it('uses cell centres, vector direction and the calm threshold', () => {
    const result = windFeatures(grid, { west: 0, east: 2, south: 0, north: 2 }, ([lon, lat]) => ({ x: lon * 100, y: lat * 100 }), 1);
    expect(result.features).toHaveLength(2);
    expect(result.features[0]).toMatchObject({ geometry: { coordinates: [0.5, 1.5] }, properties: { rotation: 90, speed: 1 } });
    expect(result.features[1]).toMatchObject({ geometry: { coordinates: [1.5, 1.5] }, properties: { rotation: 0, speed: 1 } });
  });

  it('stays empty outside the source grid and caps dense views', () => {
    expect(windFeatures(grid, { west: 4, east: 5, south: 4, north: 5 }, () => ({ x: 0, y: 0 })).features).toEqual([]);
    const dense: WindGrid = { ...grid, rows: 100, cols: 100, bounds: [0, 0, 100, 100], vectors: new Int8Array(20_000).fill(2) };
    expect(windFeatures(dense, { west: 0, east: 100, south: 0, north: 100 }, ([x, y]) => ({ x, y }), 1, 100).features.length).toBeLessThanOrEqual(100);
  });

  it('keeps score, direction marks and labels in that order', () => {
    const layers = [{ id: 'background', type: 'background' }, { id: 'admin-boundary', type: 'line' }, { id: 'place-label', type: 'symbol' }];
    expect(baseOverlayBefore(layers)).toBe('admin-boundary');
    expect(scoreOverlayBefore([...layers, { id: 'weather-wind-direction', type: 'symbol' }])).toBe('weather-wind-direction');
    expect(windLayerLayout()).toMatchObject({ 'icon-allow-overlap': false, 'icon-ignore-placement': true });
  });
});
