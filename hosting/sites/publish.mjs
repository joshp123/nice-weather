// Written by AI on 2026.10.16 by model claude opus 5.5.
// Uploads weather files to the Site's storage through the Worker.
//
//   node publish.mjs forecast <artifacts dir> <site url>   newest forecast run, then its pointer, then prunes old runs
//   node publish.mjs files <artifacts dir> <site url> <key>...   any other files, e.g. places.tsv or climate/
//
// The publish token (the Site secret WEATHER_PUBLISH_TOKEN) is read from the
// environment variable NICE_WEATHER_PUBLISH_TOKEN, never from arguments.
import { readFile, readdir, stat } from 'node:fs/promises';
import { join, relative } from 'node:path';

const [mode, artifacts, site, ...keys] = process.argv.slice(2);
const token = process.env.NICE_WEATHER_PUBLISH_TOKEN;
if (!token || !artifacts || !site || !['forecast', 'files'].includes(mode)) {
  console.error('usage: NICE_WEATHER_PUBLISH_TOKEN=... node publish.mjs forecast|files <artifacts dir> <site url> [keys...]');
  process.exit(2);
}
const authorisation = { Authorization: `Bearer ${token}` };

async function filesUnder(path) {
  if (!(await stat(path)).isDirectory()) return [path];
  const entries = await readdir(path, { recursive: true, withFileTypes: true });
  return entries.filter((entry) => entry.isFile()).map((entry) => join(entry.parentPath, entry.name));
}

async function upload(key, body) {
  for (let attempt = 1; ; attempt++) {
    const response = await fetch(new URL(`/__publish/${key}`, site), { method: 'PUT', headers: authorisation, body }).catch((error) => error);
    if (response instanceof Response && response.status === 201) return;
    if (attempt === 3) throw new Error(`upload ${key}: ${response instanceof Response ? response.status : response.message}`);
    await new Promise((resolve) => setTimeout(resolve, 1000 * attempt));
  }
}

async function uploadAll(paths) {
  const started = Date.now();
  let next = 0;
  let bytes = 0;
  await Promise.all(Array.from({ length: 12 }, async () => {
    while (next < paths.length) {
      const path = paths[next++];
      const body = await readFile(path);
      await upload(relative(artifacts, path), body);
      bytes += body.length;
    }
  }));
  console.log(`uploaded ${paths.length} files, ${(bytes / 1048576).toFixed(1)} MiB in ${((Date.now() - started) / 1000).toFixed(0)} s`);
}

if (mode === 'files') {
  await uploadAll((await Promise.all(keys.map((key) => filesUnder(join(artifacts, key))))).flat());
} else {
  const runs = (await readdir(join(artifacts, 'forecast'), { withFileTypes: true })).filter((entry) => entry.isDirectory()).map((entry) => entry.name).sort();
  const runId = runs.at(-1);
  const manifest = JSON.parse(await readFile(join(artifacts, 'forecast', runId, 'manifest.json'), 'utf8'));
  const live = await fetch(new URL('/data/forecast/current.json', site)).then((response) => (response.ok ? response.json() : null)).catch(() => null);
  if (live?.runId === runId) {
    console.log(`forecast ${runId} is already live`);
  } else {
    await uploadAll(await filesUnder(join(artifacts, 'forecast', runId)));
    // The pointer goes last, so visitors never see a run whose files are still uploading.
    await upload('forecast/current.json', JSON.stringify({ ...manifest, runId, previousRunId: live?.runId ?? null }));
    const pruned = await fetch(new URL('/__prune', site), { method: 'POST', headers: authorisation }).then((response) => response.json());
    console.log(`forecast ${runId} is live; removed ${pruned.removed} old files`);
  }
}
