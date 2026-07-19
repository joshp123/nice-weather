import { afterEach, describe, expect, it, vi } from 'vitest';
import type { WindFrameAsset } from './types';
import { parseWindGrid, StaleWindReleaseError, WindFrameLoader, windAssetUrl } from './wind';

const bounds: [number, number, number, number] = [-1, 50, 1, 51];

afterEach(() => vi.unstubAllGlobals());

describe('wind frame delivery', () => {
  it('parses the checked binary grid and rejects corruption', async () => {
    const raw = await windGrid([6, -8]);
    const asset = frame('0'.repeat(64));
    await expect(parseWindGrid(raw, asset, bounds)).resolves.toMatchObject({ rows: 1, cols: 1, vectors: new Int8Array([6, -8]) });
    new Uint8Array(raw)[128] = 7;
    await expect(parseWindGrid(raw, asset, bounds)).rejects.toThrow('checksum failed');
  });

  it('verifies the compressed asset before bounded decompression', async () => {
    const raw = await windGrid([6, -8]);
    const compressed = await gzip(raw);
    const checksum = await sha256(compressed);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(compressed, { status: 200, headers: { 'content-length': String(compressed.byteLength) } })));
    const loader = new WindFrameLoader();
    await expect(loader.load('release-1', frame(checksum), bounds)).resolves.toMatchObject({ rows: 1, cols: 1 });
    await expect(loader.load('release-1', frame('f'.repeat(64)), bounds)).rejects.toThrow('file checksum failed');
  });

  it('stops an undeclared streaming response at the compressed-size limit', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(new Uint8Array(4 * 1024 * 1024 + 1))));
    await expect(new WindFrameLoader().load('release-1', frame('0'.repeat(64)), bounds)).rejects.toThrow('too large');
  });

  it('treats a stale release distinctly and lets only the latest request win', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 409 })));
    await expect(new WindFrameLoader().load('release-1', frame('0'.repeat(64)), bounds)).rejects.toBeInstanceOf(StaleWindReleaseError);

    const raw = await windGrid([6, -8]);
    const compressed = await gzip(raw);
    const checksum = await sha256(compressed);
    let finishFirst!: (response: Response) => void;
    const first = new Promise<Response>((resolve) => { finishFirst = resolve; });
    vi.stubGlobal('fetch', vi.fn()
      .mockImplementationOnce(() => first)
      .mockResolvedValueOnce(new Response(compressed, { status: 200 })));
    const loader = new WindFrameLoader();
    const oldRequest = loader.load('release-1', frame(checksum), bounds);
    const newRequest = loader.load('release-1', { ...frame(checksum), validAt: '2026-07-18T13:00:00Z' }, bounds);
    await expect(newRequest).resolves.toMatchObject({ validAt: '2026-07-18T13:00:00Z' });
    finishFirst(new Response(compressed, { status: 200 }));
    await expect(oldRequest).resolves.toBeUndefined();
  });

  it('accepts only immutable gzip assets under the wind release path', () => {
    const asset = frame('a'.repeat(64));
    expect(windAssetUrl('release-1', asset)).toContain('/releases/release-1/wind/');
    expect(() => windAssetUrl('release-1', { ...asset, url: 'wind/frame.nww' })).toThrow('Invalid wind asset identity');
    expect(() => windAssetUrl('release-1', { ...asset, url: '../frame.nww.gz' })).toThrow('Invalid wind asset identity');
  });
});

function frame(checksum: string): WindFrameAsset {
  return { validAt: '2026-07-18T12:00:00Z', url: `wind/frame-${checksum}.nww.gz`, sha256: checksum, clippedComponents: 0 };
}

async function windGrid(vector: [number, number]): Promise<ArrayBuffer> {
  const buffer = new ArrayBuffer(130);
  const bytes = new Uint8Array(buffer);
  bytes.set([78, 87, 70, 87, 73, 78, 68, 0]);
  const view = new DataView(buffer);
  view.setUint16(8, 1, true);
  view.setUint16(10, 128, true);
  view.setUint16(12, 2, true);
  view.setUint32(16, 1, true);
  view.setUint32(20, 1, true);
  view.setFloat64(24, bounds[0], true);
  view.setFloat64(32, bounds[2], true);
  view.setFloat64(40, bounds[1], true);
  view.setFloat64(48, bounds[3], true);
  new Int8Array(buffer, 128).set(vector);
  const digestInput = new Uint8Array(66);
  digestInput.set(bytes.subarray(0, 64));
  digestInput.set(bytes.subarray(128), 64);
  bytes.set(new Uint8Array(await crypto.subtle.digest('SHA-256', digestInput)), 64);
  return buffer;
}

async function gzip(buffer: ArrayBuffer): Promise<ArrayBuffer> {
  const stream = new Blob([buffer]).stream().pipeThrough(new CompressionStream('gzip'));
  return new Response(stream).arrayBuffer();
}

async function sha256(buffer: ArrayBuffer): Promise<string> {
  const bytes = new Uint8Array(await crypto.subtle.digest('SHA-256', buffer));
  return Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('');
}
