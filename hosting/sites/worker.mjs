const WEB_ASSETS = __WEB_ASSETS__;
const WEATHER_ROOTS = new Set(['forecast', 'climate', 'places', 'best-weather']);
const IMMUTABLE_CACHE = 'public, max-age=31536000, immutable';

function artifactKey(pathname) {
  const key = decodeURIComponent(pathname).replace(/^\//, '');
  if (!WEATHER_ROOTS.has(key.split('/')[0]) || key.split('/').some(part => !part || part === '.' || part === '..')) return null;
  return key;
}

function mediaType(key) {
  const extension = key.slice(key.lastIndexOf('.'));
  return { '.json': 'application/json', '.nws': 'application/vnd.nice-weather.score-tile', '.nwt': 'application/vnd.nice-weather.daily-point-block', '.nwr': 'application/vnd.nice-weather.forecast-range-block', '.nwc': 'application/vnd.nice-weather.era5-climate-point-block', '.nwd': 'application/vnd.nice-weather.era5-climate-dateline-block' }[extension] || 'application/octet-stream';
}

function byteRange(header, size) {
  const match = /^bytes=(\d*)-(\d*)$/.exec(header);
  if (!match || (!match[1] && !match[2])) return null;
  let start = match[1] ? Number(match[1]) : Math.max(0, size - Number(match[2]));
  let end = match[2] && match[1] ? Math.min(size - 1, Number(match[2])) : size - 1;
  if (!Number.isSafeInteger(start) || !Number.isSafeInteger(end) || start < 0 || start >= size || end < start) return null;
  return { offset: start, length: end - start + 1 };
}

async function serveArtifact(request, bucket, key) {
  const rangeHeader = request.headers.get('Range');
  const head = request.method === 'HEAD' || rangeHeader ? await bucket.head(key) : await bucket.get(key);
  if (!head) return new Response('Not found', { status: 404 });
  const headers = new Headers({ 'Content-Type': mediaType(key), 'Content-Length': String(head.size), 'Cache-Control': key.endsWith('/current.json') ? 'no-cache' : IMMUTABLE_CACHE, 'ETag': head.httpEtag, 'Accept-Ranges': 'bytes', 'X-Content-Type-Options': 'nosniff' });
  if (head.customMetadata?.sha256) headers.set('X-Artifact-SHA256', head.customMetadata.sha256);
  const cachedTags = request.headers.get('If-None-Match');
  if (cachedTags?.split(',').some(tag => tag.trim() === '*' || tag.trim().replace(/^W\//, '') === head.httpEtag)) return new Response(null, { status: 304, headers });
  if (request.method === 'HEAD') return new Response(null, { headers });
  if (!rangeHeader) return new Response(head.body, { headers });
  const range = rangeHeader ? byteRange(rangeHeader, head.size) : undefined;
  if (rangeHeader && !range) return new Response(null, { status: 416, headers: { 'Content-Range': `bytes */${head.size}` } });
  const object = await bucket.get(key, range ? { range } : undefined);
  if (!object) return new Response('Not found', { status: 404 });
  if (range) {
    headers.set('Content-Length', String(range.length));
    headers.set('Content-Range', `bytes ${range.offset}-${range.offset + range.length - 1}/${head.size}`);
  }
  return new Response(object.body, { status: range ? 206 : 200, headers });
}

async function pruneForecast(bucket, continuation) {
  const object = await bucket.get('forecast/current.json');
  if (!object) throw new Error('Forecast pointer is required before pruning');
  const pointer = await object.json();
  const references = [pointer.current, pointer.previous].filter(Boolean);
  if (!pointer.current || references.some(reference => !/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(reference.indexId) || reference.path !== `runs/${reference.indexId}/index.json`)) throw new Error('Invalid forecast retention pointer');
  const runs = references.map(reference => `forecast/runs/${reference.indexId}/`);
  const shortlists = references.map(reference => `best-weather/releases/${reference.indexId}-world-`);
  let removed = 0, bytes = 0;
  const expiredRuns = new Set();
  const collections = [['forecast/runs/', runs], ['best-weather/releases/', shortlists]];
  let prefixIndex = continuation.prefixIndex || 0, cursor = continuation.cursor;
  // Sites canceled the original all-pages request after 89 seconds.
  for (let pages = 0; pages < 8 && prefixIndex < collections.length; pages++) {
      const [prefix, retained] = collections[prefixIndex];
      const page = await bucket.list({ prefix, cursor, limit: 1000 });
      const expired = page.objects.filter(object => !retained.some(prefix => object.key.startsWith(prefix)));
      if (expired.length) await bucket.delete(expired.map(object => object.key));
      for (const object of expired) {
        bytes += object.size;
        if (prefix === 'forecast/runs/') expiredRuns.add(object.key.split('/')[2]);
      }
      removed += expired.length;
      if (page.truncated) cursor = page.cursor;
      else { prefixIndex++; cursor = undefined; }
  }
  return Response.json({ removed, bytes, runIds: [...expiredRuns], continuation: prefixIndex < collections.length ? { prefixIndex, cursor } : null });
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (url.pathname.startsWith('/__publish/') || url.pathname === '/__prune') {
      if (!env.WEATHER_PUBLISH_TOKEN || request.headers.get('Authorization') !== `Bearer ${env.WEATHER_PUBLISH_TOKEN}`) return new Response('Not found', { status: 404 });
      if (url.pathname === '/__prune') {
        if (request.method !== 'POST') return new Response('Method not allowed', { status: 405 });
        return pruneForecast(env.WEATHER, request.headers.get('Content-Type') === 'application/json' ? await request.json() : {});
      }
      if (request.method !== 'PUT') return new Response('Method not allowed', { status: 405 });
      const key = artifactKey(url.pathname.slice('/__publish'.length));
      if (!key) return new Response('Invalid artifact path', { status: 400 });
      await env.WEATHER.put(key, request.body, { customMetadata: { sha256: request.headers.get('X-Artifact-SHA256') || '' } });
      return new Response(null, { status: 201 });
    }
    if (request.method !== 'GET' && request.method !== 'HEAD') return new Response('Method not allowed', { status: 405 });
    const key = artifactKey(url.pathname);
    if (key) return serveArtifact(request, env.WEATHER, key);
    const asset = WEB_ASSETS[url.pathname === '/' ? '/index.html' : url.pathname];
    if (!asset) return new Response('Not found', { status: 404 });
    const bytes = Uint8Array.from(atob(asset.data), character => character.charCodeAt(0));
    return new Response(request.method === 'HEAD' ? null : bytes, { headers: { 'Content-Type': asset.type, 'Content-Length': String(bytes.length), 'Cache-Control': url.pathname === '/' || url.pathname === '/index.html' ? 'no-cache' : IMMUTABLE_CACHE, 'X-Content-Type-Options': 'nosniff' } });
  }
};
