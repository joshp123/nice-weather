import { describe, expect, it } from 'vitest';
import {
  boundedCoveringTiles,
  coveringTiles,
  FRAGMENT_SOURCE,
  isMercatorProjection,
  scalarTextureBytes,
  SCALAR_MEDIA_TYPE,
  SCALAR_NO_DATA,
  SCALAR_TEXTURE_SIZE,
  validateScores,
  validSHA256,
  sha256Hex,
} from './scalarTileLayer';

describe('scalar tile layer', () => {
  it('uses exact XYZ coverage and shares one canonical tile across world wraps', () => {
    expect(coveringTiles({ west: -180, east: 180, south: -85, north: 85 }, 1)).toHaveLength(4);
    const dateline = coveringTiles({ west: 170, east: -170, south: -10, north: 10 }, 2);
    expect(dateline.map((tile) => [tile.id.x, tile.worldX])).toEqual([
      [3, 3], [0, 4],
      [3, 3], [0, 4],
    ]);
    expect(coveringTiles({ west: -540, east: 540, south: -10, north: 10 }, 0)).toEqual([
      { id: { z: 0, x: 0, y: 0 }, worldX: -1 },
      { id: { z: 0, x: 0, y: 0 }, worldX: 0 },
      { id: { z: 0, x: 0, y: 0 }, worldX: 1 },
    ]);
  });

  it('accepts MapLibre default projection state but rejects an explicit globe', () => {
    expect(isMercatorProjection(undefined)).toBe(true);
    expect(isMercatorProjection({})).toBe(true);
    expect(isMercatorProjection({ type: 'mercator' })).toBe(true);
    expect(isMercatorProjection({ type: 'globe' })).toBe(false);
  });

  it('returns no tiles when bounds lie wholly outside Mercator latitude', () => {
    expect(coveringTiles({ west: -10, east: 10, south: 86, north: 90 }, 2)).toEqual([]);
    expect(coveringTiles({ west: -10, east: 10, south: -90, north: -86 }, 2)).toEqual([]);
  });

  it('drops source zoom until an ultrawide viewport fits the texture cap', () => {
    const tiles = boundedCoveringTiles({ west: -36, east: 58, south: 20, north: 77 }, 9, 0, 7, 64);
    expect(new Set(tiles.map((tile) => tile.id.z))).toEqual(new Set([4]));
    expect(new Set(tiles.map((tile) => `${tile.id.x}/${tile.id.y}`)).size).toBeLessThanOrEqual(64);
  });

  it('defines a one-byte texture budget with no retained RGBA score tiles', () => {
    expect(SCALAR_TEXTURE_SIZE).toBe(258);
    expect(scalarTextureBytes()).toBe(66_564);
    expect(64 * scalarTextureBytes() + 101 * 4).toBe(4_260_500);
    expect(SCALAR_MEDIA_TYPE).toBe('application/vnd.nice-weather.score-tile');
  });

  it('accepts only canonical scores and the explicit no-data sentinel', () => {
    const scores = new Uint8Array(scalarTextureBytes()).fill(SCALAR_NO_DATA);
    scores[1] = 0;
    scores[2] = 100;
    expect(() => validateScores(scores)).not.toThrow();
    scores[3] = 101;
    expect(() => validateScores(scores)).toThrow(/invalid score 101/);
    expect(() => validateScores(new Uint8Array(256 * 256))).toThrow(/want 66564/);
  });

  it('verifies the indexed scalar body digest', async () => {
    expect(await sha256Hex(new Uint8Array([1, 2, 3]))).toBe('039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81');
    expect(validSHA256('039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81')).toBe(true);
    expect(validSHA256('')).toBe(false);
    expect(validSHA256(undefined)).toBe(false);
  });

  it('interpolates scalars before palette lookup and handles no-data as coverage', () => {
    const interpolate = FRAGMENT_SOURCE.indexOf('weightedScore / coverage');
    const palette = FRAGMENT_SOURCE.indexOf('texelFetch(u_palette');
    expect(interpolate).toBeGreaterThan(-1);
    expect(palette).toBeGreaterThan(interpolate);
    expect(FRAGMENT_SOURCE).toContain('if (score <= 100.0)');
    expect(FRAGMENT_SOURCE).toContain('if (coverage <= 0.0) discard');
  });
});
