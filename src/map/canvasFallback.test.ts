import { describe, expect, it } from 'vitest';
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { mergeDrawCache, renderScorePixels, runBounded, sampleScoreFragment, scalarCacheKey, screenToGeo, viewportBounds } from './canvasFallback';
import type { ScalarPaletteColour } from './scalarTileLayer';
import { WORLD_LAND_ASSET } from './worldLandAsset';

describe('canvas map fallback', () => {
  it('keeps no-data transparent and applies the canonical palette to scores', () => {
    const palette = Array.from({ length: 101 }, (_, score) => [score, 100 - score, 30, 255] as ScalarPaletteColour);
    const scores = new Uint8Array(258 * 258).fill(255);
    scores[10 * 258 + 10] = 50;
    expect(sampleScoreFragment(scores, [0, 0, 1, 1], undefined, undefined, palette, (10 - 0.5) / 256, (10 - 0.5) / 256)).toEqual([50, 50, 30, 255]);
    expect(sampleScoreFragment(scores, [0, 0, 1, 1], undefined, undefined, palette, (11 - 0.5) / 256, (10 - 0.5) / 256)).toBeUndefined();
  });

  it('matches WebGL nearest-centre state at detail, fallback and water boundaries', () => {
    const detail = new Uint8Array(258 * 258).fill(255), global = new Uint8Array(258 * 258).fill(255);
    const at = (scores: Uint8Array, x: number, value: number) => { scores[10 * 258 + x] = value; };
    at(detail, 10, 80); at(detail, 11, 254); at(detail, 12, 255);
    at(global, 10, 40); at(global, 11, 40); at(global, 12, 40);
    const palette = Array.from({ length: 101 }, (_, score) => [score, score, score, 255] as ScalarPaletteColour);
    const v = (10 - 0.5) / 256;
    const sample = (x: number) => sampleScoreFragment(detail, [0, 0, 1, 1], global, [0, 0, 1, 1], palette, (x - 0.5) / 256, v);
    expect(sample(10)).toEqual([80, 80, 80, 255]);
    expect(sample(11)).toEqual([40, 40, 40, 255]);
    expect(sample(12)).toBeUndefined();
    expect(sampleScoreFragment(detail, [0, 0, 1, 1], global, [0, 0, 1, 1], palette, (10 - 0.001) / 256, v)).toEqual([80, 80, 80, 255]);
    expect(sampleScoreFragment(detail, [0, 0, 1, 1], global, [0, 0, 1, 1], palette, (10 + 0.001) / 256, v)).toEqual([40, 40, 40, 255]);
    expect(sampleScoreFragment(detail, [0, 0, 1, 1], global, [0, 0, 1, 1], palette, (11 + 0.001) / 256, v)).toBeUndefined();
    expect(sampleScoreFragment(detail, [0, 0, 1, 1], global, [0, 0, 1, 1], palette, -0.001, v)).toBeUndefined();
    expect(sampleScoreFragment(detail, [0, 0, 1, 1], global, [0, 0, 1, 1], palette, 1.001, v)).toBeUndefined();
  });

  it('pre-renders valid-neighbour score interpolation at canonical map resolution', () => {
    const scores = new Uint8Array(258 * 258).fill(255);
    scores[10 * 258 + 10] = 0;
    scores[10 * 258 + 11] = 100;
    const palette = Array.from({ length: 101 }, (_, score) => [score, score, score, 255] as ScalarPaletteColour);
    const pixels = renderScorePixels(scores, [0, 0, 1, 1], undefined, undefined, palette, 512);
    const colourAt = (x: number) => Array.from(pixels.subarray((19 * 512 + x) * 4, (19 * 512 + x + 1) * 4));
    expect(colourAt(19)).toEqual([25, 25, 25, 255]);
    expect(colourAt(20)).toEqual([75, 75, 75, 255]);
  });

  it('bounds cached interpolation error at representative fractional zoom and DPR', () => {
    const scores = new Uint8Array(258 * 258).fill(255);
    for (let y = 9; y <= 11; y += 1) { scores[y * 258 + 10] = 40; scores[y * 258 + 11] = 60; }
    const palette = Array.from({ length: 101 }, (_, score) => [score, score, score, 255] as ScalarPaletteColour);
    const cached = renderScorePixels(scores, [0, 0, 1, 1], undefined, undefined, palette, 512);
    const v = (10 - 0.5) / 256, cachedY = Math.floor(v * 512);
    const maxima: number[] = [];
    for (const framebufferTileSize of [327, 653, 861, 1722]) {
      let maximum = 0;
      for (let x = 0; x < framebufferTileSize; x += 1) {
        const u = (x + 0.5) / framebufferTileSize;
        const exact = sampleScoreFragment(scores, [0, 0, 1, 1], undefined, undefined, palette, u, v);
        const cachedX = Math.min(511, Math.floor(u * 512));
        const cachedValue = cached[(cachedY * 512 + cachedX) * 4];
        if (exact) maximum = Math.max(maximum, Math.abs(exact[0] - cachedValue));
      }
      expect(maximum).toBeLessThanOrEqual(5);
      maxima.push(maximum);
    }
    expect(maxima).toEqual([1, 5, 4, 5]);
  });

  it('keeps detail, fallback and water state exact when the cached raster scales', () => {
    const detail = new Uint8Array(258 * 258).fill(255), global = new Uint8Array(258 * 258).fill(40);
    for (let y = 9; y <= 11; y += 1) { detail[y * 258 + 10] = 80; detail[y * 258 + 11] = 254; }
    const palette = Array.from({ length: 101 }, (_, score) => [score, score, score, 255] as ScalarPaletteColour);
    const cached = renderScorePixels(detail, [0, 0, 1, 1], global, [0, 0, 1, 1], palette, 512);
    const v = (10 - 0.5) / 256, cachedY = Math.floor(v * 512);
    for (const framebufferTileSize of [327, 653, 861, 1722]) {
      for (let x = 0; x < framebufferTileSize; x += 1) {
        const u = (x + 0.5) / framebufferTileSize;
        const exact = sampleScoreFragment(detail, [0, 0, 1, 1], global, [0, 0, 1, 1], palette, u, v);
        const cachedX = Math.min(511, Math.floor(u * 512)), offset = (cachedY * 512 + cachedX) * 4;
        expect(cached[offset + 3] > 0).toBe(Boolean(exact));
        if (exact) expect(cached[offset]).toBe(exact[0]);
      }
    }
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
    expect(scalarCacheKey('climate:january:overall', source)).not.toBe(scalarCacheKey('climate:january:overall', { ...source, fallback: { key: 'fallback', url: '/forecast/global.nws', sha256: 'c'.repeat(64) } }));
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
