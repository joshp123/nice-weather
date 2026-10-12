const TILE_SIZE = 512;
// This reference band keeps ordinary screens focused on useful inhabited
// latitudes. The camera is a cover, not a contain fit: very wide screens crop
// its north and south edges so one canonical world still fills the canvas.
const REFERENCE_NORTH = 78;
const REFERENCE_SOUTH = -55;

export type InitialCamera = Readonly<{
  longitude: number;
  latitude: number;
  zoom: number;
}>;

export function initialWorldwideCamera(width: number, height: number): InitialCamera {
  validateSize(width, height);
  const north = mercatorY(REFERENCE_NORTH);
  const south = mercatorY(REFERENCE_SOUTH);
  const latitude = inverseMercatorY((north + south) / 2);
  const verticalFit = Math.log2(height / (TILE_SIZE * (south - north)));
  const compact = width <= 680;
  const horizontalFit = Math.log2(width / TILE_SIZE);
  return {
    longitude: compact ? 9.5 : 0,
    latitude,
    zoom: Math.max(.5, Math.min(9, Math.max(horizontalFit, verticalFit))),
  };
}

export function minimumWorldwideCoverZoom(width: number, height: number): number {
  validateSize(width, height);
  const zoom = Math.max(.5, Math.log2(Math.max(width, height) / TILE_SIZE));
  if (zoom > 9) throw new RangeError('Map size exceeds the supported world cover.');
  return zoom;
}

function mercatorY(latitude: number): number {
  const radians = latitude * Math.PI / 180;
  return (1 - Math.asinh(Math.tan(radians)) / Math.PI) / 2;
}

function inverseMercatorY(y: number): number {
  return Math.atan(Math.sinh(Math.PI * (1 - 2 * y))) * 180 / Math.PI;
}

function validateSize(width: number, height: number): void {
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) throw new TypeError('Map size must be positive.');
}
