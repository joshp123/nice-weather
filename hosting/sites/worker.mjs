// Written by AI on 2026.10.16 by model claude opus 5.5.
// The Sites Worker. It serves the app and the weather files, and does the work
// for a clicked place so the browser only draws:
//   GET /data/...                 weather files from Sites storage (R2 binding WEATHER)
//   GET /api/place?lat=&lon=      everything the place card shows, as JSON
//   GET /api/search?q=            places by name
//   PUT /__publish/<key>          the publisher uploads files (bearer token)
//   POST /__prune                 the publisher removes forecast runs no longer current or previous

const WEB_ASSETS = /*WEB_ASSETS*/ {};
const IMMUTABLE = 'public, max-age=31536000, immutable';

// One record per grid cell in forecast/<run>/points/<date>.bin, little-endian.
// Matches dailyforecast.PointRecord in Go: temperature i16, dew point i16, rain u16
// (tenths), wet %, cloud %, wind km/h, gust km/h (255 = none), then seven scores.
const FORECAST_RECORD_BYTES = 17;
const FIRST_SCORE_OFFSET = 10;
const NO_DATA = 255;

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

function readForecastRecord(view, date) {
  const score = (index) => view.getUint8(FIRST_SCORE_OFFSET + index);
  if (score(0) === NO_DATA) return { date, scores: null };
  const gust = view.getUint8(9);
  return {
    date,
    scores: { nice: score(0), cycling: score(1), comfort: score(2), dry: score(3), clear: score(4), calmNice: score(5), calmCycling: score(6) },
    temperatureC: view.getInt16(0, true) / 10,
    dewPointC: view.getInt16(2, true) / 10,
    rainMm: view.getUint16(4, true) / 10,
    wetPercent: view.getUint8(6),
    cloudPercent: view.getUint8(7),
    windKph: view.getUint8(8),
    gustKph: gust === NO_DATA ? null : gust,
  };
}

// ---------------------------------------------------------------- storage helpers

const memo = new Map();
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
      const [name, plainName, region, latitude, longitude, population] = line.split('\t');
      return { name, key: normalise(plainName), region, latitude: Number(latitude), longitude: Number(longitude), population: Number(population) };
    });
    const byDegree = new Map();
    for (const place of places) {
      const key = `${Math.floor(place.latitude)},${Math.floor(place.longitude)}`;
      if (!byDegree.has(key)) byDegree.set(key, []);
      byDegree.get(key).push(place);
    }
    return { places, byDegree };
  })();
  return placesPromise;
}

const normalise = (text) => text.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase();

function kilometres(latitudeA, longitudeA, latitudeB, longitudeB) {
  const radians = Math.PI / 180;
  const x = (longitudeB - longitudeA) * radians * Math.cos(((latitudeA + latitudeB) / 2) * radians);
  const y = (latitudeB - latitudeA) * radians;
  return Math.sqrt(x * x + y * y) * 6371;
}

/** The town or city you clicked in, as a map app would name it. */
async function nameForPoint(bucket, latitude, longitude) {
  const { byDegree } = await loadPlaces(bucket);
  let nearest = null;
  let nearestDistance = Infinity;
  for (let dLatitude = -1; dLatitude <= 1; dLatitude++) {
    for (let dLongitude = -1; dLongitude <= 1; dLongitude++) {
      for (const place of byDegree.get(`${Math.floor(latitude) + dLatitude},${Math.floor(longitude) + dLongitude}`) ?? []) {
        // Favour bigger places slightly, so a click between villages names the town.
        const distance = kilometres(latitude, longitude, place.latitude, place.longitude) / (1 + Math.log10(1 + place.population / 5000) * 0.35);
        if (distance < nearestDistance) { nearest = place; nearestDistance = distance; }
      }
    }
  }
  if (nearest && nearestDistance < 30) return { name: nearest.name, region: nearest.region };
  if (nearest) return { name: `Near ${nearest.name}`, region: nearest.region };
  const north = latitude >= 0 ? 'N' : 'S';
  const east = longitude >= 0 ? 'E' : 'W';
  return { name: `${Math.abs(latitude).toFixed(2)}° ${north}, ${Math.abs(longitude).toFixed(2)}° ${east}`, region: '' };
}

async function search(bucket, query) {
  const { places } = await loadPlaces(bucket);
  const wanted = normalise(query.trim());
  if (wanted.length < 2) return [];
  const results = [];
  for (const place of places) {
    if (place.key.startsWith(wanted)) results.push(place);
    if (results.length === 8) break;
  }
  return results.map(({ name, region, latitude, longitude }) => ({ name, region, latitude, longitude }));
}

// ---------------------------------------------------------------- weather for one place

function cellFor(grid, latitude, longitude) {
  const row = Math.min(grid.height - 1, Math.max(0, Math.round((grid.north - latitude) / grid.resolution)));
  const column = ((Math.round((longitude - grid.west) / grid.resolution) % grid.width) + grid.width) % grid.width;
  return { row, column };
}

/** Coastal clicks can land on a sea cell; use the nearest land cell within two cells. */
async function nearestLandCell(bucket, key, grid, recordBytes, scoreOffset, latitude, longitude) {
  const centre = cellFor(grid, latitude, longitude);
  let best = null;
  for (let dRow = -2; dRow <= 2; dRow++) {
    const row = centre.row + dRow;
    if (row < 0 || row >= grid.height) continue;
    const firstColumn = Math.max(0, centre.column - 2);
    const lastColumn = Math.min(grid.width - 1, centre.column + 2);
    const view = await readBytes(bucket, key, (row * grid.width + firstColumn) * recordBytes, (lastColumn - firstColumn + 1) * recordBytes);
    if (!view) return null;
    for (let column = firstColumn; column <= lastColumn; column++) {
      if (view.getUint8((column - firstColumn) * recordBytes + scoreOffset) === NO_DATA) continue;
      const distance = Math.hypot(row - centre.row, column - centre.column);
      if (!best || distance < best.distance) best = { row, column, distance };
    }
  }
  return best;
}

async function forecastForPlace(bucket, latitude, longitude) {
  const index = await cachedJson(bucket, 'forecast/current.json', 60);
  if (!index) return null;
  const base = `forecast/${index.runId}/points`;
  const cell = await nearestLandCell(bucket, `${base}/${index.dates[1]}.bin`, index.grid, FORECAST_RECORD_BYTES, FIRST_SCORE_OFFSET, latitude, longitude);
  if (!cell) return null;
  const offset = (cell.row * index.grid.width + cell.column) * FORECAST_RECORD_BYTES;
  const days = await Promise.all(index.dates.map(async (date) => readForecastRecord(await readBytes(bucket, `${base}/${date}.bin`, offset, FORECAST_RECORD_BYTES), date)));
  return days.filter((day) => day.scores);
}

// One record per grid cell in climate/month-MM/points.bin, little-endian. The field
// list, offsets and scales are in climate/current.json ("points.fields").
function readClimateRecord(view, fields, month) {
  const value = (name) => {
    const field = fields[name];
    const raw = field.type === 'uint8' ? view.getUint8(field.offset) : field.type === 'uint16' ? view.getUint16(field.offset, true) : view.getInt16(field.offset, true);
    return Math.round(raw * field.scale * 10) / 10;
  };
  return {
    month,
    niceChance: { nice: value('niceChancePercent'), cycling: value('cyclingChancePercent') },
    awfulChance: { nice: value('niceAwfulPercent'), cycling: value('cyclingAwfulPercent') },
    medianScore: { nice: value('niceMedianScore'), cycling: value('cyclingMedianScore') },
    temperatureC: value('daytimeTemperatureP50C'),
    coolDayC: value('daytimeTemperatureP10C'),
    warmDayC: value('daytimeTemperatureP90C'),
    highC: value('daytimeMaximumP50C'),
    dewPointC: value('dewPointP50C'),
    muggyDaysPercent: value('muggyDayPercent'),
    rainFreeDaysPercent: value('rainFreeDayPercent'),
    cloudPercent: value('cloudCoverP50Percent'),
    clearDaysPercent: value('clearSkyDayPercent'),
    windKph: value('windP50KPH'),
  };
}

async function climateForPlace(bucket, latitude, longitude) {
  const index = await cachedJson(bucket, 'climate/current.json', 3600);
  if (!index) return null;
  const grid = { ...index.grid, resolution: index.grid.resolutionDegrees };
  const recordBytes = index.points.recordBytes;
  const fields = Object.fromEntries(index.points.fields.map((field) => [field.name, field]));
  const cell = await nearestLandCell(bucket, `climate/${index.months[0]}/points.bin`, grid, recordBytes, fields.niceChancePercent.offset, latitude, longitude);
  if (!cell) return null;
  const offset = (cell.row * grid.width + cell.column) * recordBytes;
  return Promise.all(index.months.map(async (key, position) => readClimateRecord(await readBytes(bucket, `climate/${key}/points.bin`, offset, recordBytes), fields, position + 1)));
}

// ---------------------------------------------------------------- HTTP

function json(value, cacheSeconds = 0) {
  return Response.json(value, { headers: { 'Cache-Control': cacheSeconds ? `public, max-age=${cacheSeconds}` : 'no-store' } });
}

const mediaTypes = { '.json': 'application/json', '.png': 'image/png', '.bin': 'application/octet-stream', '.tsv': 'text/tab-separated-values; charset=utf-8' };

async function serveData(request, bucket, key) {
  const object = await bucket.get(key);
  if (!object) return new Response('Not found', { status: 404 });
  const mutable = key.endsWith('current.json') || key.startsWith('runner/');
  return new Response(request.method === 'HEAD' ? null : object.body, {
    headers: {
      'Content-Type': mediaTypes[key.slice(key.lastIndexOf('.'))] ?? 'application/octet-stream',
      'Cache-Control': mutable ? 'public, max-age=60' : IMMUTABLE,
      ETag: object.httpEtag,
    },
  });
}

async function prune(bucket) {
  const index = await cachedJson(bucket, 'forecast/current.json', 0);
  const keep = [index?.runId, index?.previousRunId].filter(Boolean).map((runId) => `forecast/${runId}/`);
  let removed = 0;
  let cursor;
  do {
    const page = await bucket.list({ prefix: 'forecast/', cursor, limit: 1000 });
    const expired = page.objects.map((object) => object.key).filter((key) => key !== 'forecast/current.json' && !keep.some((prefix) => key.startsWith(prefix)));
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
    if (request.method !== 'PUT') return new Response('Method not allowed', { status: 405 });
    const key = url.pathname.slice('/__publish/'.length);
    if (forecastPublisher && !key.startsWith('forecast/')) return new Response('Not found', { status: 404 });
    const body = request.headers.get('Content-Encoding') === 'gzip'
      ? await new Response(request.body.pipeThrough(new DecompressionStream('gzip'))).arrayBuffer()
      : request.body;
    await bucket.put(key, body);
    memo.delete(key);
    if (key === 'places.tsv') placesPromise = undefined;
    return new Response(null, { status: 201 });
  }
  if (request.method !== 'GET' && request.method !== 'HEAD') return new Response('Method not allowed', { status: 405 });

  if (url.pathname.startsWith('/data/')) {
    const key = decodeURIComponent(url.pathname.slice('/data/'.length));
    if (key.split('/').some((part) => !part || part === '.' || part === '..')) return new Response('Not found', { status: 404 });
    return serveData(request, bucket, key);
  }
  if (url.pathname === '/api/place') {
    const latitude = Number(url.searchParams.get('lat'));
    const longitude = Number(url.searchParams.get('lon'));
    if (!Number.isFinite(latitude) || !Number.isFinite(longitude) || Math.abs(latitude) > 90) return new Response('Bad coordinates', { status: 400 });
    const [where, days, months] = await Promise.all([nameForPoint(bucket, latitude, longitude), forecastForPlace(bucket, latitude, longitude), climateForPlace(bucket, latitude, longitude)]);
    return json({ ...where, latitude, longitude, days, months }, 300);
  }
  if (url.pathname === '/api/search') return json(await search(bucket, url.searchParams.get('q') ?? ''), 86400);
  return serveAsset(url.pathname, request);
}

function serveEmbeddedAsset(pathname, request) {
  const asset = WEB_ASSETS[pathname] ?? WEB_ASSETS['/index.html'];
  const page = asset === WEB_ASSETS['/index.html'];
  const bytes = Uint8Array.from(atob(asset.data), (character) => character.charCodeAt(0));
  return new Response(request.method === 'HEAD' ? null : bytes, {
    headers: { 'Content-Type': asset.type, 'Cache-Control': page ? 'no-cache' : IMMUTABLE, 'X-Content-Type-Options': 'nosniff' },
  });
}

export default {
  fetch: (request, env) => handleRequest(request, env, serveEmbeddedAsset),
};
