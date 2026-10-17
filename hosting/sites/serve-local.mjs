// Written by AI on 2026.10.16 by model claude opus 5.5.
// Runs the Sites Worker on this machine: ./artifacts stands in for Sites storage
// and ./dist for the embedded app. http://127.0.0.1:5198
import { createServer } from 'node:http';
import { mkdir, open, readFile, readdir, stat, writeFile } from 'node:fs/promises';
import { dirname, join, extname, relative } from 'node:path';
import { Readable } from 'node:stream';
import { handleRequest } from './worker.mjs';

const root = process.cwd();
const artifacts = join(root, 'artifacts');
const dist = join(root, 'dist');
const generatedData = process.env.NICE_WEATHER_GENERATED_DATA;
async function dataPath(key) {
  if (generatedData) {
    const generated = join(generatedData, key);
    if (await stat(generated).then(info => info.isFile(), () => false)) return generated;
  }
  return join(artifacts, key);
}

const bucket = {
  async get(key, options) {
    const path = await dataPath(key);
    let size;
    try { size = (await stat(path)).size; } catch { return null; }
    const range = options?.range;
    let body;
    if (range) {
      const file = await open(path, 'r');
      try {
        body = Buffer.alloc(Math.min(range.length, Math.max(0, size - range.offset)));
        await file.read(body, 0, body.length, range.offset);
      } finally { await file.close(); }
    } else body = await readFile(path);
    return {
      size, httpEtag: `"${size}"`, body: Readable.toWeb(Readable.from([body])),
      arrayBuffer: async () => body.buffer.slice(body.byteOffset, body.byteOffset + body.byteLength),
      text: async () => body.toString('utf8'),
      json: async () => JSON.parse(body.toString('utf8')),
    };
  },
  async put(key, body) {
    const path = join(artifacts, key);
    await mkdir(dirname(path), { recursive: true });
    await writeFile(path, Buffer.from(await new Response(body).arrayBuffer()));
  },
  async head(key) {
    try { const { size } = await stat(await dataPath(key)); return { size }; } catch { return null; }
  },
  // Local runs keep every forecast; pruning only matters for Sites storage.
  async list({ prefix }) {
    const entries = await readdir(join(artifacts, prefix), { recursive: true, withFileTypes: true }).catch(() => []);
    return { objects: entries.filter((entry) => entry.isFile()).map((entry) => ({ key: relative(artifacts, join(entry.parentPath, entry.name)) })), truncated: false };
  },
  async delete() {},
};

const types = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript', '.css': 'text/css', '.png': 'image/png', '.svg': 'image/svg+xml' };
async function serveAsset(pathname) {
  const page = pathname === '/' ? 'index.html' : `${pathname.slice(1)}.html`;
  const file = extname(pathname) ? pathname.slice(1) : await stat(join(dist, page)).then(() => page, () => 'index.html');
  try {
    return new Response(await readFile(join(dist, file)), { headers: { 'Content-Type': types[extname(file)] ?? 'application/octet-stream', 'Cache-Control': 'no-cache' } });
  } catch {
    return new Response('Not found', { status: 404 });
  }
}

createServer(async (incoming, outgoing) => {
  const hasBody = incoming.method === 'PUT' || incoming.method === 'POST';
  const request = new Request(`http://127.0.0.1:5198${incoming.url}`, { method: incoming.method, headers: incoming.headers, body: hasBody ? Readable.toWeb(incoming) : undefined, duplex: 'half' });
  const started = performance.now();
  const response = await handleRequest(request, { WEATHER: bucket, WEATHER_PUBLISH_TOKEN: 'local' }, serveAsset);
  outgoing.writeHead(response.status, Object.fromEntries(response.headers));
  if (response.body) for await (const chunk of response.body) outgoing.write(chunk);
  outgoing.end();
  if (!incoming.url.startsWith('/assets/')) console.log(`${response.status} ${incoming.url} ${(performance.now() - started).toFixed(0)} ms`);
}).listen(5198, '127.0.0.1', () => console.log('Nice weather: http://127.0.0.1:5198'));
