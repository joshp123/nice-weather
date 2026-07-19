import { createHash } from 'node:crypto';
import { describe, expect, it, vi } from 'vitest';
import { EuropePlaceSearch, normalizePlaceQuery } from './placeSearch';

describe('nice-latin-fold-v1', () => {
  it.each([
    ['Cádiz', 'cadiz'],
    ['Cadiz', 'cadiz'],
    ['Łódź', 'lodz'],
    ['Lodz', 'lodz'],
    ['Málaga', 'malaga'],
    ['Malaga', 'malaga'],
    ['Rødovre', 'rodovre'],
    ['Straße', 'strasse'],
    ["  Saint-Jean  d'Angély ", 'saint jean d angely'],
    ['თბილისი', ''],
  ])('normalizes %s to %s', (input, expected) => {
    expect(normalizePlaceQuery(input)).toBe(expected);
  });
});

describe('EuropePlaceSearch', () => {
  it('rejects short and coordinate queries without loading the index', async () => {
    const fetcher = vi.fn();
    const client = new EuropePlaceSearch({ fetcher });
    await expect(client.search('Łó')).resolves.toEqual([]);
    await expect(client.search('52.37, 4.90')).resolves.toEqual([]);
    expect(fetcher).not.toHaveBeenCalled();
  });

  it('rejects absurd raw input before normalization but keeps a 160-character place key', async () => {
    const fetcher = vi.fn();
    const client = new EuropePlaceSearch({ fetcher });
    await expect(client.search('a'.repeat(513))).resolves.toEqual([]);
    expect(fetcher).not.toHaveBeenCalled();

    const key = 'a'.repeat(160);
    const harness = await searchHarness({ aaa: [row(key, key, 'ES', '51', 36.7, -4.4, 1, 1, 1)] });
    await expect(harness.client.search(key)).resolves.toEqual([expect.objectContaining({ geonameId: 1 })]);
  });

  it('finds accented names and an ordinary small place through one prefix shard', async () => {
    const records = sorted([
      row('biddestone', 'Biddestone', 'GB', 'ENG', 51.46, -2.20, 498, 2, 2655710),
      row('biddestone village', 'Biddestone', 'GB', 'ENG', 51.46, -2.20, 498, 2, 2655710),
    ]);
    const harness = await searchHarness({ bid: records });

    const results = await harness.client.search('Biddestone');

    expect(results).toEqual([expect.objectContaining({
      geonameId: 2655710,
      name: 'Biddestone',
      country: 'United Kingdom',
      admin1: 'England',
      population: 498,
      latitude: 51.46,
      longitude: -2.2,
    })]);
    expect(harness.requested()).toEqual([
      '/places/current.json',
      `/places/releases/${RELEASE}/manifest.json`,
      `/places/releases/${RELEASE}/shards/bid.json`,
    ]);
    expect(harness.client.attribution).toMatchObject({ license: 'CC BY 4.0', sourceDate: '2026-07-18' });
  });

  it('folds accented input onto the same immutable shard', async () => {
    const harness = await searchHarness({
      cad: sorted([row('cadiz', 'Cádiz', 'ES', '51', 36.52672, -6.2891, 116979, 3, 2520600)]),
      lod: sorted([row('lodz', 'Łódź', 'PL', '10', 51.75925, 19.45598, 682679, 4, 3093133)]),
      mal: sorted([row('malaga', 'Málaga', 'ES', '51', 36.72016, -4.42034, 592346, 3, 2514256)]),
    });

    expect((await harness.client.search('Cádiz'))[0]).toMatchObject({ geonameId: 2520600, name: 'Cádiz' });
    expect((await harness.client.search('Lodz'))[0]).toMatchObject({ geonameId: 3093133, name: 'Łódź' });
    expect((await harness.client.search('Málaga'))[0]).toMatchObject({ geonameId: 2514256, name: 'Málaga' });
  });

  it('keeps a non-Latin display name when its GeoNames search key is Latin', async () => {
    const harness = await searchHarness({
      tbi: [row('tbilisi', 'თბილისი', 'GE', 'TB', 41.6938, 44.8015, 1200000, 5, 611717)],
    });

    expect(await harness.client.search('Tbilisi')).toEqual([
      expect.objectContaining({ geonameId: 611717, name: 'თბილისი', country: 'Georgia' }),
    ]);
  });

  it('ranks exact keys before prefixes and deduplicates aliases by GeoName ID', async () => {
    const harness = await searchHarness({ cad: sorted([
      row('cadiz', 'Cádiz', 'ES', '51', 36.52, -6.28, 116979, 3, 1),
      row('cadiz old town', 'Cádiz', 'ES', '51', 36.52, -6.28, 116979, 3, 1),
      row('cadiz bay', 'Cádiz Bay', 'ES', '51', 36.55, -6.25, 500000, 5, 2),
    ]) });

    const results = await harness.client.search('Cadiz');

    expect(results.map((place) => place.geonameId)).toEqual([1, 2]);
  });

  it('uses viewport proximity before feature rank and population for equally matching names', async () => {
    const harness = await searchHarness({ sai: sorted([
      row('saint julian', 'Saint Julian', 'MT', '01', 35.918, 14.49, 13000, 2, 20),
      row('saint julian', 'Saint Julian', 'GB', 'WLS', 51.60, -2.90, 500000, 5, 10),
    ]) });

    const withoutViewport = await harness.client.search('Saint Julian');
    const nearMalta = await harness.client.search('Saint Julian', { viewport: { latitude: 35.9, longitude: 14.5 } });

    expect(withoutViewport.map((place) => place.geonameId)).toEqual([10, 20]);
    expect(nearMalta.map((place) => place.geonameId)).toEqual([20, 10]);
  });

  it('suppresses stale results even when a fetch implementation ignores abort', async () => {
    let resolveFirst!: (response: Response) => void;
    let pointerCalls = 0;
    let firstSignal: AbortSignal | undefined;
    const betaRecords = [row('beta', 'Beta', 'ES', '51', 36.7, -4.4, 10, 2, 2)];
    const manifest = manifestFixture({ bet: betaRecords });
    const manifestBytes = jsonBytes(manifest);
    const hash = digest(manifestBytes);
    const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const path = String(input);
      if (path === '/places/current.json') {
        pointerCalls++;
        if (pointerCalls === 1) {
          firstSignal = init?.signal as AbortSignal;
          return new Promise<Response>((resolve) => { resolveFirst = resolve; });
        }
        return jsonResponse({ releaseId: RELEASE, manifestSha256: hash });
      }
      if (path.endsWith('/manifest.json')) return new Response(manifestBytes);
      if (path.endsWith('/bet.json')) return jsonResponse(shard(betaRecords));
      throw new Error(`Unexpected ${path}`);
    });
    const client = new EuropePlaceSearch({ fetcher });

    const stale = client.search('alpha');
    const current = client.search('beta');
    resolveFirst(jsonResponse({ releaseId: RELEASE, manifestSha256: hash }));

    await expect(stale).resolves.toEqual([]);
    await expect(current).resolves.toEqual([expect.objectContaining({ geonameId: 2 })]);
    expect(firstSignal?.aborted).toBe(true);
  });

  it('evicts the least recently used completed shard from a bounded cache', async () => {
    const harness = await searchHarness({
      aaa: [row('aaaa', 'Aaaa', 'ES', '51', 36.7, -4.4, 1, 1, 1)],
      bbb: [row('bbbb', 'Bbbb', 'ES', '51', 36.7, -4.4, 1, 1, 2)],
      ccc: [row('cccc', 'Cccc', 'ES', '51', 36.7, -4.4, 1, 1, 3)],
    }, 2);

    await harness.client.search('aaaa');
    await harness.client.search('bbbb');
    await harness.client.search('aaaa');
    await harness.client.search('cccc');
    await harness.client.search('bbbb');

    expect(harness.count('/shards/aaa.json')).toBe(1);
    expect(harness.count('/shards/bbb.json')).toBe(2);
    expect(harness.count('/shards/ccc.json')).toBe(1);
  });

  it('rejects unknown or corrupt pointer, manifest and shard contracts', async () => {
    const pointerExtra = new EuropePlaceSearch({ fetcher: async () => jsonResponse({ releaseId: RELEASE, manifestSha256: 'a'.repeat(64), extra: true }) });
    await expect(pointerExtra.search('malaga')).rejects.toThrow('pointer fields');

    const wrongHash = await searchHarness({ mal: [row('malaga', 'Málaga', 'ES', '51', 36.7, -4.4, 1, 1, 1)] }, 2, { pointerHash: '0'.repeat(64) });
    await expect(wrongHash.client.search('malaga')).rejects.toThrow('checksum');

    const wrongSchema = await searchHarness({}, 2, { manifest: { ...manifestFixture({}), schema: 'nice-places-v2' } });
    await expect(wrongSchema.client.search('malaga')).rejects.toThrow('Unsupported place index schema');

    const badShard = await searchHarness({ mal: [row('malaga', 'Málaga', 'ZZ', '51', 36.7, -4.4, 1, 1, 1)] });
    await expect(badShard.client.search('malaga')).rejects.toThrow('Invalid place country');
  });

  it('rejects a structurally valid shard whose raw bytes do not match the manifest', async () => {
    const original = [row('malaga', 'Málaga', 'ES', '51', 36.7, -4.4, 1, 1, 1)];
    const altered = [row('malaga', 'Málaga', 'ES', '51', 36.7, -4.4, 999999, 1, 1)];
    const harness = await searchHarness({ mal: original }, 6, { servedShards: { mal: altered } });

    await expect(harness.client.search('malaga')).rejects.toThrow('Place shard mal checksum does not match its manifest');
  });

  it('rejects a declared oversized response before reading it', async () => {
    const cancel = vi.fn();
    const body = new ReadableStream<Uint8Array>({ cancel });
    const fetcher = vi.fn(async () => new Response(body, { headers: { 'Content-Length': '1025' } }));
    const client = new EuropePlaceSearch({ fetcher });

    await expect(client.search('malaga')).rejects.toThrow('pointer is unexpectedly large');
    expect(cancel).toHaveBeenCalledOnce();
  });

  it('cancels a streaming response as soon as it crosses its byte bound', async () => {
    const cancel = vi.fn();
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new Uint8Array(800));
        controller.enqueue(new Uint8Array(300));
      },
      cancel,
    });
    const client = new EuropePlaceSearch({ fetcher: async () => new Response(stream) });

    await expect(client.search('malaga')).rejects.toThrow('pointer is unexpectedly large');
    expect(cancel).toHaveBeenCalledOnce();
  });

  it('uses the bounded fallback when a response body stream is unavailable', async () => {
    const records = [row('malaga', 'Málaga', 'ES', '51', 36.7, -4.4, 1, 1, 1)];
    const manifest = manifestFixture({ mal: records });
    const manifestBytes = jsonBytes(manifest);
    const pointer = jsonBytes({ releaseId: RELEASE, manifestSha256: digest(manifestBytes) });
    const fetcher = vi.fn(async (input: RequestInfo | URL): Promise<Response> => {
      const path = String(input);
      if (path === '/places/current.json') return bodylessResponse(pointer);
      if (path.endsWith('/manifest.json')) return new Response(manifestBytes);
      if (path.endsWith('/mal.json')) return jsonResponse(shard(records));
      return new Response('', { status: 404 });
    });

    await expect(new EuropePlaceSearch({ fetcher }).search('malaga')).resolves.toEqual([
      expect.objectContaining({ geonameId: 1 }),
    ]);
  });
});

const RELEASE = 'places-20260718';

async function searchHarness(
  shards: Record<string, unknown[][]>,
  shardCacheSize = 6,
  overrides: { manifest?: ReturnType<typeof manifestFixture>; pointerHash?: string; servedShards?: Record<string, unknown[][]> } = {},
) {
  const manifest = overrides.manifest ?? manifestFixture(shards);
  const manifestBytes = jsonBytes(manifest);
  const hash = overrides.pointerHash ?? digest(manifestBytes);
  const servedShards = overrides.servedShards ?? shards;
  const requests: string[] = [];
  const fetcher = vi.fn(async (input: RequestInfo | URL): Promise<Response> => {
    const path = String(input);
    requests.push(path);
    if (path === '/places/current.json') return jsonResponse({ releaseId: RELEASE, manifestSha256: hash });
    if (path === `/places/releases/${RELEASE}/manifest.json`) return new Response(manifestBytes, { status: 200 });
    const match = /\/shards\/([a-z0-9_]{3})\.json$/.exec(path);
    if (match && servedShards[match[1]]) return jsonResponse(shard(servedShards[match[1]]));
    return new Response('', { status: 404 });
  });
  return {
    client: new EuropePlaceSearch({ fetcher, shardCacheSize }),
    requested: () => requests,
    count: (suffix: string) => requests.filter((path) => path.endsWith(suffix)).length,
  };
}

function manifestFixture(shards: Record<string, unknown[][]>) {
  const shardDigests = Object.fromEntries(Object.entries(shards).map(([prefix, records]) => [
    prefix,
    digest(jsonBytes(shard(records))),
  ]));
  return {
    schema: 'nice-places-v1',
    source: {
      name: 'GeoNames',
      extractDate: '2026-07-18',
      dataUrl: 'https://download.geonames.org/export/dump/',
      license: 'CC BY 4.0',
      licenseUrl: 'https://creativecommons.org/licenses/by/4.0/',
      attribution: 'Contains modified GeoNames data, licensed under CC BY 4.0.',
      modified: true,
      warranty: 'GeoNames provides the data as-is without warranty of accuracy, timeliness or completeness.',
    },
    productBounds: { west: -24, east: 45, south: 29, north: 71.5 },
    placeCount: 6,
    searchKeyCount: 8,
    shardCount: Object.keys(shards).length,
    shardPrefixLength: 3,
    minimumQueryLength: 3,
    normalization: 'nice-latin-fold-v1',
    record: ['key', 'name', 'country', 'admin1', 'latE5', 'lonE5', 'population', 'featureRank', 'geonameId'],
    countries: { ES: 'Spain', GB: 'United Kingdom', PL: 'Poland', MT: 'Malta', GE: 'Georgia' },
    admins: { 'ES.51': 'Andalusia', 'GB.ENG': 'England', 'GB.WLS': 'Wales', 'PL.10': 'Łódź Voivodeship', 'MT.01': 'Attard', 'GE.TB': 'Tbilisi' },
    shards: shardDigests,
    summary: {
      placeCount: 6,
      searchKeyCount: 8,
      shardCount: Object.keys(shards).length,
      uncompressedBytes: 1000,
      gzipBytes: 500,
      largestShardGzipBytes: 200,
    },
  };
}

function row(
  key: string,
  name: string,
  country: string,
  admin1: string,
  latitude: number,
  longitude: number,
  population: number,
  featureRank: number,
  geonameId: number,
): unknown[] {
  return [key, name, country, admin1, Math.round(latitude * 100_000), Math.round(longitude * 100_000), population, featureRank, geonameId];
}

function shard(records: unknown[][]) {
  return { v: 1, r: records };
}

function sorted(records: unknown[][]): unknown[][] {
  return records.sort((left, right) => {
    if (left[0] !== right[0]) return String(left[0]) < String(right[0]) ? -1 : 1;
    if (left[7] !== right[7]) return Number(right[7]) - Number(left[7]);
    if (left[6] !== right[6]) return Number(right[6]) - Number(left[6]);
    if (left[1] !== right[1]) return String(left[1]) < String(right[1]) ? -1 : 1;
    return Number(left[8]) - Number(right[8]);
  });
}

function jsonResponse(value: unknown): Response {
  return new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } });
}

function jsonBytes(value: unknown): Uint8Array<ArrayBuffer> {
  return new TextEncoder().encode(JSON.stringify(value));
}

function digest(bytes: Uint8Array): string {
  return createHash('sha256').update(bytes).digest('hex');
}

function bodylessResponse(bytes: Uint8Array<ArrayBuffer>): Response {
  const response = new Response(bytes.buffer, { headers: { 'Content-Length': String(bytes.byteLength) } });
  Object.defineProperty(response, 'body', { value: null });
  return response;
}
