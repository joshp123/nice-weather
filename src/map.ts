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

export type WeatherMap = Readonly<{ map: maplibregl.Map; weather: WeatherLayer }>;

export function createWeatherMap(container: HTMLElement, bands: readonly Band[], camera: { center: [number, number]; zoom: number }): Promise<WeatherMap> {
  const map = new maplibregl.Map({
    container, style, center: camera.center, zoom: camera.zoom, minZoom: 1.2, maxZoom: 11,
    attributionControl: { compact: true, customAttribution: 'Forecast © ECMWF · Climate: Copernicus ERA5' },
    dragRotate: false, pitchWithRotate: false, touchPitch: false,
  });
  map.touchZoomRotate.disableRotation();
  const weather = new WeatherLayer(bands);
  return new Promise((resolve) => {
    map.once('style.load', () => {
      map.addLayer(weather, 'sea');
      resolve({ map, weather });
    });
  });
}
