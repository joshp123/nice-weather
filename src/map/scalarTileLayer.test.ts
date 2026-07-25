import { describe, expect, it } from 'vitest';
import {
  boundedCoveringTiles,
  coveringTiles,
  FRAGMENT_SOURCE,
  isMercatorProjection,
  readScalarResponse,
  scalarTextureBytes,
  SCALAR_MEDIA_TYPE,
  SCALAR_FALLBACK,
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
    scores[3] = SCALAR_FALLBACK;
    expect(() => validateScores(scores)).toThrow(/invalid score 254/);
    expect(() => validateScores(scores, true)).not.toThrow();
    scores[3] = 101;
    expect(() => validateScores(scores)).toThrow(/invalid score 101/);
    expect(() => validateScores(new Uint8Array(256 * 256))).toThrow(/want 66564/);
  });

  it('accepts the server-faithful browser shape for gzip scalar tiles', async () => {
    const scores = new Uint8Array(scalarTextureBytes()).fill(SCALAR_NO_DATA);
    scores[0] = 72;
    const response = new Response(scores, { headers: {
      'Content-Type': SCALAR_MEDIA_TYPE,
      'Content-Encoding': 'gzip',
      'Content-Length': '391',
    } });

    await expect(readScalarResponse(response)).resolves.toEqual(scores);
  });

  it('rejects unsupported scalar encodings and oversized decoded gzip bodies', async () => {
    const valid = new Uint8Array(scalarTextureBytes()).fill(SCALAR_NO_DATA);
    const brotli = new Response(valid, { headers: {
      'Content-Type': SCALAR_MEDIA_TYPE,
      'Content-Encoding': 'br',
      'Content-Length': '100',
    } });
    await expect(readScalarResponse(brotli)).rejects.toThrow(/unsupported content encoding/);

    const oversized = new Response(new Uint8Array(scalarTextureBytes() + 1), { headers: {
      'Content-Type': SCALAR_MEDIA_TYPE,
      'Content-Encoding': 'gzip',
      'Content-Length': '100',
    } });
    await expect(readScalarResponse(oversized)).rejects.toThrow(/unexpectedly large/);
  });

  it('requires an exact identity scalar length before reading', async () => {
    const response = new Response(new Uint8Array(scalarTextureBytes()), { headers: {
      'Content-Type': SCALAR_MEDIA_TYPE,
      'Content-Length': String(scalarTextureBytes() - 1),
    } });
    await expect(readScalarResponse(response)).rejects.toThrow(/invalid content length/);
  });

  it('verifies the indexed scalar body digest', async () => {
    expect(await sha256Hex(new Uint8Array([1, 2, 3]))).toBe('039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81');
    expect(validSHA256('039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81')).toBe(true);
    expect(validSHA256('')).toBe(false);
    expect(validSHA256(undefined)).toBe(false);
  });

  it('uses centre-on-land coverage, interpolates inside land and falls back per fragment', () => {
    const interpolate = FRAGMENT_SOURCE.indexOf('weightedScore / coverage');
    const palette = FRAGMENT_SOURCE.indexOf('texelFetch(u_palette');
    expect(interpolate).toBeGreaterThan(-1);
    expect(palette).toBeGreaterThan(interpolate);
    expect(FRAGMENT_SOURCE).toContain('if (score <= 100.0)');
    expect(FRAGMENT_SOURCE).toContain('centreScore == 254.0 ? 2 : 0');
    expect(FRAGMENT_SOURCE).toContain('if (state == 2 && u_has_fallback)');
    expect(FRAGMENT_SOURCE).toContain('if (state != 1) discard');
    expect(FRAGMENT_SOURCE).toContain('float alpha = colour.a;');
    expect(FRAGMENT_SOURCE).not.toContain('colour.a * coverage');
    expect(FRAGMENT_SOURCE).toContain('u_texture_bounds');
    expect(FRAGMENT_SOURCE).toContain('u_fallback_texture_bounds');
    expect(FRAGMENT_SOURCE.indexOf('sourceUV')).toBeLessThan(interpolate);
  });
});
