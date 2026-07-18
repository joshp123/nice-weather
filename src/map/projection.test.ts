import { describe, expect, it } from 'vitest';
import { projectWebMercator, reprojectGeographicPath, unprojectWebMercator } from './projection';

describe('Web Mercator fallback projection', () => {
  const bounds: [number, number, number, number] = [0, 0, 100, 80];

  it('puts a geographic point at the same normalized Mercator row as the delivery image', () => {
    const point = projectWebMercator(50, 40, bounds, 100, 400);
    expect(point.x).toBe(50);
    expect(point.y).toBeCloseTo(274.74, 1);
  });

  it('round-trips map pointer coordinates', () => {
    const point = projectWebMercator(23.73, 37.98, bounds, 700, 420);
    const location = unprojectWebMercator(point.x, point.y, bounds, 700, 420);
    expect(location.lon).toBeCloseTo(23.73, 10);
    expect(location.lat).toBeCloseTo(37.98, 10);
  });

  it('reprojects embedded geographic base geometry onto the same surface', () => {
    const path = reprojectGeographicPath('M0,0L50,50L100,100Z', bounds, bounds, 100, 100);
    expect(path).toBe('M0,0L50,68.69L100,100Z');
  });
});
