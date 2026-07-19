import type { SymbolLayerSpecification } from 'maplibre-gl';
import type { WindGrid } from '../data/wind';

type Point = { x: number; y: number };
type Project = (coordinate: [number, number]) => Point;
type ViewBounds = { west: number; east: number; south: number; north: number };

export type WindFeatureCollection = {
  type: 'FeatureCollection';
  features: Array<{
    type: 'Feature';
    geometry: { type: 'Point'; coordinates: [number, number] };
    properties: { rotation: number; speed: number };
  }>;
};

export function windFeatures(grid: WindGrid, viewport: ViewBounds, project: Project, targetSpacing = 42, maximum = 2_500): WindFeatureCollection {
  const [west, south, east, north] = grid.bounds;
  if (viewport.east < west || viewport.west > east || viewport.north < south || viewport.south > north) return { type: 'FeatureCollection', features: [] };
  const cellWidth = (east - west) / grid.cols;
  const cellHeight = (north - south) / grid.rows;
  const centreLon = Math.max(west, Math.min(east, (viewport.west + viewport.east) / 2));
  const centreLat = Math.max(south, Math.min(north, (viewport.south + viewport.north) / 2));
  const origin = project([centreLon, centreLat]);
  const horizontal = project([Math.min(east, centreLon + cellWidth), centreLat]);
  const vertical = project([centreLon, Math.min(north, centreLat + cellHeight)]);
  const cellPixels = Math.max(0.1, Math.min(Math.abs(horizontal.x - origin.x), Math.abs(vertical.y - origin.y)));
  let stride = Math.max(1, Math.ceil(targetSpacing / cellPixels));
  const minCol = Math.max(0, Math.floor((viewport.west - west) / cellWidth));
  const maxCol = Math.min(grid.cols - 1, Math.floor((viewport.east - west) / cellWidth));
  const minRow = Math.max(0, Math.floor((north - viewport.north) / cellHeight));
  const maxRow = Math.min(grid.rows - 1, Math.floor((north - viewport.south) / cellHeight));
  const estimate = () => Math.ceil((maxCol - minCol + 1) / stride) * Math.ceil((maxRow - minRow + 1) / stride);
  if (estimate() > maximum) stride *= Math.ceil(Math.sqrt(estimate() / maximum));
  const firstCol = Math.ceil(minCol / stride) * stride;
  const firstRow = Math.ceil(minRow / stride) * stride;
  const features: WindFeatureCollection['features'] = [];
  for (let row = firstRow; row <= maxRow; row += stride) {
    for (let col = firstCol; col <= maxCol; col += stride) {
      const index = (row * grid.cols + col) * 2;
      const u = grid.vectors[index]; const v = grid.vectors[index + 1];
      if (u === -128 || v === -128) continue;
      const speed = Math.hypot(u, v) * 0.5;
      if (speed < 0.75) continue;
      features.push({
        type: 'Feature',
        geometry: { type: 'Point', coordinates: [west + (col + 0.5) * cellWidth, north - (row + 0.5) * cellHeight] },
        properties: { rotation: Math.atan2(u, v) * 180 / Math.PI, speed },
      });
    }
  }
  return { type: 'FeatureCollection', features };
}

export function baseOverlayBefore(layers: Array<{ id: string; type: string }>): string | undefined {
  return layers.find((layer) => layer.type === 'line' && /boundary|border|admin/i.test(layer.id))?.id
    ?? layers.find((layer) => layer.type === 'symbol')?.id;
}

export function scoreOverlayBefore(layers: Array<{ id: string; type: string }>): string | undefined {
  return layers.some((layer) => layer.id === 'weather-wind-direction') ? 'weather-wind-direction' : baseOverlayBefore(layers);
}

export function windLayerLayout(): NonNullable<SymbolLayerSpecification['layout']> {
  return {
    'icon-image': 'weather-wind-arrow',
    'icon-rotate': ['get', 'rotation'],
    'icon-rotation-alignment': 'map',
    'icon-size': ['interpolate', ['linear'], ['get', 'speed'], 1, 0.78, 20, 1.08],
    'icon-allow-overlap': false,
    // Direction marks never reserve collision space. Place and country labels
    // remain authoritative even though their layers are drawn afterward.
    'icon-ignore-placement': true,
  };
}
