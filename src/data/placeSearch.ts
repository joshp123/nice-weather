import { readBoundedResponse } from './boundedResponse';

const POINTER_PATH = '/places/current.json';
const SCHEMA = 'nice-places-v1';
const NORMALIZATION = 'nice-latin-fold-v1';
const MAX_RAW_QUERY_LENGTH = 512;
const MAX_NORMALIZED_QUERY_LENGTH = 160;
const POINTER_BYTES = 1_024;
const MANIFEST_BYTES = 2 * 1024 * 1024;
const SHARD_BYTES = 4 * 1024 * 1024;
const RECORD_FIELDS = [
  'key', 'name', 'country', 'admin1', 'latE5', 'lonE5',
  'population', 'featureRank', 'geonameId',
] as const;

type Fetcher = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export type PlaceSearchViewport = {
  latitude: number;
  longitude: number;
};

export type PlaceSearchOptions = {
  viewport?: PlaceSearchViewport;
};

export type PlaceSearchResult = {
  geonameId: number;
  name: string;
  countryCode: string;
  country: string;
  admin1?: string;
  latitude: number;
  longitude: number;
  population: number;
};

export type PlaceIndexAttribution = {
  text: string;
  license: 'CC BY 4.0';
  licenseUrl: string;
  sourceDate: string;
};

type Pointer = {
  releaseId: string;
  manifestSha256: string;
};

type Bounds = {
  west: number;
  east: number;
  south: number;
  north: number;
};

type Manifest = {
  releaseId: string;
  sha256: string;
  bounds: Bounds;
  countries: Readonly<Record<string, string>>;
  admins: Readonly<Record<string, string>>;
  shards: Readonly<Record<string, string>>;
  attribution: PlaceIndexAttribution;
};

type PlaceRecord = {
  key: string;
  name: string;
  countryCode: string;
  admin1Code: string;
  latitudeE5: number;
  longitudeE5: number;
  population: number;
  featureRank: number;
  geonameId: number;
};

/**
 * Browser-side reader for the immutable GeoNames place index. A search fetches
 * the revalidated pointer and at most one release-scoped shard; it never loads
 * the complete index or makes weather judgements.
 */
export class EuropePlaceSearch {
  private readonly fetcher: Fetcher;
  private readonly shardLimit: number;
  private manifest?: Manifest;
  private readonly shards = new Map<string, readonly PlaceRecord[]>();
  private active?: AbortController;
  private sequence = 0;

  constructor(options: { fetcher?: Fetcher; shardCacheSize?: number } = {}) {
    const size = options.shardCacheSize ?? 6;
    if (!Number.isInteger(size) || size < 1 || size > 20) {
      throw new Error('Place shard cache size must be between 1 and 20.');
    }
    this.fetcher = options.fetcher ?? ((input, init) => fetch(input, init));
    this.shardLimit = size;
  }

  get attribution(): PlaceIndexAttribution | undefined {
    return this.manifest?.attribution;
  }

  async search(query: string, options: PlaceSearchOptions = {}): Promise<PlaceSearchResult[]> {
    const request = ++this.sequence;
    this.active?.abort();
    const controller = new AbortController();
    this.active = controller;

    if (typeof query !== 'string' || query.length > MAX_RAW_QUERY_LENGTH) {
      if (this.isCurrent(request)) this.active = undefined;
      return [];
    }
    const parsed = parseSearchQuery(query);
    if (!parsed || parsed.name.length < 3 || parsed.name.length > MAX_NORMALIZED_QUERY_LENGTH || looksLikeCoordinates(query)) {
      if (this.isCurrent(request)) this.active = undefined;
      return [];
    }

    try {
      const viewport = parseViewport(options.viewport);
      const pointer = await this.loadPointer(controller.signal);
      if (!this.isCurrent(request)) return [];
      const manifest = await this.loadManifest(pointer, controller.signal, request);
      if (!this.isCurrent(request)) return [];
      const prefix = shardPrefix(parsed.name);
      const records = await this.loadShard(manifest, prefix, controller.signal, request);
      if (!this.isCurrent(request)) return [];
      return rank(records, parsed.name, manifest, viewport, parsed.country).slice(0, 8);
    } catch (error) {
      if (!this.isCurrent(request) || controller.signal.aborted) return [];
      throw error;
    } finally {
      if (this.isCurrent(request)) this.active = undefined;
    }
  }

  clear(): void {
    this.sequence++;
    this.active?.abort();
    this.active = undefined;
    this.manifest = undefined;
    this.shards.clear();
  }

  private isCurrent(request: number): boolean {
    return request === this.sequence;
  }

  private async loadPointer(signal: AbortSignal): Promise<Pointer> {
    const response = await this.fetcher(POINTER_PATH, { signal, cache: 'no-cache' });
    if (!response.ok) throw new Error(`Place index pointer returned ${response.status}.`);
    return parsePointer(parseJsonBytes(await readBoundedResponse(response, 'place index pointer', POINTER_BYTES), 'place index pointer'));
  }

  private async loadManifest(pointer: Pointer, signal: AbortSignal, request: number): Promise<Manifest> {
    if (this.manifest?.releaseId === pointer.releaseId && this.manifest.sha256 === pointer.manifestSha256) {
      return this.manifest;
    }
    const path = releasePath(pointer.releaseId, 'manifest.json');
    const response = await this.fetcher(path, { signal, cache: 'force-cache' });
    if (!response.ok) throw new Error(`Place index manifest returned ${response.status}.`);
    const bytes = await readBoundedResponse(response, 'place index manifest', MANIFEST_BYTES);
    const actualHash = await sha256Hex(bytes);
    if (actualHash !== pointer.manifestSha256) throw new Error('Place index manifest checksum does not match its pointer.');
    const value = parseJsonBytes(bytes, 'place index manifest');
    const next = parseManifest(value, pointer);
    if (this.isCurrent(request)) {
      this.manifest = next;
      this.shards.clear();
    }
    return next;
  }

  private async loadShard(manifest: Manifest, prefix: string, signal: AbortSignal, request: number): Promise<readonly PlaceRecord[]> {
    const expectedHash = manifest.shards[prefix];
    if (expectedHash === undefined) return [];
    const cacheKey = `${manifest.releaseId}/${prefix}`;
    const cached = this.shards.get(cacheKey);
    if (cached) {
      this.shards.delete(cacheKey);
      this.shards.set(cacheKey, cached);
      return cached;
    }

    const response = await this.fetcher(releasePath(manifest.releaseId, `shards/${prefix}.json`), {
      signal,
      cache: 'force-cache',
    });
    if (!response.ok) throw new Error(`Place index shard returned ${response.status}.`);
    const bytes = await readBoundedResponse(response, `place shard ${prefix}`, SHARD_BYTES);
    if (await sha256Hex(bytes) !== expectedHash) throw new Error(`Place shard ${prefix} checksum does not match its manifest.`);
    const records = parseShard(parseJsonBytes(bytes, `place shard ${prefix}`), prefix, manifest);
    if (!this.isCurrent(request)) return records;
    this.shards.set(cacheKey, records);
    while (this.shards.size > this.shardLimit) {
      const oldest = this.shards.keys().next().value as string | undefined;
      if (oldest === undefined) break;
      this.shards.delete(oldest);
    }
    return records;
  }
}

/** The exact browser implementation of the builder's nice-latin-fold-v1. */
export function normalizePlaceQuery(value: string): string {
  let result = '';
  let pendingSpace = false;
  const decomposed = value.toLowerCase().normalize('NFKD');
  for (const current of decomposed) {
    if (/\p{Mark}/u.test(current)) continue;
    const folded = SPECIAL_FOLDS[current] ?? current;
    for (const item of folded) {
      if ((item >= 'a' && item <= 'z') || (item >= '0' && item <= '9')) {
        if (pendingSpace && result.length > 0) result += ' ';
        result += item;
        pendingSpace = false;
      } else if (result.length > 0) {
        pendingSpace = true;
      }
    }
  }
  return result;
}

const SPECIAL_FOLDS: Readonly<Record<string, string>> = Object.freeze({
  ł: 'l', ø: 'o', đ: 'd', ð: 'd', þ: 'th', æ: 'ae', œ: 'oe', ß: 'ss', ħ: 'h', ı: 'i', ŧ: 't',
});

function parsePointer(value: unknown): Pointer {
  const data = exactObject(value, ['releaseId', 'manifestSha256'], 'place index pointer');
  return {
    releaseId: releaseId(data.releaseId),
    manifestSha256: sha256(data.manifestSha256, 'manifestSha256'),
  };
}

function parseManifest(value: unknown, pointer: Pointer): Manifest {
  const data = exactObject(value, [
    'schema', 'source', 'productBounds', 'placeCount', 'searchKeyCount', 'shardCount',
    'shardPrefixLength', 'minimumQueryLength', 'normalization', 'record', 'countries', 'admins', 'shards', 'summary',
  ], 'place index manifest');
  if (data.schema !== SCHEMA || data.normalization !== NORMALIZATION) throw new Error('Unsupported place index schema.');
  if (data.shardPrefixLength !== 3 || data.minimumQueryLength !== 3) throw new Error('Unsupported place index sharding contract.');
  const record = stringArray(data.record, 'place record fields');
  if (record.length !== RECORD_FIELDS.length || record.some((field, index) => field !== RECORD_FIELDS[index])) {
    throw new Error('Unsupported place record contract.');
  }
  const placeCount = integer(data.placeCount, 'placeCount', 1, 1_100_000);
  const searchKeyCount = integer(data.searchKeyCount, 'searchKeyCount', placeCount, 5_000_000);
  const shardCount = integer(data.shardCount, 'shardCount', 1, 7_500);
  const source = exactObject(data.source, [
    'name', 'extractDate', 'dataUrl', 'license', 'licenseUrl', 'attribution', 'modified', 'warranty',
  ], 'place source');
  if (source.name !== 'GeoNames' || source.dataUrl !== 'https://download.geonames.org/export/dump/' ||
      source.license !== 'CC BY 4.0' || source.licenseUrl !== 'https://creativecommons.org/licenses/by/4.0/' || source.modified !== true) {
    throw new Error('Unsupported place source contract.');
  }
  const sourceDate = dateText(source.extractDate, 'source extractDate');
  const attribution = text(source.attribution, 'source attribution', 240);
  text(source.warranty, 'source warranty', 300);
  const bounds = parseBounds(data.productBounds);
  const countries = labelMap(data.countries, /^[A-Z]{2}$/, 'countries');
  const admins = labelMap(data.admins, /^[A-Z]{2}\.[A-Za-z0-9_-]+$/, 'admins');
  const shards = digestMap(data.shards, shardCount);
  if (Object.keys(countries).length === 0) throw new Error('Place index contains no countries.');

  const summary = exactObject(data.summary, [
    'placeCount', 'searchKeyCount', 'shardCount', 'uncompressedBytes', 'gzipBytes', 'largestShardGzipBytes',
  ], 'place summary');
  if (integer(summary.placeCount, 'summary placeCount', 1, 1_100_000) !== placeCount ||
      integer(summary.searchKeyCount, 'summary searchKeyCount', placeCount, 5_000_000) !== searchKeyCount ||
      integer(summary.shardCount, 'summary shardCount', 1, 7_500) !== shardCount) {
    throw new Error('Place index summary counts do not match its manifest.');
  }
  integer(summary.uncompressedBytes, 'summary uncompressedBytes', 1, Number.MAX_SAFE_INTEGER);
  const gzipBytes = integer(summary.gzipBytes, 'summary gzipBytes', 1, Number.MAX_SAFE_INTEGER);
  const largestShardGzipBytes = integer(summary.largestShardGzipBytes, 'summary largestShardGzipBytes', 1, 256 * 1024);
  if (gzipBytes < largestShardGzipBytes) throw new Error('Place index byte summary is inconsistent.');

  return {
    releaseId: pointer.releaseId,
    sha256: pointer.manifestSha256,
    bounds,
    countries,
    admins,
    shards,
    attribution: { text: attribution, license: 'CC BY 4.0', licenseUrl: String(source.licenseUrl), sourceDate },
  };
}

function parseShard(value: unknown, prefix: string, manifest: Manifest): readonly PlaceRecord[] {
  const data = exactObject(value, ['v', 'r'], `place shard ${prefix}`);
  if (data.v !== 1) throw new Error('Unsupported place shard version.');
  if (!Array.isArray(data.r) || data.r.length === 0 || data.r.length > 100_000) throw new Error('Invalid place shard records.');
  const records = data.r.map((row, index) => parseRecord(row, index, prefix, manifest));
  for (let index = 1; index < records.length; index++) {
    if (compareBuilderOrder(records[index - 1], records[index]) > 0) throw new Error('Place shard records are not in canonical order.');
  }
  return records;
}

function parseRecord(value: unknown, index: number, prefix: string, manifest: Manifest): PlaceRecord {
  if (!Array.isArray(value) || value.length !== 9) throw new Error(`Invalid place record ${index + 1}.`);
  const key = text(value[0], 'place key', 200);
  if (normalizePlaceQuery(key) !== key || shardPrefix(key) !== prefix) throw new Error(`Invalid place key in record ${index + 1}.`);
  const name = text(value[1], 'place name', 200);
  const countryCode = text(value[2], 'place country', 2);
  if (!/^[A-Z]{2}$/.test(countryCode) || !manifest.countries[countryCode]) throw new Error(`Invalid place country in record ${index + 1}.`);
  const admin1Code = optionalCode(value[3], 'place admin1');
  const latitudeE5 = integer(value[4], 'place latitude', -9_000_000, 9_000_000);
  const longitudeE5 = integer(value[5], 'place longitude', -18_000_000, 18_000_000);
  const latitude = latitudeE5 / 100_000;
  const longitude = longitudeE5 / 100_000;
  // The builder bounds the source coordinate before rounding it to 1e-5°.
  // A valid near-edge value can therefore quantize exactly onto any boundary.
  if (longitude < manifest.bounds.west || longitude > manifest.bounds.east || latitude < manifest.bounds.south || latitude > manifest.bounds.north) {
    throw new Error(`Place record ${index + 1} is outside product bounds.`);
  }
  return {
    key,
    name,
    countryCode,
    admin1Code,
    latitudeE5,
    longitudeE5,
    population: integer(value[6], 'place population', 0, Number.MAX_SAFE_INTEGER),
    featureRank: integer(value[7], 'place featureRank', 1, 5),
    geonameId: integer(value[8], 'place geonameId', 1, Number.MAX_SAFE_INTEGER),
  };
}

function parseSearchQuery(value: string): { name: string; country?: string } | undefined {
  const comma = value.indexOf(',');
  const name = normalizePlaceQuery(comma < 0 ? value : value.slice(0, comma));
  const country = comma < 0 ? '' : normalizePlaceQuery(value.slice(comma + 1));
  if (!name || country.length > MAX_NORMALIZED_QUERY_LENGTH) return undefined;
  return { name, country: country || undefined };
}

function rank(records: readonly PlaceRecord[], query: string, manifest: Manifest, viewport?: PlaceSearchViewport, country?: string): PlaceSearchResult[] {
  const countryCodes = country === undefined ? undefined : matchingCountryCodes(manifest, country);
  const bestById = new Map<number, PlaceRecord>();
  for (const record of records) {
    if (!record.key.startsWith(query) || countryCodes && !countryCodes.has(record.countryCode)) continue;
    const previous = bestById.get(record.geonameId);
    if (!previous || compareMatches(record, previous, query, viewport) < 0) bestById.set(record.geonameId, record);
  }
  return [...bestById.values()]
    .sort((left, right) => compareMatches(left, right, query, viewport))
    .map((record) => ({
      geonameId: record.geonameId,
      name: record.name,
      countryCode: record.countryCode,
      country: manifest.countries[record.countryCode],
      admin1: manifest.admins[`${record.countryCode}.${record.admin1Code}`],
      latitude: record.latitudeE5 / 100_000,
      longitude: record.longitudeE5 / 100_000,
      population: record.population,
    }));
}

function compareMatches(left: PlaceRecord, right: PlaceRecord, query: string, viewport?: PlaceSearchViewport): number {
  const exact = Number(right.key === query) - Number(left.key === query);
  if (exact !== 0) return exact;
  if (left.featureRank !== right.featureRank) return right.featureRank - left.featureRank;
  const populationImportance = populationBand(right.population) - populationBand(left.population);
  if (populationImportance !== 0) return populationImportance;
  if (viewport) {
    const distance = distanceSquared(left, viewport) - distanceSquared(right, viewport);
    if (distance !== 0) return distance;
  }
  if (left.population !== right.population) return right.population - left.population;
  const normalizedName = lexical(normalizePlaceQuery(left.name), normalizePlaceQuery(right.name));
  if (normalizedName !== 0) return normalizedName;
  const name = lexical(left.name, right.name);
  if (name !== 0) return name;
  return left.geonameId - right.geonameId;
}

function matchingCountryCodes(manifest: Manifest, qualifier: string): ReadonlySet<string> {
  const matches = new Set<string>();
  for (const [code, label] of Object.entries(manifest.countries)) {
    const normalizedLabel = normalizePlaceQuery(label);
    if (qualifier === code.toLowerCase() || qualifier === normalizedLabel || normalizedLabel.startsWith('the ') && qualifier === normalizedLabel.slice(4)) {
      matches.add(code);
    }
  }
  return matches;
}

function populationBand(population: number): number {
  if (population >= 1_000_000) return 5;
  if (population >= 100_000) return 4;
  if (population >= 10_000) return 3;
  if (population >= 1_000) return 2;
  if (population > 0) return 1;
  return 0;
}

function compareBuilderOrder(left: PlaceRecord, right: PlaceRecord): number {
  const key = lexical(left.key, right.key);
  if (key !== 0) return key;
  if (left.featureRank !== right.featureRank) return right.featureRank - left.featureRank;
  if (left.population !== right.population) return right.population - left.population;
  const name = lexical(left.name, right.name);
  if (name !== 0) return name;
  return left.geonameId - right.geonameId;
}

function distanceSquared(record: PlaceRecord, viewport: PlaceSearchViewport): number {
  const latitude = record.latitudeE5 / 100_000;
  const longitude = record.longitudeE5 / 100_000;
  const latitudeDelta = latitude - viewport.latitude;
  const longitudeDelta = (longitude - viewport.longitude) * Math.cos(viewport.latitude * Math.PI / 180);
  return latitudeDelta * latitudeDelta + longitudeDelta * longitudeDelta;
}

function parseViewport(value: PlaceSearchViewport | undefined): PlaceSearchViewport | undefined {
  if (value === undefined) return undefined;
  if (!Number.isFinite(value.latitude) || value.latitude < -90 || value.latitude > 90 ||
      !Number.isFinite(value.longitude) || value.longitude < -180 || value.longitude > 180) {
    throw new Error('Invalid place-search viewport.');
  }
  return value;
}

function parseBounds(value: unknown): Bounds {
  const data = exactObject(value, ['west', 'east', 'south', 'north'], 'product bounds');
  const bounds = {
    west: finiteNumber(data.west, 'west'), east: finiteNumber(data.east, 'east'),
    south: finiteNumber(data.south, 'south'), north: finiteNumber(data.north, 'north'),
  };
  if (bounds.west < -180 || bounds.east > 180 || bounds.south < -90 || bounds.north > 90 ||
      bounds.west >= bounds.east || bounds.south >= bounds.north) throw new Error('Invalid place product bounds.');
  return bounds;
}

function labelMap(value: unknown, keyPattern: RegExp, label: string): Readonly<Record<string, string>> {
  const data = object(value, label);
  const result: Record<string, string> = Object.create(null) as Record<string, string>;
  for (const [key, item] of Object.entries(data)) {
    if (!keyPattern.test(key)) throw new Error(`Invalid ${label} key.`);
    result[key] = text(item, `${label} label`, 160);
  }
  return Object.freeze(result);
}

function digestMap(value: unknown, expectedCount: number): Readonly<Record<string, string>> {
  const data = object(value, 'shard digests');
  const entries = Object.entries(data);
  if (entries.length !== expectedCount) throw new Error('Shard digest count does not match the manifest.');
  const result: Record<string, string> = Object.create(null) as Record<string, string>;
  for (const [prefix, digest] of entries) {
    if (!/^[a-z0-9][a-z0-9_]{2}$/.test(prefix)) throw new Error('Invalid shard digest key.');
    result[prefix] = sha256(digest, `shard ${prefix} checksum`);
  }
  return Object.freeze(result);
}

function exactObject(value: unknown, keys: readonly string[], label: string): Record<string, unknown> {
  const data = object(value, label);
  const actual = Object.keys(data).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) {
    throw new Error(`Invalid ${label} fields.`);
  }
  return data;
}

function object(value: unknown, label: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error(`Invalid ${label}.`);
  return value as Record<string, unknown>;
}

function stringArray(value: unknown, label: string): string[] {
  if (!Array.isArray(value) || value.some((item) => typeof item !== 'string')) throw new Error(`Invalid ${label}.`);
  return value;
}

function text(value: unknown, label: string, maxLength: number): string {
  if (typeof value !== 'string' || value.length === 0 || value.length > maxLength || value.trim() !== value || CONTROL_CHARACTERS.test(value)) {
    throw new Error(`Invalid ${label}.`);
  }
  return value;
}

function optionalCode(value: unknown, label: string): string {
  if (typeof value !== 'string' || value.length > 32 || !/^[A-Za-z0-9_-]*$/.test(value)) throw new Error(`Invalid ${label}.`);
  return value;
}

function integer(value: unknown, label: string, minimum: number, maximum: number): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < minimum || value > maximum) throw new Error(`Invalid ${label}.`);
  return value;
}

function finiteNumber(value: unknown, label: string): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) throw new Error(`Invalid ${label}.`);
  return value;
}

function dateText(value: unknown, label: string): string {
  const result = text(value, label, 10);
  if (!/^\d{4}-\d{2}-\d{2}$/.test(result)) throw new Error(`Invalid ${label}.`);
  const date = new Date(`${result}T00:00:00Z`);
  if (Number.isNaN(date.getTime()) || date.toISOString().slice(0, 10) !== result) throw new Error(`Invalid ${label}.`);
  return result;
}

function releaseId(value: unknown): string {
  if (typeof value !== 'string' || value.length < 1 || value.length > 128 || !/^[A-Za-z0-9][A-Za-z0-9_-]*$/.test(value)) {
    throw new Error('Invalid place releaseId.');
  }
  return value;
}

function sha256(value: unknown, label: string): string {
  if (typeof value !== 'string' || !/^[0-9a-f]{64}$/.test(value)) throw new Error(`Invalid ${label}.`);
  return value;
}

function shardPrefix(key: string): string {
  return (key + '___').slice(0, 3).replaceAll(' ', '_');
}

function releasePath(release: string, suffix: string): string {
  return `/places/releases/${encodeURIComponent(release)}/${suffix}`;
}

function parseJsonBytes(bytes: Uint8Array, label: string): unknown {
  let decoded: string;
  try {
    decoded = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
  } catch {
    throw new Error(`Invalid UTF-8 in ${label}.`);
  }
  try {
    return JSON.parse(decoded) as unknown;
  } catch {
    throw new Error(`Invalid JSON in ${label}.`);
  }
}

async function sha256Hex(bytes: Uint8Array): Promise<string> {
  if (!globalThis.crypto?.subtle) throw new Error('This browser cannot verify the place index.');
  const digest = await globalThis.crypto.subtle.digest('SHA-256', bytes as Uint8Array<ArrayBuffer>);
  return [...new Uint8Array(digest)].map((value) => value.toString(16).padStart(2, '0')).join('');
}

function looksLikeCoordinates(value: string): boolean {
  return /^\s*[+-]?\d+(?:\.\d+)?\s*[,;]\s*[+-]?\d+(?:\.\d+)?\s*$/.test(value);
}

function lexical(left: string, right: string): number {
  return left < right ? -1 : left > right ? 1 : 0;
}

const CONTROL_CHARACTERS = /[\u0000-\u001f\u007f]/;
