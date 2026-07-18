export type GeographicBounds = [west: number, south: number, east: number, north: number];

const WEB_MERCATOR_MAX_LATITUDE = 85.0511287798066;

export function projectWebMercator(lon: number, lat: number, bounds: GeographicBounds, width: number, height: number) {
  const northY = mercatorY(bounds[3]);
  const southY = mercatorY(bounds[1]);
  return {
    x: (lon - bounds[0]) / (bounds[2] - bounds[0]) * width,
    y: (northY - mercatorY(lat)) / (northY - southY) * height,
  };
}

export function unprojectWebMercator(x: number, y: number, bounds: GeographicBounds, width: number, height: number) {
  const northY = mercatorY(bounds[3]);
  const southY = mercatorY(bounds[1]);
  const projectedY = northY - y / height * (northY - southY);
  return {
    lon: bounds[0] + x / width * (bounds[2] - bounds[0]),
    lat: Math.atan(Math.sinh(projectedY)) * 180 / Math.PI,
  };
}

// The embedded Natural Earth geometry was simplified in an equirectangular
// canvas. Reproject each absolute M/L vertex so the offline fallback, weather
// texture, labels and pointer coordinates share the same Web Mercator surface.
export function reprojectGeographicPath(
  path: string,
  sourceBounds: GeographicBounds,
  targetBounds: GeographicBounds,
  width: number,
  height: number,
) {
  return path.replace(/([ML])(-?\d+(?:\.\d+)?),(-?\d+(?:\.\d+)?)/g, (_match, command: string, xText: string, yText: string) => {
    const sourceX = Number(xText);
    const sourceY = Number(yText);
    const lon = sourceBounds[0] + sourceX / width * (sourceBounds[2] - sourceBounds[0]);
    const lat = sourceBounds[3] - sourceY / height * (sourceBounds[3] - sourceBounds[1]);
    const point = projectWebMercator(lon, lat, targetBounds, width, height);
    return `${command}${compact(point.x)},${compact(point.y)}`;
  });
}

function mercatorY(latitude: number) {
  const clamped = Math.max(-WEB_MERCATOR_MAX_LATITUDE, Math.min(WEB_MERCATOR_MAX_LATITUDE, latitude));
  return Math.asinh(Math.tan(clamped * Math.PI / 180));
}

function compact(value: number) {
  return Number(value.toFixed(2));
}
