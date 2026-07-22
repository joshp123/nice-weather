import { describe, expect, it } from 'vitest';
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { mergeDrawCache, runBounded, scalarCacheKey, scorePixels, screenToGeo, viewportBounds } from './canvasFallback';
import type { ScalarPaletteColour } from './scalarTileLayer';
import { WORLD_LAND_ASSET } from './worldLandAsset';

describe('canvas map fallback', () => {
  it('keeps no-data transparent and applies the canonical palette to scores', () => {
    const palette = Array.from({ length: 101 }, (_, score) => [score, 100 - score, 30, 255] as ScalarPaletteColour);
    const result = scorePixels(new Uint8Array([0, 50, 100, 255]), palette);
    expect(Array.from(result)).toEqual([
      0, 100, 30, 209,
      50, 50, 30, 209,
      100, 0, 30, 209,
      0, 0, 0, 0,
    ]);
  });

  it('maps the viewport centre to the selected longitude and latitude', () => {
    const view = { longitude: 12.5, latitude: 41.9, zoom: 4 };
    const centre = screenToGeo(720, 450, 1440, 900, view);
    expect(centre.lon).toBeCloseTo(12.5, 8);
    expect(centre.lat).toBeCloseTo(41.9, 8);
    const bounds = viewportBounds(1440, 900, view);
    expect(bounds.west).toBeLessThan(12.5);
    expect(bounds.east).toBeGreaterThan(12.5);
    expect(bounds.south).toBeLessThan(41.9);
    expect(bounds.north).toBeGreaterThan(41.9);
  });

  it('binds the local fallback map to the pinned Natural Earth authority', async () => {
    const bytes = await readFile(new URL('../../public/world-land.png', import.meta.url));
    expect(createHash('sha256').update(bytes).digest('hex')).toBe(WORLD_LAND_ASSET.sha256);
    expect(WORLD_LAND_ASSET).toMatchObject({
      width: 2048,
      height: 2048,
      projection: 'EPSG:3857',
      source: {
        dataset: 'Natural Earth 5.1.1 ne_10m_land',
        archiveSha256: 'e547d749445eaa0964aba76738090ec88f5e63c4585122170f98c67a7ea922dc',
        licence: 'public domain',
      },
    });
  });

  it('keeps scalar cache identities separate across overlays and releases', () => {
    const source = { key: '3/4/2', url: '/climate/releases/january/tile.nws', sha256: 'a'.repeat(64) };
    expect(scalarCacheKey('climate:january:overall', source)).not.toBe(scalarCacheKey('climate:february:overall', source));
    expect(scalarCacheKey('climate:january:overall', source)).not.toBe(scalarCacheKey('climate:january:overall', { ...source, url: '/climate/releases/new/tile.nws', sha256: 'b'.repeat(64) }));
  });

  it('bounds concurrent tile work and propagates any tile failure', async () => {
    const controller = new AbortController();
    let active = 0, maximum = 0;
    const values = await runBounded([1, 2, 3, 4, 5, 6, 7], 3, controller.signal, async (value) => {
      active += 1; maximum = Math.max(maximum, active);
      await new Promise((resolve) => setTimeout(resolve, 1));
      active -= 1;
      return value * 2;
    });
    expect(values).toEqual([2, 4, 6, 8, 10, 12, 14]);
    expect(maximum).toBe(3);
    await expect(runBounded([1, 2], 2, controller.signal, async (value) => {
      if (value === 2) throw new Error('bad checksum');
      return value;
    })).rejects.toThrow('bad checksum');
  });

  it('keeps every tile needed by the current draw when a full cache gains a miss', () => {
    const cache = new Map([['visible-hit', 1], ['old', 2]]);
    const draw = mergeDrawCache(cache, ['visible-hit', 'new'], [['new', 3]], 2);
    expect([...draw.entries()]).toEqual([['visible-hit', 1], ['new', 3]]);
    expect(cache.has('visible-hit')).toBe(false);
    expect([...cache.entries()]).toEqual([['old', 2], ['new', 3]]);
  });
});
