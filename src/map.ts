// Written by AI on 2026.10.16 by model claude opus 5.5.
// A quiet dark basemap: land, the weather, then sea, borders and city names on top.
import maplibregl, { type StyleSpecification } from 'maplibre-gl';
import 'maplibre-gl/dist/maplibre-gl.css';
import { WeatherLayer } from './weatherLayer';
import type { Band } from './palette';

const labelText: maplibregl.ExpressionSpecification = ['coalesce', ['get', 'name:en'], ['get', 'name:latin'], ['get', 'name']];
const pointGeometry: maplibregl.ExpressionSpecification = ['match', ['geometry-type'], ['MultiPoint', 'Point'], true, false];

function placeLabel(id: string, filter: maplibregl.ExpressionSpecification, minzoom: number, sizes: [number, number, number, number]): maplibregl.LayerSpecification {
  return {
    id, type: 'symbol', source: 'openmaptiles', 'source-layer': 'place', minzoom, filter: ['all', pointGeometry, filter],
    layout: {
      'text-field': labelText,
      'text-font': ['Noto Sans Bold'],
      'text-size': ['interpolate', ['linear'], ['zoom'], sizes[0], sizes[1], sizes[2], sizes[3]],
      'text-max-width': 8,
      'text-padding': 6,
    },
    paint: { 'text-color': '#f4f6f8', 'text-halo-color': 'rgba(10, 13, 18, 0.9)', 'text-halo-width': 1.6 },
  };
}

const style: StyleSpecification = {
  version: 8,
  glyphs: 'https://tiles.openfreemap.org/fonts/{fontstack}/{range}.pbf',
  sources: { openmaptiles: { type: 'vector', url: 'https://tiles.openfreemap.org/planet' } },
  layers: [
    { id: 'land', type: 'background', paint: { 'background-color': '#3a3f47' } },
    { id: 'sea', type: 'fill', source: 'openmaptiles', 'source-layer': 'water', filter: ['!=', ['get', 'brunnel'], 'tunnel'], paint: { 'fill-color': '#0f1217' } },
    { id: 'state-borders', type: 'line', source: 'openmaptiles', 'source-layer': 'boundary', minzoom: 3, filter: ['all', ['==', ['get', 'admin_level'], 4], ['!=', ['get', 'maritime'], 1]], paint: { 'line-color': 'rgba(255,255,255,0.14)', 'line-width': 0.6 } },
    { id: 'country-borders', type: 'line', source: 'openmaptiles', 'source-layer': 'boundary', filter: ['all', ['==', ['get', 'admin_level'], 2], ['!=', ['get', 'maritime'], 1], ['!', ['has', 'claimed_by']]], paint: { 'line-color': 'rgba(255,255,255,0.32)', 'line-width': ['interpolate', ['linear'], ['zoom'], 1, 0.6, 6, 1.2] } },
    placeLabel('villages', ['==', ['get', 'class'], 'village'], 11, [11, 13, 14, 15]),
    placeLabel('towns', ['==', ['get', 'class'], 'town'], 7, [7, 13, 12, 16]),
    placeLabel('cities', ['all', ['==', ['get', 'class'], 'city'], ['>', ['get', 'rank'], 3]], 4.5, [4.5, 13, 10, 18]),
    placeLabel('large-cities', ['all', ['==', ['get', 'class'], 'city'], ['<=', ['get', 'rank'], 3]], 2, [2, 14, 8, 22]),
  ],
};

export type WeatherMap = Readonly<{
  map: maplibregl.Map;
  weather: WeatherLayer;
  /** Marks the chosen spot and writes its name beside it; the base map's own label there gives way. */
  showChosenPlace: (place: { latitude: number; longitude: number; name: string } | null) => void;
}>;

function ringImage(): ImageData {
  const size = 44;
  const canvas = new OffscreenCanvas(size, size);
  const context = canvas.getContext('2d')!;
  context.beginPath();
  context.arc(size / 2, size / 2, 12, 0, Math.PI * 2);
  context.lineWidth = 9;
  context.strokeStyle = 'rgba(0, 0, 0, 0.45)';
  context.stroke();
  context.lineWidth = 5;
  context.strokeStyle = '#ffffff';
  context.stroke();
  return context.getImageData(0, 0, size, size);
}

export function createWeatherMap(container: HTMLElement, bands: readonly Band[], camera: { center: [number, number]; zoom: number } | null): Promise<WeatherMap> {
  const map = new maplibregl.Map({
    container, style, center: camera?.center ?? [10, 25], zoom: camera?.zoom ?? 1.6, minZoom: 1.2, maxZoom: 11,
    attributionControl: { compact: true, customAttribution: 'Forecast © ECMWF · Climate: Copernicus ERA5' },
    dragRotate: false, pitchWithRotate: false, touchPitch: false,
  });
  map.touchZoomRotate.disableRotation();
  // First visit: show the inhabited world, not the empty Arctic.
  if (!camera) map.fitBounds([[-125, -45], [150, 62]], { animate: false });
  const weather = new WeatherLayer(bands);
  return new Promise((resolve) => {
    map.once('style.load', () => {
      map.addLayer(weather, 'sea');
      map.addImage('chosen-place', ringImage(), { pixelRatio: 2 });
      map.addSource('chosen-place', { type: 'geojson', data: { type: 'FeatureCollection', features: [] } });
      map.addLayer({
        id: 'chosen-place', type: 'symbol', source: 'chosen-place',
        layout: {
          'icon-image': 'chosen-place', 'icon-allow-overlap': true,
          'text-field': ['get', 'name'], 'text-font': ['Noto Sans Bold'], 'text-size': 15,
          'text-anchor': 'left', 'text-offset': [1.1, 0], 'text-optional': true,
        },
        paint: { 'text-color': '#ffffff', 'text-halo-color': 'rgba(10, 13, 18, 0.95)', 'text-halo-width': 2 },
      });
      // The credit stays a small "i" button so it never sits under the controls.
      container.querySelector('.maplibregl-ctrl-attrib')?.classList.remove('maplibregl-compact-show');
      const showChosenPlace: WeatherMap['showChosenPlace'] = (place) => {
        (map.getSource('chosen-place') as maplibregl.GeoJSONSource).setData({
          type: 'FeatureCollection',
          features: place ? [{ type: 'Feature', properties: { name: place.name }, geometry: { type: 'Point', coordinates: [place.longitude, place.latitude] } }] : [],
        });
      };
      resolve({ map, weather, showChosenPlace });
    });
  });
}
