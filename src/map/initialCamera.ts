const TILE_SIZE = 512;
const MERCATOR_LIMIT = 85.0511287798066;
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

export function constrainWorldwideCamera(camera: InitialCamera, width: number, height: number): InitialCamera {
  const zoom = Math.min(9, Math.max(camera.zoom, minimumWorldwideCoverZoom(width, height)));
  const size = TILE_SIZE * 2 ** zoom;
  const longitude = Number.isFinite(camera.longitude) ? camera.longitude : 0;
  const latitude = Number.isFinite(camera.latitude) ? camera.latitude : 0;
  const centreX = clamp((longitude + 180) / 360 * size, width / 2, size - width / 2);
  const centreY = clamp(mercatorY(clamp(latitude, -MERCATOR_LIMIT, MERCATOR_LIMIT)) * size, height / 2, size - height / 2);
  return {
    longitude: centreX / size * 360 - 180,
    latitude: inverseMercatorY(centreY / size),
    zoom,
  };
}

export function canonicalWorldRect(camera: InitialCamera, width: number, height: number): { x: number; y: number; size: number } {
  const constrained = constrainWorldwideCamera(camera, width, height);
  const size = TILE_SIZE * 2 ** constrained.zoom;
  return {
    x: width / 2 - (constrained.longitude + 180) / 360 * size,
    y: height / 2 - mercatorY(constrained.latitude) * size,
    size,
  };
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

function clamp(value: number, minimum: number, maximum: number): number {
  return Math.max(minimum, Math.min(maximum, value));
}
