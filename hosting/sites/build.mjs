import { cp, mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const hostingRoot = fileURLToPath(new URL('.', import.meta.url));
const webRoot = resolve(process.argv[2] || resolve(hostingRoot, '../../dist'));
const outputRoot = resolve(process.argv[3] || resolve(hostingRoot, '../../output/sites'));
const release = process.argv[4] ? JSON.parse(await readFile(resolve(process.argv[4]), 'utf8')) : null;
const types = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8', '.svg': 'image/svg+xml', '.png': 'image/png', '.ico': 'image/x-icon' };
const assets = {};
async function visit(directory, prefix = '') {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const name = `${prefix}/${entry.name}`;
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) await visit(path, name);
    else assets[name] = { type: types[name.slice(name.lastIndexOf('.'))] || 'application/octet-stream', data: (await readFile(path)).toString('base64') };
  }
}
await visit(webRoot);
if (!assets['/index.html']) throw new Error('Build the frontend before packaging Sites');
const source = (await readFile(resolve(hostingRoot, 'worker.mjs'), 'utf8')).replace('/*WEB_ASSETS*/ {}', JSON.stringify(assets));
await mkdir(resolve(outputRoot, 'worker'), { recursive: true });
await mkdir(resolve(outputRoot, '.openai'), { recursive: true });
await writeFile(resolve(outputRoot, 'worker/index.js'), source);
for (const module of ['fine-map.mjs', 'fine-place.mjs', 'fine-replay.mjs', 'fine-credits.mjs', 'fine-preview.mjs', 'fine-preview-font.mjs', 'share-template.mjs', 'share-assets.mjs', 'share-image.mjs', 'day-counts.mjs', 'score-bands.mjs', 'awful-maximum.json', 'named-place.mjs', 'place-presentation.mjs']) await cp(resolve(hostingRoot, module), resolve(outputRoot, 'worker', module));
await cp(resolve(hostingRoot, '.openai/hosting.json'), resolve(outputRoot, '.openai/hosting.json'));
await writeFile(resolve(outputRoot, '.gitignore'), '/dist/\n');
await writeFile(resolve(outputRoot, 'package.json'), '{"type":"module"}\n');
await mkdir(resolve(outputRoot, 'dist/server'), { recursive: true });
await mkdir(resolve(outputRoot, 'dist/.openai'), { recursive: true });
await writeFile(resolve(outputRoot, 'dist/server/index.js'), source);
for (const module of ['fine-map.mjs', 'fine-place.mjs', 'fine-replay.mjs', 'fine-credits.mjs', 'fine-preview.mjs', 'fine-preview-font.mjs', 'share-template.mjs', 'share-assets.mjs', 'share-image.mjs', 'day-counts.mjs', 'score-bands.mjs', 'awful-maximum.json', 'named-place.mjs', 'place-presentation.mjs']) await cp(resolve(hostingRoot, module), resolve(outputRoot, 'dist/server', module));
await cp(resolve(hostingRoot, '.openai/hosting.json'), resolve(outputRoot, 'dist/.openai/hosting.json'));
const releaseStorage = (await readFile(resolve(hostingRoot, 'release-storage.mjs'), 'utf8')).replace('/*CLIMATE_RELEASE*/ null', JSON.stringify(release));
for (const directory of ['worker', 'dist/server']) await writeFile(resolve(outputRoot, directory, 'release-storage.mjs'), releaseStorage);
const awfulMaximum = JSON.parse(await readFile(resolve(hostingRoot, 'awful-maximum.json'), 'utf8'));
const scoreBands = (await readFile(resolve(hostingRoot, 'score-bands.mjs'), 'utf8'))
  .replace("import awfulMaximum from './awful-maximum.json' with {type:'json'};", `const awfulMaximum = ${JSON.stringify(awfulMaximum)};`);
for (const directory of ['worker', 'dist/server']) await writeFile(resolve(outputRoot, directory, 'score-bands.mjs'), scoreBands);
console.log(`Sites Worker: ${Object.keys(assets).length} web files, ${Buffer.byteLength(source)} bytes`);
