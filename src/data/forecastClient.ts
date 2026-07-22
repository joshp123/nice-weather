import { readBoundedResponse } from './boundedResponse';
import type { ComponentScore, DailyPoint, ForecastWindowPoint, LegendCategory, Profile } from './types';
import type { ScalarTileID, ScalarTileSource } from '../map/scalarTileLayer';

const POINTER_PATH = '/forecast/current.json';
const POINTER_BYTES = 2_048;
const INDEX_BYTES = 64 * 1_024;
const CATALOG_BYTES = 512 * 1_024;
const SCALAR_BYTES = 258 * 258;
const DISPLAY_BYTES = 192 + 256 * 256 * 8;
const DAILY_HEADER_BYTES = 320;
const RANGE_HEADER_BYTES = 256;
const DAILY_RECORD_BYTES = 30;
const RANGE_RECORD_BYTES = 44;
const DAILY_MEDIA = 'application/vnd.nice-weather.forecast-block';
const RANGE_MEDIA = 'application/vnd.nice-weather.forecast-range-block';
const DISPLAY_MEDIA = 'application/vnd.nice-weather.display-point-tile';
const SCALAR_MEDIA = 'application/vnd.nice-weather.score-tile';
const FIELDS = ['thermal', 'dryness', 'sun', 'nice_score', 'nice_wind', 'cycling_score', 'cycling_wind'] as const;

export type ForecastField = typeof FIELDS[number];
export type ForecastTier = 'europe-detail' | 'global';
export type ForecastScalarTile = Readonly<{ field: ForecastField; zoom: number; x: number; y: number; path: string; bytes: number; sha256: string; mediaType?: string }>;
export type ForecastDisplayPoint = Readonly<{ zoom: number; x: number; y: number; path: string; bytes: number; sha256: string; payloadSha256: string; mediaType: string }>;
export type ForecastPointBlock = Readonly<{ row: number; column: number; rows: number; columns: number; omitted: boolean; path?: string; bytes?: number; sha256?: string; payloadSha256?: string; mediaType: string }>;
export type ForecastCoverage = Readonly<{ zoom: number; x: number; y: number; omitted: boolean }>;
export type ForecastLandIdentity = Readonly<{
  authority: 'Natural Earth'; dataset: 'ne_10m_land'; version: '5.1.1';
  sourceArchiveSha256: string; maskSha256: string; derivationContract: 'web-mercator-all-touched-v1';
}>;

type TierReference = Readonly<{
  date: string; provider: string; tier: ForecastTier; product: string; runUtc: string; nativeResolutionKm: number;
  sourceEvidenceSha256: string; catalogPath: string; catalogBytes: number; catalogSha256: string;
}>;
type RangeReference = Readonly<{
  period: 'next-14-days'; provider: 'ecmwf-ifs'; tier: 'global'; product: string; runUtc: string; nativeResolutionKm: 28;
  staleAfterHours: 18 | 36; catalogPath: string; catalogBytes: number; catalogSha256: string;
}>;

export type ForecastIndex = Readonly<{
  version: 2; productContract: 'nice-weather-forecast-run-v3'; indexId: string; firstDate: string; lastDate: string;
  days: readonly Readonly<{ date: string; tiers: readonly TierReference[] }>[]; range: RangeReference;
}>;

export type DailyForecastCatalog = Readonly<{
  version: 2; releaseId: string; localDate: string; provider: string; tier: ForecastTier;
  grid: Readonly<{ west: number; east: number; south: number; north: number; rows: number; columns: number }>;
  evidence: Readonly<{ product: string; runUtc: string; nativeResolutionKm: number; sourceEvidenceSha256: string }>;
  land: ForecastLandIdentity;
  scalarMaxZoom: 3 | 5; pointBlocks: readonly ForecastPointBlock[]; displayPoints: readonly ForecastDisplayPoint[];
  scalarCoverage: readonly ForecastCoverage[]; scalarTiles: readonly ForecastScalarTile[]; root: string;
}>;

export type DayForecastProduct = Readonly<{ indexId: string; date: string; tiers: readonly DailyForecastCatalog[] }>;

export type RangeForecastCatalog = Readonly<{
  version: 1; productContract: 'nice-weather-forecast-range-v1'; releaseId: string; period: 'next-14-days'; dates: readonly string[];
  source: Readonly<{ provider: 'ecmwf-ifs'; tier: 'global'; product: string; runUtc: string; nativeResolutionKm: 28; staleAfterHours: 36 }>;
  pointBlocks: readonly Readonly<{ row: number; column: number; omitted: boolean; path?: string; bytes?: number; sha256?: string; payloadSha256?: string }>[];
  scalarCoverage: readonly ForecastCoverage[]; scalarTiles: readonly ForecastScalarTile[]; root: string;
}>;

export type ForecastSnapshot = Readonly<{ index: ForecastIndex; range: RangeForecastCatalog }>;
export type ForecastPresentation = Readonly<{ legend: readonly LegendCategory[]; generatedAt: string }>;

export class ForecastDataError extends Error {
  constructor(readonly code: 'unavailable' | 'invalid' | 'stale', message?: string) {
    super(message ?? (code === 'unavailable' ? 'The forecast is not available right now.' : code === 'stale' ? 'The forecast changed. Try again.' : 'The forecast could not be read.'));
    this.name = 'ForecastDataError';
  }
}

type Pointer = Readonly<{ version: 1; current: Readonly<{ indexId: string; path: string; sha256: string }> }>;

export class WorldwideForecastClient {
  readonly #fetcher: typeof fetch;
  readonly #json = new Map<string, unknown>();
  readonly #blocks = new Map<string, Uint8Array>();
  #active?: AbortController;
  #generation = 0;

  constructor(fetcher?: typeof fetch) {
    this.#fetcher = fetcher ?? ((input, init) => globalThis.fetch(input, init));
  }

  async loadIndex(signal?: AbortSignal): Promise<ForecastIndex> { return (await this.#snapshot(signal)).index; }

  async loadRangeCatalog(signal?: AbortSignal): Promise<RangeForecastCatalog> { return (await this.#snapshot(signal)).range; }

  async loadSnapshot(signal?: AbortSignal): Promise<ForecastSnapshot> { return this.#snapshot(signal); }

  async loadDayCatalog(snapshot: ForecastSnapshot, dateValue: string, signal?: AbortSignal): Promise<DayForecastProduct> {
    const { index } = snapshot;
    const day = index.days.find(({ date }) => date === dateValue);
    if (!day) throw new ForecastDataError('invalid');
    const tiers: DailyForecastCatalog[] = [];
    for (const reference of day.tiers) tiers.push(await this.#daily(index, reference, signal));
    await this.#confirmPointerID(index.indexId, signal);
    return Object.freeze({ indexId: index.indexId, date: day.date, tiers: Object.freeze(tiers) });
  }

  async loadRangePoint(snapshot: ForecastSnapshot, longitude: number, latitude: number, profile: Profile, presentation: ForecastPresentation, signal?: AbortSignal): Promise<ForecastWindowPoint> {
    const { index, range } = snapshot;
    const location = xyzLocation(longitude, latitude, 3);
    const blockRow = Math.floor(location.globalY / 64), blockColumn = Math.floor(location.globalX / 64);
    const block = range.pointBlocks.find((item) => item.row === blockRow && item.column === blockColumn);
    if (!block || block.omitted) throw new ForecastDataError('unavailable', 'There is no complete weather record here.');
    const bytes = await this.#block(`${range.root}/${requiredPath(block.path)}`, requiredInteger(block.bytes), requiredSHA(block.sha256), RANGE_MEDIA, signal);
    validateRangeHeader(bytes, range, blockRow, blockColumn, requiredSHA(block.payloadSha256));
    const local = (location.globalY % 64) * 64 + location.globalX % 64;
    const record = bytes.subarray(RANGE_HEADER_BYTES + local * RANGE_RECORD_BYTES, RANGE_HEADER_BYTES + (local + 1) * RANGE_RECORD_BYTES);
    const result = decodeRangeRecord(record, longitude, latitude, index, range, profile, presentation);
    await this.#confirmPointerID(index.indexId, signal);
    return result;
  }

  async loadDailyPoint(product: DayForecastProduct, longitude: number, latitude: number, profile: Profile, presentation: ForecastPresentation, detailEnabled: boolean, signal?: AbortSignal): Promise<DailyPoint> {
    this.#active?.abort(new ForecastDataError('stale'));
    const active = new AbortController(); this.#active = active; const generation = ++this.#generation;
    const combined = combineSignals(active.signal, signal);
    try {
      const selected = forecastTierForPoint(product, longitude, latitude, detailEnabled);
      const display = await this.#displayRecord(selected, longitude, latitude, combined);
      if (!display) throw new ForecastDataError('invalid', 'Detailed forecast coverage is incomplete here.');
      const native = await this.#nativeRecord(selected, longitude, latitude, combined);
      if (!native) throw new ForecastDataError('invalid', 'Forecast evidence is incomplete here.');
      if (generation !== this.#generation) throw new ForecastDataError('stale');
      await this.#confirmPointerID(product.indexId, combined);
      return decodeDailyRecord(native, display, longitude, latitude, selected, profile, presentation);
    } finally { if (this.#active === active) this.#active = undefined; }
  }

  scalarSourceForOutputTile(product: RangeForecastCatalog | DayForecastProduct, field: ForecastField, output: ScalarTileID): ScalarTileSource | undefined {
    if (!FIELDS.includes(field) || !tileID(output)) return undefined;
    if ('period' in product) return scalarSource(product, product, field, output);
    const global = product.tiers.find(({ tier }) => tier === 'global');
    const detail = product.tiers.find(({ tier }) => tier === 'europe-detail');
    if (!global) throw new ForecastDataError('invalid');
    if (output.z === 5 && detail && declaredTile(detail, output)) return scalarSource(detail, detail, field, output);
    return scalarSource(global, global, field, output);
  }

  sourceZoomFor(product: RangeForecastCatalog | DayForecastProduct, mapZoom: number): number {
    if ('period' in product || !product.tiers.some(({ tier }) => tier === 'europe-detail') || mapZoom < 4) return 3;
    return 5;
  }

  async #snapshot(signal?: AbortSignal): Promise<ForecastSnapshot> {
    const pointer = await this.#pointer(signal); const index = await this.#index(pointer, signal); const range = await this.#range(index, signal);
    await this.#confirm(pointer, signal); return Object.freeze({ index, range });
  }

  async #pointer(signal?: AbortSignal): Promise<Pointer> {
    const value = parseJSON(await this.#jsonBytes(POINTER_PATH, POINTER_BYTES, false, signal));
    const row = exact(value, ['version', 'current', 'previous'], ['previous']);
    if (row.version !== 1) throw new ForecastDataError('invalid');
    const current = exact(row.current, ['indexId', 'path', 'sha256']);
    const indexId = identifier(current.indexId), path = safePath(current.path), sha256 = sha(current.sha256);
    if (path !== `runs/${indexId}/index.json`) throw new ForecastDataError('invalid');
    return Object.freeze({ version: 1, current: Object.freeze({ indexId, path, sha256 }) });
  }

  async #index(pointer: Pointer, signal?: AbortSignal): Promise<ForecastIndex> {
    const key = `${pointer.current.path}:${pointer.current.sha256}`;
    const cached = this.#json.get(key) as ForecastIndex | undefined; if (cached) { touch(this.#json, key, cached, 8); return cached; }
    const bytes = await this.#jsonBytes(`/forecast/${pointer.current.path}`, INDEX_BYTES, true, signal);
    if (await digest(bytes) !== pointer.current.sha256) throw new ForecastDataError('invalid');
    const index = parseIndex(parseJSON(bytes), pointer.current.indexId); insert(this.#json, key, index, 8); return index;
  }

  async #range(index: ForecastIndex, signal?: AbortSignal): Promise<RangeForecastCatalog> {
    const reference = index.range, key = `${reference.catalogPath}:${reference.catalogSha256}`;
    const cached = this.#json.get(key) as RangeForecastCatalog | undefined; if (cached) { touch(this.#json, key, cached, 8); return cached; }
    const bytes = await this.#jsonBytes(`/forecast/${reference.catalogPath}`, reference.catalogBytes, true, signal);
    if (bytes.byteLength !== reference.catalogBytes || await digest(bytes) !== reference.catalogSha256) throw new ForecastDataError('invalid');
    const root = `/forecast/${reference.catalogPath.slice(0, reference.catalogPath.lastIndexOf('/'))}`;
    const range = parseRange(parseJSON(bytes), index, root); insert(this.#json, key, range, 8); return range;
  }

  async #daily(index: ForecastIndex, reference: TierReference, signal?: AbortSignal): Promise<DailyForecastCatalog> {
    const key = `${reference.catalogPath}:${reference.catalogSha256}`;
    const cached = this.#json.get(key) as DailyForecastCatalog | undefined; if (cached) { touch(this.#json, key, cached, 24); return cached; }
    const bytes = await this.#jsonBytes(`/forecast/${reference.catalogPath}`, reference.catalogBytes, true, signal);
    if (bytes.byteLength !== reference.catalogBytes || await digest(bytes) !== reference.catalogSha256) throw new ForecastDataError('invalid');
    const root = `/forecast/${reference.catalogPath.slice(0, reference.catalogPath.lastIndexOf('/'))}`;
    const catalog = parseDaily(parseJSON(bytes), index, reference, root); insert(this.#json, key, catalog, 24); return catalog;
  }

  async #displayRecord(catalog: DailyForecastCatalog, lon: number, lat: number, signal?: AbortSignal): Promise<Uint8Array | undefined> {
    const location = xyzLocation(lon, lat, catalog.scalarMaxZoom);
    const asset = catalog.displayPoints.find(({ zoom, x, y }) => zoom === location.id.z && x === location.id.x && y === location.id.y);
    if (!asset) return undefined;
    const bytes = await this.#block(`${catalog.root}/${asset.path}`, asset.bytes, asset.sha256, DISPLAY_MEDIA, signal);
    validateDisplayHeader(bytes, catalog, location.id, asset.payloadSha256);
    const start = 192 + (location.row * 256 + location.column) * 8;
    const record = bytes.subarray(start, start + 8);
    if (record[7] === 0) return undefined;
    if (record[7] !== 1 || record.subarray(0, 7).some((value) => value > 100)) throw new ForecastDataError('invalid');
    return record;
  }

  async #nativeRecord(catalog: DailyForecastCatalog, lon: number, lat: number, signal?: AbortSignal): Promise<Uint8Array | undefined> {
    const location = nativeLocation(catalog, lon, lat); if (!location) return undefined;
    const blockRow = Math.floor(location.row / 64), blockColumn = Math.floor(location.column / 64);
    const block = catalog.pointBlocks.find((item) => item.row === blockRow && item.column === blockColumn);
    if (!block || block.omitted) return undefined;
    const bytes = await this.#block(`${catalog.root}/${requiredPath(block.path)}`, requiredInteger(block.bytes), requiredSHA(block.sha256), DAILY_MEDIA, signal);
    validateDailyHeader(bytes, catalog, block, requiredSHA(block.payloadSha256));
    const localRow = location.row - blockRow * 64, localColumn = location.column - blockColumn * 64;
    const start = DAILY_HEADER_BYTES + (localRow * block.columns + localColumn) * DAILY_RECORD_BYTES;
    const record = bytes.subarray(start, start + DAILY_RECORD_BYTES);
    return record.every((value) => value === 0) ? undefined : record;
  }

  async #block(url: string, bytes: number, checksum: string, media: string, signal?: AbortSignal): Promise<Uint8Array> {
    const key = `${url}:${checksum}`, cached = this.#blocks.get(key); if (cached) return touch(this.#blocks, key, cached, 16);
    let response: Response; try { response = await this.#fetcher(url, { signal, cache: 'force-cache', headers: { Range: `bytes=0-${bytes - 1}` } }); } catch { throw new ForecastDataError('unavailable'); }
    if (response.status === 404 || response.status === 409 || response.status === 410) throw new ForecastDataError('invalid');
    if (response.status !== 206 || response.headers.get('Content-Type')?.toLowerCase() !== media || response.headers.has('Content-Encoding') || response.headers.get('Content-Range') !== `bytes 0-${bytes - 1}/${bytes}` || response.headers.get('Content-Length') !== String(bytes)) throw new ForecastDataError('invalid');
    const body = await readBoundedResponse(response, 'forecast point block', bytes);
    if (body.byteLength !== bytes || await digest(body) !== checksum) throw new ForecastDataError('invalid');
    insert(this.#blocks, key, body, 16); return body;
  }

  async #jsonBytes(path: string, maximum: number, immutable: boolean, signal?: AbortSignal): Promise<Uint8Array> {
    let response: Response; try { response = await this.#fetcher(path, { signal, cache: immutable ? 'force-cache' : 'no-cache' }); } catch { throw new ForecastDataError('unavailable'); }
    if (response.status === 404 || response.status === 409 || response.status === 410) throw new ForecastDataError(immutable ? 'invalid' : 'unavailable');
    if (!response.ok) throw new ForecastDataError('unavailable');
    const encoding = response.headers.get('Content-Encoding')?.trim().toLowerCase() ?? 'identity';
    if (response.headers.get('Content-Type')?.split(';')[0].trim().toLowerCase() !== 'application/json' || (encoding !== 'identity' && encoding !== 'gzip')) throw new ForecastDataError('invalid');
    const declared = response.headers.get('Content-Length');
    if (encoding === 'identity' && (declared === null || !/^\d+$/.test(declared) || !Number.isSafeInteger(Number(declared)) || Number(declared) < 1 || Number(declared) > maximum)) throw new ForecastDataError('invalid');
    const bytes = await readBoundedResponse(response, 'forecast JSON', maximum);
    if (bytes.byteLength < 1 || (encoding === 'identity' && bytes.byteLength !== Number(declared))) throw new ForecastDataError('invalid');
    return bytes;
  }

  async #confirm(pointer: Pointer, signal?: AbortSignal) { const after = await this.#pointer(signal); if (after.current.indexId !== pointer.current.indexId || after.current.sha256 !== pointer.current.sha256) throw new ForecastDataError('stale'); }
  async #confirmPointerID(indexId: string, signal?: AbortSignal) { if ((await this.#pointer(signal)).current.indexId !== indexId) throw new ForecastDataError('stale'); }
}

function parseIndex(value: unknown, indexId: string): ForecastIndex {
  const row = exact(value, ['version', 'productContract', 'indexId', 'firstDate', 'lastDate', 'tierSelection', 'days', 'range']);
  if (row.version !== 2 || row.productContract !== 'nice-weather-forecast-run-v3' || identifier(row.indexId) !== indexId) throw new ForecastDataError('invalid');
  const selection = exact(row.tierSelection, ['method', 'priority', 'blend']);
  if (selection.method !== 'choose-one-complete-tier-before-colour' || selection.blend !== false || JSON.stringify(selection.priority) !== '["europe-detail","global"]') throw new ForecastDataError('invalid');
  const days = list(row.days).map((entry) => {
    const day = exact(entry, ['date', 'tiers']); const dateValue = date(day.date);
    const tiers = list(day.tiers).map((tier) => parseTier(tier, dateValue, indexId));
    if (tiers.length < 1 || tiers.length > 2 || tiers[tiers.length - 1].tier !== 'global' || (tiers.length === 2 && tiers[0].tier !== 'europe-detail')) throw new ForecastDataError('invalid');
    return Object.freeze({ date: dateValue, tiers: Object.freeze(tiers) });
  });
  if (days.length !== 14 || days.some((day, i) => day.date !== addDays(days[0].date, i))) throw new ForecastDataError('invalid');
  const range = parseRangeReference(row.range, indexId);
  const firstDate = date(row.firstDate), lastDate = date(row.lastDate);
  if (firstDate !== days[0].date || lastDate !== days[13].date) throw new ForecastDataError('invalid');
  return Object.freeze({ version: 2, productContract: 'nice-weather-forecast-run-v3', indexId, firstDate, lastDate, days: Object.freeze(days), range });
}

function parseTier(value: unknown, dateValue: string, indexId: string): TierReference {
  const row = exact(value, ['date', 'provider', 'tier', 'product', 'runUtc', 'nativeResolutionKm', 'sourceEvidenceSha256', 'catalogPath', 'catalogBytes', 'catalogSha256']);
  const tier = row.tier === 'global' ? 'global' : row.tier === 'europe-detail' ? 'europe-detail' : fail();
  const provider = text(row.provider), product = text(row.product), runUtc = timestamp(row.runUtc), resolution = positive(row.nativeResolutionKm);
  if (row.date !== dateValue || (tier === 'global' ? provider !== 'ecmwf-ifs' || resolution !== 28 : provider !== 'dwd-icon-eu' || resolution !== 7)) throw new ForecastDataError('invalid');
  const catalogPath = safePath(row.catalogPath);
  if (catalogPath !== `runs/${indexId}/forecast-days/${dateValue}/${tier}/catalog.json`) throw new ForecastDataError('invalid');
  return Object.freeze({ date: dateValue, provider, tier, product, runUtc, nativeResolutionKm: resolution, sourceEvidenceSha256: sha(row.sourceEvidenceSha256), catalogPath, catalogBytes: boundedInteger(row.catalogBytes, 1, CATALOG_BYTES), catalogSha256: sha(row.catalogSha256) });
}

function parseRangeReference(value: unknown, indexId: string): RangeReference {
  const row = exact(value, ['period', 'provider', 'tier', 'product', 'runUtc', 'nativeResolutionKm', 'staleAfterHours', 'catalogPath', 'catalogBytes', 'catalogSha256']);
  const catalogPath = safePath(row.catalogPath);
  if (row.period !== 'next-14-days' || row.provider !== 'ecmwf-ifs' || row.tier !== 'global' || row.nativeResolutionKm !== 28 || !supportedStaleAfterHours(row.staleAfterHours) || catalogPath !== `runs/${indexId}/forecast-ranges/next-14-days/global/catalog.json`) throw new ForecastDataError('invalid');
  return Object.freeze({ period: 'next-14-days', provider: 'ecmwf-ifs', tier: 'global', product: text(row.product), runUtc: timestamp(row.runUtc), nativeResolutionKm: 28, staleAfterHours: row.staleAfterHours, catalogPath, catalogBytes: boundedInteger(row.catalogBytes, 1, CATALOG_BYTES), catalogSha256: sha(row.catalogSha256) });
}

function parseDaily(value: unknown, index: ForecastIndex, reference: TierReference, root: string): DailyForecastCatalog {
  const row = exact(value, ['version', 'releaseId', 'localDate', 'provider', 'tier', 'grid', 'evidence', 'land', 'scalarMaxZoom', 'scores', 'coverage', 'pointBlocks', 'displayPoints', 'scalarCoverage', 'scalarTiles']);
  if (row.version !== 2 || row.releaseId !== index.indexId || row.localDate !== reference.date || row.provider !== reference.provider || row.tier !== reference.tier) throw new ForecastDataError('invalid');
  const gridRow = exact(row.grid, ['projection', 'west', 'east', 'south', 'north', 'rows', 'columns', 'cellDegrees', 'cellRegistration']);
  const grid = Object.freeze({ west: finite(gridRow.west), east: finite(gridRow.east), south: finite(gridRow.south), north: finite(gridRow.north), rows: positiveInteger(gridRow.rows), columns: positiveInteger(gridRow.columns) });
  const expected = reference.tier === 'global' ? [-180, 180, -90, 90, 720, 1440, 3] : [-24, 45, 29, 71.5, 680, 1104, 5];
  if (gridRow.projection !== 'EPSG:4326' || gridRow.cellRegistration !== 'cell-centre' || [grid.west, grid.east, grid.south, grid.north, grid.rows, grid.columns, row.scalarMaxZoom].some((value, i) => value !== expected[i])) throw new ForecastDataError('invalid');
  const evidenceRow = exact(row.evidence, ['product', 'runUtc', 'nativeResolutionKm', 'sourceEvidenceSha256']);
  const evidence = Object.freeze({ product: text(evidenceRow.product), runUtc: timestamp(evidenceRow.runUtc), nativeResolutionKm: positive(evidenceRow.nativeResolutionKm), sourceEvidenceSha256: sha(evidenceRow.sourceEvidenceSha256) });
  if (evidence.product !== reference.product || evidence.runUtc !== reference.runUtc || evidence.nativeResolutionKm !== reference.nativeResolutionKm || evidence.sourceEvidenceSha256 !== reference.sourceEvidenceSha256) throw new ForecastDataError('invalid');
  const landRow = exact(row.land, ['authority', 'dataset', 'version', 'sourceArchiveSha256', 'maskSha256', 'derivationContract']);
  if (landRow.authority !== 'Natural Earth' || landRow.dataset !== 'ne_10m_land' || landRow.version !== '5.1.1' || landRow.derivationContract !== 'web-mercator-all-touched-v1') throw new ForecastDataError('invalid');
  const land = Object.freeze({ authority: 'Natural Earth' as const, dataset: 'ne_10m_land' as const, version: '5.1.1' as const, sourceArchiveSha256: sha(landRow.sourceArchiveSha256), maskSha256: sha(landRow.maskSha256), derivationContract: 'web-mercator-all-touched-v1' as const });
  parseScores(row.scores);
  const pointBlocks = Object.freeze(list(row.pointBlocks).map(parsePointBlock));
  const displayPoints = Object.freeze(list(row.displayPoints).map(parseDisplayPoint));
  const scalarCoverage = Object.freeze(list(row.scalarCoverage).map(parseCoverage));
  const scalarTiles = Object.freeze(list(row.scalarTiles).map(parseScalar));
  validateScalarCollections(scalarCoverage, scalarTiles, row.scalarMaxZoom as 3 | 5);
  const displayKeys = new Set(displayPoints.map(({ zoom, x, y }) => `${zoom}/${x}/${y}`));
  const availableKeys = scalarCoverage.filter(({ omitted }) => !omitted).map(({ zoom, x, y }) => `${zoom}/${x}/${y}`);
  if (displayKeys.size !== displayPoints.length || availableKeys.some((key) => !displayKeys.has(key)) || displayKeys.size !== availableKeys.length) throw new ForecastDataError('invalid');
  return Object.freeze({ version: 2, releaseId: index.indexId, localDate: reference.date, provider: reference.provider, tier: reference.tier, grid, evidence, land, scalarMaxZoom: row.scalarMaxZoom as 3 | 5, pointBlocks, displayPoints, scalarCoverage, scalarTiles, root });
}

function parseRange(value: unknown, index: ForecastIndex, root: string): RangeForecastCatalog {
  const row = exact(value, ['version', 'productContract', 'releaseId', 'period', 'dates', 'source', 'grid', 'scores', 'pointBlockLayout', 'inputs', 'coverage', 'pointBlocks', 'scalarCoverage', 'scalarTiles']);
  if (row.version !== 1 || row.productContract !== 'nice-weather-forecast-range-v1' || row.releaseId !== index.indexId || row.period !== 'next-14-days') throw new ForecastDataError('invalid');
  const dates = list(row.dates).map(date); if (JSON.stringify(dates) !== JSON.stringify(index.days.map(({ date }) => date))) throw new ForecastDataError('invalid');
  const sourceRow = exact(row.source, ['provider', 'tier', 'product', 'runUtc', 'nativeResolutionKm', 'staleAfterHours']);
  if (sourceRow.provider !== 'ecmwf-ifs' || sourceRow.tier !== 'global' || sourceRow.nativeResolutionKm !== 28 || !supportedStaleAfterHours(sourceRow.staleAfterHours) || sourceRow.staleAfterHours !== index.range.staleAfterHours || timestamp(sourceRow.runUtc) !== index.range.runUtc) throw new ForecastDataError('invalid');
  const source = Object.freeze({ provider: 'ecmwf-ifs' as const, tier: 'global' as const, product: text(sourceRow.product), runUtc: index.range.runUtc, nativeResolutionKm: 28 as const, staleAfterHours: 36 as const });
  if (source.product !== index.range.product) throw new ForecastDataError('invalid');
  const grid = exact(row.grid, ['projection', 'zoom', 'tileSize', 'rows', 'columns']); if (grid.projection !== 'EPSG:3857-XYZ' || grid.zoom !== 3 || grid.tileSize !== 256 || grid.rows !== 2048 || grid.columns !== 2048) throw new ForecastDataError('invalid');
  parseScores(row.scores, true);
  const layout = exact(row.pointBlockLayout, ['rows', 'columns', 'headerBytes', 'recordBytes', 'mediaType']); if (layout.rows !== 64 || layout.columns !== 64 || layout.headerBytes !== 256 || layout.recordBytes !== 44 || layout.mediaType !== RANGE_MEDIA) throw new ForecastDataError('invalid');
  const inputs = list(row.inputs).map((entry) => {
    const input = exact(entry, ['date', 'path', 'bytes', 'sha256']);
    return { date: date(input.date), path: safePath(input.path), bytes: boundedInteger(input.bytes, 1, CATALOG_BYTES), sha256: sha(input.sha256) };
  });
  const expectedInputs = index.days.map((day) => {
    const global = day.tiers.find(({ tier }) => tier === 'global');
    if (!global) throw new ForecastDataError('invalid');
    return { date: day.date, path: global.catalogPath.replace(`runs/${index.indexId}/`, ''), bytes: global.catalogBytes, sha256: global.catalogSha256 };
  });
  if (JSON.stringify(inputs) !== JSON.stringify(expectedInputs)) throw new ForecastDataError('invalid');
  const pointBlocks = Object.freeze(list(row.pointBlocks).map((item) => { const block = exact(item, ['row', 'column', 'omitted', 'path', 'bytes', 'sha256', 'payloadSha256', 'dataRecords', 'noDataRecords'], ['path', 'bytes', 'sha256', 'payloadSha256']); return Object.freeze({ row: boundedInteger(block.row, 0, 31), column: boundedInteger(block.column, 0, 31), omitted: boolean(block.omitted), ...(block.omitted ? {} : { path: safePath(block.path), bytes: boundedInteger(block.bytes, RANGE_HEADER_BYTES + RANGE_RECORD_BYTES, 200_000), sha256: sha(block.sha256), payloadSha256: sha(block.payloadSha256) }) }); }));
  const scalarCoverage = Object.freeze(list(row.scalarCoverage).map(parseCoverage)); const scalarTiles = Object.freeze(list(row.scalarTiles).map(parseScalar));
  validateScalarCollections(scalarCoverage, scalarTiles, 3);
  return Object.freeze({ version: 1, productContract: 'nice-weather-forecast-range-v1', releaseId: index.indexId, period: 'next-14-days', dates: Object.freeze(dates), source, pointBlocks, scalarCoverage, scalarTiles, root });
}

function supportedStaleAfterHours(value: unknown): value is 18 | 36 { return value === 18 || value === 36; }

function parseScores(value: unknown, range = false) {
  const fields = exact(value, range ? ['unit', 'noDataValue', 'mediaType', 'profiles', 'fields'] : ['unit', 'noDataValue', 'profiles', 'fields']);
  if (fields.unit !== 'canonical-score-0-100' || fields.noDataValue !== 255 || (range && fields.mediaType !== SCALAR_MEDIA) || JSON.stringify(fields.profiles) !== '["nice","cycling"]') throw new ForecastDataError('invalid');
  const contracts = list(fields.fields).map((entry) => { const row = exact(entry, ['id', 'profile']); return [text(row.id), text(row.profile)]; });
  const expected = [['thermal', 'shared'], ['dryness', 'shared'], ['sun', 'shared'], ['nice_score', 'nice'], ['nice_wind', 'nice'], ['cycling_score', 'cycling'], ['cycling_wind', 'cycling']];
  if (JSON.stringify(contracts) !== JSON.stringify(expected)) throw new ForecastDataError('invalid');
}

function parsePointBlock(value: unknown): ForecastPointBlock {
  const row = exact(value, ['row', 'column', 'rows', 'columns', 'omitted', 'path', 'bytes', 'sha256', 'payloadSha256', 'mediaType', 'dataRecords', 'noDataRecords'], ['path', 'bytes', 'sha256', 'payloadSha256']);
  const result: ForecastPointBlock = { row: nonNegativeInteger(row.row), column: nonNegativeInteger(row.column), rows: boundedInteger(row.rows, 1, 64), columns: boundedInteger(row.columns, 1, 64), omitted: boolean(row.omitted), mediaType: text(row.mediaType) };
  if (result.mediaType !== DAILY_MEDIA) throw new ForecastDataError('invalid');
  return Object.freeze(result.omitted ? result : { ...result, path: safePath(row.path), bytes: boundedInteger(row.bytes, DAILY_HEADER_BYTES + DAILY_RECORD_BYTES, 128 * 1024), sha256: sha(row.sha256), payloadSha256: sha(row.payloadSha256) });
}

function parseDisplayPoint(value: unknown): ForecastDisplayPoint { const row = exact(value, ['zoom', 'x', 'y', 'path', 'bytes', 'sha256', 'payloadSha256', 'mediaType']); const result = { zoom: boundedInteger(row.zoom, 0, 5), x: nonNegativeInteger(row.x), y: nonNegativeInteger(row.y), path: safePath(row.path), bytes: boundedInteger(row.bytes, 1, DISPLAY_BYTES), sha256: sha(row.sha256), payloadSha256: sha(row.payloadSha256), mediaType: text(row.mediaType) }; if (result.bytes !== DISPLAY_BYTES || result.mediaType !== DISPLAY_MEDIA) throw new ForecastDataError('invalid'); return Object.freeze(result); }
function parseCoverage(value: unknown): ForecastCoverage { const row = exact(value, ['zoom', 'x', 'y', 'omitted', 'dataRecords', 'noDataRecords']); return Object.freeze({ zoom: boundedInteger(row.zoom, 0, 5), x: nonNegativeInteger(row.x), y: nonNegativeInteger(row.y), omitted: boolean(row.omitted) }); }
function parseScalar(value: unknown): ForecastScalarTile { const row = exact(value, ['field', 'zoom', 'x', 'y', 'path', 'bytes', 'sha256', 'mediaType', 'dataTexels', 'noDataTexels'], ['mediaType']); const field = FIELDS.includes(row.field as ForecastField) ? row.field as ForecastField : fail(); const result = { field, zoom: boundedInteger(row.zoom, 0, 5), x: nonNegativeInteger(row.x), y: nonNegativeInteger(row.y), path: safePath(row.path), bytes: boundedInteger(row.bytes, 1, SCALAR_BYTES), sha256: sha(row.sha256), ...(row.mediaType === undefined ? {} : { mediaType: text(row.mediaType) }) }; if (result.bytes !== SCALAR_BYTES || (result.mediaType && result.mediaType !== SCALAR_MEDIA)) throw new ForecastDataError('invalid'); return Object.freeze(result); }

function validateScalarCollections(coverage: readonly ForecastCoverage[], tiles: readonly ForecastScalarTile[], maximumZoom: number) {
  const coverageKeys = coverage.map(({ zoom, x, y }) => `${zoom}/${x}/${y}`);
  const available = new Set(coverage.filter((item) => !item.omitted).map(({ zoom, x, y }) => `${zoom}/${x}/${y}`));
  if (!coverage.length || !tiles.length || FIELDS.some((field) => !tiles.some((tile) => tile.field === field))) throw new ForecastDataError('invalid');
  if (new Set(coverageKeys).size !== coverage.length || coverage.some(({ zoom }) => zoom !== maximumZoom)) throw new ForecastDataError('invalid');
  const tileKeys = tiles.map(({ field, zoom, x, y }) => `${field}/${zoom}/${x}/${y}`);
  if (new Set(tileKeys).size !== tiles.length) throw new ForecastDataError('invalid');
  for (const item of [...coverage, ...tiles]) if (item.zoom > maximumZoom || item.x >= 2 ** item.zoom || item.y >= 2 ** item.zoom) throw new ForecastDataError('invalid');
  if (available.size === 0) throw new ForecastDataError('invalid');
  for (const key of available) for (const field of FIELDS) if (!tileKeys.includes(`${field}/${key}`)) throw new ForecastDataError('invalid');
  const coordinateFields = new Map<string, Set<string>>();
  for (const tile of tiles) {
    const key = `${tile.zoom}/${tile.x}/${tile.y}`;
    const fields = coordinateFields.get(key) ?? new Set<string>(); fields.add(tile.field); coordinateFields.set(key, fields);
  }
  if ([...coordinateFields.values()].some((fields) => fields.size !== FIELDS.length)) throw new ForecastDataError('invalid');
}

function declaredTile(catalog: DailyForecastCatalog, tile: ScalarTileID): boolean { return catalog.scalarCoverage.some((item) => item.zoom === tile.z && item.x === tile.x && item.y === tile.y && !item.omitted); }

export function forecastDetailDeclared(product: DayForecastProduct, tile: ScalarTileID): boolean {
  const detail = product.tiers.find(({ tier }) => tier === 'europe-detail');
  return Boolean(detail && tile.z === 5 && declaredTile(detail, tile));
}

function scalarSource(catalog: Readonly<{ root: string; scalarTiles: readonly ForecastScalarTile[]; scalarMaxZoom?: 3 | 5 }>, _identity: unknown, field: ForecastField, output: ScalarTileID): ScalarTileSource | undefined {
  const maximum = catalog.scalarMaxZoom ?? 3;
  const sourceZoom = Math.min(output.z, maximum), factor = 2 ** (output.z - sourceZoom);
  const x = Math.floor(output.x / factor), y = Math.floor(output.y / factor);
  const tile = catalog.scalarTiles.find((item) => item.field === field && item.zoom === sourceZoom && item.x === x && item.y === y);
  if (!tile) return undefined;
  const childX = output.x % factor, childY = output.y % factor;
  return Object.freeze({
    key: `${catalog.root}/${tile.path}:${tile.sha256}:${output.z}/${output.x}/${output.y}`,
    url: `${catalog.root}/${tile.path}`, sha256: tile.sha256,
    ...(factor === 1 ? {} : { textureBounds: [childX / factor, childY / factor, (childX + 1) / factor, (childY + 1) / factor] as const }),
  });
}

export function forecastTierForPoint(product: DayForecastProduct, lon: number, lat: number, detailEnabled = true): DailyForecastCatalog {
  const detail = product.tiers.find(({ tier }) => tier === 'europe-detail');
  if (detailEnabled && detail) { const location = xyzLocation(lon, lat, 5); if (declaredTile(detail, location.id)) return detail; }
  const global = product.tiers.find(({ tier }) => tier === 'global'); if (!global) throw new ForecastDataError('invalid'); return global;
}

function xyzLocation(longitude: number, latitude: number, zoom: number) {
  if (!Number.isFinite(longitude) || !Number.isFinite(latitude) || Math.abs(latitude) > 85.0511287798066) throw new ForecastDataError('unavailable', 'There is no complete weather record here.');
  const count = 2 ** zoom, wrapped = ((longitude + 180) % 360 + 360) % 360;
  const gx = wrapped / 360 * count * 256; const radians = latitude * Math.PI / 180;
  const gy = (0.5 - Math.asinh(Math.tan(radians)) / (2 * Math.PI)) * count * 256;
  if (gy < 0 || gy >= count * 256) throw new ForecastDataError('unavailable');
  const globalX = Math.min(count * 256 - 1, Math.floor(gx)), globalY = Math.min(count * 256 - 1, Math.floor(gy));
  return { id: { z: zoom, x: Math.floor(globalX / 256), y: Math.floor(globalY / 256) }, row: globalY % 256, column: globalX % 256, globalX, globalY };
}

function nativeLocation(catalog: DailyForecastCatalog, longitude: number, latitude: number): { row: number; column: number } | undefined {
  if (!Number.isFinite(longitude) || !Number.isFinite(latitude)) return undefined;
  const grid = catalog.grid; let lon = longitude;
  if (catalog.tier === 'global') lon = ((lon + 180) % 360 + 360) % 360 - 180;
  if (lon < grid.west || lon > grid.east || latitude < grid.south || latitude > grid.north) return undefined;
  const column = Math.min(grid.columns - 1, Math.floor((lon - grid.west) / (grid.east - grid.west) * grid.columns));
  const row = Math.min(grid.rows - 1, Math.floor((grid.north - latitude) / (grid.north - grid.south) * grid.rows));
  return row < 0 || row >= grid.rows || column < 0 || column >= grid.columns ? undefined : { row, column };
}

function validateRangeHeader(bytes: Uint8Array, catalog: RangeForecastCatalog, row: number, column: number, payloadSHA: string) {
  if (bytes.byteLength < RANGE_HEADER_BYTES || ascii(bytes, 0, 8) !== 'NWFRANGE') throw new ForecastDataError('invalid');
  const view = dataView(bytes); if (view.getUint16(8, true) !== 1 || view.getUint16(10, true) !== 256 || view.getUint16(12, true) !== 44 || view.getUint16(14, true) !== 64 || view.getUint16(16, true) !== 64 || bytes[18] !== 3 || bytes[19] !== 1 || view.getUint32(20, true) !== row || view.getUint32(24, true) !== column || view.getUint32(28, true) !== 2048 || view.getUint32(32, true) !== 2048 || !allZero(bytes.subarray(39, 64)) || hex(bytes.subarray(192, 224)) !== payloadSHA || !allZero(bytes.subarray(224, 256))) throw new ForecastDataError('invalid');
  const releaseLength = bytes[36], periodLength = bytes[37], tierLength = bytes[38];
  if (releaseLength > 64 || periodLength > 32 || tierLength > 32 || ascii(bytes, 64, 64 + releaseLength) !== catalog.releaseId || ascii(bytes, 128, 128 + periodLength) !== 'next-14-days' || ascii(bytes, 160, 160 + tierLength) !== 'global' || !allZero(bytes.subarray(64 + releaseLength, 128)) || !allZero(bytes.subarray(128 + periodLength, 160)) || !allZero(bytes.subarray(160 + tierLength, 192))) throw new ForecastDataError('invalid');
}

function validateDisplayHeader(bytes: Uint8Array, catalog: DailyForecastCatalog, id: ScalarTileID, payloadSHA: string) {
  if (bytes.byteLength !== DISPLAY_BYTES || ascii(bytes, 0, 8) !== 'NWFXYZPT') throw new ForecastDataError('invalid'); const view = dataView(bytes);
  const releaseLength = bytes[26], periodLength = bytes[27], tierLength = bytes[28];
  if (view.getUint16(8, true) !== 1 || view.getUint16(10, true) !== 192 || view.getUint16(12, true) !== 8 || view.getUint16(14, true) !== 256 || bytes[16] !== id.z || bytes[17] !== 1 || view.getUint32(18, true) !== id.x || view.getUint32(22, true) !== id.y || releaseLength > 64 || periodLength > 32 || tierLength > 32 || !allZero(bytes.subarray(29, 32)) || ascii(bytes, 32, 32 + releaseLength) !== catalog.releaseId || ascii(bytes, 96, 96 + periodLength) !== catalog.localDate || ascii(bytes, 128, 128 + tierLength) !== catalog.tier || !allZero(bytes.subarray(32 + releaseLength, 96)) || !allZero(bytes.subarray(96 + periodLength, 128)) || !allZero(bytes.subarray(128 + tierLength, 160)) || hex(bytes.subarray(160, 192)) !== payloadSHA) throw new ForecastDataError('invalid');
}

function validateDailyHeader(bytes: Uint8Array, catalog: DailyForecastCatalog, block: ForecastPointBlock, payloadSHA: string) {
  if (bytes.byteLength !== block.bytes || ascii(bytes, 0, 8) !== 'NWFCSTPT') throw new ForecastDataError('invalid'); const view = dataView(bytes);
  const releaseLength = bytes[36], providerLength = bytes[37], tierLength = bytes[38], dateLength = bytes[39], runLength = bytes[76];
  const expectedGridKind = catalog.tier === 'global' ? 1 : 2;
  if (view.getUint16(8, true) !== 2 || view.getUint16(10, true) !== 320 || view.getUint16(12, true) !== 30 || bytes[14] !== 1 || bytes[15] !== expectedGridKind || view.getUint16(16, true) !== block.rows || view.getUint16(18, true) !== block.columns || view.getUint32(20, true) !== block.row || view.getUint32(24, true) !== block.column || view.getUint32(28, true) !== catalog.grid.rows || view.getUint32(32, true) !== catalog.grid.columns || releaseLength > 64 || providerLength > 24 || tierLength > 24 || dateLength > 16 || runLength > 24 || view.getFloat64(40, true) !== catalog.grid.west || view.getFloat64(48, true) !== catalog.grid.east || view.getFloat64(56, true) !== catalog.grid.south || view.getFloat64(64, true) !== catalog.grid.north || view.getUint32(72, true) !== block.rows * block.columns * DAILY_RECORD_BYTES || !allZero(bytes.subarray(77, 80)) || ascii(bytes, 80, 80 + releaseLength) !== catalog.releaseId || ascii(bytes, 144, 144 + providerLength) !== catalog.provider || ascii(bytes, 168, 168 + tierLength) !== catalog.tier || ascii(bytes, 192, 192 + dateLength) !== catalog.localDate || ascii(bytes, 208, 208 + runLength) !== catalog.evidence.runUtc || !allZero(bytes.subarray(80 + releaseLength, 144)) || !allZero(bytes.subarray(144 + providerLength, 168)) || !allZero(bytes.subarray(168 + tierLength, 192)) || !allZero(bytes.subarray(192 + dateLength, 208)) || !allZero(bytes.subarray(208 + runLength, 232)) || hex(bytes.subarray(232, 264)) !== catalog.evidence.sourceEvidenceSha256 || hex(bytes.subarray(264, 296)) !== payloadSHA || !allZero(bytes.subarray(296, 320))) throw new ForecastDataError('invalid');
}

function decodeRangeRecord(bytes: Uint8Array, lon: number, lat: number, index: ForecastIndex, catalog: RangeForecastCatalog, profile: Profile, presentation: ForecastPresentation): ForecastWindowPoint {
  if (bytes.byteLength !== 44 || bytes.every((value) => value === 0)) throw new ForecastDataError('unavailable', 'There is no complete weather record here.');
  if (bytes[43] !== 1 || bytes.subarray(0, 43).some((value) => value > 100)) throw new ForecastDataError('invalid');
  const offset = profile === 'nice' ? 3 : 9, dayOffset = profile === 'nice' ? 15 : 29, score = bytes[offset];
  const scores = Array.from(bytes.subarray(dayOffset, dayOffset + 14)); const legend = presentation.legend;
  return { kind: 'range', releaseId: index.indexId, lat, lon, periodLabel: 'Next 14 days', score, category: rating(score, legend), components: components(bytes[0], bytes[1], bytes[2], bytes[offset + 2], legend), window: { startDate: index.firstDate, endDate: index.lastDate, dayCount: 14, niceDays: bytes[offset + 3], awfulDays: bytes[offset + 4], medianScore: bytes[offset + 1], longestPoorRun: bytes[offset + 5] }, days: scores.map((value, i) => ({ date: catalog.dates[i], score: value, category: rating(value, legend) })), source: { basis: 'forecast-window', detailLabel: 'Worldwide forecast', resolutionKm: 28, artifactGeneratedAt: presentation.generatedAt } };
}

function decodeDailyRecord(bytes: Uint8Array, display: Uint8Array, lon: number, lat: number, catalog: DailyForecastCatalog, profile: Profile, presentation: ForecastPresentation): DailyPoint {
  if (bytes.byteLength !== 30) throw new ForecastDataError('invalid'); const view = dataView(bytes), flags = bytes[20];
  if ((flags & 1) === 0 || (flags & ~0x7f) !== 0 || bytes[21] !== 0) throw new ForecastDataError('invalid');
  const dew = view.getInt16(6, true), gust = view.getUint16(12, true), solar = view.getUint16(14, true), wet = view.getUint16(16, true), cloud = bytes[18], humidity = bytes[19];
  const offset = profile === 'nice' ? 3 : 5, score = display[offset], temperature = view.getInt16(0, true) / 10;
  const pointComponents = components(display[0], display[1], display[2], display[offset + 1], presentation.legend);
  return { kind: 'daily', releaseId: catalog.releaseId, lat, lon, periodLabel: new Intl.DateTimeFormat('en-GB', { weekday: 'long', day: 'numeric', month: 'short', timeZone: 'UTC' }).format(new Date(`${catalog.localDate}T12:00:00Z`)), score, category: rating(score, presentation.legend), components: pointComponents, summary: dailySummary(temperature, bytes[29], pointComponents.sun), metrics: { temperatureC: temperature, minTempC: view.getInt16(2, true) / 10, maxTempC: view.getInt16(4, true) / 10, rainMm: view.getUint16(8, true) / 10, windKmh: view.getUint16(10, true) / 10, dewPointC: flags & 2 ? dew / 10 : undefined, gustKmh: flags & 4 ? gust / 10 : undefined, solarEnergyMJm2: flags & 8 ? solar / 100 : undefined, cloudPct: flags & 16 ? cloud : undefined, humidityPct: flags & 32 ? humidity : undefined, wetTimeFraction: flags & 64 ? wet / 1000 : undefined }, source: { basis: 'forecast', detailLabel: catalog.tier === 'europe-detail' ? 'Detailed forecast' : 'Worldwide forecast', providerLabel: catalog.tier === 'europe-detail' ? 'European detail' : 'Worldwide forecast', resolutionKm: catalog.evidence.nativeResolutionKm, temporalResolutionHours: catalog.tier === 'europe-detail' ? 1 : 6, artifactGeneratedAt: presentation.generatedAt, runAt: catalog.evidence.runUtc, validFrom: `${catalog.localDate}T00:00:00Z`, validTo: `${addDays(catalog.localDate, 1)}T00:00:00Z` } };
}

function components(comfort: number, dryness: number, sun: number, wind: number, legend: readonly LegendCategory[]) { const value = (score: number) => ({ value: score, category: rating(score, legend) }); return { comfort: value(comfort), dryness: value(dryness), sun: value(sun), wind: value(wind) }; }
function rating(score: number, legend: readonly LegendCategory[]) { const found = legend.find(({ min, max }) => score >= min && score <= max); if (!found) throw new ForecastDataError('invalid'); return found.label; }
export function dailySummary(temperature: number, code: number, lightEvidence?: ComponentScore): string {
  if (!Number.isInteger(code) || code < 0 || code >= 240) throw new ForecastDataError('invalid');
  const thermal = Math.floor(code / 80), rest = code % 80, wind = Math.floor(rest / 16), rain = rest % 4;
  const light = lightEvidence?.category === 'Awful' || lightEvidence?.category === 'Poor' ? 'overcast'
    : lightEvidence?.category === 'Mixed' ? 'mixed cloud'
      : lightEvidence?.category === 'Good' || lightEvidence?.category === 'Very good' ? 'bright'
        : lightEvidence?.category === 'Excellent' ? 'sunny' : undefined;
  const parts = [...(thermal ? [['too hot', 'too cold'][thermal - 1]] : []), ['dry', 'brief rain', 'some rain', 'wet'][rain], ...(light ? [light] : []), ['light wind', 'breezy', 'windy', 'strong gusts', 'severe gusts'][wind]];
  return `${Math.round(temperature)}°C · ${parts.join(' · ')}`;
}

function parseJSON(bytes: Uint8Array): unknown { try { return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)); } catch { throw new ForecastDataError('invalid'); } }
function exact(value: unknown, fields: readonly string[], optional: readonly string[] = []): Record<string, unknown> { if (!value || typeof value !== 'object' || Array.isArray(value)) throw new ForecastDataError('invalid'); const row = value as Record<string, unknown>, allowed = new Set(fields); if (Object.keys(row).some((key) => !allowed.has(key)) || fields.some((key) => !optional.includes(key) && !(key in row))) throw new ForecastDataError('invalid'); return row; }
function list(value: unknown): unknown[] { if (!Array.isArray(value)) throw new ForecastDataError('invalid'); return value; }
function text(value: unknown): string { if (typeof value !== 'string' || !value || value.trim() !== value) throw new ForecastDataError('invalid'); return value; }
function identifier(value: unknown) { const result = text(value); if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(result)) throw new ForecastDataError('invalid'); return result; }
function safePath(value: unknown) { const result = text(value); if (result.startsWith('/') || result.includes('\\') || result.split('/').some((part) => !part || part === '.' || part === '..')) throw new ForecastDataError('invalid'); return result; }
function requiredPath(value: unknown): string { return safePath(value); }
function sha(value: unknown) { const result = text(value); if (!/^[0-9a-f]{64}$/.test(result)) throw new ForecastDataError('invalid'); return result; }
function requiredSHA(value: unknown): string { return sha(value); }
function date(value: unknown) { const result = text(value); if (!/^\d{4}-\d{2}-\d{2}$/.test(result) || new Date(`${result}T00:00:00Z`).toISOString().slice(0, 10) !== result) throw new ForecastDataError('invalid'); return result; }
function timestamp(value: unknown) { const result = text(value); if (Number.isNaN(Date.parse(result))) throw new ForecastDataError('invalid'); return result; }
function finite(value: unknown) { if (typeof value !== 'number' || !Number.isFinite(value)) throw new ForecastDataError('invalid'); return value; }
function positive(value: unknown) { const result = finite(value); if (result <= 0) throw new ForecastDataError('invalid'); return result; }
function boundedInteger(value: unknown, min: number, max: number) { if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < min || value > max) throw new ForecastDataError('invalid'); return value; }
function positiveInteger(value: unknown) { return boundedInteger(value, 1, Number.MAX_SAFE_INTEGER); }
function nonNegativeInteger(value: unknown) { return boundedInteger(value, 0, Number.MAX_SAFE_INTEGER); }
function requiredInteger(value: unknown) { return positiveInteger(value); }
function boolean(value: unknown) { if (typeof value !== 'boolean') throw new ForecastDataError('invalid'); return value; }
function tileID(value: ScalarTileID) { return Number.isInteger(value.z) && value.z >= 0 && value.z <= 5 && Number.isInteger(value.x) && value.x >= 0 && value.x < 2 ** value.z && Number.isInteger(value.y) && value.y >= 0 && value.y < 2 ** value.z; }
function addDays(value: string, count: number) { const result = new Date(`${value}T00:00:00Z`); result.setUTCDate(result.getUTCDate() + count); return result.toISOString().slice(0, 10); }
function dataView(bytes: Uint8Array) { return new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength); }
function ascii(bytes: Uint8Array, start: number, end: number) { return new TextDecoder().decode(bytes.subarray(start, end)); }
function hex(bytes: Uint8Array) { return [...bytes].map((value) => value.toString(16).padStart(2, '0')).join(''); }
function allZero(bytes: Uint8Array) { return bytes.every((value) => value === 0); }
async function digest(bytes: Uint8Array) { return hex(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes as Uint8Array<ArrayBuffer>))); }
function fail(): never { throw new ForecastDataError('invalid'); }
function insert<K, V>(map: Map<K, V>, key: K, value: V, limit: number) { map.delete(key); map.set(key, value); while (map.size > limit) map.delete(map.keys().next().value!); }
function touch<K, V>(map: Map<K, V>, key: K, value: V, limit: number): V { insert(map, key, value, limit); return value; }
function combineSignals(left: AbortSignal, right?: AbortSignal) { if (!right) return left; return AbortSignal.any([left, right]); }
