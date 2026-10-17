// Written by AI on 2026.10.16 by model claude opus 5.5.
// The Sites Worker. It serves the app and the weather files, and does the work
// for a clicked place so the browser only draws:
//   GET /data/...                 weather files from Sites storage (R2 binding WEATHER)
//   GET /api/place?lat=&lon=      the place's name and its typical year, as JSON
//   GET /api/search?q=            places by name
//   PUT /__publish/<key>          the publisher uploads files (bearer token)
//   POST /__prune                 the publisher removes forecast runs no longer current or previous

import { createFinePlaceLoader, placeNumbers, placeMonths } from './fine-place.mjs';

const WEB_ASSETS = /*WEB_ASSETS*/ {};
const IMMUTABLE = 'public, max-age=31536000, immutable';

async function refreshSignature(secret, expires) {
  const key = await crypto.subtle.importKey('raw', new TextEncoder().encode(secret), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
  return btoa(String.fromCharCode(...new Uint8Array(await crypto.subtle.sign('HMAC', key, new TextEncoder().encode(`forecast:${expires}`)))));
}

async function isForecastPublisher(request, env) {
  const token = request.headers.get('Authorization')?.replace(/^Bearer /, '') ?? '';
  const [scope, expires, signature] = token.split('.');
  return scope === 'forecast' && Number(expires) > Date.now() && Number(expires) <= Date.now() + 3600000
    && signature === await refreshSignature(env.WEATHER_PUBLISH_TOKEN, expires);
}

async function siteTool(request, env) {
  const { id, method, params } = await request.json();
  const reply = (result) => Response.json({ jsonrpc: '2.0', id, result });
  if (method === 'server/discover') return reply({ supportedVersions: ['2026-07-28'], capabilities: { tools: {} } });
  if (method === 'initialize') return reply({ protocolVersion: '2026-07-28', capabilities: { tools: {} }, serverInfo: { name: 'Nice Weather', version: '1' } });
  if (method === 'tools/list') return reply({ tools: [{ name: 'begin_forecast_refresh', description: 'Start the owner’s daily ECMWF forecast refresh. Returns a temporary upload credential and the standalone program URL. Follow the saved instructions; keep credentials out of output.', inputSchema: { type: 'object', properties: {}, additionalProperties: false } }] });
  if (method === 'tools/call' && params?.name === 'begin_forecast_refresh') {
    if (!env.WEATHER_OWNER_EMAIL || request.headers.get('oai-authenticated-user-email') !== env.WEATHER_OWNER_EMAIL) return reply({ isError: true, content: [{ type: 'text', text: 'Only the Site owner can refresh the forecast.' }] });
    const expires = String(Date.now() + 3600000);
    const origin = new URL(request.url).origin;
    return reply({ content: [{ type: 'text', text: JSON.stringify({ token: `forecast.${expires}.${await refreshSignature(env.WEATHER_PUBLISH_TOKEN, expires)}`, expiresAt: new Date(Number(expires)).toISOString(), binary: `${origin}/data/runner/niceweather-linux-amd64`, instructions: `${origin}/data/runner/refresh.md`, site: origin }) }] });
  }
  return Response.json({ jsonrpc: '2.0', id, error: { code: -32601, message: 'Method not found' } });
}

// ---------------------------------------------------------------- storage helpers

const memo = new Map();
const fineLoaders = new WeakMap();
function finePlaces(bucket) {
  if (!fineLoaders.has(bucket)) fineLoaders.set(bucket, createFinePlaceLoader(async key => {
    const object = await bucket.get(key);
    return object ? new Uint8Array(await object.arrayBuffer()) : null;
  }));
  return fineLoaders.get(bucket);
}
async function cachedJson(bucket, key, seconds) {
  const hit = memo.get(key);
  if (hit && hit.expires > Date.now()) return hit.value;
  const object = await bucket.get(key);
  const value = object ? await object.json() : null;
  memo.set(key, { value, expires: Date.now() + seconds * 1000 });
  return value;
}

async function readBytes(bucket, key, offset, length) {
  const object = await bucket.get(key, { range: { offset, length } });
  return object ? new DataView(await object.arrayBuffer()) : null;
}

// ---------------------------------------------------------------- places

let placesPromise;
function loadPlaces(bucket) {
  placesPromise ??= (async () => {
    const text = await (await bucket.get('places.tsv')).text();
    const places = text.split('\n').map((line) => {
      const [name, plainName, region, latitude, longitude, population, geonamesPlainName = plainName, searchAliases = ''] = line.split('\t');
      const keys = [...new Set([plainName, geonamesPlainName, ...searchAliases.split('|')].filter(Boolean).map(normalise))];
      return { name, keys, slug: slugFor(plainName, region), region, latitude: Number(latitude), longitude: Number(longitude), population: Number(population) };
    });
    const bySlug = new Map();
    for (const place of [...places].sort((left, right) => right.population - left.population)) {
      if (!bySlug.has(place.slug)) bySlug.set(place.slug, place);
    }
    const byFirstLetters = new Map();
    for (const place of places) {
      for (const firstLetters of new Set(place.keys.map(key => key.slice(0, 2)))) {
        if (!byFirstLetters.has(firstLetters)) byFirstLetters.set(firstLetters, []);
        byFirstLetters.get(firstLetters).push(place);
      }
    }
    const byDegree = new Map();
    for (const place of places) {
      const key = `${Math.floor(place.latitude)},${Math.floor(place.longitude)}`;
      if (!byDegree.has(key)) byDegree.set(key, []);
      byDegree.get(key).push(place);
    }
    return { places, byDegree, bySlug, byFirstLetters };
  })();
  return placesPromise;
}

const normalise = (text) => text.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase();
const kebab = (text) => normalise(text).replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
const countryOf = (region) => kebab(region.split(',').pop() ?? '');
/** "Chania" in "Crete, Greece" -> "greece/chania": the country, then the place, as a readable URL path. */
const slugFor = (plainName, region) => `${countryOf(region)}/${kebab(plainName)}`;

function kilometres(latitudeA, longitudeA, latitudeB, longitudeB) {
  const radians = Math.PI / 180;
  const x = (longitudeB - longitudeA) * radians * Math.cos(((latitudeA + latitudeB) / 2) * radians);
  const y = (latitudeB - latitudeA) * radians;
  return Math.sqrt(x * x + y * y) * 6371;
}

/** The town or city you clicked in, as a map app would name it: size counts, but falls off fast with distance,
 *  so a click in Manila names Manila, not one of its districts or Quezon City next door. */
async function nameForPoint(bucket, latitude, longitude) {
  const { byDegree, bySlug } = await loadPlaces(bucket);
  let best = null;
  let bestPull = 0;
  let nearest = null;
  let nearestDistance = Infinity;
  for (let dLatitude = -1; dLatitude <= 1; dLatitude++) {
    for (let dLongitude = -1; dLongitude <= 1; dLongitude++) {
      for (const place of byDegree.get(`${Math.floor(latitude) + dLatitude},${Math.floor(longitude) + dLongitude}`) ?? []) {
        const distance = kilometres(latitude, longitude, place.latitude, place.longitude);
        const pull = place.population / (distance + 2) ** 2;
        if (distance < 30 && pull > bestPull) { best = place; bestPull = pull; }
        if (distance < nearestDistance) { nearest = place; nearestDistance = distance; }
      }
    }
  }
  if (best) return { name: best.name, region: best.region, slug: bySlug.get(best.slug) === best ? best.slug : undefined };
  if (nearest) return { name: `Near ${nearest.name}`, region: nearest.region };
  const north = latitude >= 0 ? 'N' : 'S';
  const east = longitude >= 0 ? 'E' : 'W';
  return { name: `${Math.abs(latitude).toFixed(2)}° ${north}, ${Math.abs(longitude).toFixed(2)}° ${east}`, region: '' };
}

// Islands and regions people search for, which the town list does not name, with the towns that stand for them.
const AREA_TOWNS = {
  tenerife: ['spain/adeje', 'spain/arona', 'spain/santa-cruz-de-tenerife'], 'gran canaria': ['spain/las-palmas-de-gran-canaria'], lanzarote: ['spain/arrecife'],
  fuerteventura: ['spain/puerto-del-rosario'], 'canary islands': ['spain/arona', 'spain/las-palmas-de-gran-canaria'], canaries: ['spain/arona', 'spain/las-palmas-de-gran-canaria'],
  mallorca: ['spain/palma'], majorca: ['spain/palma'], ibiza: ['spain/ibiza'], madeira: ['portugal/funchal'], algarve: ['portugal/faro', 'portugal/albufeira'],
  'costa del sol': ['spain/malaga', 'spain/marbella'], crete: ['greece/chania', 'greece/heraklion'], santorini: ['greece/fira'], corfu: ['greece/kerkyra'],
  corsica: ['france/ajaccio'], sardinia: ['italy/cagliari'], sicily: ['italy/palermo', 'italy/catania'], cyprus: ['cyprus/limassol', 'cyprus/paphos'],
  malta: ['malta/birkirkara', 'malta/valletta'], provence: ['france/marseille', 'france/aix-en-provence'], tuscany: ['italy/florence'], amalfi: ['italy/amalfi'],
  bali: ['indonesia/denpasar'], phuket: ['thailand/phuket'], maui: ['united-states/kahului'], oahu: ['united-states/honolulu'], hawaii: ['united-states/honolulu'],
};

const COORDINATES = /^\s*(-?\d{1,2}(?:\.\d+)?)\s*[, ]\s*(-?\d{1,3}(?:\.\d+)?)\s*$/;
const asResult = ({ name, region, latitude, longitude, slug }) => ({ name, region, latitude, longitude, slug });

function editDistanceAtMost(left, right, limit) {
  if (Math.abs(left.length - right.length) > limit) return false;
  let previous = Array.from({ length: right.length + 1 }, (_, index) => index);
  for (let i = 1; i <= left.length; i++) {
    const current = [i];
    for (let j = 1; j <= right.length; j++) current[j] = Math.min(previous[j] + 1, current[j - 1] + 1, previous[j - 1] + (left[i - 1] === right[j - 1] ? 0 : 1));
    if (Math.min(...current) > limit) return false;
    previous = current;
  }
  return previous[right.length] <= limit;
}

async function search(bucket, query) {
  const { places, bySlug, byFirstLetters } = await loadPlaces(bucket);
  const coordinates = query.match(COORDINATES);
  if (coordinates) {
    const latitude = Number(coordinates[1]);
    const longitude = Number(coordinates[2]);
    if (Math.abs(latitude) <= 90 && Math.abs(longitude) <= 180) return [{ ...(await nameForPoint(bucket, latitude, longitude)), latitude, longitude }];
  }
  const wanted = normalise(query.trim()).replace(/\s+/g, ' ');
  if (wanted.length < 2) return [];
  const exact = (place) => (place.keys.includes(wanted) ? 0 : 1);
  const byBiggest = (left, right) => exact(left) - exact(right) || right.population - left.population;
  const areaTowns = (AREA_TOWNS[wanted] ?? []).map((slug) => bySlug.get(slug)).filter(Boolean);
  let found = places.filter((place) => place.keys.some(key => key.startsWith(wanted)));
  // "Málaga Spain", "san diego, california": a name, then its region or country.
  if (!found.length && /[ ,]/.test(wanted)) {
    const words = wanted.split(/[ ,]+/).filter(Boolean);
    for (let split = words.length - 1; split >= 1 && !found.length; split--) {
      const name = words.slice(0, split).join(' ');
      const where = words.slice(split).join(' ');
      found = places.filter((place) => place.keys.some(key => key.startsWith(name)) && normalise(place.region).includes(where));
    }
  }
  // Another spelling of the same name ("Marrakech" for Marrakesh): close names with the same first letters.
  if (found.length < 3 && wanted.length >= 4) {
    const close = (byFirstLetters.get(wanted.slice(0, 2)) ?? []).filter((place) => !found.includes(place) && place.keys.some(key => editDistanceAtMost(key.slice(0, wanted.length + 1), wanted, 2)));
    found = [...found, ...close.sort(byBiggest).slice(0, 5)];
  }
  found.sort(byBiggest);
  return [...areaTowns, ...found.filter((place) => !areaTowns.includes(place))].slice(0, 8).map(asResult);
}

// ---------------------------------------------------------------- a place's typical year

// v6/climate/facts.bin: the 0.25° grid (1440 x 721, north to south, from 180°W), 72 bytes a point:
// twelve months x [air high, feels-like at 21:00, dew point (°C = byte / 3 - 30), wet days %, sunny %, wind km/h x 2].
// Sea points are all zeros. See .agent/v6-contract.md.
const FACTS_KEY = 'v6/climate/facts.bin';
const FINE_WIDTH = 1440;
const FINE_HEIGHT = 721;
const FACT_BYTES_PER_POINT = 72;
const temperatureFromByte = (byte) => Math.round((byte / 3 - 30) * 10) / 10;

function decodeTypicalYear(view, offset) {
  return Array.from({ length: 12 }, (_, month) => {
    const at = offset + month * 6;
    return {
      month: month + 1,
      highC: temperatureFromByte(view.getUint8(at)),
      feelsAt21C: temperatureFromByte(view.getUint8(at + 1)),
      dewPointC: temperatureFromByte(view.getUint8(at + 2)),
      wetDaysPercent: view.getUint8(at + 3),
      sunnyPercent: view.getUint8(at + 4),
      windKmh: view.getUint8(at + 5) / 2,
    };
  });
}

/** The same exact-coordinate, row-major nearest land point used by the browser. */
async function nearestLandPoint(bucket, latitude, longitude) {
  const offsets = await Promise.all(Array.from({ length: 12 }, (_, month) => monthlyOffsets(bucket, month + 1)));
  const index = nearestFineIndex(latitude, longitude, offsets);
  if (index === null) return null;
  const view = await readBytes(bucket, FACTS_KEY, index * FACT_BYTES_PER_POINT, FACT_BYTES_PER_POINT);
  if (!view) return null;
  return { row: Math.floor(index / FINE_WIDTH), column: index % FINE_WIDTH, months: decodeTypicalYear(view, 0) };
}

/** A place's typical year with the default taste's score for each month (null where unknown), or null at sea. */
async function typicalYearForPlace(bucket, latitude, longitude) {
  const fine = finePlaces(bucket);
  if (await fine.enabled()) {
    const packet = await fine.load(latitude, longitude);
    return packet ? placeMonths(packet) : null;
  }
  const point = await nearestLandPoint(bucket, latitude, longitude);
  if (!point) return null;
  const scores = await readBytes(bucket, 'v6/climate/scores.bin', (point.row * FINE_WIDTH + point.column) * 12, 12);
  return point.months.map((month, index) => ({ ...month, score: scores && scores.getUint8(index) !== 255 ? scores.getUint8(index) : null }));
}

// ---------------------------------------------------------------- shareable URLs

// App paths: /, /<period>, /<slug>, /<slug>/<period>, /@<lat>,<lon>[,<zoom>z][/<period>].
const MONTH_WORDS = ['january', 'february', 'march', 'april', 'may', 'june', 'july', 'august', 'september', 'october', 'november', 'december'];
const MONTH_ABBREVIATIONS = MONTH_WORDS.map(word => word.slice(0, 3));

function periodFromWord(word) {
  if (word === 'next-14-days') return { word, kind: 'forecast', when: 'over the next 14 days' };
  if (/^day-(?:[0-9]|1[0-3])$/.test(word)) return { word, kind: 'forecast', day: Number(word.slice(4)), when: `on forecast day ${Number(word.slice(4)) + 1}` };
  const month = MONTH_WORDS.indexOf(word);
  if (month >= 0) return { word, kind: 'climate', months: [month + 1], when: `in ${word[0].toUpperCase()}${word.slice(1)}` };
  const run = word.split('-');
  if (run.length === 2 && run.every(part => MONTH_ABBREVIATIONS.includes(part))) {
    const start = MONTH_ABBREVIATIONS.indexOf(run[0]), end = MONTH_ABBREVIATIONS.indexOf(run[1]);
    const months = Array.from({ length: (end - start + 12) % 12 + 1 }, (_, index) => (start + index) % 12 + 1);
    const capitalised = index => MONTH_WORDS[index][0].toUpperCase() + MONTH_WORDS[index].slice(1);
    return { word, kind: 'climate', months, when: `from ${capitalised(start)} to ${capitalised(end)}` };
  }
  if (word === 'year') return { word, kind: 'climate', months: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12], when: 'all year' };
  return null;
}

/** A place and period, or null for an unknown path. */
async function viewFromPath(bucket, pathname) {
  const parts = pathname.split('/').filter(Boolean).map(decodeURIComponent);
  if (parts.length > 3) return null;
  if (!parts.length) return { place: null, period: null };
  if (parts[0].startsWith('@')) {
    const [latitude, longitude] = parts[0].slice(1).split(',').map(Number);
    if (!Number.isFinite(latitude) || !Number.isFinite(longitude) || Math.abs(latitude) > 90 || parts.length > 2) return null;
    const period = parts[1] ? periodFromWord(parts[1]) : null;
    if (parts[1] && !period) return null;
    return { place: { ...(await nameForPoint(bucket, latitude, longitude)), latitude, longitude }, period };
  }
  if (parts.length === 1 && periodFromWord(parts[0])) return { place: null, period: periodFromWord(parts[0]) };
  const { bySlug } = await loadPlaces(bucket);
  const place = parts.length >= 2 ? bySlug.get(`${parts[0]}/${parts[1]}`) : null;
  if (!place) return null;
  const period = parts[2] ? periodFromWord(parts[2]) : null;
  if (parts[2] && !period) return null;
  return { place, period };
}

// The twelve monthly bytes cannot describe a run's distribution. Read the same
// sampled days and fine offsets as the app for a place's default-taste preview.
const MONTH_DAYS = [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
const THRESHOLDS = [0, 25, 40, 55, 70, 85, 100];
const wrap = (value, width) => (value % width + width) % width;
const clamp = (value, low, high) => Math.max(low, Math.min(high, value));
const weatherFiles = new Map();
function weatherFile(bucket, key, decode) {
  if (!weatherFiles.has(key)) {
    weatherFiles.set(key, (async () => {
      const object = await bucket.get(key);
      if (!object) throw new Error(`Weather file missing: ${key}`);
      return decode(new Uint8Array(await object.arrayBuffer()));
    })().catch(error => { weatherFiles.delete(key); throw error; }));
  }
  return weatherFiles.get(key);
}

async function decodeGreyscalePng(png) {
  const view = new DataView(png.buffer, png.byteOffset, png.byteLength);
  const width = view.getUint32(16), height = view.getUint32(20);
  if (width !== FINE_WIDTH || height !== FINE_HEIGHT || png[24] !== 8 || png[25] !== 0 || png[28] !== 0) throw new Error('Expected a fine-grid greyscale PNG');
  const chunks = [];
  for (let at = 8; at < png.length;) {
    const length = view.getUint32(at);
    if (String.fromCharCode(...png.subarray(at + 4, at + 8)) === 'IDAT') chunks.push(png.subarray(at + 8, at + 8 + length));
    at += length + 12;
  }
  const filtered = new Uint8Array(await new Response(new Blob(chunks).stream().pipeThrough(new DecompressionStream('deflate'))).arrayBuffer());
  const values = new Uint8Array(width * height);
  for (let row = 0; row < height; row++) {
    const filter = filtered[row * (width + 1)], source = row * (width + 1) + 1, target = row * width;
    for (let column = 0; column < width; column++) {
      const left = column ? values[target + column - 1] : 0, up = row ? values[target - width + column] : 0, diagonal = row && column ? values[target - width + column - 1] : 0;
      let predictor = 0;
      if (filter === 1) predictor = left;
      else if (filter === 2) predictor = up;
      else if (filter === 3) predictor = (left + up) >> 1;
      else if (filter === 4) {
        const estimate = left + up - diagonal;
        const a = Math.abs(estimate - left), b = Math.abs(estimate - up), c = Math.abs(estimate - diagonal);
        predictor = a <= b && a <= c ? left : b <= c ? up : diagonal;
      }
      values[target + column] = (filtered[source + column] + predictor) & 255;
    }
  }
  return values;
}
const monthlyOffsets = (bucket, month) => weatherFile(bucket, `v6/climate/offsets-${String(month).padStart(2, '0')}.png`, decodeGreyscalePng);

function decodeCellOrdinals(bytes) {
  const pairs = new Uint16Array(bytes.buffer, bytes.byteOffset, bytes.byteLength / 2);
  const land = new Int32Array(360 * 180).fill(-1);
  for (let cell = 0; cell < pairs.length / 2; cell++) land[pairs[cell * 2] * 360 + pairs[cell * 2 + 1]] = cell;
  const rowNearest = new Int32Array(land.length).fill(-1), rowDistance = new Float64Array(land.length).fill(Infinity);
  for (let row = 0; row < 180; row++) {
    let nearest = -1080;
    for (let column = -360; column < 720; column++) {
      if (land[row * 360 + wrap(column, 360)] >= 0) nearest = column;
      if (column >= 0 && column < 360 && nearest !== -1080) {
        rowNearest[row * 360 + column] = land[row * 360 + wrap(nearest, 360)];
        rowDistance[row * 360 + column] = (column - nearest) ** 2;
      }
    }
    nearest = 1080;
    for (let column = 719; column >= -360; column--) {
      if (land[row * 360 + wrap(column, 360)] >= 0) nearest = column;
      if (column >= 0 && column < 360 && nearest !== 1080 && (column - nearest) ** 2 < rowDistance[row * 360 + column]) {
        rowNearest[row * 360 + column] = land[row * 360 + wrap(nearest, 360)];
        rowDistance[row * 360 + column] = (column - nearest) ** 2;
      }
    }
  }
  const ordinals = new Uint16Array(land.length);
  for (let row = 0; row < 180; row++) for (let column = 0; column < 360; column++) {
    const index = row * 360 + column;
    if (land[index] >= 0) { ordinals[index] = land[index]; continue; }
    let distance = Infinity;
    for (let sourceRow = 0; sourceRow < 180; sourceRow++) {
      const candidate = sourceRow * 360 + column, squared = (row - sourceRow) ** 2 + rowDistance[candidate];
      if (squared < distance) { distance = squared; ordinals[index] = rowNearest[candidate]; }
    }
  }
  return ordinals;
}

function nearestFineIndex(latitude, longitude, offsets) {
  if (!Number.isFinite(latitude) || !Number.isFinite(longitude) || latitude < -60 || latitude > 90) return null;
  const x = wrap((longitude + 180) * 4, FINE_WIDTH), y = (90 - latitude) * 4;
  const centreColumn = Math.round(x), centreRow = Math.round(y);
  let nearest = null, distance = Infinity;
  for (let dr = -2; dr <= 2; dr++) {
    const row = centreRow + dr;
    if (row < 0 || row > 600) continue;
    for (let dc = -2; dc <= 2; dc++) {
      const column = centreColumn + dc, index = row * FINE_WIDTH + wrap(column, FINE_WIDTH);
      if (offsets.some(month => month[index] === 255)) continue;
      const squared = (row - y) ** 2 + (column - x) ** 2;
      if (squared < distance) { nearest = index; distance = squared; }
    }
  }
  return nearest;
}

function curve(points, value) {
  if (value <= points[0][0]) return points[0][1];
  for (let i = 1; i < points.length; i++) if (value <= points[i][0]) {
    const [x, y] = points[i - 1], [nextX, nextY] = points[i];
    return y + (value - x) / (nextX - x) * (nextY - y);
  }
  return points.at(-1)[1];
}
const COLD = [[9,0],[11,3],[13,10],[15,22],[17,40],[19,62],[21,85],[23,100]];
const HEAT = [[28,100],[31,85],[34,60],[37,30],[40,10],[43,0]];
const EVENING = [[4,0],[8,30],[12,65],[16,90],[19,100]];
const RAIN = [[0,1],[0.5,0.95],[1,0.88],[2,0.74],[4,0.5],[8,0.22],[15,0.05]];
const WIND = [[15,1],[20,0.97],[25,0.92],[30,0.85],[40,0.65],[50,0.4],[65,0.15]];
const HUMIDITY = [[16,1],[18,0.95],[20,0.85],[22,0.65],[24,0.4],[26,0.2],[28,0.1]];
const SUN = [[0,0.75],[0.3,0.85],[0.6,0.95],[0.8,1]];
function defaultDayScore(bytes, at, shift) {
  const sun = Array.from({length: 6}, (_, i) => [i * 2, bytes[at + i] / 3 - 30 + shift]);
  const shade = Array.from({length: 4}, (_, i) => [i * 3 + 2, bytes[at + 6 + i] / 3 - 30 + shift]);
  const daylight = Math.max(1, bytes[at + 14]);
  let daytime = 0;
  for (let hour = 0; hour < daylight; hour++) daytime += Math.min(curve(COLD, curve(sun, hour)), curve(HEAT, curve(shade, hour)));
  const evening = Math.min(curve(EVENING, shade[3][1]), curve(HEAT, shade[3][1]));
  return (daytime + 2 * evening) / (daylight + 2)
    * curve(RAIN, (bytes[at + 11] / 50) ** 2) * curve(WIND, bytes[at + 12] / 2)
    * curve(HUMIDITY, bytes[at + 10] / 3 - 30) * curve(SUN, bytes[at + 13] / daylight);
}

async function defaultPeriodJudgement(bucket, latitude, longitude, period) {
  const fine = finePlaces(bucket);
  if (period.kind === 'climate' && await fine.enabled()) {
    const packet = await fine.load(latitude, longitude);
    return packet ? placeNumbers(packet, period) : null;
  }
  let groups;
  if (period.kind === 'forecast') {
    const current = await cachedJson(bucket, 'v6/forecast/current.json', 60);
    if (!current) return null;
    const days = period.day === undefined ? Array.from({length: 14}, (_, day) => day) : [period.day];
    groups = days.map(day => {
      const date = new Date(`${current.firstDate}T12:00:00Z`); date.setUTCDate(date.getUTCDate() + day);
      return {month: date.getUTCMonth() + 1, samples: 14, firstDay: day, count: 1, days: 1, key: `v6/forecast/${current.run}/days.bin`};
    });
  } else groups = period.months.map(month => ({month, samples: 8, firstDay: 0, count: 8, days: MONTH_DAYS[month - 1], key: `v6/climate/month-${String(month).padStart(2, '0')}.bin`}));
  const [ordinals, ...offsets] = await Promise.all([weatherFile(bucket, 'v6/cells.bin', decodeCellOrdinals), ...groups.map(group => monthlyOffsets(bucket, group.month))]);
  const index = nearestFineIndex(latitude, longitude, offsets);
  if (index === null) return null;
  const row = Math.floor(index / FINE_WIDTH), column = index % FINE_WIDTH;
  const cell = ordinals[clamp(Math.floor(row / 4), 0, 179) * 360 + Math.floor(column / 4)];
  const totalDays = groups.reduce((sum, group) => sum + group.days, 0), shares = THRESHOLDS.map(() => 0);
  let singleDayScore = 0;
  await Promise.all(groups.map(async (group, groupIndex) => {
    const shiftLevel = clamp(((offsets[groupIndex][index] - 128) / 10 + 15) / 3, 0, 10), lower = Math.min(Math.floor(shiftLevel), 9), upperWeight = shiftLevel - lower;
    const view = await readBytes(bucket, group.key, cell * group.samples * 15, group.samples * 15);
    if (!view) throw new Error(`Weather records missing: ${group.key}`);
    const bytes = new Uint8Array(view.buffer, view.byteOffset, view.byteLength);
    for (let sample = group.firstDay; sample < group.firstDay + group.count; sample++) {
      const interpolatedScore = (1 - upperWeight) * defaultDayScore(bytes, sample * 15, -15 + lower * 3)
        + upperWeight * defaultDayScore(bytes, sample * 15, -15 + (lower + 1) * 3);
      const score = Math.round(interpolatedScore * 10000) / 10000;
      singleDayScore = score;
      for (let threshold = 0; threshold < THRESHOLDS.length; threshold++) if (score >= THRESHOLDS[threshold]) shares[threshold] += group.days / group.count / totalDays;
    }
  }));
  let score = 0;
  if (totalDays === 1) score = singleDayScore;
  else if (shares[5] >= 0.5 - 1e-9) score = 85 + Math.max(0, shares[5] - 0.5) * 30;
  else for (let band = 0; band < 5; band++) if (shares[band] >= 0.5 - 1e-9 && shares[band + 1] < 0.5 - 1e-9) {
    score = THRESHOLDS[band] + (THRESHOLDS[band + 1] - THRESHOLDS[band]) * Math.max(0, shares[band] - 0.5) / (shares[band] - shares[band + 1]); break;
  }
  const dayCount = share => Math.round(share * totalDays * 1e9) / 1e9;
  return {score: Math.round(score * 10000) / 10000, totalDays, niceDayCount: dayCount(shares[3]), perfectDayCount: dayCount(shares[5]), awfulDayCount: dayCount(1 - shares[1])};
}

const BAND_WORDS = [[85, 'Perfect'], [70, 'Beautiful'], [55, 'Nice'], [40, 'Fair'], [25, 'Poor'], [0, 'Awful']];
const bandWord = (score) => BAND_WORDS.find(([minimum]) => score >= minimum)[1];
const escapeHtml = (text) => text.replace(/[&<>"]/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[character]);

async function previewFor(bucket, origin, pathname, view) {
  const { place } = view;
  const period = view.period ?? (place ? { word: 'next-14-days', kind: 'forecast', when: 'over the next 14 days' } : null);
  const placeName = place ? place.name : null;
  let title = 'Nice Weather: where on earth is the weather nice?';
  let description = 'A map of the whole world coloured by how nice the weather feels, for the next 14 days or any month of a typical year. Set your own taste.';
  if (placeName && period) {
    title = `${placeName} ${period.when}: Nice Weather`;
    const judgement = await defaultPeriodJudgement(bucket, place.latitude, place.longitude, period);
    description = judgement === null ? `How nice the weather is in ${placeName} ${period.when}.` : `${placeName} ${period.when}: ${bandWord(judgement.score)}${judgement.totalDays > 1 ? ' most days' : ''}, nice score ${Math.floor(judgement.score)} out of 100.`;
    if (judgement && judgement.totalDays > 1) description += ` Nice or better on ${Math.round(judgement.niceDayCount)} of ${judgement.totalDays} days, perfect on ${Math.round(judgement.perfectDayCount)}, awful on ${Math.round(judgement.awfulDayCount)}.`;
  } else if (placeName) {
    title = `${placeName}: Nice Weather`;
    description = `How nice the weather will be in ${placeName} over the next 14 days, and in every month of a typical year.`;
  } else if (period?.kind === 'climate') {
    title = `Where is the weather nice ${period.when}? Nice Weather`;
  }
  const card = async (key) => ((await bucket.head(key)) ? `${origin}/data/${key}` : null);
  const namedPlace = place?.slug && !pathname.startsWith('/@') && !place.slug.startsWith('palestine/');
  const fineClimate = place && period?.kind === 'climate' && await finePlaces(bucket).enabled();
  const image = !fineClimate && period && (!place || namedPlace)
    ? await card(`v6/cards/typical-day/${namedPlace ? place.slug + '/' : 'world-'}${period.word}.png`) : null;
  return { title, description, image, url: `${origin}${pathname}` };
}

function withPreview(html, preview) {
  const tags = [
    `<meta name="description" content="${escapeHtml(preview.description)}" />`,
    `<link rel="canonical" href="${escapeHtml(preview.url)}" />`,
    `<meta property="og:type" content="website" />`,
    `<meta property="og:site_name" content="Nice Weather" />`,
    `<meta property="og:title" content="${escapeHtml(preview.title)}" />`,
    `<meta property="og:description" content="${escapeHtml(preview.description)}" />`,
    `<meta property="og:url" content="${escapeHtml(preview.url)}" />`,
    ...(preview.image ? [
      `<meta property="og:image" content="${escapeHtml(preview.image)}" />`,
      `<meta property="og:image:width" content="1200" />`,
      `<meta property="og:image:height" content="630" />`,
    ] : []),
    `<meta name="twitter:card" content="${preview.image ? 'summary_large_image' : 'summary'}" />`,
    `<meta name="twitter:title" content="${escapeHtml(preview.title)}" />`,
    `<meta name="twitter:description" content="${escapeHtml(preview.description)}" />`,
    ...(preview.image ? [`<meta name="twitter:image" content="${escapeHtml(preview.image)}" />`] : []),
  ].join('\n    ');
  return html
    .replace(/<title>[^<]*<\/title>/, `<title>${escapeHtml(preview.title)}</title>`)
    .replace(/<meta name="description"[^>]*>\s*/, '')
    .replace('</head>', `    ${tags}\n  </head>`);
}

// ---------------------------------------------------------------- daily outlooks

// Written by AI on 2026.10.17 by model GPT-6.1 Sol.
const OUTLOOK_REGIONS = {
  world: { name: 'World', centre: '20,0,1.5z' },
  europe: { name: 'Europe', centre: '49,15,3z' },
  usa: { name: 'USA', centre: '39,-98,3z' },
};
const OUTLOOK_STYLE = `
  :root{font-family:Barlow,system-ui,sans-serif;color:#233b38;background:#f4f5ef;font-synthesis:none}
  *{box-sizing:border-box}body{margin:0}main{max-width:1280px;margin:auto;padding:28px 32px 48px}
  header,nav,.actions{display:flex;align-items:center;gap:24px;flex-wrap:wrap}header{justify-content:space-between;margin-bottom:32px}
  a{color:inherit;text-underline-offset:4px}.brand{font-size:36px;font-weight:800;letter-spacing:-2px;text-decoration:none}.brand span{color:#b67c20}
  nav{gap:20px;font-size:16px}nav [aria-current]{font-weight:750;text-decoration-thickness:3px}
  .eyebrow{font-size:14px;letter-spacing:.1em;text-transform:uppercase;color:#526560;margin:0 0 12px}
  h1{font-size:clamp(28px,4vw,46px);line-height:1.12;letter-spacing:-.025em;max-width:1000px;margin:0 0 24px;font-weight:650}
  .poster{display:block;width:100%;height:auto;border:1px solid #dce1d7;border-radius:8px;background:#e8ede5}
  .actions{gap:12px;margin:24px 0 16px}.actions a,.actions button{font:inherit;font-weight:600;border:1px solid #c4cec3;border-radius:6px;padding:12px 18px;background:transparent;color:inherit;text-decoration:none;cursor:pointer}
  .actions button{background:#233b38;color:#fff;border-color:#233b38}a:focus-visible,button:focus-visible{outline:3px solid #b67c20;outline-offset:4px}
  .note{font-size:15px;line-height:1.6;color:#526560;margin:0}.status{min-height:24px;font-size:14px}.unavailable{max-width:720px;padding:48px 0}
  @media(max-width:600px){main{padding:20px 16px 32px}header{gap:20px;margin-bottom:28px}.brand{font-size:32px}nav{gap:16px}.actions{align-items:stretch}.actions a,.actions button{text-align:center}}
`;

async function outlookPage(request, bucket, url) {
  const region = url.pathname.slice('/outlook/'.length);
  const settings = Object.hasOwn(OUTLOOK_REGIONS, region) ? OUTLOOK_REGIONS[region] : null;
  if (!settings) return new Response('Not found', { status: 404 });
  const tomorrow = new Date();
  tomorrow.setUTCDate(tomorrow.getUTCDate() + 1);
  const tomorrowDate = tomorrow.toISOString().slice(0, 10);
  const requestedDate = url.searchParams.get('date') ?? tomorrowDate;
  const outlookDate = new Date(`${requestedDate}T12:00:00Z`);
  if (!/^\d{4}-\d{2}-\d{2}$/.test(requestedDate) || Number.isNaN(outlookDate.valueOf()) || outlookDate.toISOString().slice(0, 10) !== requestedDate) {
    return new Response('Use a date in YYYY-MM-DD format.', { status: 400 });
  }
  const forTomorrow = requestedDate === tomorrowDate;
  const current = await cachedJson(bucket, 'v6/forecast/current.json', 0);
  const runs = current ? [current.run, ...(url.searchParams.has('date') ? [current.previousRun] : [])].filter(Boolean) : [];
  let key = null;
  let outlook = null;
  let latestDate = null;
  for (const run of runs) {
    const candidateKey = `v6/forecast/${run}/outlook/${region}`;
    const candidate = await cachedJson(bucket, `${candidateKey}.json`, 0);
    if (run === current.run) latestDate = candidate?.date;
    if (candidate?.date === requestedDate && candidate.run === run && await bucket.head(`${candidateKey}.png`)) {
      key = candidateKey;
      outlook = candidate;
      break;
    }
  }
  const ready = outlook !== null;
  const sentence = ready ? (forTomorrow ? outlook.sentence : outlook.sentence.replace(' tomorrow, ', ' on ').replace(', will be in and around ', ', in and around ')) : null;
  const image = ready ? `${url.origin}/data/${key}.png` : null;
  const fullDate = outlookDate.toLocaleDateString('en-GB', { timeZone: 'UTC', weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' });
  const title = `${settings.name}: nice weather outlook for ${fullDate} | Nice!`;
  const description = ready ? sentence : forTomorrow
    ? `Tomorrow’s nice weather outlook for ${settings.name} is being refreshed.`
    : `The nice weather outlook for ${settings.name} on ${fullDate} is unavailable.`;
  const navigation = Object.entries(OUTLOOK_REGIONS).map(([word, area]) => `<a href="/outlook/${word}"${word === region ? ' aria-current="page"' : ''}>${area.name}</a>`).join('');
  const dayInCurrentForecast = current ? Math.round((outlookDate - new Date(`${current.firstDate}T12:00:00Z`)) / 86400000) : -1;
  const explore = dayInCurrentForecast >= 0 && dayInCurrentForecast < current.days
    ? `<a href="/@${settings.centre}/day-${dayInCurrentForecast}">Explore ${forTomorrow ? 'tomorrow' : 'this date'}</a>` : '';
  const content = ready ? `
    <p class="eyebrow">${settings.name} · ${escapeHtml(fullDate)}${forTomorrow ? ' · Tomorrow' : ''}</p>
    <h1>${escapeHtml(sentence)}</h1>
    <img class="poster" src="${escapeHtml(image)}" width="1200" height="630" alt="${escapeHtml(sentence)} Map coloured by the day’s nice weather scores." />
    <div class="actions">
      <button id="copy-post" type="button">Copy post</button>
      <a href="${escapeHtml(outlook.fullImage ?? image)}" download="nice-weather-${region}-${outlook.date}.png">Download image</a>
      ${explore}
    </div>
    <p class="note">Places have at least ${(outlook.minimumPopulation ?? 10000).toLocaleString('en-GB')} residents and are at least 250 km apart. Ranked by the default taste; tied whole-number scores favour larger places. Each winning spot is named after the largest eligible place within 50 km and five score points. Forecast from ECMWF. Dates use UTC.</p>
    <p class="status" id="copy-status" role="status"></p>
    <script>
      document.getElementById('copy-post').addEventListener('click', async () => {
        const status = document.getElementById('copy-status');
        try {
          await navigator.clipboard.writeText(document.querySelector('h1').textContent + '\\n' + document.querySelector('link[rel="canonical"]').href);
          status.textContent = 'Post copied.';
        } catch {
          status.textContent = 'Select the sentence above and copy this page’s address to share it.';
        }
      });
    </script>` : `
    <div class="unavailable"><p class="eyebrow">${settings.name} · ${escapeHtml(fullDate)}</p>
    <h1>${forTomorrow ? 'Tomorrow’s outlook is being refreshed.' : 'This dated outlook is unavailable.'}</h1>
    <p class="note">${forTomorrow ? `${latestDate ? `The latest outlook was for ${escapeHtml(latestDate)}. ` : ''}Please check back after the daily forecast refresh.` : `This link is for ${escapeHtml(fullDate)}. Its forecast is no longer available.`}</p>
    <div class="actions"><a href="${forTomorrow ? '/' : `/outlook/${region}`}">${forTomorrow ? 'Explore the weather map' : 'See the current outlook'}</a></div></div>`;
  const html = withPreview(`<!doctype html><html lang="en-GB"><head><meta charset="utf-8" /><meta name="viewport" content="width=device-width,initial-scale=1" /><title>Nice!</title><style>${OUTLOOK_STYLE}</style></head>
    <body><main><header><a class="brand" href="/" aria-label="Nice! weather map">Nice<span>!</span></a><nav aria-label="Outlook region">${navigation}</nav></header>${content}</main></body></html>`,
  { title, description, image, url: `${url.origin}/outlook/${region}?date=${requestedDate}` });
  return new Response(request.method === 'HEAD' ? null : html, {
    status: ready ? 200 : 503,
    headers: { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store', ...(!ready ? { 'Retry-After': '300' } : {}) },
  });
}

// ---------------------------------------------------------------- HTTP

function json(value, cacheSeconds = 0) {
  return Response.json(value, { headers: { 'Cache-Control': cacheSeconds ? `public, max-age=${cacheSeconds}` : 'no-store' } });
}

const mediaTypes = { '.json': 'application/json', '.png': 'image/png', '.bin': 'application/octet-stream', '.tsv': 'text/tab-separated-values; charset=utf-8' };

async function serveData(request, bucket, key) {
  const object = await bucket.get(key);
  if (!object) return new Response('Not found', { status: 404 });
  const mutable = key.endsWith('current.json') || key.startsWith('runner/') || key.includes('/outlook/');
  return new Response(request.method === 'HEAD' ? null : object.body, {
    headers: {
      'Content-Type': mediaTypes[key.slice(key.lastIndexOf('.'))] ?? 'application/octet-stream',
      'Cache-Control': mutable ? 'public, max-age=60' : IMMUTABLE,
      ETag: object.httpEtag,
    },
  });
}

/** Removes forecast runs that are neither the current nor the previous run. */
async function prune(bucket) {
  const prefix = 'v6/forecast/';
  const pointer = `${prefix}current.json`;
  const index = await cachedJson(bucket, pointer, 0);
  if (!index) return json({ removed: 0 });
  const keep = [index.run, index.previousRun].filter(Boolean).map((run) => `${prefix}${run}/`);
  let removed = 0;
  let cursor;
  do {
    const page = await bucket.list({ prefix, cursor, limit: 1000 });
    const expired = page.objects.map((object) => object.key).filter((key) => key !== pointer && !keep.some((kept) => key.startsWith(kept)));
    if (expired.length) await bucket.delete(expired);
    removed += expired.length;
    cursor = page.truncated ? page.cursor : undefined;
  } while (cursor);
  return json({ removed });
}

export async function handleRequest(request, env, serveAsset) {
  const url = new URL(request.url);
  const bucket = env.WEATHER;

  if (url.pathname === '/mcp' && request.method === 'POST') return siteTool(request, env);

  if (url.pathname.startsWith('/__publish/') || url.pathname === '/__prune') {
    const ownerPublisher = env.WEATHER_PUBLISH_TOKEN && request.headers.get('Authorization') === `Bearer ${env.WEATHER_PUBLISH_TOKEN}`;
    const forecastPublisher = env.WEATHER_PUBLISH_TOKEN && await isForecastPublisher(request, env);
    if (!ownerPublisher && !forecastPublisher) return new Response('Not found', { status: 404 });
    if (url.pathname === '/__prune' && request.method === 'POST') return prune(bucket);
    const key = url.pathname.slice('/__publish/'.length);
    if (request.method !== 'PUT') return new Response('Method not allowed', { status: 405 });
    if (forecastPublisher && !key.startsWith('v6/forecast/')) return new Response('Not found', { status: 404 });
    const body = request.headers.get('Content-Encoding') === 'gzip'
      ? await new Response(request.body.pipeThrough(new DecompressionStream('gzip'))).arrayBuffer()
      : request.body;
    await bucket.put(key, body);
    memo.delete(key);
    weatherFiles.delete(key);
    if (key.startsWith('v6/fine/') || key.startsWith('fine-')) fineLoaders.delete(bucket);
    if (key === 'places.tsv') placesPromise = undefined;
    return new Response(null, { status: 201 });
  }
  if (request.method !== 'GET' && request.method !== 'HEAD') return new Response('Method not allowed', { status: 405 });

  if (url.pathname.startsWith('/data/')) {
    const key = decodeURIComponent(url.pathname.slice('/data/'.length));
    if (key.split('/').some((part) => !part || part === '.' || part === '..')) return new Response('Not found', { status: 404 });
    return serveData(request, bucket, key);
  }
  if (url.pathname.startsWith('/outlook/')) return outlookPage(request, bucket, url);
  if (url.pathname === '/api/place') {
    const latitude = Number(url.searchParams.get('lat'));
    const longitude = Number(url.searchParams.get('lon'));
    if (!Number.isFinite(latitude) || !Number.isFinite(longitude) || Math.abs(latitude) > 90) return new Response('Bad coordinates', { status: 400 });
    const fine = finePlaces(bucket);
    const [where, months, fineEnabled] = await Promise.all([nameForPoint(bucket, latitude, longitude), typicalYearForPlace(bucket, latitude, longitude), fine.enabled()]);
    const period = periodFromWord(url.searchParams.get('period') ?? 'year');
    const judgement = fineEnabled && period?.kind === 'climate' ? await defaultPeriodJudgement(bucket, latitude, longitude, period) : undefined;
    return json({ ...where, latitude, longitude, months, ...(fineEnabled ? { resolutionArcSeconds: 30, fineReplay: true, judgement } : {}) }, 3600);
  }
  if (url.pathname === '/api/search') return json(await search(bucket, url.searchParams.get('q') ?? ''), 86400);
  if (url.pathname.startsWith('/api/slug/')) {
    const { bySlug } = await loadPlaces(bucket);
    const place = bySlug.get(decodeURIComponent(url.pathname.slice('/api/slug/'.length)));
    if (!place) return new Response('Not found', { status: 404 });
    return json({ name: place.name, region: place.region, latitude: place.latitude, longitude: place.longitude, slug: place.slug }, 86400);
  }
  const isAppPath = (url.pathname.startsWith('/@') || !/\.[a-z0-9]+$/i.test(url.pathname)) && !url.pathname.startsWith('/api/');
  if (isAppPath && url.pathname !== '/method') {
    const view = await viewFromPath(bucket, url.pathname);
    const page = await serveAsset('/index.html', request);
    if (request.method === 'HEAD') return new Response(null, { status: view ? page.status : 404, headers: page.headers });
    const html = await page.text();
    if (!view) return new Response(withPreview(html, await previewFor(bucket, url.origin, '/', { place: null, period: null })), { status: 404, headers: page.headers });
    return new Response(withPreview(html, await previewFor(bucket, url.origin, url.pathname, view)), { headers: page.headers });
  }
  return serveAsset(url.pathname, request);
}

function serveEmbeddedAsset(pathname, request) {
  const asset = WEB_ASSETS[pathname] ?? WEB_ASSETS[`${pathname}.html`] ?? WEB_ASSETS['/index.html'];
  const page = asset === WEB_ASSETS['/index.html'];
  const bytes = Uint8Array.from(atob(asset.data), (character) => character.charCodeAt(0));
  return new Response(request.method === 'HEAD' ? null : bytes, {
    headers: { 'Content-Type': asset.type, 'Cache-Control': page ? 'no-cache' : IMMUTABLE, 'X-Content-Type-Options': 'nosniff' },
  });
}

export default {
  fetch: (request, env) => handleRequest(request, env, serveEmbeddedAsset),
};
