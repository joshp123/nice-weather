// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// The base map: land, the weather (the engine's layer sits just under the sea), sea, borders, then city names
// with their score, so the answer reads straight off the map. Dark or light to match the device.
import maplibregl, { type ExpressionSpecification, type GeoJSONSource, type LayerSpecification, type StyleSpecification } from 'maplibre-gl';
import 'maplibre-gl/dist/maplibre-gl.css';
import type { Point } from '../engine';
import type { Camera } from './state';

export type Theme = 'dark' | 'light';

const THEMES = {
  dark: { land: '#363b43', sea: '#0e1116', state: 'rgba(255,255,255,0.14)', country: 'rgba(255,255,255,0.34)', text: '#f6f7f9', halo: 'rgba(10,13,18,0.92)' },
  light: { land: '#d5d9df', sea: '#f3f5f7', state: 'rgba(20,24,32,0.16)', country: 'rgba(20,24,32,0.4)', text: '#15181d', halo: 'rgba(255,255,255,0.92)' },
} as const;

/** The score line under a city name; the name alone where there is no score. */
const englishName: ExpressionSpecification = ['coalesce', ['get', 'name:en'], ['get', 'name']];
const scoredLabel: ExpressionSpecification = [
  'case', ['has', 'score'],
  ['format', englishName, {}, '\n', {}, ['to-string', ['get', 'score']], { 'font-scale': 1.12 }],
  englishName,
];

/** Labels grow a little on very wide screens, such as a 57-inch monitor, where 13 px would be lost. */
const labelScale = () => (window.innerWidth >= 2200 ? 1.35 : 1);

function cityLayer(colours: (typeof THEMES)[Theme]): LayerSpecification {
  return {
    id: 'city-labels', type: 'symbol', source: 'city-scores',
    filter: ['step', ['zoom'], ['>=', ['get', 'population'], 4000000], 2, ['>=', ['get', 'population'], 1000000], 3, ['>=', ['get', 'population'], 300000], 4, ['>=', ['get', 'population'], 80000], 5, ['>=', ['get', 'population'], 20000], 6, true],
    layout: {
      'text-field': scoredLabel,
      'text-font': ['Noto Sans Bold'],
      'text-size': ['interpolate', ['linear'], ['zoom'], 1, 12 * labelScale(), 9, 16 * labelScale()],
      'text-line-height': 1.15,
      'text-max-width': 9,
      'text-padding': 16,
      'symbol-sort-key': ['-', 0, ['get', 'population']],
      'text-allow-overlap': false,
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
      'palestine-label': { type: 'geojson', data: { type: 'FeatureCollection', features: [{ type: 'Feature', properties: {}, geometry: { type: 'Point', coordinates: [34.8, 31.15] } }] } },
      'chosen-place': { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
    },
    layers: [
      { id: 'land', type: 'background', paint: { 'background-color': colours.land } },
      { id: 'sea', type: 'fill', source: 'openmaptiles', 'source-layer': 'water', filter: ['!=', ['get', 'brunnel'], 'tunnel'], paint: { 'fill-color': colours.sea } },
      { id: 'state-borders', type: 'line', source: 'openmaptiles', 'source-layer': 'boundary', minzoom: 3, filter: ['all', ['==', ['get', 'admin_level'], 4], ['!=', ['get', 'maritime'], 1]], paint: { 'line-color': colours.state, 'line-width': 0.6 } },
      { id: 'country-borders', type: 'line', source: 'openmaptiles', 'source-layer': 'boundary', filter: ['all', ['==', ['get', 'admin_level'], 2], ['!=', ['get', 'maritime'], 1], ['!', ['has', 'claimed_by']]], paint: { 'line-color': colours.country, 'line-width': ['interpolate', ['linear'], ['zoom'], 1, 0.6, 6, 1.2] } },
      {
        id: 'palestine-label', type: 'symbol', source: 'palestine-label', minzoom: 3, maxzoom: 6,
        layout: { 'text-field': 'Palestine', 'text-font': ['Noto Sans Regular'], 'text-size': 13 * labelScale(), 'text-letter-spacing': 0.08, 'text-padding': 10, 'text-allow-overlap': true },
        paint: { 'text-color': colours.text, 'text-halo-color': colours.halo, 'text-halo-width': 1.5, 'text-opacity': 0.75 },
      },
      cityLayer(colours),
      {
        id: 'chosen-place', type: 'symbol', source: 'chosen-place',
        layout: {
          'icon-image': 'chosen-place', 'icon-allow-overlap': true,
          'text-field': scoredLabel, 'text-font': ['Noto Sans Bold'], 'text-size': 15 * labelScale(),
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

export type CityPoint = Point & Readonly<{ name: string; slug: string; population: number }>;

export type WeatherMap = Readonly<{
  map: maplibregl.Map;
  theme: Theme;
  citiesReady: Promise<void>;
  loadRemainingCities(): Promise<void>;
  /** Marks the chosen spot and writes its name beside it. */
  showChosenPlace(place: { latitude: number; longitude: number; name: string; score?: number } | null): void;
  /** All loaded cities, independent of basemap tile requests. */
  visibleCities(): CityPoint[];
  /** Writes a score under each city name. NaN leaves the name alone. */
  labelCities(cities: readonly CityPoint[], scores: ArrayLike<number>): void;
}>;

export function createMap(container: HTMLElement, theme: Theme, camera: Camera | null, dataUrl = '/data/v6'): Promise<WeatherMap> {
  let cities: CityPoint[] = [];
  const fetchLabels = (file: string) => fetch(`${dataUrl}/${file}`).then(response => {
    if (!response.ok) throw new Error(`City labels: HTTP ${response.status}`);
    return response.json() as Promise<[string, string, number, number, number][]>;
  });
  const labels = fetchLabels('labels-1.json');
  let remainingCities: Promise<void> | undefined;
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
        citiesReady: labels.then(rows => {
          cities = rows.map(([name, slug, latitude, longitude, population]) => ({ name, slug, latitude, longitude, population }));
          weatherMap.labelCities(cities, []);
        }).catch(error => { console.error(error); }),
        loadRemainingCities() {
          return remainingCities ??= weatherMap.citiesReady.then(() => fetchLabels('labels-2.json')).then(rows => {
            cities.push(...rows.map(([name, slug, latitude, longitude, population]) => ({ name, slug, latitude, longitude, population })));
          }).catch(error => { console.error(error); });
        },
        showChosenPlace(place) {
          (map.getSource('chosen-place') as GeoJSONSource).setData({
            type: 'FeatureCollection',
            features: place ? [{ type: 'Feature', properties: { name: place.name, ...(Number.isFinite(place.score) ? { score: Math.floor(place.score!) } : {}) }, geometry: { type: 'Point', coordinates: [place.longitude, place.latitude] } }] : [],
          });
        },
        visibleCities() { return cities; },
        labelCities(cities, scores) {
          (map.getSource('city-scores') as GeoJSONSource).setData({
            type: 'FeatureCollection',
            features: cities.map((city, index) => ({
              type: 'Feature',
              properties: { name: city.name, population: city.population, slug: city.slug, ...(Number.isFinite(scores[index]) ? { score: Math.floor(scores[index]) } : {}) },
              geometry: { type: 'Point', coordinates: [city.longitude, city.latitude] },
            })),
          });
        },
      };
      resolve(weatherMap);
    });
  });
}

/** Great-circle distance keeps the separation rule correct across the date line. */
export function kilometresBetween(a: Point, b: Point): number {
  const radians = Math.PI / 180;
  const latitudeDistance = (b.latitude - a.latitude) * radians;
  const longitudeDistance = (b.longitude - a.longitude) * radians;
  const haversine = Math.sin(latitudeDistance / 2) ** 2
    + Math.cos(a.latitude * radians) * Math.cos(b.latitude * radians) * Math.sin(longitudeDistance / 2) ** 2;
  return 6371 * 2 * Math.asin(Math.min(1, Math.sqrt(haversine)));
}
