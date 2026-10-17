// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// The base map: land, the weather (the engine's layer sits just under the sea), sea, borders, then city names
// with their score, so the answer reads straight off the map. Dark or light to match the device.
import maplibregl, { type ExpressionSpecification, type GeoJSONSource, type LayerSpecification, type StyleSpecification } from 'maplibre-gl';
import 'maplibre-gl/dist/maplibre-gl.css';
import type { Point } from '../engine';
import type { Camera } from './state';
import { bands } from '../palette';

export type Theme = 'dark' | 'light';

function mapDesign() {
  const properties = getComputedStyle(document.documentElement);
  const colour = (name: string) => properties.getPropertyValue(`--map-${name}`).trim();
  const number = (name: string) => Number.parseFloat(colour(name));
  return {
    land: colour('land'), sea: colour('sea'), state: colour('state-border'), country: colour('country-border'), text: colour('label'), halo: colour('label-halo'),
    labelScale: number('label-scale'), labelSmall: number('label-small'), labelLarge: number('label-large'), labelLineHeight: number('label-line-height'), labelWidth: number('label-width'), labelPadding: number('label-padding'), labelHaloWidth: number('label-halo-width'),
    stateWidth: number('state-width'), countryWidthSmall: number('country-width-small'), countryWidthLarge: number('country-width-large'),
    homeLineWidth: number('home-line-width'), homeDash: number('home-dash'), homeDashGap: number('home-dash-gap'), homeOpacity: number('home-opacity'), homeRadius: number('home-radius'), homeStroke: number('home-stroke'),
    chosenSize: number('chosen-size'), chosenOffset: number('chosen-offset'), chosenHalo: number('chosen-halo'),
    countMinimumWidth: number('count-minimum-width'), countPadding: number('count-padding'), countCharacterWidth: number('count-character-width'), countHeight: number('count-height'), countFontSize: number('count-font-size'), countStroke: number('count-stroke'), countOutline: colour('count-outline'), countTextLow: colour('count-text-low'), countTextHigh: colour('count-text-high'),
    ringSize: number('ring-size'), ringRadius: number('ring-radius'), ringOutlineWidth: number('ring-outline-width'), ringStrokeWidth: number('ring-stroke-width'), ringOutline: colour('ring-outline'), ringStroke: colour('ring-stroke'),
    showWholeWorld: number('show-whole-world'), wideLongitude: number('wide-longitude'), wideLatitude: number('wide-latitude'), wideZoom: number('wide-zoom'),
  };
}
type MapDesign = ReturnType<typeof mapDesign>;

/** A city's nice-day count is printed in a coloured pill below its name. */
const englishName: ExpressionSpecification = ['coalesce', ['get', 'name:en'], ['get', 'name']];
const scoredLabel: ExpressionSpecification = [
  'case', ['has', 'countImage'],
  ['format', englishName, {}, '\n', {}, ['image', ['get', 'countImage']], {}],
  englishName,
];
const minimumLabelPopulations = [4000000, 1000000, 300000, 80000, 20000, 0];

function cityLayer(colours: MapDesign): LayerSpecification {
  return {
    id: 'city-labels', type: 'symbol', source: 'city-scores',
    filter: ['step', ['zoom'], ['>=', ['get', 'population'], minimumLabelPopulations[0]], ...minimumLabelPopulations.slice(1).flatMap((population, index) => [index + 2, ['>=', ['get', 'population'], population]])] as ExpressionSpecification,
    layout: {
      'text-field': scoredLabel,
      'text-font': ['Noto Sans Bold'],
      'text-size': ['interpolate', ['linear'], ['zoom'], 1, colours.labelSmall * colours.labelScale, 9, colours.labelLarge * colours.labelScale],
      'text-line-height': colours.labelLineHeight,
      'text-max-width': colours.labelWidth,
      'text-padding': colours.labelPadding,
      'symbol-sort-key': ['-', 0, ['get', 'population']],
      'text-allow-overlap': false,
    },
    paint: { 'text-color': colours.text, 'text-halo-color': colours.halo, 'text-halo-width': colours.labelHaloWidth },
  };
}

function style(colours: MapDesign): StyleSpecification {
  return {
    version: 8,
    glyphs: 'https://tiles.openfreemap.org/fonts/{fontstack}/{range}.pbf',
    sources: {
      openmaptiles: { type: 'vector', url: 'https://tiles.openfreemap.org/planet' },
      'city-scores': { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
      'palestine-label': { type: 'geojson', data: { type: 'FeatureCollection', features: [{ type: 'Feature', properties: {}, geometry: { type: 'Point', coordinates: [34.8, 31.15] } }] } },
      'chosen-place': { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
      'home-connection': { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
    },
    layers: [
      { id: 'sea-background', type: 'background', paint: { 'background-color': colours.sea } },
      { id: 'sea', type: 'fill', source: 'openmaptiles', 'source-layer': 'water', filter: ['!=', ['get', 'brunnel'], 'tunnel'], paint: { 'fill-color': colours.sea } },
      { id: 'state-borders', type: 'line', source: 'openmaptiles', 'source-layer': 'boundary', minzoom: 3, filter: ['all', ['==', ['get', 'admin_level'], 4], ['!=', ['get', 'maritime'], 1]], paint: { 'line-color': colours.state, 'line-width': colours.stateWidth } },
      { id: 'country-borders', type: 'line', source: 'openmaptiles', 'source-layer': 'boundary', filter: ['all', ['==', ['get', 'admin_level'], 2], ['!=', ['get', 'maritime'], 1], ['!', ['has', 'claimed_by']]], paint: { 'line-color': colours.country, 'line-width': ['interpolate', ['linear'], ['zoom'], 1, colours.countryWidthSmall, 6, colours.countryWidthLarge] } },
      { id: 'home-connection', type: 'line', source: 'home-connection', filter: ['==', ['geometry-type'], 'LineString'], layout: { 'line-cap': 'round' }, paint: { 'line-color': colours.text, 'line-width': colours.homeLineWidth, 'line-dasharray': [colours.homeDash, colours.homeDashGap], 'line-opacity': colours.homeOpacity } },
      { id: 'home-dot', type: 'circle', source: 'home-connection', filter: ['==', ['geometry-type'], 'Point'], paint: { 'circle-radius': colours.homeRadius, 'circle-color': colours.sea, 'circle-stroke-color': colours.text, 'circle-stroke-width': colours.homeStroke } },
      {
        id: 'palestine-label', type: 'symbol', source: 'palestine-label', minzoom: 3, maxzoom: 6,
        layout: { 'text-field': 'Palestine', 'text-font': ['Noto Sans Regular'], 'text-size': colours.countFontSize * colours.labelScale, 'text-letter-spacing': 0.08, 'text-padding': 10, 'text-allow-overlap': true },
        paint: { 'text-color': colours.text, 'text-halo-color': colours.halo, 'text-halo-width': colours.labelHaloWidth, 'text-opacity': 0.75 },
      },
      cityLayer(colours),
      {
        id: 'chosen-place', type: 'symbol', source: 'chosen-place',
        layout: {
          'icon-image': 'chosen-place', 'icon-allow-overlap': true,
          'text-field': scoredLabel, 'text-font': ['Noto Sans Bold'], 'text-size': colours.chosenSize * colours.labelScale,
          'text-anchor': 'left', 'text-offset': [colours.chosenOffset, 0], 'text-optional': true,
        },
        paint: { 'text-color': colours.text, 'text-halo-color': colours.halo, 'text-halo-width': colours.chosenHalo },
      },
    ],
  };
}

function ringImage(design: MapDesign): ImageData {
  const size = design.ringSize;
  const canvas = new OffscreenCanvas(size, size);
  const context = canvas.getContext('2d')!;
  context.beginPath();
  context.arc(size / 2, size / 2, design.ringRadius, 0, Math.PI * 2);
  context.lineWidth = design.ringOutlineWidth;
  context.strokeStyle = design.ringOutline;
  context.stroke();
  context.lineWidth = design.ringStrokeWidth;
  context.strokeStyle = design.ringStroke;
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
  showHomeConnection(home: Point | null, destination: Point | null): void;
  /** All loaded cities, independent of basemap tile requests. */
  visibleCities(): CityPoint[];
  labelCandidates(): CityPoint[];
  /** Colours nice-day counts by band; single-day views use the day's score. */
  labelCities(cities: readonly CityPoint[], scores: ArrayLike<number>, niceCounts?: ArrayLike<number>): void;
}>;

export function createMap(container: HTMLElement, theme: Theme, camera: Camera | null, dataUrl = '/data/v6'): Promise<WeatherMap> {
  const design = mapDesign();
  container.style.backgroundColor = design.sea;
  let cities: CityPoint[] = [];
  const fetchLabels = (file: string) => fetch(`${dataUrl}/${file}`).then(response => {
    if (!response.ok) throw new Error(`City labels: HTTP ${response.status}`);
    return response.json() as Promise<[string, string, number, number, number][]>;
  });
  const labels = fetchLabels('labels-1.json');
  let remainingCities: Promise<void> | undefined;
  const map = new maplibregl.Map({
    container, style: style(design), center: camera ? [camera.center[0], camera.center[1]] : [10, 25], zoom: camera?.zoom ?? 1.6, minZoom: 1.1, maxZoom: 11,
    attributionControl: { compact: true, customAttribution: 'Forecast © ECMWF · Climate: Copernicus ERA5' },
    dragRotate: false, pitchWithRotate: false, touchPitch: false,
    renderWorldCopies: false,
    canvasContextAttributes: { preserveDrawingBuffer: true },
    transformConstrain: design.showWholeWorld === 1 ? (center, zoom) => ({
      center: new maplibregl.LngLat(center.wrap().lng, Math.max(-85, Math.min(85, center.lat))),
      zoom: Math.max(1.1, Math.min(11, zoom)),
    }) : undefined,
  });
  map.touchZoomRotate.disableRotation();
  if (!camera) {
    if (design.showWholeWorld === 1) map.jumpTo({ center: [design.wideLongitude, design.wideLatitude], zoom: design.wideZoom });
    else map.fitBounds([[-125, -45], [150, 62]], { animate: false });
  }

  return new Promise(resolve => {
    map.once('style.load', () => {
      map.addImage('chosen-place', ringImage(design), { pixelRatio: 2 });
      const countImage = (score: number, count: number): string => {
        let band = bands[0];
        for (const candidate of bands) if (score >= candidate.minimumScore) band = candidate;
        const countText = String(Math.round(count));
        const name = `nice-count-${band.name}-${countText}`;
        if (!map.hasImage(name)) {
          const scale = design.labelScale;
          const width = Math.max(design.countMinimumWidth, design.countPadding + countText.length * design.countCharacterWidth) * scale;
          const height = design.countHeight * scale;
          const canvas = new OffscreenCanvas(Math.ceil(width * 2), Math.ceil(height * 2));
          const draw = canvas.getContext('2d')!;
          draw.scale(2, 2);
          draw.fillStyle = band.colour.slice(0, 7);
          draw.beginPath();
          draw.roundRect(1, 1, width - 2, height - 2, (height - 2) / 2);
          draw.fill();
          draw.strokeStyle = design.countOutline;
          draw.lineWidth = design.countStroke;
          draw.stroke();
          draw.font = `700 ${design.countFontSize * scale}px sans-serif`;
          draw.textAlign = 'center';
          draw.textBaseline = 'middle';
          draw.fillStyle = band.minimumScore < 40 ? design.countTextLow : design.countTextHigh;
          draw.fillText(countText, width / 2, height / 2 + 0.5);
          map.addImage(name, draw.getImageData(0, 0, canvas.width, canvas.height), { pixelRatio: 2 });
        }
        return name;
      };
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
        showHomeConnection(home, destination) {
          (map.getSource('home-connection') as GeoJSONSource).setData({
            type: 'FeatureCollection',
            features: home && destination ? [
              { type: 'Feature', properties: {}, geometry: { type: 'LineString', coordinates: [[home.longitude, home.latitude], [destination.longitude, destination.latitude]] } },
              { type: 'Feature', properties: {}, geometry: { type: 'Point', coordinates: [home.longitude, home.latitude] } },
            ] : [],
          });
        },
        visibleCities() { return cities; },
        labelCandidates() {
          const minimumPopulation = minimumLabelPopulations[Math.min(5, Math.max(0, Math.floor(map.getZoom()) - 1))];
          return cities.filter(city => city.population >= minimumPopulation);
        },
        labelCities(cities, scores, niceCounts) {
          (map.getSource('city-scores') as GeoJSONSource).setData({
            type: 'FeatureCollection',
            features: cities.map((city, index) => {
              const score = scores[index];
              const count = niceCounts?.[index];
              return {
                type: 'Feature',
                properties: {
                  name: city.name, population: city.population, slug: city.slug,
                  ...(Number.isFinite(score) ? { score: Math.floor(score) } : {}),
                  ...(Number.isFinite(score) && count !== undefined && Number.isFinite(count) ? { countImage: countImage(score, count) } : {}),
                },
                geometry: { type: 'Point', coordinates: [city.longitude, city.latitude] },
              };
            }),
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
