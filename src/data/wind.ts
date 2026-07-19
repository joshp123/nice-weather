import type { WindFrameAsset } from './types';

const HEADER_BYTES = 128;
const RECORD_BYTES = 2;
const VERSION = 1;
const MAX_WIND_BYTES = 4 * 1024 * 1024;
const MAGIC = [78, 87, 70, 87, 73, 78, 68, 0] as const;

export type WindGrid = {
  validAt: string;
  bounds: [number, number, number, number];
  rows: number;
  cols: number;
  vectors: Int8Array;
};

export class StaleWindReleaseError extends Error {
  constructor() {
    super('The forecast changed while wind direction was loading.');
    this.name = 'StaleWindReleaseError';
  }
}

// Owns cancellation and latest-request-wins sequencing. React never needs to
// reason about an old frame completing after a new day or time was selected.
export class WindFrameLoader {
  private generation = 0;
  private controller?: AbortController;

  clear() {
    this.generation += 1;
    this.controller?.abort();
    this.controller = undefined;
  }

  async load(releaseId: string, asset: WindFrameAsset, expectedBounds: [number, number, number, number]): Promise<WindGrid | undefined> {
    this.clear();
    const generation = this.generation;
    const controller = new AbortController();
    this.controller = controller;
    try {
      const response = await fetch(windAssetUrl(releaseId, asset), { cache: 'force-cache', signal: controller.signal });
      if (response.status === 409) throw new StaleWindReleaseError();
      if (!response.ok) throw new Error('Wind direction is not available for this time.');
      const declared = Number(response.headers.get('content-length'));
      if (Number.isFinite(declared) && declared > MAX_WIND_BYTES) throw new Error('Wind direction data is too large.');
      const compressed = await readBounded(response, MAX_WIND_BYTES);
      const actualAssetChecksum = hex(new Uint8Array(await crypto.subtle.digest('SHA-256', compressed)));
      if (actualAssetChecksum !== asset.sha256.toLowerCase()) throw new Error('Wind direction file checksum failed.');
      const buffer = await gunzipBounded(compressed);
      const grid = await parseWindGrid(buffer, asset, expectedBounds);
      return generation === this.generation ? grid : undefined;
    } catch (error) {
      if (controller.signal.aborted && generation !== this.generation) return undefined;
      throw error;
    } finally {
      if (generation === this.generation) this.controller = undefined;
    }
  }
}

export async function parseWindGrid(buffer: ArrayBuffer, asset: WindFrameAsset, expectedBounds: [number, number, number, number]): Promise<WindGrid> {
  if (buffer.byteLength < HEADER_BYTES || buffer.byteLength > MAX_WIND_BYTES) throw new Error('Invalid wind grid size.');
  const bytes = new Uint8Array(buffer);
  if (MAGIC.some((value, index) => bytes[index] !== value)) throw new Error('Invalid wind grid format.');
  const view = new DataView(buffer);
  if (view.getUint16(8, true) !== VERSION || view.getUint16(10, true) !== HEADER_BYTES || view.getUint16(12, true) !== RECORD_BYTES) throw new Error('Unsupported wind grid format.');
  if (!zero(bytes.subarray(14, 16)) || !zero(bytes.subarray(56, 64)) || !zero(bytes.subarray(96, 128))) throw new Error('Invalid wind grid header.');
  const rows = view.getUint32(16, true); const cols = view.getUint32(20, true);
  const bounds: [number, number, number, number] = [view.getFloat64(24, true), view.getFloat64(40, true), view.getFloat64(32, true), view.getFloat64(48, true)];
  if (!rows || !cols || rows * cols > (MAX_WIND_BYTES - HEADER_BYTES) / RECORD_BYTES || buffer.byteLength !== HEADER_BYTES + rows * cols * RECORD_BYTES) throw new Error('Invalid wind grid geometry.');
  if (bounds.some((value) => !Number.isFinite(value)) || bounds.some((value, index) => value !== expectedBounds[index])) throw new Error('Wind grid bounds do not match the forecast.');
  const expected = hex(bytes.subarray(64, 96));
  const digestInput = new Uint8Array(64 + buffer.byteLength - HEADER_BYTES);
  digestInput.set(bytes.subarray(0, 64));
  digestInput.set(bytes.subarray(HEADER_BYTES), 64);
  const actual = hex(new Uint8Array(await crypto.subtle.digest('SHA-256', digestInput)));
  if (actual !== expected) throw new Error('Wind grid checksum failed.');
  const vectors = new Int8Array(buffer, HEADER_BYTES);
  for (let index = 0; index < vectors.length; index += 2) {
    if ((vectors[index] === -128) !== (vectors[index + 1] === -128)) throw new Error('Wind grid contains partial no-data.');
  }
  return { validAt: asset.validAt, bounds, rows, cols, vectors };
}

export function windAssetUrl(releaseId: string, asset: WindFrameAsset): string {
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(releaseId) || !/^wind\/(?!.*(?:^|\/)\.\.?(?:\/|$))[^?#\\]+\.nww\.gz$/.test(asset.url) || !/^[0-9a-f]{64}$/.test(asset.sha256) || !asset.url.includes(asset.sha256)) throw new Error('Invalid wind asset identity.');
  return `/releases/${encodeURIComponent(releaseId)}/${asset.url}?v=${asset.sha256}`;
}

async function gunzipBounded(compressed: ArrayBuffer): Promise<ArrayBuffer> {
  if (typeof DecompressionStream === 'undefined') throw new Error('This browser cannot read wind direction data.');
  const reader = new Blob([compressed]).stream().pipeThrough(new DecompressionStream('gzip')).getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      total += value.byteLength;
      if (total > MAX_WIND_BYTES) throw new Error('Wind direction data expands beyond its limit.');
      chunks.push(value);
    }
  } catch (error) {
    await reader.cancel().catch(() => undefined);
    if (error instanceof Error && error.message === 'Wind direction data expands beyond its limit.') throw error;
    throw new Error('Wind direction data is damaged.');
  }
  const output = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    output.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return output.buffer;
}

async function readBounded(response: Response, limit: number): Promise<ArrayBuffer> {
  if (!response.body) {
    const buffer = await response.arrayBuffer();
    if (buffer.byteLength > limit) throw new Error('Wind direction data is too large.');
    return buffer;
  }
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      total += value.byteLength;
      if (total > limit) throw new Error('Wind direction data is too large.');
      chunks.push(value);
    }
  } catch (error) {
    await reader.cancel().catch(() => undefined);
    throw error;
  }
  const output = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    output.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return output.buffer;
}

function zero(values: Uint8Array): boolean { return values.every((value) => value === 0); }
function hex(values: Uint8Array): string { return Array.from(values, (value) => value.toString(16).padStart(2, '0')).join(''); }
