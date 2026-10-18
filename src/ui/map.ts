// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// The base map: land, the weather (the engine's layer sits just under the sea), sea, borders, then city names
// with their score, so the answer reads straight off the map. Dark or light to match the device.
import maplibregl, { type ExpressionSpecification, type GeoJSONSource, type SymbolLayerSpecification, type StyleSpecification } from 'maplibre-gl';
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

function cityLayer(colours: MapDesign): SymbolLayerSpecification {
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
      'world-map': { type: 'vector', url: 'https://tiles.openfreemap.org/planet', maxzoom: 1 },
      openmaptiles: { type: 'vector', url: 'https://tiles.openfreemap.org/planet', minzoom: 3 },
      'headline-pointers': { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
      'city-scores': { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
      'palestine-label': { type: 'geojson', data: { type: 'FeatureCollection', features: [{ type: 'Feature', properties: { longitude: 34.8, latitude: 31.15 }, geometry: { type: 'Point', coordinates: [34.8, 31.15] } }] } },
      'chosen-place': { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
      'home-connection': { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
    },
    layers: [
      { id: 'sea-background', type: 'background', paint: { 'background-color': colours.sea } },
      { id: 'sea', type: 'fill', source: 'world-map', 'source-layer': 'water', maxzoom: 3, filter: ['!=', ['get', 'brunnel'], 'tunnel'], paint: { 'fill-color': colours.sea } },
      { id: 'detailed-sea', type: 'fill', source: 'openmaptiles', 'source-layer': 'water', minzoom: 3, filter: ['!=', ['get', 'brunnel'], 'tunnel'], paint: { 'fill-color': colours.sea } },
      { id: 'state-borders', type: 'line', source: 'openmaptiles', 'source-layer': 'boundary', minzoom: 3, filter: ['all', ['==', ['get', 'admin_level'], 4], ['!=', ['get', 'maritime'], 1]], paint: { 'line-color': colours.state, 'line-width': colours.stateWidth } },
      { id: 'world-borders', type: 'line', source: 'world-map', 'source-layer': 'boundary', maxzoom: 3, filter: ['all', ['==', ['get', 'admin_level'], 2], ['!=', ['get', 'maritime'], 1], ['!', ['has', 'claimed_by']]], paint: { 'line-color': colours.country, 'line-width': colours.countryWidthSmall } },
      { id: 'country-borders', type: 'line', source: 'openmaptiles', 'source-layer': 'boundary', minzoom: 3, filter: ['all', ['==', ['get', 'admin_level'], 2], ['!=', ['get', 'maritime'], 1], ['!', ['has', 'claimed_by']]], paint: { 'line-color': colours.country, 'line-width': ['interpolate', ['linear'], ['zoom'], 1, colours.countryWidthSmall, 6, colours.countryWidthLarge] } },
      { id: 'home-connection', type: 'line', source: 'home-connection', filter: ['==', ['geometry-type'], 'LineString'], layout: { 'line-cap': 'round' }, paint: { 'line-color': colours.text, 'line-width': colours.homeLineWidth, 'line-dasharray': [colours.homeDash, colours.homeDashGap], 'line-opacity': colours.homeOpacity } },
      { id: 'home-dot', type: 'circle', source: 'home-connection', filter: ['==', ['geometry-type'], 'Point'], paint: { 'circle-radius': colours.homeRadius, 'circle-color': colours.sea, 'circle-stroke-color': colours.text, 'circle-stroke-width': colours.homeStroke } },
      {
        id: 'palestine-label', type: 'symbol', source: 'palestine-label', minzoom: 3, maxzoom: 6,
        layout: { 'text-field': 'Palestine', 'text-font': ['Noto Sans Regular'], 'text-size': colours.countFontSize * colours.labelScale, 'text-letter-spacing': 0.08, 'text-padding': 10, 'text-allow-overlap': true },
        paint: { 'text-color': colours.text, 'text-halo-color': colours.halo, 'text-halo-width': colours.labelHaloWidth, 'text-opacity': 0.75 },
      },
      cityLayer(colours),
      { id: 'headline-pointers', type: 'line', source: 'headline-pointers', paint: { 'line-color': colours.text, 'line-width': colours.homeLineWidth, 'line-opacity': colours.homeOpacity } },
      { id: 'headline-points', type: 'circle', source: 'city-scores', filter: ['has', 'headlineRank'], paint: { 'circle-radius': colours.homeRadius, 'circle-color': colours.text, 'circle-stroke-color': colours.halo, 'circle-stroke-width': colours.homeStroke } },
      { ...cityLayer(colours), id: 'headline-labels', filter: ['has', 'headlineRank'], layout: {
        ...cityLayer(colours).layout, 'symbol-sort-key': ['get', 'headlineRank'],
        'text-allow-overlap': true, 'text-offset': ['get', 'headlineOffset'],
      } },
      {
        id: 'chosen-place', type: 'symbol', source: 'chosen-place',
        layout: {
          'icon-image': 'chosen-place', 'icon-allow-overlap': true,
          'text-field': '', 'text-font': ['Noto Sans Bold'], 'text-size': colours.chosenSize * colours.labelScale,
          'text-max-width': colours.labelWidth,
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
  rankingCandidates(minimumPopulation: number): CityPoint[];
  labelledPlaces(): (CityPoint & { displayedNumber?: number })[];
  /** Colours nice-day counts by band; single-day views use the day's score. */
  labelCities(cities: readonly CityPoint[], scores: ArrayLike<number>, niceCounts?: ArrayLike<number>, prioritySlugs?: readonly string[]): void;
}>;

export function createMap(container: HTMLElement, theme: Theme, camera: Camera | null, dataUrl = '/data/v6'): Promise<WeatherMap> {
  const design = mapDesign();
  container.style.backgroundColor = design.sea;
  let cities: CityPoint[] = [];
  let chosenPlace: {latitude: number; longitude: number; name: string; score?: number} | null = null;
  let previousLabels: {cities: readonly CityPoint[]; scores: ArrayLike<number>; niceCounts?: ArrayLike<number>; prioritySlugs: readonly string[]} | undefined;
  const fetchLabels = (file: string) => fetch(`/api/fine/${file}`).then(response => {
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
      const viewportLabelFilter = (left: number, right: number, vertical: number): ExpressionSpecification => {
        const northWest = map.unproject([left, vertical]);
        const southEast = map.unproject([container.clientWidth - right, container.clientHeight - vertical]);
        const west = Math.max(-180, northWest.lng), east = Math.min(180, southEast.lng);
        return ['all', ['>', ['get', 'longitude'], west], ['<', ['get', 'longitude'], east], ['>', ['get', 'latitude'], southEast.lat], ['<', ['get', 'latitude'], northWest.lat]];
      };
      const keepLabelsInsideViewport = () => {
        const zoom = map.getZoom();
        const fontSize = (design.labelSmall + (design.labelLarge - design.labelSmall) * Math.max(0, Math.min(1, (zoom - 1) / 8))) * design.labelScale;
        const halfWidth = fontSize * design.labelWidth / 2 + design.labelHaloWidth;
        const halfHeight = (fontSize * design.labelLineHeight * 3 + design.countHeight * design.labelScale) / 2;
        const minimumPopulation = minimumLabelPopulations[Math.min(5, Math.max(0, Math.floor(zoom) - 1))];
        map.setFilter('city-labels', ['all', ['!', ['has', 'headlineRank']], ['!', ['has', 'chosen']], ['>=', ['get', 'population'], minimumPopulation], viewportLabelFilter(halfWidth, halfWidth, halfHeight)]);
        map.setFilter('chosen-place', viewportLabelFilter(design.ringSize / 2, design.ringSize / 2, design.ringSize / 2));
        const countryFontSize = design.countFontSize * design.labelScale;
        const countryHalfWidth = countryFontSize * design.labelWidth / 2 + design.labelHaloWidth;
        map.setFilter('palestine-label', viewportLabelFilter(countryHalfWidth, countryHalfWidth, countryFontSize * design.labelLineHeight));
      };
      keepLabelsInsideViewport();
      map.on('move', keepLabelsInsideViewport);
      map.on('resize', keepLabelsInsideViewport);
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
          const changed = chosenPlace?.latitude !== place?.latitude || chosenPlace?.longitude !== place?.longitude || chosenPlace?.name !== place?.name;
          chosenPlace = place;
          (map.getSource('chosen-place') as GeoJSONSource).setData({
            type: 'FeatureCollection',
            features: place ? [{ type: 'Feature', properties: { name: place.name, longitude: place.longitude, latitude: place.latitude, ...(Number.isFinite(place.score) ? { score: Math.floor(place.score!) } : {}) }, geometry: { type: 'Point', coordinates: [place.longitude, place.latitude] } }] : [],
          });
          if (changed && previousLabels) weatherMap.labelCities(previousLabels.cities, previousLabels.scores, previousLabels.niceCounts, previousLabels.prioritySlugs);
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
        labelledPlaces() {
          const seen = new Set<string>();
          return map.queryRenderedFeatures({ layers: ['headline-labels', 'city-labels', 'chosen-place'] }).flatMap(feature => {
            const properties = feature.properties;
            const slug = properties.slug as string | undefined;
            const city = cities.find(candidate => slug ? candidate.slug === slug : candidate.name === properties.name && candidate.latitude === properties.latitude && candidate.longitude === properties.longitude);
            if (!city || seen.has(city.slug)) return [];
            const screenPoint = map.project([city.longitude, city.latitude]);
            const coveringElement = document.elementFromPoint(screenPoint.x, screenPoint.y);
            if (coveringElement && !container.contains(coveringElement)) return [];
            seen.add(city.slug);
            return [{ ...city, displayedNumber: Number.isFinite(properties.displayedNumber) ? properties.displayedNumber : undefined }];
          });
        },
        rankingCandidates(minimumPopulation) {
          const fontSize = (design.labelSmall + (design.labelLarge - design.labelSmall) * Math.max(0, Math.min(1, (map.getZoom() - 1) / 8))) * design.labelScale;
          const halfWidth = fontSize * design.labelWidth / 2 + design.labelHaloWidth;
          const halfHeight = (fontSize * design.labelLineHeight * 3 + design.countHeight * design.labelScale) / 2;
          return cities.filter(city => {
            if (city.population < minimumPopulation) return false;
            const point = map.project([city.longitude, city.latitude]);
            if (point.x < halfWidth || point.x > container.clientWidth - halfWidth || point.y < halfHeight || point.y > container.clientHeight - halfHeight) return false;
            return [[0, 0], [-halfWidth, -halfHeight], [halfWidth, halfHeight], [-halfWidth, halfHeight], [halfWidth, -halfHeight]].every(([x, y]) => {
              const element = document.elementFromPoint(point.x + x, point.y + y);
              return !element || container.contains(element);
            });
          });
        },
        labelCandidates() {
          const minimumPopulation = minimumLabelPopulations[Math.min(5, Math.max(0, Math.floor(map.getZoom()) - 1))];
          return cities.filter(city => city.population >= minimumPopulation);
        },
        labelCities(cities, scores, niceCounts, prioritySlugs = []) {
          previousLabels = {cities, scores, niceCounts, prioritySlugs};
          const isChosen = (city: CityPoint) => Boolean(chosenPlace && kilometresBetween(city, chosenPlace) < 1);
          const fontSize = (design.labelSmall + (design.labelLarge - design.labelSmall) * Math.max(0, Math.min(1, (map.getZoom() - 1) / 8))) * design.labelScale;
          type LabelBox = {x: number; y: number; width: number; height: number};
          type Placement = {slug: string; point: maplibregl.Point; box: LabelBox; end: maplibregl.Point};
          const insideBox = (point: maplibregl.Point, box: LabelBox) => Math.abs(point.x - box.x) < box.width / 2 && Math.abs(point.y - box.y) < box.height / 2;
          const cross = (a: maplibregl.Point, b: maplibregl.Point, c: maplibregl.Point) => (b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x);
          const linesCross = (a: maplibregl.Point, b: maplibregl.Point, c: maplibregl.Point, d: maplibregl.Point) => cross(a, b, c) * cross(a, b, d) < 0 && cross(c, d, a) * cross(c, d, b) < 0;
          const crossesBox = (start: maplibregl.Point, end: maplibregl.Point, box: LabelBox) => {
            if (insideBox(start, box) || insideBox(end, box)) return true;
            const corners = [[-1, -1], [1, -1], [1, 1], [-1, 1]].map(([x, y]) => new maplibregl.Point(box.x + x * box.width / 2, box.y + y * box.height / 2));
            return corners.some((corner, index) => linesCross(start, end, corner, corners[(index + 1) % 4]));
          };
          const boxesOverlap = (a: LabelBox, b: LabelBox) => Math.abs(a.x - b.x) < (a.width + b.width) / 2 && Math.abs(a.y - b.y) < (a.height + b.height) / 2;
          const options = prioritySlugs.flatMap(slug => {
            const city = cities.find(place => place.slug === slug);
            if (!city) return [];
            const point = map.project([city.longitude, city.latitude]);
            const textWidth = city.name.length * fontSize * 0.65;
            const width = Math.min(textWidth, fontSize * design.labelWidth) + design.labelPadding * 2;
            const height = fontSize * design.labelLineHeight * Math.ceil(textWidth / (fontSize * design.labelWidth)) + design.countHeight * design.labelScale + design.labelPadding * 2;
            const choices: Placement[] = [];
            for (let row = -4; row <= 4; row++) for (let column = -4; column <= 4; column++) {
              const box = {x: point.x + column * width / 2, y: point.y + row * height / 2, width, height};
              const clear = [-1, 0, 1].every(x => [-1, 0, 1].every(y => {
                const px = box.x + x * width / 2, py = box.y + y * height / 2;
                const element = document.elementFromPoint(px, py);
                return px > 0 && px < container.clientWidth && py > 0 && py < container.clientHeight && (!element || container.contains(element));
              }));
              if (!clear) continue;
              const dx = point.x - box.x, dy = point.y - box.y;
              const edge = Math.max(Math.abs(dx) / (width / 2), Math.abs(dy) / (height / 2));
              const end = edge <= 1 ? point : new maplibregl.Point(box.x + dx / edge, box.y + dy / edge);
              choices.push({slug, point, box, end});
            }
            choices.sort((a, b) => Math.hypot(a.box.x - a.point.x, a.box.y - a.point.y) - Math.hypot(b.box.x - b.point.x, b.box.y - b.point.y));
            return [choices];
          });
          const placeLabels = (index: number, placed: Placement[]): Placement[] | null => {
            if (index === options.length) return placed;
            for (const choice of options[index]) {
              if (placed.some(other => boxesOverlap(choice.box, other.box)
                || linesCross(choice.point, choice.end, other.point, other.end)
                || crossesBox(choice.point, choice.end, other.box) || crossesBox(other.point, other.end, choice.box))) continue;
              const result = placeLabels(index + 1, [...placed, choice]);
              if (result) return result;
            }
            return null;
          };
          const placed = placeLabels(0, []) ?? [];
          const offsets = new Map(placed.map(({slug, point, box}) => [slug, [(box.x - point.x) / fontSize, (box.y - point.y) / fontSize]]));
          const pointers: GeoJSON.Feature<GeoJSON.LineString>[] = placed.filter(({point, end}) => point.dist(end) > 0).map(({point, end}) => ({
            type: 'Feature', properties: {}, geometry: {type: 'LineString', coordinates: [map.unproject(point).toArray(), map.unproject(end).toArray()]},
          }));
          (map.getSource('headline-pointers') as GeoJSONSource).setData({type: 'FeatureCollection', features: pointers});
          (map.getSource('city-scores') as GeoJSONSource).setData({
            type: 'FeatureCollection',
            features: cities.map((city, index) => {
              const score = scores[index];
              const count = niceCounts?.[index];
              return {
                type: 'Feature',
                properties: {
                  name: city.name, population: city.population, slug: city.slug, longitude: city.longitude, latitude: city.latitude,
                  ...(isChosen(city) ? {chosen: true} : {}),
                  ...(offsets.has(city.slug) ? {headlineRank: prioritySlugs.indexOf(city.slug), headlineOffset: offsets.get(city.slug)} : {}),
                  ...(Number.isFinite(score) ? { score: Math.floor(score) } : {}),
                  ...(Number.isFinite(score) && count !== undefined && Number.isFinite(count) ? { countImage: countImage(score, count), displayedNumber: Math.round(count) } : {}),
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
