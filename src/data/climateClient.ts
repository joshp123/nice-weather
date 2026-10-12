import { readBoundedResponse } from './boundedResponse';

const POINTER_PATH = '/climate/current.json';
const MAX_POINTER_BYTES = 1_024;
const MAX_INDEX_BYTES = 64 * 1_024;
const MAX_CATALOG_BYTES = 32 * 1_024 * 1_024;
const MAX_POINT_BLOCKS = 2_100;
const MAX_DATELINE_BLOCKS = 46;
const MAX_SCALAR_TILES = 3_000;
const MAX_SESSION_IDENTITIES = 128;
const HEADER_BYTES = 64;
const RECORD_BYTES = 64;
const BLOCK_ROWS = 16;
const BLOCK_COLUMNS = 32;
const ROWS = 721;
const COLUMNS = 1_440;
const GRID_DEGREES = 0.25;
const MAIN_BLOCK_BYTES = HEADER_BYTES + BLOCK_ROWS * BLOCK_COLUMNS * RECORD_BYTES;
const DATELINE_BLOCK_BYTES = HEADER_BYTES + BLOCK_ROWS * RECORD_BYTES;
const MAIN_MEDIA_TYPE = 'application/vnd.nice-weather.era5-climate-point-block';
const DATELINE_MEDIA_TYPE = 'application/vnd.nice-weather.era5-climate-dateline-block';
const SCALAR_MEDIA_TYPE = 'application/vnd.nice-weather.score-tile';
const SCALAR_BYTES = 258 * 258;
const SOURCE_KIND = 'hourly-reanalysis-reduced-to-iana-civil-local-days';
const LOCAL_DAY = 'IANA civil local daytime 10:00–20:00, exact valid dates, 2006–2025';
const LAND_AUTHORITY = 'Natural Earth 5.1.1 source-cell-footprint intersection';
const DISPLAY_MASK_AUTHORITY = 'Natural Earth 5.1.1 centre-on-land at exact ERA5 0.25 degree grid points';
const DATELINE_MEANING = 'independent +180 degree civil-day records; never alias to -180 degree records';
const MAXIMUM_ZOOM_MEANING = 'exact evaluated point value sampled only where the ERA5 grid-point centre is land';
const PARENT_AGGREGATION = 'rounded arithmetic mean of available four child texels; no-data excluded';
const FIELDS = [
  'comfort_good', 'dry_good', 'clear_sky_good', 'nice_good', 'nice_calm_good',
  'cycling_good', 'cycling_calm_good',
] as const;

const scalarTileIndexes = new WeakMap<readonly ClimateScalarTile[], ReadonlyMap<string, ClimateScalarTile>>();

export type ClimateField = typeof FIELDS[number];
export type ClimateIndexState = 'complete';
export type ClimateGeometry = Readonly<{
  projection: 'EPSG:4326'; west: -180; east: 180; south: -90; north: 90;
  rows: 721; columns: 1440; cellResolutionDegrees: 0.25;
  cellRegistration: 'exact-era5-grid-point-centres';
}>;
export type ClimateReferencePeriod = Readonly<{ firstYear: 2006; lastYear: 2025 }>;
export type ClimateDatelineSemantics = Readonly<{
  longitude: 180;
  meaning: typeof DATELINE_MEANING;
  canonicalColumns: 1440;
  overrideRecordBytes: 64;
}>;
export type ClimateProbabilitySemantics = Readonly<{
  unit: 'whole-percent';
  denominator: 'complete-real-days-in-reference-month';
  threshold: 'canonical-rating-good-or-better';
  goodMinimumScore: 55;
  noDataValue: 255;
  fields: readonly ClimateField[];
}>;
export type ClimateScalarSemantics = Readonly<{
  maximumZoomMeaning: typeof MAXIMUM_ZOOM_MEANING;
  parentAggregation: typeof PARENT_AGGREGATION;
}>;
export type ClimateIndexSource = Readonly<{
  dataset: 'ERA5';
  kind: typeof SOURCE_KIND;
  gridDegrees: 0.25;
  localDay: typeof LOCAL_DAY;
  landAuthority: typeof LAND_AUTHORITY;
  landSourceSha256: string;
  publicationAuthorityKind: 'checksum-trusted-acquisition-plan' | 'checksum-trusted-publication-authority-v1';
  logicalCellCount: number;
  datelineCellCount: number;
  displayMaskAuthority: typeof DISPLAY_MASK_AUTHORITY;
  displayMaskDerivation: string;
  displayMaskSha256: string;
  displayMaskBytes: 1038240;
}>;
export type ClimateMonthReference = Readonly<{ period: string; releaseId: string; catalogSha256: string }>;
export type ClimateIndex = Readonly<{
  version: 2;
  indexId: string;
  state: ClimateIndexState;
  referencePeriod: ClimateReferencePeriod;
  geometry: ClimateGeometry;
  source: ClimateIndexSource;
  positiveDateline: ClimateDatelineSemantics;
  probability: ClimateProbabilitySemantics;
  scalarSemantics: ClimateScalarSemantics;
  maximumZoom: number;
  pointRecordBytes: 64;
  months: readonly ClimateMonthReference[];
}>;

export type ClimateRatingShares = Readonly<{
  awful: number; poor: number; mixed: number; good: number; veryGood: number; excellent: number;
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
export type ClimatePhysicalEvidence = Readonly<{
  daytimeTemperatureP10C: number;
  daytimeTemperatureP50C: number;
  daytimeTemperatureP90C: number;
  daytimeMinimumP50C: number;
  daytimeMaximumP50C: number;
  dewPointP50C: number;
  dewPointP90C: number;
  relativeHumidityP50Percent: number;
  muggyDayPercent: number;
  rainFreeDayPercent: number;
  rainP90MM: number;
  cloudCoverP50Percent: number;
  clearSkyDayPercent: number;
  windP50KPH: number;
  windP90KPH: number;
  days: number;
}>;
export type ClimatePointEvidence = Readonly<{
  releaseId: string;
  period: string;
  sourceCell: Readonly<{ latitude: number; longitude: number }>;
  usedAdjacentCell: boolean;
  comfortGoodPercent: number;
  dryGoodPercent: number;
  clearSkyGoodPercent: number;
  nice: ClimateProfileEvidence;
  cycling: ClimateProfileEvidence;
  physical: ClimatePhysicalEvidence;
}>;

type PointAssetBase = Readonly<{
  latitudeBlock: number;
  latitudeCount: number;
  path: string;
  bytes: number;
  sha256: string;
  payloadSha256: string;
  dataRecords: number;
  noDataRecords: number;
}>;
export type ClimatePointBlock = PointAssetBase & Readonly<{
  longitudeBlock: number;
  longitudeCount: 32;
  mediaType: typeof MAIN_MEDIA_TYPE;
}>;
export type ClimateDatelineBlock = PointAssetBase & Readonly<{ mediaType: typeof DATELINE_MEDIA_TYPE }>;
export type ClimateScalarTile = Readonly<{
  field: ClimateField; zoom: number; x: number; y: number; path: string;
  bytes: typeof SCALAR_BYTES; sha256: string; mediaType: typeof SCALAR_MEDIA_TYPE;
  dataTexels: number; noDataTexels: number;
}>;
export type ClimateCatalog = Readonly<{
  version: 2;
  releaseId: string;
  period: string;
  geometry: ClimateGeometry;
  source: Readonly<{
    dataset: 'ERA5'; kind: typeof SOURCE_KIND;
    referenceFirstYear: 2006; referenceLastYear: 2025; gridDegrees: 0.25;
    localDay: typeof LOCAL_DAY; landAuthority: typeof LAND_AUTHORITY;
    landSourceSha256: string; acquisitionPlanSha256: string; logicalPlanSha256: string;
    publicationAuthorityKind: ClimateIndexSource['publicationAuthorityKind'];
    publicationAuthoritySha256: string; logicalCellCount: number; datelineCellCount: number;
    displayMaskAuthority: typeof DISPLAY_MASK_AUTHORITY; displayMaskDerivation: string;
    displayMaskSha256: string; displayMaskPath: 'display-mask.bin'; displayMaskBytes: 1038240;
  }>;
  positiveDateline: ClimateDatelineSemantics;
  probability: ClimateProbabilitySemantics;
  scalarSemantics: ClimateScalarSemantics;
  maximumZoom: number;
  pointRecordBytes: 64;
  pointBlocks: readonly ClimatePointBlock[];
  datelineOverrides: readonly ClimateDatelineBlock[];
  scalarTiles: readonly ClimateScalarTile[];
}>;
export type ClimateClientOptions = Readonly<{
  fetcher?: typeof fetch; indexCacheSize?: number; catalogCacheSize?: number; blockCacheSize?: number;
}>;

export class ClimateDataError extends Error {
  readonly code: 'unavailable' | 'invalid' | 'stale';
  constructor(code: 'unavailable' | 'invalid' | 'stale') {
    super(code === 'unavailable' ? 'Climate data is not available right now.' : code === 'stale' ? 'Climate data changed. Try again.' : 'Climate data could not be read.');
    this.name = 'ClimateDataError';
    this.code = code;
  }
}

type Pointer = Readonly<{ version: 2; indexId: string; indexSha256: string }>;
type Snapshot = Readonly<{ pointer: Pointer; index: ClimateIndex }>;
type PointLocation = Readonly<{
  latitude: number; longitude: number; latitudeIndex: number; longitudeIndex: number;
  latitudeBlock: number; longitudeBlock: number; dateline: boolean;
}>;

export class WorldwideClimateClient {
  readonly #fetcher: typeof fetch;
  readonly #indexCacheSize: number;
  readonly #catalogCacheSize: number;
  readonly #blockCacheSize: number;
  readonly #indexCache = new Map<string, ClimateIndex>();
  readonly #catalogCache = new Map<string, ClimateCatalog>();
  readonly #blockCache = new Map<string, Uint8Array>();
  readonly #indexDigests = new Map<string, string>();
  readonly #releaseDigests = new Map<string, string>();
  #activePoint?: AbortController;
  #pointGeneration = 0;

  constructor(options: ClimateClientOptions = {}) {
    this.#fetcher = options.fetcher ?? ((input, init) => globalThis.fetch(input, init));
    this.#indexCacheSize = boundedCacheSize(options.indexCacheSize, 2, 1, 4);
    this.#catalogCacheSize = boundedCacheSize(options.catalogCacheSize, 2, 1, 4);
    this.#blockCacheSize = boundedCacheSize(options.blockCacheSize, 16, 1, 64);
  }

  async loadIndex(signal?: AbortSignal): Promise<ClimateIndex> {
    try { return (await this.#loadSnapshot(signal)).index; }
    catch (error) { throw clientError(error, signal); }
  }

  async loadCatalog(period: string, signal?: AbortSignal): Promise<ClimateCatalog | undefined> {
    if (!periodID(period)) throw new ClimateDataError('invalid');
    try {
      const snapshot = await this.#loadSnapshot(signal);
      const reference = snapshot.index.months.find((month) => month.period === period);
      if (!reference) return undefined;
      const catalog = await this.#catalogFor(snapshot.index, reference, signal);
      await this.#confirmPointer(snapshot.pointer, signal);
      return catalog;
    } catch (error) { throw clientError(error, signal); }
  }

  async loadPoint(period: string, longitude: number, latitude: number, signal?: AbortSignal, allowAdjacent = false): Promise<ClimatePointEvidence | undefined> {
    if (!periodID(period)) throw new ClimateDataError('invalid');
    this.#activePoint?.abort(new ClimateDataError('stale'));
    const controller = new AbortController();
    this.#activePoint = controller;
    const generation = ++this.#pointGeneration;
    const combined = combineSignals(controller.signal, signal);
    try {
      const snapshot = await this.#loadSnapshot(combined);
      this.#assertCurrent(generation, controller, combined);
      const reference = snapshot.index.months.find((month) => month.period === period);
      if (!reference) return undefined;
      const catalog = await this.#catalogFor(snapshot.index, reference, combined);
      this.#assertCurrent(generation, controller, combined);
      const location = locatePoint(longitude, latitude);
      let evidence = await this.#recordAt(catalog, location, false, combined, generation, controller);
      if (!evidence && allowAdjacent) {
        const candidates = adjacentLocations(location).sort((left, right) => {
          const difference = geographicDistanceKm(latitude, longitude, left.latitude, left.longitude) - geographicDistanceKm(latitude, longitude, right.latitude, right.longitude);
          return Math.abs(difference) > 1e-9 ? difference : left.latitudeIndex - right.latitudeIndex || left.longitudeIndex - right.longitudeIndex;
        });
        for (const candidate of candidates) {
          evidence = await this.#recordAt(catalog, candidate, true, combined, generation, controller);
          if (evidence) break;
        }
      }
      await this.#confirmCurrent(snapshot.pointer, combined, generation, controller);
      return evidence;
    } catch (error) {
      if (signal?.aborted) throw abortReason(signal);
      if (controller.signal.aborted || generation !== this.#pointGeneration) throw new ClimateDataError('stale');
      if (error instanceof ClimateDataError) throw error;
      throw new ClimateDataError('invalid');
    } finally {
      if (this.#activePoint === controller) this.#activePoint = undefined;
    }
  }

  findScalarTile(catalog: ClimateCatalog, field: ClimateField, zoom: number, x: number, y: number): ClimateScalarTile | undefined {
    if (!FIELDS.includes(field) || !integer(zoom, 0, catalog.maximumZoom) || !integer(x, 0, 2 ** zoom - 1) || !integer(y, 0, 2 ** zoom - 1)) return undefined;
    return scalarTileIndex(catalog.scalarTiles).get(`${field}/${zoom}/${x}/${y}`);
  }

  scalarTileUrl(catalog: ClimateCatalog, tile: ClimateScalarTile): string {
    if (scalarTileIndex(catalog.scalarTiles).get(`${tile.field}/${tile.zoom}/${tile.x}/${tile.y}`) !== tile) throw new ClimateDataError('invalid');
    return climateAssetUrl(catalog.releaseId, tile.path);
  }

  async #loadSnapshot(signal?: AbortSignal): Promise<Snapshot> {
    const pointer = await this.#loadPointer(signal);
    const index = await this.#indexFor(pointer, signal);
    await this.#confirmPointer(pointer, signal);
    return { pointer, index };
  }

  async #loadPointer(signal?: AbortSignal): Promise<Pointer> {
    const data = exactObject(parseJSON(await this.#loadExactJSON(POINTER_PATH, MAX_POINTER_BYTES, signal, false)), ['version', 'indexId', 'indexSha256']);
    if (data.version !== 2 || !releaseID(data.indexId) || !sha256Value(data.indexSha256)) throw new ClimateDataError('invalid');
    return { version: 2, indexId: data.indexId, indexSha256: data.indexSha256 };
  }

  async #indexFor(pointer: Pointer, signal?: AbortSignal): Promise<ClimateIndex> {
    const key = `${pointer.indexId}:${pointer.indexSha256}`;
    const cached = touch(this.#indexCache, key);
    if (cached) return cached;
    const known = this.#indexDigests.get(pointer.indexId);
    if (known !== undefined && known !== pointer.indexSha256) throw new ClimateDataError('stale');
    const bytes = await this.#loadExactJSON(`/climate/indexes/${pointer.indexId}.json`, MAX_INDEX_BYTES, signal, true);
    if (await digestHex(bytes) !== pointer.indexSha256) throw new ClimateDataError('invalid');
    const index = parseIndex(parseJSON(bytes));
    if (index.indexId !== pointer.indexId) throw new ClimateDataError('invalid');
    rememberIdentity(this.#indexDigests, pointer.indexId, pointer.indexSha256);
    insertBounded(this.#indexCache, key, index, this.#indexCacheSize);
    return index;
  }

  async #catalogFor(index: ClimateIndex, reference: ClimateMonthReference, signal?: AbortSignal): Promise<ClimateCatalog> {
    const key = `${reference.releaseId}:${reference.catalogSha256}`;
    const cached = touch(this.#catalogCache, key);
    if (cached) { requireCatalogMatchesIndex(cached, index, reference); return cached; }
    const known = this.#releaseDigests.get(reference.releaseId);
    if (known !== undefined && known !== reference.catalogSha256) throw new ClimateDataError('stale');
    const bytes = await this.#loadExactJSON(`/climate/releases/${reference.releaseId}/catalog.json`, MAX_CATALOG_BYTES, signal, true);
    if (await digestHex(bytes) !== reference.catalogSha256) throw new ClimateDataError('invalid');
    const catalog = parseCatalog(parseJSON(bytes));
    requireCatalogMatchesIndex(catalog, index, reference);
    rememberIdentity(this.#releaseDigests, reference.releaseId, reference.catalogSha256);
    insertBounded(this.#catalogCache, key, catalog, this.#catalogCacheSize);
    return catalog;
  }

  async #loadExactJSON(path: string, maximumBytes: number, signal: AbortSignal | undefined, immutable: boolean): Promise<Uint8Array> {
    let response: Response;
    try { response = await this.#fetcher(path, { signal, cache: immutable ? 'force-cache' : 'no-cache' }); }
    catch { if (signal?.aborted) throw abortReason(signal); throw new ClimateDataError('unavailable'); }
    if (response.status === 404 || response.status === 409 || response.status === 410) throw new ClimateDataError(immutable ? 'stale' : 'unavailable');
    if (!response.ok) throw new ClimateDataError('unavailable');
    if (!jsonMediaType(response)) throw new ClimateDataError('invalid');
    try {
      const bytes = await readBoundedResponse(response, 'climate JSON', maximumBytes);
      assertNotAborted(signal);
      if (bytes.byteLength < 1) throw new ClimateDataError('invalid');
      return bytes;
    } catch (error) { throw clientError(error, signal); }
  }

  async #loadBlock(url: string, asset: ClimatePointBlock | ClimateDatelineBlock, signal: AbortSignal): Promise<Uint8Array> {
    let response: Response;
    try { response = await this.#fetcher(url, { signal, headers: { Range: `bytes=0-${asset.bytes - 1}` } }); }
    catch { if (signal.aborted) throw abortReason(signal); throw new ClimateDataError('unavailable'); }
    if (response.status === 404 || response.status === 409 || response.status === 410) throw new ClimateDataError('stale');
    if (response.status !== 206 || response.headers.get('Content-Type')?.trim().toLowerCase() !== asset.mediaType) throw new ClimateDataError('invalid');
    if (response.headers.get('Content-Range') !== `bytes 0-${asset.bytes - 1}/${asset.bytes}`) throw new ClimateDataError('invalid');
    let bytes: Uint8Array;
    try { bytes = await readBoundedResponse(response, 'ERA5 climate point block', asset.bytes); }
    catch { throw new ClimateDataError('invalid'); }
    if (bytes.byteLength !== asset.bytes || await digestHex(bytes) !== asset.sha256) throw new ClimateDataError('invalid');
    parseHeader(bytes.subarray(0, HEADER_BYTES), asset);
    if (await digestHex(bytes.subarray(HEADER_BYTES)) !== asset.payloadSha256) throw new ClimateDataError('invalid');
    return bytes;
  }

  async #recordAt(catalog: ClimateCatalog, location: PointLocation, usedAdjacentCell: boolean, signal: AbortSignal, generation: number, controller: AbortController): Promise<ClimatePointEvidence | undefined> {
    this.#assertCurrent(generation, controller, signal);
    const asset = location.dateline
      ? catalog.datelineOverrides.find((candidate) => candidate.latitudeBlock === location.latitudeBlock)
      : catalog.pointBlocks.find((candidate) => candidate.latitudeBlock === location.latitudeBlock && candidate.longitudeBlock === location.longitudeBlock);
    if (!asset) return undefined;
    const url = climateAssetUrl(catalog.releaseId, asset.path);
    const key = `${url}:${asset.sha256}`;
    let bytes = touch(this.#blockCache, key);
    if (!bytes) {
      bytes = await this.#loadBlock(url, asset, signal);
      this.#assertCurrent(generation, controller, signal);
      insertBounded(this.#blockCache, key, bytes, this.#blockCacheSize);
    }
    const localRow = location.latitudeIndex - asset.latitudeBlock * BLOCK_ROWS;
    if (localRow < 0 || localRow >= asset.latitudeCount) throw new ClimateDataError('invalid');
    const localColumn = location.dateline ? 0 : location.longitudeIndex - (asset as ClimatePointBlock).longitudeBlock * BLOCK_COLUMNS;
    if (localColumn < 0 || localColumn >= (location.dateline ? 1 : BLOCK_COLUMNS)) throw new ClimateDataError('invalid');
    const start = HEADER_BYTES + (localRow * (location.dateline ? 1 : BLOCK_COLUMNS) + localColumn) * RECORD_BYTES;
    if (start + RECORD_BYTES > asset.bytes) throw new ClimateDataError('invalid');
    return decodeRecord(bytes.subarray(start, start + RECORD_BYTES), catalog.releaseId, catalog.period, location, usedAdjacentCell);
  }

  #assertCurrent(generation: number, controller: AbortController, signal: AbortSignal): void {
    if (signal.aborted && controller.signal.aborted) throw new ClimateDataError('stale');
    if (signal.aborted) throw abortReason(signal);
    if (generation !== this.#pointGeneration || controller.signal.aborted) throw new ClimateDataError('stale');
  }
  async #confirmPointer(pointer: Pointer, signal?: AbortSignal): Promise<void> {
    if (!samePointer(pointer, await this.#loadPointer(signal))) throw new ClimateDataError('stale');
  }
  async #confirmCurrent(pointer: Pointer, signal: AbortSignal, generation: number, controller: AbortController): Promise<void> {
    await this.#confirmPointer(pointer, signal);
    this.#assertCurrent(generation, controller, signal);
  }
}

function parseIndex(value: unknown): ClimateIndex {
  const data = exactObject(value, ['version', 'indexId', 'state', 'referencePeriod', 'geometry', 'source', 'positiveDateline', 'probability', 'scalarSemantics', 'maximumZoom', 'pointRecordBytes', 'months']);
  if (data.version !== 2 || !releaseID(data.indexId) || data.state !== 'complete' || !integer(data.maximumZoom, 0, 4) || data.pointRecordBytes !== RECORD_BYTES) throw new ClimateDataError('invalid');
  const referencePeriod = parseReferencePeriod(data.referencePeriod);
  const geometry = parseGeometry(data.geometry);
  const source = parseIndexSource(data.source);
  const positiveDateline = parseDateline(data.positiveDateline);
  const probability = parseProbability(data.probability);
  const scalarSemantics = parseScalarSemantics(data.scalarSemantics);
  if (!Array.isArray(data.months) || data.months.length !== 12) throw new ClimateDataError('invalid');
  const months = data.months.map(parseMonthReference);
  for (let index = 0; index < months.length; index++) {
    if (index > 0 && months[index - 1].period >= months[index].period) throw new ClimateDataError('invalid');
    if (months[index].period !== `month-${String(index + 1).padStart(2, '0')}`) throw new ClimateDataError('invalid');
  }
  return deepFreeze({ version: 2 as const, indexId: data.indexId, state: data.state, referencePeriod, geometry, source, positiveDateline, probability, scalarSemantics, maximumZoom: data.maximumZoom, pointRecordBytes: 64 as const, months });
}

function parseCatalog(value: unknown): ClimateCatalog {
  const data = exactObject(value, ['version', 'releaseId', 'period', 'geometry', 'source', 'positiveDateline', 'probability', 'scalarSemantics', 'maximumZoom', 'pointRecordBytes', 'pointBlocks', 'datelineOverrides', 'scalarTiles']);
  if (data.version !== 2 || !releaseID(data.releaseId) || !periodID(data.period) || !integer(data.maximumZoom, 0, 4) || data.pointRecordBytes !== RECORD_BYTES) throw new ClimateDataError('invalid');
  const geometry = parseGeometry(data.geometry);
  const source = parseCatalogSource(data.source);
  const positiveDateline = parseDateline(data.positiveDateline);
  const probability = parseProbability(data.probability);
  const scalarSemantics = parseScalarSemantics(data.scalarSemantics);
  if (!Array.isArray(data.pointBlocks) || data.pointBlocks.length < 1 || data.pointBlocks.length > MAX_POINT_BLOCKS || !Array.isArray(data.datelineOverrides) || data.datelineOverrides.length > MAX_DATELINE_BLOCKS || !Array.isArray(data.scalarTiles) || data.scalarTiles.length < 1 || data.scalarTiles.length > MAX_SCALAR_TILES) throw new ClimateDataError('invalid');
  const pointBlocks = data.pointBlocks.map((item: unknown) => parsePointBlock(item, data.period));
  const datelineOverrides = data.datelineOverrides.map((item: unknown) => parseDatelineBlock(item, data.period));
  validatePointOrder(pointBlocks, datelineOverrides);
  if (pointBlocks.reduce((sum, block) => sum + block.dataRecords, 0) + datelineOverrides.reduce((sum, block) => sum + block.dataRecords, 0) !== source.logicalCellCount || datelineOverrides.reduce((sum, block) => sum + block.dataRecords, 0) !== source.datelineCellCount) throw new ClimateDataError('invalid');
  const scalarTiles = data.scalarTiles.map((item: unknown) => parseScalarTile(item, data.period, data.maximumZoom));
  validateScalarTiles(scalarTiles, data.maximumZoom);
  return deepFreeze({ version: 2 as const, releaseId: data.releaseId, period: data.period, geometry, source, positiveDateline, probability, scalarSemantics, maximumZoom: data.maximumZoom, pointRecordBytes: 64 as const, pointBlocks, datelineOverrides, scalarTiles });
}

function parseReferencePeriod(value: unknown): ClimateReferencePeriod {
  const data = exactObject(value, ['firstYear', 'lastYear']);
  if (data.firstYear !== 2006 || data.lastYear !== 2025) throw new ClimateDataError('invalid');
  return { firstYear: 2006, lastYear: 2025 };
}
function parseGeometry(value: unknown): ClimateGeometry {
  const data = exactObject(value, ['projection', 'west', 'east', 'south', 'north', 'rows', 'columns', 'cellResolutionDegrees', 'cellRegistration']);
  if (data.projection !== 'EPSG:4326' || data.west !== -180 || data.east !== 180 || data.south !== -90 || data.north !== 90 || data.rows !== ROWS || data.columns !== COLUMNS || data.cellResolutionDegrees !== GRID_DEGREES || data.cellRegistration !== 'exact-era5-grid-point-centres') throw new ClimateDataError('invalid');
  return data as ClimateGeometry;
}
function parseIndexSource(value: unknown): ClimateIndexSource {
  const data = exactObject(value, ['dataset', 'kind', 'gridDegrees', 'localDay', 'landAuthority', 'landSourceSha256', 'publicationAuthorityKind', 'logicalCellCount', 'datelineCellCount', 'displayMaskAuthority', 'displayMaskDerivation', 'displayMaskSha256', 'displayMaskBytes']);
  validateSharedSource(data);
  return data as ClimateIndexSource;
}
function parseCatalogSource(value: unknown): ClimateCatalog['source'] {
  const data = exactObject(value, ['dataset', 'kind', 'referenceFirstYear', 'referenceLastYear', 'gridDegrees', 'localDay', 'landAuthority', 'landSourceSha256', 'acquisitionPlanSha256', 'logicalPlanSha256', 'publicationAuthorityKind', 'publicationAuthoritySha256', 'logicalCellCount', 'datelineCellCount', 'displayMaskAuthority', 'displayMaskDerivation', 'displayMaskSha256', 'displayMaskPath', 'displayMaskBytes']);
  validateSharedSource(data);
  if (data.referenceFirstYear !== 2006 || data.referenceLastYear !== 2025 || !trustedSHA(data.acquisitionPlanSha256) || !trustedSHA(data.logicalPlanSha256) || !trustedSHA(data.publicationAuthoritySha256) || !integer(data.logicalCellCount, 1, ROWS * COLUMNS + ROWS) || !integer(data.datelineCellCount, 0, ROWS) || data.displayMaskPath !== 'display-mask.bin') throw new ClimateDataError('invalid');
  if (data.publicationAuthorityKind === 'checksum-trusted-acquisition-plan' ? data.publicationAuthoritySha256 !== data.acquisitionPlanSha256 : data.publicationAuthoritySha256 === data.acquisitionPlanSha256) throw new ClimateDataError('invalid');
  return data as ClimateCatalog['source'];
}
function validateSharedSource(data: Record<string, any>): void {
  if (data.dataset !== 'ERA5' || data.kind !== SOURCE_KIND || data.gridDegrees !== GRID_DEGREES || data.localDay !== LOCAL_DAY || data.landAuthority !== LAND_AUTHORITY || !trustedSHA(data.landSourceSha256) || (data.publicationAuthorityKind !== 'checksum-trusted-acquisition-plan' && data.publicationAuthorityKind !== 'checksum-trusted-publication-authority-v1') || !integer(data.logicalCellCount, 1, ROWS * COLUMNS + ROWS) || !integer(data.datelineCellCount, 0, ROWS) || data.displayMaskAuthority !== DISPLAY_MASK_AUTHORITY || typeof data.displayMaskDerivation !== 'string' || data.displayMaskDerivation.length < 1 || !trustedSHA(data.displayMaskSha256) || data.displayMaskBytes !== ROWS * COLUMNS) throw new ClimateDataError('invalid');
}
function parseDateline(value: unknown): ClimateDatelineSemantics {
  const data = exactObject(value, ['longitude', 'meaning', 'canonicalColumns', 'overrideRecordBytes']);
  if (data.longitude !== 180 || data.meaning !== DATELINE_MEANING || data.canonicalColumns !== COLUMNS || data.overrideRecordBytes !== RECORD_BYTES) throw new ClimateDataError('invalid');
  return data as ClimateDatelineSemantics;
}
function parseProbability(value: unknown): ClimateProbabilitySemantics {
  const data = exactObject(value, ['unit', 'denominator', 'threshold', 'goodMinimumScore', 'noDataValue', 'fields']);
  if (data.unit !== 'whole-percent' || data.denominator !== 'complete-real-days-in-reference-month' || data.threshold !== 'canonical-rating-good-or-better' || data.goodMinimumScore !== 55 || data.noDataValue !== 255 || !sameArray(data.fields, FIELDS)) throw new ClimateDataError('invalid');
  return {
    unit: 'whole-percent', denominator: 'complete-real-days-in-reference-month',
    threshold: 'canonical-rating-good-or-better', goodMinimumScore: 55,
    noDataValue: 255, fields: [...FIELDS],
  };
}
function parseScalarSemantics(value: unknown): ClimateScalarSemantics {
  const data = exactObject(value, ['maximumZoomMeaning', 'parentAggregation']);
  if (data.maximumZoomMeaning !== MAXIMUM_ZOOM_MEANING || data.parentAggregation !== PARENT_AGGREGATION) throw new ClimateDataError('invalid');
  return data as ClimateScalarSemantics;
}
function parseMonthReference(value: unknown): ClimateMonthReference {
  const data = exactObject(value, ['period', 'releaseId', 'catalogSha256']);
  if (!periodID(data.period) || !releaseID(data.releaseId) || !sha256Value(data.catalogSha256)) throw new ClimateDataError('invalid');
  return data as ClimateMonthReference;
}

function parsePointBlock(value: unknown, period: string): ClimatePointBlock {
  const data = exactObject(value, ['latitudeBlock', 'longitudeBlock', 'latitudeCount', 'longitudeCount', 'path', 'bytes', 'sha256', 'payloadSha256', 'mediaType', 'dataRecords', 'noDataRecords']);
  if (!integer(data.latitudeBlock, 0, 45) || !integer(data.longitudeBlock, 0, 44) || data.latitudeCount !== blockLength(data.latitudeBlock, ROWS, BLOCK_ROWS) || data.longitudeCount !== BLOCK_COLUMNS || data.path !== `points/${period}/main/${pad3(data.latitudeBlock)}/${pad3(data.longitudeBlock)}.nwc` || data.bytes !== MAIN_BLOCK_BYTES || !sha256Value(data.sha256) || !sha256Value(data.payloadSha256) || data.mediaType !== MAIN_MEDIA_TYPE || !integer(data.dataRecords, 1, BLOCK_ROWS * BLOCK_COLUMNS) || !integer(data.noDataRecords, 0, BLOCK_ROWS * BLOCK_COLUMNS) || data.dataRecords + data.noDataRecords !== BLOCK_ROWS * BLOCK_COLUMNS) throw new ClimateDataError('invalid');
  return data as ClimatePointBlock;
}
function parseDatelineBlock(value: unknown, period: string): ClimateDatelineBlock {
  const data = exactObject(value, ['latitudeBlock', 'latitudeCount', 'path', 'bytes', 'sha256', 'payloadSha256', 'mediaType', 'dataRecords', 'noDataRecords']);
  if (!integer(data.latitudeBlock, 0, 45) || data.latitudeCount !== blockLength(data.latitudeBlock, ROWS, BLOCK_ROWS) || data.path !== `points/${period}/dateline/${pad3(data.latitudeBlock)}.nwd` || data.bytes !== DATELINE_BLOCK_BYTES || !sha256Value(data.sha256) || !sha256Value(data.payloadSha256) || data.mediaType !== DATELINE_MEDIA_TYPE || !integer(data.dataRecords, 1, BLOCK_ROWS) || !integer(data.noDataRecords, 0, BLOCK_ROWS) || data.dataRecords + data.noDataRecords !== BLOCK_ROWS) throw new ClimateDataError('invalid');
  return data as ClimateDatelineBlock;
}
function parseScalarTile(value: unknown, period: string, maximumZoom: number): ClimateScalarTile {
  const data = exactObject(value, ['field', 'zoom', 'x', 'y', 'path', 'bytes', 'sha256', 'mediaType', 'dataTexels', 'noDataTexels']);
  if (!FIELDS.includes(data.field) || !integer(data.zoom, 0, maximumZoom)) throw new ClimateDataError('invalid');
  const maximum = 2 ** data.zoom - 1;
  if (!integer(data.x, 0, maximum) || !integer(data.y, 0, maximum) || data.path !== `scores/${period}/${data.field}/${data.zoom}/${data.x}/${data.y}.nws` || data.bytes !== SCALAR_BYTES || !sha256Value(data.sha256) || data.mediaType !== SCALAR_MEDIA_TYPE || !integer(data.dataTexels, 1, SCALAR_BYTES) || !integer(data.noDataTexels, 0, SCALAR_BYTES) || data.dataTexels + data.noDataTexels !== SCALAR_BYTES) throw new ClimateDataError('invalid');
  return data as ClimateScalarTile;
}
function validatePointOrder(main: readonly ClimatePointBlock[], dateline: readonly ClimateDatelineBlock[]): void {
  let previous = -1;
  for (const block of main) { const rank = block.latitudeBlock * 45 + block.longitudeBlock; if (rank <= previous) throw new ClimateDataError('invalid'); previous = rank; }
  previous = -1;
  for (const block of dateline) { if (block.latitudeBlock <= previous) throw new ClimateDataError('invalid'); previous = block.latitudeBlock; }
}
function validateScalarTiles(tiles: readonly ClimateScalarTile[], maximumZoom: number): void {
  const coverage = new Map<ClimateField, Set<string>>(FIELDS.map((field) => [field, new Set()]));
  let previous = '';
  for (const tile of tiles) {
    const rank = `${String(FIELDS.indexOf(tile.field)).padStart(2, '0')}/${tile.zoom}/${String(tile.y).padStart(10, '0')}/${String(tile.x).padStart(10, '0')}`;
    if (rank <= previous) throw new ClimateDataError('invalid'); previous = rank;
    const set = coverage.get(tile.field)!; const key = tileKey(tile.zoom, tile.x, tile.y);
    if (set.has(key)) throw new ClimateDataError('invalid'); set.add(key);
  }
  const baseline = coverage.get(FIELDS[0])!;
  if (!baseline.has('0/0/0')) throw new ClimateDataError('invalid');
  for (let zoom = 0; zoom <= maximumZoom; zoom++) if (![...baseline].some((key) => key.startsWith(`${zoom}/`))) throw new ClimateDataError('invalid');
  for (const field of FIELDS.slice(1)) if (!sameSet(baseline, coverage.get(field)!)) throw new ClimateDataError('invalid');
  for (const key of baseline) {
    const [zoom, x, y] = key.split('/').map(Number);
    if (zoom > 0 && !baseline.has(tileKey(zoom - 1, Math.floor(x / 2), Math.floor(y / 2)))) throw new ClimateDataError('invalid');
    if (zoom < maximumZoom && ![0, 1].some((dy) => [0, 1].some((dx) => baseline.has(tileKey(zoom + 1, x * 2 + dx, y * 2 + dy))))) throw new ClimateDataError('invalid');
  }
}

function locatePoint(longitude: number, latitude: number): PointLocation {
  if (!Number.isFinite(longitude) || !Number.isFinite(latitude) || latitude < -90 || latitude > 90) throw new ClimateDataError('invalid');
  const latitudeIndex = clamp(Math.round((90 - latitude) / GRID_DEGREES), 0, ROWS - 1);
  const normalized = wrapLongitude(longitude);
  const positiveDateline = normalized === -180 && longitude > 0 || normalized >= 179.875;
  const longitudeIndex = positiveDateline ? COLUMNS : clamp(Math.round((normalized + 180) / GRID_DEGREES), 0, COLUMNS - 1);
  return pointLocation(latitudeIndex, longitudeIndex);
}
function pointLocation(latitudeIndex: number, longitudeIndex: number): PointLocation {
  const dateline = longitudeIndex === COLUMNS;
  return {
    latitude: cleanCoordinate(90 - latitudeIndex * GRID_DEGREES),
    longitude: dateline ? 180 : cleanCoordinate(-180 + longitudeIndex * GRID_DEGREES),
    latitudeIndex, longitudeIndex,
    latitudeBlock: Math.floor(latitudeIndex / BLOCK_ROWS),
    longitudeBlock: dateline ? 0 : Math.floor(longitudeIndex / BLOCK_COLUMNS),
    dateline,
  };
}
function adjacentLocations(location: PointLocation): PointLocation[] {
  const result: PointLocation[] = [];
  for (let rowOffset = -1; rowOffset <= 1; rowOffset++) for (let columnOffset = -1; columnOffset <= 1; columnOffset++) {
    if (rowOffset === 0 && columnOffset === 0) continue;
    const row = location.latitudeIndex + rowOffset; const column = location.longitudeIndex + columnOffset;
    if (row >= 0 && row < ROWS && column >= 0 && column <= COLUMNS) result.push(pointLocation(row, column));
  }
  return result;
}

function parseHeader(bytes: Uint8Array, asset: ClimatePointBlock | ClimateDatelineBlock): void {
  const dateline = !('longitudeBlock' in asset);
  if (bytes.byteLength !== HEADER_BYTES || ascii(bytes.subarray(0, 8)) !== (dateline ? 'NWE5CDL1' : 'NWE5CPT1')) throw new ClimateDataError('invalid');
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  if (view.getUint16(8, true) !== 1 || view.getUint16(10, true) !== HEADER_BYTES || view.getUint16(12, true) !== RECORD_BYTES || view.getUint16(14, true) !== BLOCK_ROWS || view.getUint16(16, true) !== (dateline ? 1 : BLOCK_COLUMNS) || !allZero(bytes.subarray(18, 20)) || view.getUint32(20, true) !== asset.latitudeBlock || view.getUint32(24, true) !== (dateline ? 0 : asset.longitudeBlock) || !allZero(bytes.subarray(28, 32)) || hex(bytes.subarray(32, 64)) !== asset.payloadSha256) throw new ClimateDataError('invalid');
}
function decodeRecord(bytes: Uint8Array, releaseId: string, period: string, location: PointLocation, usedAdjacentCell: boolean): ClimatePointEvidence | undefined {
  if (bytes.byteLength !== RECORD_BYTES || (bytes[0] & ~1) !== 0) throw new ClimateDataError('invalid');
  if (bytes[0] === 0) { if (!allZero(bytes.subarray(1))) throw new ClimateDataError('invalid'); return undefined; }
  if (bytes[1] > 100 || bytes[2] > 100 || bytes[3] > 100 || bytes[63] !== 0) throw new ClimateDataError('invalid');
  const nice = decodeProfile(bytes.subarray(4, 20));
  const cycling = decodeProfile(bytes.subarray(20, 36));
  if (cycling.calmGoodPercent > nice.calmGoodPercent) throw new ClimateDataError('invalid');
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const physical: ClimatePhysicalEvidence = {
    daytimeTemperatureP10C: view.getInt16(36, true) / 10,
    daytimeTemperatureP50C: view.getInt16(38, true) / 10,
    daytimeTemperatureP90C: view.getInt16(40, true) / 10,
    daytimeMinimumP50C: view.getInt16(42, true) / 10,
    daytimeMaximumP50C: view.getInt16(44, true) / 10,
    dewPointP50C: view.getInt16(46, true) / 10,
    dewPointP90C: view.getInt16(48, true) / 10,
    rainP90MM: view.getUint16(50, true) / 10,
    windP50KPH: view.getUint16(52, true) / 10,
    windP90KPH: view.getUint16(54, true) / 10,
    relativeHumidityP50Percent: bytes[56], muggyDayPercent: bytes[57], rainFreeDayPercent: bytes[58],
    cloudCoverP50Percent: bytes[59], clearSkyDayPercent: bytes[60], days: view.getUint16(61, true),
  };
  if (physical.daytimeTemperatureP10C > physical.daytimeTemperatureP50C || physical.daytimeTemperatureP50C > physical.daytimeTemperatureP90C || physical.daytimeMinimumP50C > physical.daytimeTemperatureP50C || physical.daytimeTemperatureP50C > physical.daytimeMaximumP50C || physical.dewPointP50C > physical.dewPointP90C || physical.windP50KPH > physical.windP90KPH || physical.relativeHumidityP50Percent > 100 || physical.muggyDayPercent > 100 || physical.rainFreeDayPercent > 100 || physical.cloudCoverP50Percent > 100 || physical.clearSkyDayPercent > 100 || physical.days < 1) throw new ClimateDataError('invalid');
  return deepFreeze({ releaseId, period, sourceCell: { latitude: location.latitude, longitude: location.longitude }, usedAdjacentCell, comfortGoodPercent: bytes[1], dryGoodPercent: bytes[2], clearSkyGoodPercent: bytes[3], nice, cycling, physical });
}
function decodeProfile(bytes: Uint8Array): ClimateProfileEvidence {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const ratingShares = { awful: view.getUint16(4, true), poor: view.getUint16(6, true), mixed: view.getUint16(8, true), good: view.getUint16(10, true), veryGood: view.getUint16(12, true), excellent: view.getUint16(14, true) };
  const medianScore = bytes[0]; const calmGoodPercent = bytes[1]; const poorWeekPermille = view.getUint16(2, true);
  const shares = Object.values(ratingShares);
  if (medianScore > 100 || calmGoodPercent > 100 || poorWeekPermille > 1000 || shares.some((value) => value > 1000) || shares.reduce((sum, value) => sum + value, 0) !== 1000) throw new ClimateDataError('invalid');
  const goodOrBetterPermille = ratingShares.good + ratingShares.veryGood + ratingShares.excellent;
  return { medianScore, calmGoodPercent, poorWeekPermille, ratingShares, goodOrBetterPermille, goodOrBetterPercent: Math.floor((goodOrBetterPermille + 5) / 10), awfulPermille: ratingShares.awful, awfulPercent: Math.floor((ratingShares.awful + 5) / 10) };
}

function requireCatalogMatchesIndex(catalog: ClimateCatalog, index: ClimateIndex, reference: ClimateMonthReference): void {
  if (catalog.releaseId !== reference.releaseId || catalog.period !== reference.period || catalog.source.referenceFirstYear !== index.referencePeriod.firstYear || catalog.source.referenceLastYear !== index.referencePeriod.lastYear || !sameSharedSource(catalog.source, index.source) || catalog.maximumZoom !== index.maximumZoom || catalog.pointRecordBytes !== index.pointRecordBytes) throw new ClimateDataError('invalid');
}
function sameSharedSource(catalog: ClimateCatalog['source'], index: ClimateIndexSource): boolean {
  return catalog.dataset === index.dataset && catalog.kind === index.kind && catalog.gridDegrees === index.gridDegrees && catalog.localDay === index.localDay && catalog.landAuthority === index.landAuthority && catalog.landSourceSha256 === index.landSourceSha256 && catalog.publicationAuthorityKind === index.publicationAuthorityKind && catalog.logicalCellCount === index.logicalCellCount && catalog.datelineCellCount === index.datelineCellCount && catalog.displayMaskAuthority === index.displayMaskAuthority && catalog.displayMaskDerivation === index.displayMaskDerivation && catalog.displayMaskSha256 === index.displayMaskSha256 && catalog.displayMaskBytes === index.displayMaskBytes;
}
function scalarTileIndex(tiles: readonly ClimateScalarTile[]): ReadonlyMap<string, ClimateScalarTile> {
  let index = scalarTileIndexes.get(tiles);
  if (!index) { index = new Map(tiles.map((tile) => [`${tile.field}/${tile.zoom}/${tile.x}/${tile.y}`, tile])); scalarTileIndexes.set(tiles, index); }
  return index;
}
function exactObject(value: unknown, keys: readonly string[]): Record<string, any> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new ClimateDataError('invalid');
  const object = value as Record<string, any>; const actual = Object.keys(object).sort(); const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) throw new ClimateDataError('invalid');
  return object;
}
function parseJSON(bytes: Uint8Array): unknown { try { return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)); } catch { throw new ClimateDataError('invalid'); } }
function climateAssetUrl(release: string, path: string): string { if (!releaseID(release) || !safeAssetPath(path)) throw new ClimateDataError('invalid'); return `/climate/releases/${release}/${path}`; }
function jsonMediaType(response: Response): boolean { return /^application\/json(?:\s*;\s*charset=[A-Za-z0-9._-]+)?$/i.test(response.headers.get('Content-Type')?.trim() ?? ''); }
function safeAssetPath(value: unknown): value is string { return typeof value === 'string' && value.length > 0 && !value.includes('\\') && !value.includes('\0') && !value.startsWith('/') && value.split('/').every((part) => part !== '' && part !== '.' && part !== '..'); }
function releaseID(value: unknown): value is string { return typeof value === 'string' && value.length <= 64 && /^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(value); }
function periodID(value: unknown): value is string { return typeof value === 'string' && /^month-(0[1-9]|1[0-2])$/.test(value); }
function sha256Value(value: unknown): value is string { return typeof value === 'string' && /^[a-f0-9]{64}$/.test(value); }
function trustedSHA(value: unknown): value is string { return sha256Value(value) && /[1-9a-f]/.test(value); }
function integer(value: unknown, minimum: number, maximum: number): value is number { return typeof value === 'number' && Number.isSafeInteger(value) && value >= minimum && value <= maximum; }
function blockLength(block: number, count: number, size: number): number { return Math.min(size, count - block * size); }
function tileKey(zoom: number, x: number, y: number): string { return `${zoom}/${x}/${y}`; }
function pad3(value: number): string { return String(value).padStart(3, '0'); }
function clamp(value: number, minimum: number, maximum: number): number { return Math.min(maximum, Math.max(minimum, value)); }
function allZero(bytes: Uint8Array): boolean { return bytes.every((value) => value === 0); }
function hex(bytes: Uint8Array): string { return Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join(''); }
function ascii(bytes: Uint8Array): string { if (bytes.some((value) => value > 0x7f)) throw new ClimateDataError('invalid'); return String.fromCharCode(...bytes); }
function cleanCoordinate(value: number): number { return Object.is(value, -0) ? 0 : Number(value.toFixed(10)); }
function wrapLongitude(longitude: number): number { return ((longitude + 180) % 360 + 360) % 360 - 180; }
function geographicDistanceKm(latitudeA: number, longitudeA: number, latitudeB: number, longitudeB: number): number { const radians = Math.PI / 180; const latitudeDelta = (latitudeB - latitudeA) * radians; const longitudeDelta = wrapLongitude(longitudeB - longitudeA) * radians; const a = Math.sin(latitudeDelta / 2) ** 2 + Math.cos(latitudeA * radians) * Math.cos(latitudeB * radians) * Math.sin(longitudeDelta / 2) ** 2; return 2 * 6371.0088 * Math.asin(Math.min(1, Math.sqrt(a))); }
function samePointer(left: Pointer, right: Pointer): boolean { return left.version === right.version && left.indexId === right.indexId && left.indexSha256 === right.indexSha256; }
function sameArray(left: unknown, right: readonly unknown[]): boolean { return Array.isArray(left) && left.length === right.length && left.every((item, index) => item === right[index]); }
function sameSet(left: Set<string>, right: Set<string>): boolean { return left.size === right.size && [...left].every((item) => right.has(item)); }
function rememberIdentity(map: Map<string, string>, identity: string, digest: string): void { const known = map.get(identity); if (known !== undefined && known !== digest) throw new ClimateDataError('stale'); if (known === undefined && map.size >= MAX_SESSION_IDENTITIES) throw new ClimateDataError('invalid'); map.set(identity, digest); }
async function digestHex(bytes: Uint8Array): Promise<string> { const digest = await crypto.subtle.digest('SHA-256', bytes as BufferSource); return hex(new Uint8Array(digest)); }
function combineSignals(internal: AbortSignal, external?: AbortSignal): AbortSignal { return external ? AbortSignal.any([internal, external]) : internal; }
function abortReason(signal: AbortSignal): unknown { return signal.reason ?? new DOMException('Aborted', 'AbortError'); }
function assertNotAborted(signal?: AbortSignal): void { if (signal?.aborted) throw abortReason(signal); }
function clientError(error: unknown, signal?: AbortSignal): unknown { if (signal?.aborted) return abortReason(signal); return error instanceof ClimateDataError ? error : new ClimateDataError('invalid'); }
function boundedCacheSize(value: number | undefined, fallback: number, minimum: number, maximum: number): number { if (value === undefined) return fallback; if (!integer(value, minimum, maximum)) throw new TypeError('Climate cache size is invalid.'); return value; }
function touch<K, V>(cache: Map<K, V>, key: K): V | undefined { const value = cache.get(key); if (value === undefined) return undefined; cache.delete(key); cache.set(key, value); return value; }
function insertBounded<K, V>(cache: Map<K, V>, key: K, value: V, maximum: number): void { cache.delete(key); cache.set(key, value); while (cache.size > maximum) cache.delete(cache.keys().next().value as K); }
function deepFreeze<T>(value: T): T { if (typeof value !== 'object' || value === null || Object.isFrozen(value)) return value; for (const child of Object.values(value as Record<string, unknown>)) deepFreeze(child); return Object.freeze(value); }
