import { readBoundedResponse } from './boundedResponse';

const POINTER_PATH = '/climate/current.json';
const MAX_POINTER_BYTES = 1_024;
const MAX_CATALOG_BYTES = 32 * 1024 * 1024;
const MAX_POINT_BLOCKS = 20_000;
const MAX_SCALAR_TILES = 20_000;
const MAX_SESSION_RELEASES = 128;
const HEADER_BYTES = 160;
const RECORD_BYTES = 36;
const BLOCK_MEDIA_TYPE = 'application/vnd.nice-weather.climate-block';
const SCALAR_MEDIA_TYPE = 'application/vnd.nice-weather.score-tile';
const SCALAR_BYTES = 258 * 258;
const FIELDS = [
  'comfort_good', 'dry_good', 'bright_good', 'nice_good', 'nice_calm_good',
  'cycling_good', 'cycling_calm_good',
] as const;
const DERIVATION_CONTRACT = 'agera5-v2.0|daily-metrics-v1|extrema-order-v1|weather-score-v1|climate-distribution-v1|natural-earth-5.1.1-centre-land|2006-2025';

export type ClimateField = typeof FIELDS[number];

export type ClimateRatingShares = Readonly<{
  awful: number;
  poor: number;
  mixed: number;
  good: number;
  veryGood: number;
  excellent: number;
}>;

export type ClimateProfileEvidence = Readonly<{
  medianScore: number;
  calmGoodPercent: number;
  poorWeekPermille: number;
  ratingShares: ClimateRatingShares;
  goodOrBetterPermille: number;
  goodOrBetterPercent: number;
  awfulPermille: number;
  awfulPercent: number;
}>;

export type ClimatePointEvidence = Readonly<{
  releaseId: string;
  period: string;
  sourceCell: Readonly<{ latitude: number; longitude: number }>;
  comfortGoodPercent: number;
  dryGoodPercent: number;
  brightGoodPercent: number;
  nice: ClimateProfileEvidence;
  cycling: ClimateProfileEvidence;
}>;

export type ClimatePointBlock = Readonly<{
  latitudeBlock: number;
  longitudeBlock: number;
  latitudeCount: number;
  longitudeCount: number;
  path: string;
  bytes: number;
  sha256: string;
  payloadSha256: string;
  mediaType: typeof BLOCK_MEDIA_TYPE;
  dataRecords: number;
  noDataRecords: number;
}>;

export type ClimateScalarTile = Readonly<{
  field: ClimateField;
  zoom: number;
  x: number;
  y: number;
  path: string;
  bytes: typeof SCALAR_BYTES;
  sha256: string;
  mediaType: typeof SCALAR_MEDIA_TYPE;
  dataTexels: number;
  noDataTexels: number;
}>;

export type ClimateCatalog = Readonly<{
  version: 1;
  releaseId: string;
  period: string;
  geometry: Readonly<{
    projection: 'EPSG:4326'; west: -179.95; east: 179.95; south: -85.05; north: 85.05;
    rows: 1701; columns: 3599; cellResolutionDegrees: 0.1; cellRegistration: 'centre';
  }>;
  source: Readonly<{
    dataset: 'AgERA5'; version: '2.0'; kind: 'topography-corrected-interpolated-era5';
    referencePeriod: Readonly<{ firstYear: 2006; lastYear: 2025 }>;
    deliveryGridDegrees: 0.1; atmosphericEvidenceKm: 30; precipitationEvidenceDegrees: 0.25;
    localDay: 'agera5-eight-longitude-zones-at-three-hour-offsets-not-civil-time';
    landMask: 'natural-earth-5.1.1-centre-on-land';
  }>;
  provenance: Readonly<{
    sourceEvidenceSha256: string;
    landMaskSetSha256: string;
    derivationContract: typeof DERIVATION_CONTRACT;
  }>;
  probability: Readonly<{
    unit: 'whole-percent';
    denominator: 'complete-real-days-in-reference-month';
    threshold: 'canonical-rating-good-or-better';
    goodMinimumScore: 55;
    noDataValue: 255;
    fields: readonly ClimateField[];
  }>;
  maximumZoom: 4;
  pointBlocks: readonly ClimatePointBlock[];
  scalarTiles: readonly ClimateScalarTile[];
}>;

export type ClimateClientOptions = Readonly<{
  fetcher?: typeof fetch;
  catalogCacheSize?: number;
  blockCacheSize?: number;
}>;

export class ClimateDataError extends Error {
  readonly code: 'unavailable' | 'invalid' | 'stale';

  constructor(code: 'unavailable' | 'invalid' | 'stale') {
    super(code === 'unavailable'
      ? 'Climate data is not available right now.'
      : code === 'stale' ? 'Climate data changed. Try again.' : 'Climate data could not be read.');
    this.name = 'ClimateDataError';
    this.code = code;
  }
}

type Pointer = Readonly<{ releaseId: string; catalogSha256: string }>;
type PointLocation = Readonly<{
  latitude: number;
  longitude: number;
  latitudeIndex: number;
  longitudeIndex: number;
  latitudeBlock: number;
  longitudeBlock: number;
}>;
export class WorldwideClimateClient {
  readonly #fetcher: typeof fetch;
  readonly #catalogCacheSize: number;
  readonly #blockCacheSize: number;
  readonly #catalogCache = new Map<string, ClimateCatalog>();
  readonly #releaseDigests = new Map<string, string>();
  readonly #blockCache = new Map<string, Uint8Array>();
  #activePoint?: AbortController;
  #pointGeneration = 0;

  constructor(options: ClimateClientOptions = {}) {
    this.#fetcher = options.fetcher ?? fetch;
    this.#catalogCacheSize = boundedCacheSize(options.catalogCacheSize, 2, 1, 4);
    this.#blockCacheSize = boundedCacheSize(options.blockCacheSize, 16, 1, 64);
  }

  async loadCatalog(signal?: AbortSignal): Promise<ClimateCatalog> {
    try {
      assertNotAborted(signal);
      const pointer = await this.#loadPointer(signal);
      assertNotAborted(signal);
      const catalog = await this.#catalogFor(pointer, signal);
      assertNotAborted(signal);
      const after = await this.#loadPointer(signal);
      assertNotAborted(signal);
      if (!samePointer(pointer, after)) throw new ClimateDataError('stale');
      return catalog;
    } catch (error) {
      if (signal?.aborted) throw signal.reason ?? new DOMException('Aborted', 'AbortError');
      throw error;
    }
  }

  async loadPoint(period: string, longitude: number, latitude: number, signal?: AbortSignal): Promise<ClimatePointEvidence | undefined> {
    this.#activePoint?.abort(new ClimateDataError('stale'));
    const controller = new AbortController();
    this.#activePoint = controller;
    const generation = ++this.#pointGeneration;
    const combined = combineSignals(controller.signal, signal);

    try {
      const pointer = await this.#loadPointer(combined);
      this.#assertCurrent(generation, controller, combined);
      const catalog = await this.#catalogFor(pointer, combined);
      this.#assertCurrent(generation, controller, combined);
      if (period !== catalog.period) throw new ClimateDataError('stale');

      const location = locatePoint(longitude, latitude, catalog);
      if (!location) {
        await this.#confirmPointer(pointer, combined, generation, controller);
        return undefined;
      }
      const block = catalog.pointBlocks.find((candidate) =>
        candidate.latitudeBlock === location.latitudeBlock && candidate.longitudeBlock === location.longitudeBlock);
      if (!block) {
        await this.#confirmPointer(pointer, combined, generation, controller);
        return undefined;
      }

      const url = climateAssetUrl(catalog.releaseId, block.path);
      const blockKey = `${url}:${block.sha256}`;
      let blockBytes = touch(this.#blockCache, blockKey);
      if (!blockBytes) {
        blockBytes = await this.#loadBlock(url, catalog, block, combined);
        this.#assertCurrent(generation, controller, combined);
        insertBounded(this.#blockCache, blockKey, blockBytes, this.#blockCacheSize);
      }

      const row = location.latitudeIndex - block.latitudeBlock * 16;
      const column = location.longitudeIndex - block.longitudeBlock * 32;
      if (row < 0 || row >= block.latitudeCount || column < 0 || column >= block.longitudeCount) {
        throw new ClimateDataError('invalid');
      }
      const index = row * block.longitudeCount + column;
      const start = HEADER_BYTES + index * RECORD_BYTES;
      if (!Number.isSafeInteger(start) || start + RECORD_BYTES > block.bytes) throw new ClimateDataError('invalid');
      const record = blockBytes.subarray(start, start + RECORD_BYTES);
      const evidence = decodeRecord(record, catalog.releaseId, catalog.period, location);

      await this.#confirmPointer(pointer, combined, generation, controller);
      return evidence;
    } catch (error) {
      if (signal?.aborted) throw signal.reason ?? new DOMException('Aborted', 'AbortError');
      if (controller.signal.aborted || generation !== this.#pointGeneration) throw new ClimateDataError('stale');
      if (error instanceof ClimateDataError) throw error;
      throw new ClimateDataError('invalid');
    } finally {
      if (this.#activePoint === controller) this.#activePoint = undefined;
    }
  }

  findScalarTile(catalog: ClimateCatalog, field: ClimateField, zoom: number, x: number, y: number): ClimateScalarTile | undefined {
    if (!FIELDS.includes(field) || !integer(zoom, 0, 4) || !integer(x, 0, 2 ** zoom - 1) || !integer(y, 0, 2 ** zoom - 1)) return undefined;
    return catalog.scalarTiles.find((tile) => tile.field === field && tile.zoom === zoom && tile.x === x && tile.y === y);
  }

  scalarTileUrl(catalog: ClimateCatalog, tile: ClimateScalarTile): string {
    if (!catalog.scalarTiles.includes(tile)) throw new ClimateDataError('invalid');
    return climateAssetUrl(catalog.releaseId, tile.path);
  }

  async #loadPointer(signal?: AbortSignal): Promise<Pointer> {
    let response: Response;
    try {
      response = await this.#fetcher(POINTER_PATH, { signal, cache: 'no-cache' });
    } catch (error) {
      if (signal?.aborted) throw signal.reason ?? error;
      throw new ClimateDataError('unavailable');
    }
    if (!response.ok) throw new ClimateDataError('unavailable');
    if (!jsonMediaType(response)) throw new ClimateDataError('invalid');
    try {
      const bytes = await readBoundedResponse(response, 'climate pointer', MAX_POINTER_BYTES);
      const data = exactObject(parseJSON(bytes), ['releaseId', 'catalogSha256']);
      if (!releaseID(data.releaseId) || !sha256Value(data.catalogSha256)) throw new ClimateDataError('invalid');
      return { releaseId: data.releaseId, catalogSha256: data.catalogSha256 };
    } catch (error) {
      if (error instanceof ClimateDataError) throw error;
      throw new ClimateDataError('invalid');
    }
  }

  async #catalogFor(pointer: Pointer, signal?: AbortSignal): Promise<ClimateCatalog> {
    const knownDigest = this.#releaseDigests.get(pointer.releaseId);
    if (knownDigest !== undefined && knownDigest !== pointer.catalogSha256) throw new ClimateDataError('stale');
    if (knownDigest === undefined && this.#releaseDigests.size >= MAX_SESSION_RELEASES) throw new ClimateDataError('invalid');
    const key = `${pointer.releaseId}:${pointer.catalogSha256}`;
    const cached = touch(this.#catalogCache, key);
    if (cached) return cached;

    const url = `/climate/releases/${pointer.releaseId}/catalog.json`;
    let response: Response;
    try {
      response = await this.#fetcher(url, { signal });
    } catch (error) {
      if (signal?.aborted) throw signal.reason ?? error;
      throw new ClimateDataError('unavailable');
    }
    if (!response.ok) throw new ClimateDataError(response.status === 404 || response.status === 409 || response.status === 410 ? 'stale' : 'unavailable');
    if (!jsonMediaType(response)) throw new ClimateDataError('invalid');
    let bytes: Uint8Array;
    try {
      bytes = await readBoundedResponse(response, 'climate catalog', MAX_CATALOG_BYTES);
    } catch {
      throw new ClimateDataError('invalid');
    }
    if (bytes.byteLength === 0 || await digestHex(bytes) !== pointer.catalogSha256) throw new ClimateDataError('invalid');
    const catalog = parseCatalog(parseJSON(bytes));
    if (catalog.releaseId !== pointer.releaseId) throw new ClimateDataError('stale');
    const currentDigest = this.#releaseDigests.get(pointer.releaseId);
    if (currentDigest !== undefined && currentDigest !== pointer.catalogSha256) throw new ClimateDataError('stale');
    if (currentDigest === undefined && this.#releaseDigests.size >= MAX_SESSION_RELEASES) throw new ClimateDataError('invalid');
    this.#releaseDigests.set(pointer.releaseId, pointer.catalogSha256);
    insertBounded(this.#catalogCache, key, catalog, this.#catalogCacheSize);
    return catalog;
  }

  async #loadBlock(url: string, catalog: ClimateCatalog, block: ClimatePointBlock, signal: AbortSignal): Promise<Uint8Array> {
    let response: Response;
    try {
      response = await this.#fetcher(url, { signal, headers: { Range: `bytes=0-${block.bytes - 1}` } });
    } catch (error) {
      if (signal.aborted) throw signal.reason ?? error;
      throw new ClimateDataError('unavailable');
    }
    if (response.status === 404 || response.status === 409 || response.status === 410) throw new ClimateDataError('stale');
    if (response.status !== 206 || response.headers.get('Content-Type')?.trim().toLowerCase() !== block.mediaType || response.headers.has('Content-Encoding')) {
      throw new ClimateDataError('invalid');
    }
    if (response.headers.get('Content-Range') !== `bytes 0-${block.bytes - 1}/${block.bytes}`) throw new ClimateDataError('invalid');
    if (response.headers.get('Content-Length') !== String(block.bytes)) throw new ClimateDataError('invalid');
    let bytes: Uint8Array;
    try {
      bytes = await readBoundedResponse(response, 'climate point block', block.bytes);
    } catch {
      throw new ClimateDataError('invalid');
    }
    if (bytes.byteLength !== block.bytes || await digestHex(bytes) !== block.sha256) throw new ClimateDataError('invalid');
    parseHeader(bytes.subarray(0, HEADER_BYTES), catalog, block);
    if (await digestHex(bytes.subarray(HEADER_BYTES)) !== block.payloadSha256) throw new ClimateDataError('invalid');
    return bytes;
  }

  #assertCurrent(generation: number, controller: AbortController, signal: AbortSignal): void {
    if (signal.aborted && controller.signal.aborted) throw new ClimateDataError('stale');
    if (signal.aborted) throw signal.reason ?? new DOMException('Aborted', 'AbortError');
    if (generation !== this.#pointGeneration || controller.signal.aborted) throw new ClimateDataError('stale');
  }

  async #confirmPointer(pointer: Pointer, signal: AbortSignal, generation: number, controller: AbortController): Promise<void> {
    const after = await this.#loadPointer(signal);
    this.#assertCurrent(generation, controller, signal);
    if (!samePointer(pointer, after)) throw new ClimateDataError('stale');
  }
}

function parseCatalog(value: unknown): ClimateCatalog {
  const data = exactObject(value, ['version', 'releaseId', 'period', 'geometry', 'source', 'provenance', 'probability', 'maximumZoom', 'pointBlocks', 'scalarTiles']);
  if (data.version !== 1 || !releaseID(data.releaseId) || !periodID(data.period) || data.maximumZoom !== 4) throw new ClimateDataError('invalid');

  const geometry = exactObject(data.geometry, ['projection', 'west', 'east', 'south', 'north', 'rows', 'columns', 'cellResolutionDegrees', 'cellRegistration']);
  if (geometry.projection !== 'EPSG:4326' || geometry.west !== -179.95 || geometry.east !== 179.95 || geometry.south !== -85.05 || geometry.north !== 85.05 || geometry.rows !== 1701 || geometry.columns !== 3599 || geometry.cellResolutionDegrees !== 0.1 || geometry.cellRegistration !== 'centre') throw new ClimateDataError('invalid');

  const source = exactObject(data.source, ['dataset', 'version', 'kind', 'referencePeriod', 'deliveryGridDegrees', 'atmosphericEvidenceKm', 'precipitationEvidenceDegrees', 'localDay', 'landMask']);
  const reference = exactObject(source.referencePeriod, ['firstYear', 'lastYear']);
  if (source.dataset !== 'AgERA5' || source.version !== '2.0' || source.kind !== 'topography-corrected-interpolated-era5' || reference.firstYear !== 2006 || reference.lastYear !== 2025 || source.deliveryGridDegrees !== 0.1 || source.atmosphericEvidenceKm !== 30 || source.precipitationEvidenceDegrees !== 0.25 || source.localDay !== 'agera5-eight-longitude-zones-at-three-hour-offsets-not-civil-time' || source.landMask !== 'natural-earth-5.1.1-centre-on-land') throw new ClimateDataError('invalid');

  const provenance = exactObject(data.provenance, ['sourceEvidenceSha256', 'landMaskSetSha256', 'derivationContract']);
  if (!nonzeroSHA256(provenance.sourceEvidenceSha256) || !nonzeroSHA256(provenance.landMaskSetSha256) || provenance.derivationContract !== DERIVATION_CONTRACT) throw new ClimateDataError('invalid');

  const probability = exactObject(data.probability, ['unit', 'denominator', 'threshold', 'goodMinimumScore', 'noDataValue', 'fields']);
  if (probability.unit !== 'whole-percent' || probability.denominator !== 'complete-real-days-in-reference-month' || probability.threshold !== 'canonical-rating-good-or-better' || probability.goodMinimumScore !== 55 || probability.noDataValue !== 255 || !sameArray(probability.fields, FIELDS)) throw new ClimateDataError('invalid');

  if (!Array.isArray(data.pointBlocks) || data.pointBlocks.length < 1 || data.pointBlocks.length > MAX_POINT_BLOCKS || !Array.isArray(data.scalarTiles) || data.scalarTiles.length < 1 || data.scalarTiles.length > MAX_SCALAR_TILES) throw new ClimateDataError('invalid');
  const pointBlocks = data.pointBlocks.map((item: unknown) => pointBlock(item, data.period));
  validatePointOrder(pointBlocks);
  const scalarTiles = data.scalarTiles.map((item: unknown) => scalarTile(item, data.period));
  validateScalarTiles(scalarTiles);

  return deepFreeze({
    version: 1 as const,
    releaseId: data.releaseId,
    period: data.period,
    geometry: geometry as ClimateCatalog['geometry'],
    source: { ...source, referencePeriod: reference } as ClimateCatalog['source'],
    provenance: provenance as ClimateCatalog['provenance'],
    probability: {
      unit: 'whole-percent',
      denominator: 'complete-real-days-in-reference-month',
      threshold: 'canonical-rating-good-or-better',
      goodMinimumScore: 55,
      noDataValue: 255,
      fields: [...FIELDS],
    },
    maximumZoom: 4 as const,
    pointBlocks,
    scalarTiles,
  });
}

function pointBlock(value: unknown, period: string): ClimatePointBlock {
  const data = exactObject(value, ['latitudeBlock', 'longitudeBlock', 'latitudeCount', 'longitudeCount', 'path', 'bytes', 'sha256', 'payloadSha256', 'mediaType', 'dataRecords', 'noDataRecords']);
  if (!integer(data.latitudeBlock, 0, 112) || !integer(data.longitudeBlock, 0, 112)) throw new ClimateDataError('invalid');
  const latitudeCount = blockLength(data.latitudeBlock, 1801, 16);
  const longitudeCount = blockLength(data.longitudeBlock, 3600, 32);
  const records = latitudeCount * longitudeCount;
  const firstLatitude = data.latitudeBlock * 16;
  const firstLongitude = data.longitudeBlock * 32;
  const intersectsCrop = firstLatitude + latitudeCount - 1 >= 50 && firstLatitude <= 1750
    && firstLongitude + longitudeCount - 1 >= 1 && firstLongitude <= 3599;
  const expectedPath = `points/${period}/${String(data.latitudeBlock).padStart(3, '0')}/${String(data.longitudeBlock).padStart(3, '0')}.nwc`;
  if (!intersectsCrop || data.latitudeCount !== latitudeCount || data.longitudeCount !== longitudeCount || data.path !== expectedPath || !safeAssetPath(data.path) || data.bytes !== HEADER_BYTES + records * RECORD_BYTES || !sha256Value(data.sha256) || !sha256Value(data.payloadSha256) || data.mediaType !== BLOCK_MEDIA_TYPE || !integer(data.dataRecords, 1, records) || !integer(data.noDataRecords, 0, records) || data.dataRecords + data.noDataRecords !== records) throw new ClimateDataError('invalid');
  return data as ClimatePointBlock;
}

function scalarTile(value: unknown, period: string): ClimateScalarTile {
  const data = exactObject(value, ['field', 'zoom', 'x', 'y', 'path', 'bytes', 'sha256', 'mediaType', 'dataTexels', 'noDataTexels']);
  if (!FIELDS.includes(data.field) || !integer(data.zoom, 0, 4)) throw new ClimateDataError('invalid');
  const maximum = 2 ** data.zoom - 1;
  const expectedPath = `scores/${period}/${data.field}/${data.zoom}/${data.x}/${data.y}.nws`;
  if (!integer(data.x, 0, maximum) || !integer(data.y, 0, maximum) || data.path !== expectedPath || !safeAssetPath(data.path) || data.bytes !== SCALAR_BYTES || !sha256Value(data.sha256) || data.mediaType !== SCALAR_MEDIA_TYPE || !integer(data.dataTexels, 1, SCALAR_BYTES) || !integer(data.noDataTexels, 0, SCALAR_BYTES) || data.dataTexels + data.noDataTexels !== SCALAR_BYTES) throw new ClimateDataError('invalid');
  return data as ClimateScalarTile;
}

function validatePointOrder(blocks: readonly ClimatePointBlock[]): void {
  let previous = -1;
  for (const block of blocks) {
    const rank = block.latitudeBlock * 113 + block.longitudeBlock;
    if (rank <= previous) throw new ClimateDataError('invalid');
    previous = rank;
  }
}

function validateScalarTiles(tiles: readonly ClimateScalarTile[]): void {
  const coverage = new Map<ClimateField, Set<string>>(FIELDS.map((field) => [field, new Set()]));
  let previous = '';
  for (const tile of tiles) {
    const rank = `${String(FIELDS.indexOf(tile.field)).padStart(2, '0')}/${tile.zoom}/${String(tile.y).padStart(10, '0')}/${String(tile.x).padStart(10, '0')}`;
    if (rank <= previous) throw new ClimateDataError('invalid');
    previous = rank;
    const key = tileKey(tile.zoom, tile.x, tile.y);
    const field = coverage.get(tile.field)!;
    if (field.has(key)) throw new ClimateDataError('invalid');
    field.add(key);
  }
  const baseline = coverage.get(FIELDS[0])!;
  if (!baseline.has(tileKey(0, 0, 0))) throw new ClimateDataError('invalid');
  for (let zoom = 0; zoom <= 4; zoom++) if (![...baseline].some((key) => key.startsWith(`${zoom}/`))) throw new ClimateDataError('invalid');
  for (const field of FIELDS.slice(1)) if (!sameSet(baseline, coverage.get(field)!)) throw new ClimateDataError('invalid');
  for (const key of baseline) {
    const [zoom, x, y] = key.split('/').map(Number);
    if (zoom > 0 && !baseline.has(tileKey(zoom - 1, Math.floor(x / 2), Math.floor(y / 2)))) throw new ClimateDataError('invalid');
    if (zoom < 4) {
      let child = false;
      for (let dy = 0; dy < 2; dy++) for (let dx = 0; dx < 2; dx++) child ||= baseline.has(tileKey(zoom + 1, x * 2 + dx, y * 2 + dy));
      if (!child) throw new ClimateDataError('invalid');
    }
  }
}

function locatePoint(longitude: number, latitude: number, catalog: ClimateCatalog): PointLocation | undefined {
  if (!Number.isFinite(longitude) || !Number.isFinite(latitude) || latitude < -90 || latitude > 90) throw new ClimateDataError('invalid');
  const wrapped = ((longitude + 180) % 360 + 360) % 360 - 180;
  const geometry = catalog.geometry;
  if (wrapped < geometry.west || wrapped > geometry.east || latitude < geometry.south || latitude > geometry.north) return undefined;
  let cropColumn = Math.floor((wrapped - geometry.west) / geometry.cellResolutionDegrees);
  let cropLatitude = Math.floor((latitude - geometry.south) / geometry.cellResolutionDegrees);
  if (cropColumn === geometry.columns) cropColumn--;
  if (cropLatitude === geometry.rows) cropLatitude--;
  if (!integer(cropColumn, 0, geometry.columns - 1) || !integer(cropLatitude, 0, geometry.rows - 1)) return undefined;
  const longitudeIndex = cropColumn + 1;
  const latitudeIndex = cropLatitude + 50;
  return {
    longitude: cleanCoordinate(-180 + longitudeIndex / 10),
    latitude: cleanCoordinate(-90 + latitudeIndex / 10),
    longitudeIndex,
    latitudeIndex,
    longitudeBlock: Math.floor(longitudeIndex / 32),
    latitudeBlock: Math.floor(latitudeIndex / 16),
  };
}

function parseHeader(bytes: Uint8Array, catalog: ClimateCatalog, block: ClimatePointBlock): void {
  if (bytes.byteLength !== HEADER_BYTES || ascii(bytes.subarray(0, 8)) !== 'NWAG5CLM') throw new ClimateDataError('invalid');
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  if (view.getUint16(8, true) !== 1 || view.getUint16(10, true) !== HEADER_BYTES || view.getUint16(12, true) !== RECORD_BYTES || view.getUint16(14, true) !== block.latitudeCount || view.getUint16(16, true) !== block.longitudeCount || view.getUint16(18, true) !== block.latitudeBlock || view.getUint16(20, true) !== block.longitudeBlock || bytes[24] !== 1 || bytes[25] !== 1 || !allZero(bytes.subarray(26, 32))) throw new ClimateDataError('invalid');
  const releaseLength = bytes[22];
  const periodLength = bytes[23];
  if (releaseLength !== catalog.releaseId.length || periodLength !== catalog.period.length || releaseLength > 64 || periodLength > 32 || ascii(bytes.subarray(32, 32 + releaseLength)) !== catalog.releaseId || ascii(bytes.subarray(96, 96 + periodLength)) !== catalog.period || !allZero(bytes.subarray(32 + releaseLength, 96)) || !allZero(bytes.subarray(96 + periodLength, 128))) throw new ClimateDataError('invalid');
  const payloadSha256 = hex(bytes.subarray(128, 160));
  if (payloadSha256 !== block.payloadSha256) throw new ClimateDataError('invalid');
}

function decodeRecord(bytes: Uint8Array, releaseId: string, period: string, location: PointLocation): ClimatePointEvidence | undefined {
  if (bytes.byteLength !== RECORD_BYTES || (bytes[0] & ~1) !== 0) throw new ClimateDataError('invalid');
  if (bytes[0] === 0) {
    if (!allZero(bytes.subarray(1))) throw new ClimateDataError('invalid');
    return undefined;
  }
  if (bytes[1] > 100 || bytes[2] > 100 || bytes[3] > 100) throw new ClimateDataError('invalid');
  const nice = decodeProfile(bytes.subarray(4, 20));
  const cycling = decodeProfile(bytes.subarray(20, 36));
  if (cycling.calmGoodPercent > nice.calmGoodPercent) throw new ClimateDataError('invalid');
  return deepFreeze({
    releaseId, period,
    sourceCell: { latitude: location.latitude, longitude: location.longitude },
    comfortGoodPercent: bytes[1], dryGoodPercent: bytes[2], brightGoodPercent: bytes[3],
    nice, cycling,
  });
}

function decodeProfile(bytes: Uint8Array): ClimateProfileEvidence {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const ratingShares = {
    awful: view.getUint16(4, true), poor: view.getUint16(6, true), mixed: view.getUint16(8, true),
    good: view.getUint16(10, true), veryGood: view.getUint16(12, true), excellent: view.getUint16(14, true),
  };
  const medianScore = bytes[0];
  const calmGoodPercent = bytes[1];
  const poorWeekPermille = view.getUint16(2, true);
  const shares = Object.values(ratingShares);
  if (medianScore > 100 || calmGoodPercent > 100 || poorWeekPermille > 1000 || shares.some((value) => value > 1000) || shares.reduce((sum, value) => sum + value, 0) !== 1000) throw new ClimateDataError('invalid');
  const goodOrBetterPermille = ratingShares.good + ratingShares.veryGood + ratingShares.excellent;
  return {
    medianScore, calmGoodPercent, poorWeekPermille, ratingShares,
    goodOrBetterPermille,
    goodOrBetterPercent: Math.floor((goodOrBetterPermille + 5) / 10),
    awfulPermille: ratingShares.awful,
    awfulPercent: Math.floor((ratingShares.awful + 5) / 10),
  };
}

function exactObject(value: unknown, keys: readonly string[]): Record<string, any> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new ClimateDataError('invalid');
  const object = value as Record<string, any>;
  const actual = Object.keys(object).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) throw new ClimateDataError('invalid');
  return object;
}

function parseJSON(bytes: Uint8Array): unknown {
  try {
    return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as unknown;
  } catch {
    throw new ClimateDataError('invalid');
  }
}

function climateAssetUrl(release: string, path: string): string {
  if (!releaseID(release) || !safeAssetPath(path)) throw new ClimateDataError('invalid');
  return `/climate/releases/${release}/${path}`;
}

function jsonMediaType(response: Response): boolean {
  return /^application\/json(?:\s*;\s*charset=[A-Za-z0-9._-]+)?$/i.test(response.headers.get('Content-Type')?.trim() ?? '');
}

function safeAssetPath(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && !value.includes('\\') && !value.includes('\0') && !value.startsWith('/') && value.split('/').every((part) => part !== '' && part !== '.' && part !== '..');
}

function releaseID(value: unknown): value is string { return typeof value === 'string' && value.length <= 64 && /^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(value); }
function periodID(value: unknown): value is string { return typeof value === 'string' && /^month-(0[1-9]|1[0-2])$/.test(value); }
function sha256Value(value: unknown): value is string { return typeof value === 'string' && /^[a-f0-9]{64}$/.test(value); }
function nonzeroSHA256(value: unknown): value is string { return sha256Value(value) && /[1-9a-f]/.test(value); }
function integer(value: unknown, minimum: number, maximum: number): value is number { return typeof value === 'number' && Number.isSafeInteger(value) && value >= minimum && value <= maximum; }
function blockLength(block: number, count: number, size: number): number { return Math.min(size, count - block * size); }
function tileKey(zoom: number, x: number, y: number): string { return `${zoom}/${x}/${y}`; }
function allZero(bytes: Uint8Array): boolean { return bytes.every((value) => value === 0); }
function hex(bytes: Uint8Array): string { return Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join(''); }
function ascii(bytes: Uint8Array): string {
  if (bytes.some((value) => value > 0x7f)) throw new ClimateDataError('invalid');
  return String.fromCharCode(...bytes);
}
function cleanCoordinate(value: number): number { return Object.is(value, -0) ? 0 : Number(value.toFixed(10)); }
function samePointer(left: Pointer, right: Pointer): boolean { return left.releaseId === right.releaseId && left.catalogSha256 === right.catalogSha256; }
function sameArray(left: unknown, right: readonly unknown[]): boolean { return Array.isArray(left) && left.length === right.length && left.every((item, index) => item === right[index]); }
function sameSet(left: Set<string>, right: Set<string>): boolean { return left.size === right.size && [...left].every((item) => right.has(item)); }

async function digestHex(bytes: Uint8Array): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', bytes as BufferSource);
  return hex(new Uint8Array(digest));
}

function combineSignals(internal: AbortSignal, external?: AbortSignal): AbortSignal {
  return external ? AbortSignal.any([internal, external]) : internal;
}

function assertNotAborted(signal?: AbortSignal): void {
  if (signal?.aborted) throw signal.reason ?? new DOMException('Aborted', 'AbortError');
}

function boundedCacheSize(value: number | undefined, fallback: number, minimum: number, maximum: number): number {
  if (value === undefined) return fallback;
  if (!integer(value, minimum, maximum)) throw new TypeError('Climate cache size is invalid.');
  return value;
}

function touch<K, V>(cache: Map<K, V>, key: K): V | undefined {
  const value = cache.get(key);
  if (value === undefined) return undefined;
  cache.delete(key);
  cache.set(key, value);
  return value;
}

function insertBounded<K, V>(cache: Map<K, V>, key: K, value: V, maximum: number, evicted?: (key: K) => void): void {
  cache.delete(key);
  cache.set(key, value);
  while (cache.size > maximum) {
    const oldest = cache.keys().next().value as K;
    cache.delete(oldest);
    evicted?.(oldest);
  }
}

function deepFreeze<T>(value: T): T {
  if (typeof value !== 'object' || value === null || Object.isFrozen(value)) return value;
  for (const child of Object.values(value as Record<string, unknown>)) deepFreeze(child);
  return Object.freeze(value);
}
