// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// The base map: land, the weather (the engine's layer sits just under the sea), sea, borders, then city names
// with their score, so the answer reads straight off the map. Dark or light to match the device.
import maplibregl, { type ExpressionSpecification, type GeoJSONSource, type LayerSpecification, type StyleSpecification } from 'maplibre-gl';
import 'maplibre-gl/dist/maplibre-gl.css';
import type { Point } from '../engine';
import type { Camera } from './state';

export type Theme = 'dark' | 'light';

const labelText: ExpressionSpecification = ['coalesce', ['get', 'name:en'], ['get', 'name:latin'], ['get', 'name']];
const pointGeometry: ExpressionSpecification = ['match', ['geometry-type'], ['MultiPoint', 'Point'], true, false];

const THEMES = {
  dark: { land: '#363b43', sea: '#0e1116', state: 'rgba(255,255,255,0.14)', country: 'rgba(255,255,255,0.34)', text: '#f6f7f9', halo: 'rgba(10,13,18,0.92)' },
  light: { land: '#d5d9df', sea: '#f3f5f7', state: 'rgba(20,24,32,0.16)', country: 'rgba(20,24,32,0.4)', text: '#15181d', halo: 'rgba(255,255,255,0.92)' },
} as const;

/** The score line under a city name; the name alone where there is no score. */
const scoredLabel: ExpressionSpecification = [
  'case', ['has', 'score'],
  ['format', ['get', 'name'], {}, '\n', {}, ['to-string', ['get', 'score']], { 'font-scale': 1.12 }],
  ['get', 'name'],
];

/** Labels grow a little on very wide screens, such as a 57-inch monitor, where 13 px would be lost. */
const labelScale = () => (window.innerWidth >= 2200 ? 1.35 : 1);

function cityLayer(id: string, filter: ExpressionSpecification, minzoom: number, sizes: [number, number, number, number], colours: (typeof THEMES)[Theme]): LayerSpecification {
  return {
    id, type: 'symbol', source: 'city-scores', minzoom, filter,
    layout: {
      'text-field': scoredLabel,
      'text-font': ['Noto Sans Bold'],
      'text-size': ['interpolate', ['linear'], ['zoom'], sizes[0], sizes[1] * labelScale(), sizes[2], sizes[3] * labelScale()],
      'text-line-height': 1.1,
      'text-max-width': 8,
      'text-padding': 4,
      'symbol-sort-key': ['get', 'rank'],
    },
    paint: { 'text-color': colours.text, 'text-halo-color': colours.halo, 'text-halo-width': 1.5 },
  };
}

function style(theme: Theme): StyleSpecification {
  const colours = THEMES[theme];
  return {
    version: 8,
    glyphs: 'https://tiles.openfreemap.org/fonts/{fontstack}/{range}.pbf',
    sources: {
      openmaptiles: { type: 'vector', url: 'https://tiles.openfreemap.org/planet' },
      'city-scores': { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
      'chosen-place': { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
    },
    layers: [
      { id: 'land', type: 'background', paint: { 'background-color': colours.land } },
      { id: 'sea', type: 'fill', source: 'openmaptiles', 'source-layer': 'water', filter: ['!=', ['get', 'brunnel'], 'tunnel'], paint: { 'fill-color': colours.sea } },
      { id: 'state-borders', type: 'line', source: 'openmaptiles', 'source-layer': 'boundary', minzoom: 3, filter: ['all', ['==', ['get', 'admin_level'], 4], ['!=', ['get', 'maritime'], 1]], paint: { 'line-color': colours.state, 'line-width': 0.6 } },
      { id: 'country-borders', type: 'line', source: 'openmaptiles', 'source-layer': 'boundary', filter: ['all', ['==', ['get', 'admin_level'], 2], ['!=', ['get', 'maritime'], 1], ['!', ['has', 'claimed_by']]], paint: { 'line-color': colours.country, 'line-width': ['interpolate', ['linear'], ['zoom'], 1, 0.6, 6, 1.2] } },
      {
        id: 'villages', type: 'symbol', source: 'openmaptiles', 'source-layer': 'place', minzoom: 10, filter: ['all', pointGeometry, ['==', ['get', 'class'], 'village']],
        layout: { 'text-field': labelText, 'text-font': ['Noto Sans Regular'], 'text-size': 12 * labelScale(), 'text-max-width': 8, 'text-padding': 6 },
        paint: { 'text-color': colours.text, 'text-halo-color': colours.halo, 'text-halo-width': 1.3 },
      },
      cityLayer('towns', ['==', ['get', 'class'], 'town'], 7, [7, 12, 12, 15], colours),
      cityLayer('cities', ['all', ['==', ['get', 'class'], 'city'], ['>', ['get', 'rank'], 3]], 4, [4, 12, 10, 17], colours),
      cityLayer('large-cities', ['all', ['==', ['get', 'class'], 'city'], ['<=', ['get', 'rank'], 3]], 1.5, [1.5, 13, 8, 21], colours),
      {
        id: 'chosen-place', type: 'symbol', source: 'chosen-place',
        layout: {
          'icon-image': 'chosen-place', 'icon-allow-overlap': true,
          'text-field': ['get', 'name'], 'text-font': ['Noto Sans Bold'], 'text-size': 15 * labelScale(),
          'text-anchor': 'left', 'text-offset': [1.1, 0], 'text-optional': true,
        },
        paint: { 'text-color': colours.text, 'text-halo-color': colours.halo, 'text-halo-width': 2 },
      },
    ],
  };
}

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

export type CityPoint = Point & Readonly<{ name: string; rank: number; class: string }>;

export type WeatherMap = Readonly<{
  map: maplibregl.Map;
  theme: Theme;
  /** Marks the chosen spot and writes its name beside it. */
  showChosenPlace(place: { latitude: number; longitude: number; name: string } | null): void;
  /** The cities and towns the base map shows right now, without duplicates. */
  visibleCities(): CityPoint[];
  /** Writes a score under each city name. NaN leaves the name alone. */
  labelCities(cities: readonly CityPoint[], scores: ArrayLike<number>): void;
}>;

export function createMap(container: HTMLElement, theme: Theme, camera: Camera | null): Promise<WeatherMap> {
  const map = new maplibregl.Map({
    container, style: style(theme), center: camera ? [camera.center[0], camera.center[1]] : [10, 25], zoom: camera?.zoom ?? 1.6, minZoom: 1.1, maxZoom: 11,
    attributionControl: { compact: true, customAttribution: 'Forecast © ECMWF · Climate: Copernicus ERA5' },
    dragRotate: false, pitchWithRotate: false, touchPitch: false,
  });
  map.touchZoomRotate.disableRotation();
  if (!camera) map.fitBounds([[-125, -45], [150, 62]], { animate: false });

  return new Promise(resolve => {
    map.once('style.load', () => {
      map.addImage('chosen-place', ringImage(), { pixelRatio: 2 });
      container.querySelector('.maplibregl-ctrl-attrib')?.classList.remove('maplibregl-compact-show');
      const weatherMap: WeatherMap = {
        map,
        theme,
        showChosenPlace(place) {
          (map.getSource('chosen-place') as GeoJSONSource).setData({
            type: 'FeatureCollection',
            features: place ? [{ type: 'Feature', properties: { name: place.name }, geometry: { type: 'Point', coordinates: [place.longitude, place.latitude] } }] : [],
          });
        },
        visibleCities() {
          const seen = new Set<string>();
          const cities: CityPoint[] = [];
          const features = map.querySourceFeatures('openmaptiles', { sourceLayer: 'place', filter: ['all', pointGeometry, ['in', ['get', 'class'], ['literal', ['city', 'town']]]] });
          for (const feature of features) {
            if (feature.geometry.type !== 'Point') continue;
            const [longitude, latitude] = feature.geometry.coordinates;
            const name = String(feature.properties['name:en'] ?? feature.properties['name:latin'] ?? feature.properties.name ?? '');
            const key = `${name}|${latitude.toFixed(2)}|${longitude.toFixed(2)}`;
            if (!name || seen.has(key)) continue;
            seen.add(key);
            cities.push({ name, latitude, longitude, rank: Number(feature.properties.rank ?? 20), class: String(feature.properties.class) });
          }
          return cities;
        },
        labelCities(cities, scores) {
          (map.getSource('city-scores') as GeoJSONSource).setData({
            type: 'FeatureCollection',
            features: cities.map((city, index) => ({
              type: 'Feature',
              properties: { name: city.name, rank: city.rank, class: city.class, ...(Number.isFinite(scores[index]) ? { score: Math.round(scores[index]) } : {}) },
              geometry: { type: 'Point', coordinates: [city.longitude, city.latitude] },
            })),
          });
        },
      };
      resolve(weatherMap);
    });
  });
}

/** Rough great-circle distance, for keeping headline places apart. */
export function kilometresBetween(a: Point, b: Point): number {
  const radians = Math.PI / 180;
  const x = (b.longitude - a.longitude) * radians * Math.cos(((a.latitude + b.latitude) / 2) * radians);
  const y = (b.latitude - a.latitude) * radians;
  return Math.sqrt(x * x + y * y) * 6371;
}
