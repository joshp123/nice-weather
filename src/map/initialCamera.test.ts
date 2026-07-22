import { describe, expect, it, vi } from 'vitest';
import { drawLand, viewportBounds, visibleTiles } from './canvasFallback';
import { canonicalWorldRect, constrainWorldwideCamera, initialWorldwideCamera, minimumWorldwideCoverZoom } from './initialCamera';

describe('initial worldwide camera', () => {
  const viewports = [
    [393, 852, -35.522, 54.522, 78, -55],
    [430, 932, -35.532, 54.532, 78, -55],
    [1366, 768, -173.604, 173.604, 78, -55],
    [1728, 1117, -150.994, 150.994, 78, -55],
    [2560, 1440, -173.519, 173.519, 78, -55],
    // A 4096:1440 Mercator view cannot contain the full reference latitude
    // band and remain full-bleed without repeating the world. It intentionally
    // crops remote north/south edges while keeping every longitude pannable.
    [4096, 1440, -180, 180, 68.337, -30.291],
  ] as const;

  it('uses one full-bleed canonical world with the intentional crop at every required size', () => {
    for (const [width, height, west, east, north, south] of viewports) {
      const camera = initialWorldwideCamera(width, height);
      const bounds = viewportBounds(width, height, camera);
      const worldPixels = 512 * 2 ** camera.zoom;
      expect(worldPixels, `${width}x${height} horizontal cover`).toBeGreaterThanOrEqual(width - .001);
      expect(worldPixels, `${width}x${height} vertical cover`).toBeGreaterThanOrEqual(height - .001);
      expect(bounds.east - bounds.west, `${width}x${height} longitude span`).toBeLessThanOrEqual(360.001);
      expect(bounds.west, `${width}x${height} canonical west edge`).toBeGreaterThanOrEqual(-180.001);
      expect(bounds.east, `${width}x${height} canonical east edge`).toBeLessThanOrEqual(180.001);
      expect(bounds.west, `${width}x${height} visible west`).toBeCloseTo(west, 2);
      expect(bounds.east, `${width}x${height} visible east`).toBeCloseTo(east, 2);
      expect(bounds.north, `${width}x${height} visible north`).toBeCloseTo(north, 2);
      expect(bounds.south, `${width}x${height} visible south`).toBeCloseTo(south, 2);
      const world = canonicalWorldRect(camera, width, height);
      expect(world.x, `${width}x${height} canonical world left`).toBeLessThanOrEqual(.001);
      expect(world.x + world.size, `${width}x${height} canonical world right`).toBeGreaterThanOrEqual(width - .001);
    }
  });

  it('keeps phone entry on Europe and Africa and excludes Antarctica from normal desktop entry', () => {
    const phone = initialWorldwideCamera(393, 852);
    const phoneBounds = viewportBounds(393, 852, phone);

    expect(phone.longitude).toBe(9.5);
    expect(phone.latitude).toBeCloseTo(30, 2);
    expect(phone.zoom).toBeGreaterThan(1.5);
    expect(phoneBounds.west).toBeLessThan(-10);
    expect(phoneBounds.east).toBeGreaterThan(40);
    for (const [width, height] of viewports.slice(2, 5)) {
      const bounds = viewportBounds(width, height, initialWorldwideCamera(width, height));
      expect(bounds.south, `${width}x${height}`).toBeGreaterThan(-60);
    }
  });

  it('derives the minimum zoom that keeps the canonical world over the whole canvas', () => {
    for (const [width, height] of viewports) {
      const zoom = minimumWorldwideCoverZoom(width, height);
      const size = 512 * 2 ** zoom;
      expect(size).toBeGreaterThanOrEqual(width - .001);
      expect(size).toBeGreaterThanOrEqual(height - .001);
    }
  });

  it('clamps zoom-out and extreme pan to a single covering world at every required size', () => {
    for (const [width, height] of viewports) {
      const camera = constrainWorldwideCamera({ longitude: 720, latitude: 89, zoom: -4 }, width, height);
      const world = canonicalWorldRect(camera, width, height);
      expect(camera.zoom).toBeCloseTo(minimumWorldwideCoverZoom(width, height), 8);
      expect(world.x).toBeLessThanOrEqual(.001);
      expect(world.y).toBeLessThanOrEqual(.001);
      expect(world.x + world.size).toBeGreaterThanOrEqual(width - .001);
      expect(world.y + world.size).toBeGreaterThanOrEqual(height - .001);
      const bounds = viewportBounds(width, height, camera);
      expect(bounds.west).toBeGreaterThanOrEqual(-180.001);
      expect(bounds.east).toBeLessThanOrEqual(180.001);
    }
  });

  it('preserves useful interior pan and zoom while constraining only world edges', () => {
    for (const [width, height] of viewports) {
      const zoom = minimumWorldwideCoverZoom(width, height) + 2;
      const camera = constrainWorldwideCamera({ longitude: 35, latitude: 20, zoom }, width, height);
      expect(camera.longitude).toBeCloseTo(35, 8);
      expect(camera.latitude).toBeCloseTo(20, 8);
      expect(camera.zoom).toBeCloseTo(zoom, 8);
    }
  });

  it('restores cover after a large resize without resetting an owned camera', () => {
    const phone = initialWorldwideCamera(393, 852);
    const wide = constrainWorldwideCamera(phone, 4096, 1440);
    const wideWorld = canonicalWorldRect(wide, 4096, 1440);
    expect(wide.zoom).toBeGreaterThanOrEqual(3);
    expect(wideWorld.x).toBeLessThanOrEqual(.001);
    expect(wideWorld.x + wideWorld.size).toBeGreaterThanOrEqual(4096 - .001);

    const portraitAgain = constrainWorldwideCamera(wide, 430, 932);
    expect(portraitAgain.zoom).toBe(wide.zoom);
    expect(portraitAgain.longitude).toBeCloseTo(wide.longitude, 8);
  });

  it('draws and requests only one canonical canvas world', () => {
    const ultrawide = initialWorldwideCamera(4096, 1440);
    expect(ultrawide.longitude).toBe(0);
    expect(ultrawide.latitude).toBeCloseTo(30, 2);
    expect(ultrawide.zoom).toBeCloseTo(3, 6);
    const drawImage = vi.fn();
    drawLand({ drawImage } as unknown as CanvasRenderingContext2D, {} as CanvasImageSource, ultrawide, 4096, 1440);
    expect(drawImage).toHaveBeenCalledOnce();

    const tiles = visibleTiles(viewportBounds(4096, 1440, ultrawide), 3);
    expect(tiles.every(({ worldX }) => worldX >= 0 && worldX < 8)).toBe(true);
    expect(new Set(tiles.map(({ id }) => `${id.z}/${id.x}/${id.y}`)).size).toBe(tiles.length);
  });

  it('rejects an unusable map size', () => {
    expect(() => initialWorldwideCamera(0, 852)).toThrow('Map size must be positive.');
  });
});
