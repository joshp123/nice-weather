// Written by AI on 2026.10.16 by model claude opus 5.5.
// Uploads weather files to the Site's storage through the Worker.
//
//   node publish.mjs files <artifacts dir> <site url> <key>...   any other files, e.g. places.tsv or climate/
//
// The publish token (the Site secret WEATHER_PUBLISH_TOKEN) is read from the
// environment variable NICE_WEATHER_PUBLISH_TOKEN, never from arguments.
import { readFile, readdir, stat } from 'node:fs/promises';
import { join, relative } from 'node:path';
import { gzipSync } from 'node:zlib';

const [mode, artifacts, site, ...keys] = process.argv.slice(2);
const token = process.env.NICE_WEATHER_PUBLISH_TOKEN;
if (!token || !artifacts || !site || mode !== 'files' || !keys.length) {
  console.error('usage: NICE_WEATHER_PUBLISH_TOKEN=... node publish.mjs files <artifacts dir> <site url> [keys...]');
  process.exit(2);
}
const authorisation = { Authorization: `Bearer ${token}` };

async function filesUnder(path) {
  if (!(await stat(path)).isDirectory()) return [path];
  const entries = await readdir(path, { recursive: true, withFileTypes: true });
  return entries.filter((entry) => entry.isFile()).map((entry) => join(entry.parentPath, entry.name));
}

async function upload(key, body) {
  body = gzipSync(body);
  for (let attempt = 1; ; attempt++) {
    const response = await fetch(new URL(`/__publish/${key}`, site), { method: 'PUT', headers: { ...authorisation, 'Content-Encoding': 'gzip' }, body }).catch((error) => error);
    if (response instanceof Response && response.status === 201) return;
    if (attempt === 3) throw new Error(`upload ${key}: ${response instanceof Response ? response.status : response.message}`);
    await new Promise((resolve) => setTimeout(resolve, 1000 * attempt));
  }
}

async function uploadAll(paths) {
  const started = Date.now();
  let next = 0;
  let bytes = 0;
  await Promise.all(Array.from({ length: 2 }, async () => {
    while (next < paths.length) {
      const path = paths[next++];
      const body = await readFile(path);
      await upload(relative(artifacts, path), body);
      bytes += body.length;
    }
  }));
  console.log(`uploaded ${paths.length} files, ${(bytes / 1048576).toFixed(1)} MiB in ${((Date.now() - started) / 1000).toFixed(0)} s`);
}

await uploadAll((await Promise.all(keys.map((key) => filesUnder(join(artifacts, key))))).flat());
