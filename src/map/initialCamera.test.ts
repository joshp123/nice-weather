import { describe, expect, it } from 'vitest';
import { initialWorldwideCamera, minimumWorldwideCoverZoom } from './initialCamera';

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


});

function viewportBounds(width: number, height: number, camera: ReturnType<typeof initialWorldwideCamera>) {
  const size = 512 * 2 ** camera.zoom;
  const centreY = .5 - Math.asinh(Math.tan(camera.latitude * Math.PI / 180)) / (2 * Math.PI);
  const world = { size, x: width / 2 - (camera.longitude + 180) / 360 * size, y: height / 2 - centreY * size };
  const latitude = (pixel: number) => Math.atan(Math.sinh(Math.PI * (1 - 2 * pixel / world.size))) * 180 / Math.PI;
  return { west: -world.x / world.size * 360 - 180, east: (width - world.x) / world.size * 360 - 180, north: latitude(-world.y), south: latitude(height - world.y) };
}
