import { readFile, readdir, rm, stat } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { dirname, join, resolve } from 'node:path';
import { createInterface } from 'node:readline';

const artifactRoot = resolve(process.argv[2] || 'artifacts');
const origin = new URL(process.argv[3]);
const concurrency = Number(process.argv[4] || 48);
process.stdin.isTTY && process.stdin.setRawMode?.(true);
console.log('Ready for publisher credentials JSON on stdin (input is hidden).');
const input = createInterface({ input: process.stdin, terminal: false });
const credentials = JSON.parse(await new Promise(resolve => input.once('line', resolve)));
input.close();
process.stdin.isTTY && process.stdin.setRawMode?.(false);
const platformHeaders = credentials.siteAccessToken ? { 'OAI-Sites-Authorization': `Bearer ${credentials.siteAccessToken}` } : {};
const pointers = [];
const immutable = new Set();
async function visit(path) {
  const info = await stat(join(artifactRoot, path));
  if (!info.isDirectory()) { immutable.add(path); return; }
  for (const entry of await readdir(join(artifactRoot, path))) await visit(`${path}/${entry}`);
}
async function pointer(root) {
  const path = `${root}/current.json`;
  let bytes;
  try { bytes = await readFile(join(artifactRoot, path)); }
  catch (error) { if (error.code === 'ENOENT') return null; throw error; }
  pointers.push({ path, bytes });
  return JSON.parse(bytes);
}
const forecast = await pointer('forecast');
if (forecast) {
  await visit(`forecast/${dirname(forecast.current.path)}`);
  if (forecast.previous) await visit(`forecast/${dirname(forecast.previous.path)}`);
}
const places = await pointer('places');
if (places) await visit(`places/releases/${places.releaseId}`);
const best = await pointer('best-weather');
if (best) await visit(best.url.replace(/^\//, ''));
const climate = await pointer('climate');
if (climate) {
  const indexPath = `climate/indexes/${climate.indexId}.json`;
  const indexBytes = await readFile(join(artifactRoot, indexPath));
  if (createHash('sha256').update(indexBytes).digest('hex') !== climate.indexSha256) throw new Error('Climate index does not match its active pointer');
  const index = JSON.parse(indexBytes);
  immutable.add(indexPath);
  for (const month of index.months) {
    const releaseRoot = `climate/releases/${month.releaseId}`;
    const catalog = await readFile(join(artifactRoot, releaseRoot, 'catalog.json'));
    if (createHash('sha256').update(catalog).digest('hex') !== month.catalogSha256) throw new Error(`Climate catalog does not match accepted index month ${month.period}`);
    await visit(releaseRoot);
  }
}
const paths = [...immutable];
const started = Date.now();
let finished = 0, uploadedBytes = 0, skipped = 0, next = 0;
async function storageRequest(url, options) {
  for (let attempt = 0; ; attempt++) {
    try {
      const response = await fetch(url, { redirect: 'manual', ...options });
      if (response.status < 500 || attempt === 2) return response;
      await response.body?.cancel();
    } catch (error) { if (attempt === 2) throw error; }
    await new Promise(resolve => setTimeout(resolve, 500 * (attempt + 1)));
  }
}
async function upload(path, bytes, mutable = false) {
  const sha256 = createHash('sha256').update(bytes).digest('hex');
  if (!mutable) {
    const existing = await storageRequest(new URL(`/${path}`, origin), { method: 'HEAD', headers: platformHeaders });
    if (existing.ok && existing.headers.get('X-Artifact-SHA256') === sha256) { skipped++; return; }
    if (existing.status !== 404 && !existing.ok) throw new Error(`HEAD ${path}: ${existing.status}`);
  }
  const response = await storageRequest(new URL(`/__publish/${path}`, origin), { method: 'PUT', headers: { ...platformHeaders, Authorization: `Bearer ${credentials.publishToken}`, 'X-Artifact-SHA256': sha256 }, body: bytes });
  if (response.status !== 201) throw new Error(`PUT ${path}: ${response.status} ${(await response.text()).slice(0, 160)}`);
  uploadedBytes += bytes.length;
}
async function pruneLocalForecast() {
  const active = JSON.parse(await readFile(join(artifactRoot, 'forecast/current.json')));
  if (active.current.indexId !== forecast.current.indexId) throw new Error('Forecast changed during publication; rerun before local retention');
  const runIds = [active.current, active.previous].filter(Boolean).map(reference => reference.indexId);
  let runs = 0, bytes = 0, shortlists = 0;
  async function fileBytes(path) {
    const info = await stat(path);
    if (!info.isDirectory()) return info.size;
    let total = 0;
    for (const entry of await readdir(path)) total += await fileBytes(join(path, entry));
    return total;
  }
  for (const entry of await readdir(join(artifactRoot, 'forecast/runs'), { withFileTypes: true })) {
    if (!entry.isDirectory() || !entry.name.startsWith('ifs-') || runIds.includes(entry.name)) continue;
    const path = join(artifactRoot, 'forecast/runs', entry.name);
    bytes += await fileBytes(path);
    await rm(path, { recursive: true });
    runs++;
  }
  const shortlistRoot = join(artifactRoot, 'best-weather/releases');
  const entries = await readdir(shortlistRoot, { withFileTypes: true }).catch(error => { if (error.code === 'ENOENT') return []; throw error; });
  for (const entry of entries) {
    if (!entry.isFile() || !entry.name.startsWith('ifs-') || !entry.name.endsWith('.json') || runIds.some(id => entry.name.startsWith(`${id}-world-`))) continue;
    const path = join(shortlistRoot, entry.name);
    bytes += (await stat(path)).size;
    await rm(path);
    shortlists++;
  }
  console.log(`Local forecast retention: ${runs} old runs, ${shortlists} old shortlists, ${bytes} bytes reclaimed`);
}
const progress = setInterval(() => console.log(`Weather storage: ${finished}/${paths.length} immutable files, ${(uploadedBytes / 1048576).toFixed(1)} MiB uploaded, ${skipped} already present`), 5000);
try {
  await Promise.all(Array.from({ length: concurrency }, async () => {
    while (next < paths.length) {
      const path = paths[next++];
      await upload(path, await readFile(join(artifactRoot, path)));
      finished++;
    }
  }));
  // The shortlist must exist before a newly selected forecast can request it.
  pointers.sort((left, right) => Number(left.path === 'forecast/current.json') - Number(right.path === 'forecast/current.json'));
  for (const { path, bytes } of pointers) await upload(path, bytes, true);
  let continuation, removedFiles = 0, removedBytes = 0;
  const expiredRuns = new Set();
  do {
    const retained = await storageRequest(new URL('/__prune', origin), { method: 'POST', headers: { ...platformHeaders, Authorization: `Bearer ${credentials.publishToken}`, 'Content-Type': 'application/json' }, body: JSON.stringify(continuation || {}) });
    if (!retained.ok) throw new Error(`Forecast retention: ${retained.status}`);
    const reclaimed = await retained.json();
    removedFiles += reclaimed.removed; removedBytes += reclaimed.bytes;
    for (const id of reclaimed.runIds) expiredRuns.add(id);
    continuation = reclaimed.continuation;
  } while (continuation);
  console.log(`Sites forecast retention: ${expiredRuns.size} old runs, ${removedFiles} generated files, ${removedBytes} bytes reclaimed; current and previous retained`);
  if (forecast) await pruneLocalForecast();
  console.log(`Published ${paths.length} immutable files and ${pointers.length} pointers; ${(uploadedBytes / 1048576).toFixed(1)} MiB, ${((Date.now() - started) / 1000).toFixed(1)} seconds`);
} finally { clearInterval(progress); }
