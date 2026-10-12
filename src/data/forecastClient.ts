import { readBoundedResponse } from './boundedResponse';
import type { DailyPoint, ForecastWindowPoint, LegendCategory, Profile } from './types';
import type { ScalarTileID, ScalarTileSource } from '../map/scalarTileLayer';

const POINTER_PATH = '/forecast/current.json';
const POINTER_BYTES = 2_048;
const INDEX_BYTES = 64 * 1_024;
const DAILY_CATALOG_BYTES = 1_024 * 1_024;
const RANGE_CATALOG_BYTES = 2 * 1_024 * 1_024;
const SCALAR_BYTES = 258 * 258;
const DAILY_POINT_BLOCK_HEADER_BYTES = 192;
const DAILY_POINT_BLOCK_SIDE = 64;
const DAILY_POINT_BLOCK_BYTES = DAILY_POINT_BLOCK_HEADER_BYTES + DAILY_POINT_BLOCK_SIDE * DAILY_POINT_BLOCK_SIDE * 30;
const RANGE_HEADER_BYTES = 256;
const DAILY_RECORD_BYTES = 30;
const RANGE_RECORD_BYTES = 44;
const RANGE_MEDIA = 'application/vnd.nice-weather.forecast-range-block';
const DAILY_POINT_BLOCK_MEDIA = 'application/vnd.nice-weather.daily-point-block';
const SCALAR_MEDIA = 'application/vnd.nice-weather.score-tile';
const FIELDS = ['thermal', 'dryness', 'sun', 'nice_score', 'nice_wind', 'cycling_score', 'cycling_wind'] as const;

const scalarTileIndexes = new WeakMap<readonly ForecastScalarTile[], ReadonlyMap<string, ForecastScalarTile>>();
const scalarCoverageIndexes = new WeakMap<readonly ForecastCoverage[], ReadonlySet<string>>();

export type ForecastField = typeof FIELDS[number];
export type ForecastTier = 'europe-detail' | 'global';
export type ForecastScalarTile = Readonly<{ field: ForecastField; zoom: number; x: number; y: number; path: string; bytes: number; sha256: string; mediaType?: string }>;
export type ForecastDailyPointBlock = Readonly<{ zoom: number; x: number; y: number; path: string; bytes: number; sha256: string; payloadSha256: string; mediaType: string; dataRecords: number; noDataRecords: number }>;
export type ForecastCoverage = Readonly<{ zoom: number; x: number; y: number; omitted: boolean; dataRecords: number; fallbackRecords: number; noDataRecords: number }>;
export type ForecastLandIdentity = Readonly<{
  authority: 'Natural Earth'; dataset: 'ne_10m_land'; version: '5.1.1';
  sourceArchiveSha256: string; maskSha256: string; derivationContract: 'web-mercator-centre-on-land-v1';
}>;

type TierReference = Readonly<{
  releaseId: string; date: string; provider: string; tier: ForecastTier; product: string; runUtc: string; nativeResolutionKm: number;
  sourceEvidenceSha256: string; catalogPath: string; catalogBytes: number; catalogSha256: string;
}>;
type RangeReference = Readonly<{
  period: 'next-14-days'; provider: 'ecmwf-ifs'; tier: ForecastTier; product: string; runUtc: string; nativeResolutionKm: 28;
  staleAfterHours: 36; catalogPath: string; catalogBytes: number; catalogSha256: string;
}>;

export type ForecastIndex = Readonly<{
  version: 3; productContract: 'nice-weather-forecast-run-v7'; indexId: string; firstDate: string; lastDate: string;
  days: readonly Readonly<{ date: string; tiers: readonly TierReference[] }>[]; range: RangeReference; regionalRange?: RangeReference;
}>;

export type DailyForecastCatalog = Readonly<{
  version: 4; releaseId: string; localDate: string; provider: string; tier: ForecastTier;
  grid: Readonly<{ west: number; east: number; south: number; north: number; rows: number; columns: number }>;
  evidence: Readonly<{ product: string; runUtc: string; nativeResolutionKm: number; sourceEvidenceSha256: string }>;
  land: ForecastLandIdentity;
  scalarMaxZoom: 3 | 5; dailyPointBlocks: readonly ForecastDailyPointBlock[];
  scalarCoverage: readonly ForecastCoverage[]; scalarTiles: readonly ForecastScalarTile[]; root: string;
}>;

export type DayForecastProduct = Readonly<{ indexId: string; date: string; tiers: readonly DailyForecastCatalog[] }>;

export type RangeForecastCatalog = Readonly<{
  version: 1; productContract: 'nice-weather-forecast-range-v3'; releaseId: string; period: 'next-14-days'; dates: readonly string[];
  source: Readonly<{ provider: 'ecmwf-ifs'; tier: 'global'; product: string; runUtc: string; nativeResolutionKm: 28; staleAfterHours: 36 }>;
  pointBlocks: readonly Readonly<{ row: number; column: number; omitted: boolean; path?: string; bytes?: number; sha256?: string; payloadSha256?: string }>[];
  scalarCoverage: readonly ForecastCoverage[]; scalarTiles: readonly ForecastScalarTile[]; root: string; tier: ForecastTier; scalarMaxZoom: 3 | 5; regional?: RangeForecastCatalog;
}>;

export type ForecastSnapshot = Readonly<{ index: ForecastIndex; range: RangeForecastCatalog }>;
export type ForecastPresentation = Readonly<{ legend: readonly LegendCategory[]; generatedAt: string }>;

export class ForecastDataError extends Error {
  constructor(readonly code: 'unavailable' | 'invalid' | 'stale', message?: string) {
    super(message ?? (code === 'unavailable' ? 'The forecast is not available right now.' : code === 'stale' ? 'The forecast changed. Try again.' : 'The forecast could not be read.'));
    this.name = 'ForecastDataError';
  }
}

export function freshnessLabel(updatedAt: string, now = new Date()): string {
  const elapsedMinutes = Math.max(0, Math.round((now.getTime() - new Date(updatedAt).getTime()) / 60_000));
  if (!Number.isFinite(elapsedMinutes)) return 'Update time unknown';
  if (elapsedMinutes < 2) return 'Updated just now';
  if (elapsedMinutes < 60) return `Updated ${elapsedMinutes} minutes ago`;
  const hours = Math.round(elapsedMinutes / 60);
  if (hours < 24) return `Updated ${hours} ${hours === 1 ? 'hour' : 'hours'} ago`;
  const days = Math.round(hours / 24);
  return `Updated ${days} ${days === 1 ? 'day' : 'days'} ago`;
}

export function isStale(updatedAt: string, staleAfterHours: number, now = new Date()): boolean {
  const age = now.getTime() - new Date(updatedAt).getTime();
  return !Number.isFinite(age) || age > staleAfterHours * 3_600_000;
}

export function forecastRunStatus(runAt: string, staleAfterHours: number, now = new Date()): { stale: boolean; label: string } {
  const stale = isStale(runAt, staleAfterHours, now);
  return { stale, label: stale ? 'Forecast is out of date' : freshnessLabel(runAt, now) };
}

type Pointer = Readonly<{ version: 1; current: Readonly<{ indexId: string; path: string; sha256: string }> }>;

export class WorldwideForecastClient {
  readonly #fetcher: typeof fetch;
  readonly #json = new Map<string, unknown>();
  readonly #blocks = new Map<string, Uint8Array>();

  constructor(fetcher?: typeof fetch) {
    this.#fetcher = fetcher ?? ((input, init) => globalThis.fetch(input, init));
  }

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

  async loadRangePoint(snapshot: ForecastSnapshot, longitude: number, latitude: number, profile: Profile, presentation: ForecastPresentation, signal?: AbortSignal, allowNearbyLand = false): Promise<ForecastWindowPoint> {
    const { index, range } = snapshot;
    for (const catalog of [range.regional, range]) {
      if (!catalog) continue;
      const location = xyzLocation(longitude, latitude, catalog.scalarMaxZoom);
      const blockRow = Math.floor(location.globalY / 64), blockColumn = Math.floor(location.globalX / 64);
      const block = catalog.pointBlocks.find((item) => item.row === blockRow && item.column === blockColumn);
      if (!block || block.omitted) continue;
      const bytes = await this.#block(`${catalog.root}/${requiredPath(block.path)}`, requiredInteger(block.bytes), requiredSHA(block.sha256), RANGE_MEDIA, signal);
      validateRangeHeader(bytes, catalog, blockRow, blockColumn, requiredSHA(block.payloadSha256));
      const local = (location.globalY % 64) * 64 + location.globalX % 64;
      const record = bytes.subarray(RANGE_HEADER_BYTES + local * RANGE_RECORD_BYTES, RANGE_HEADER_BYTES + (local + 1) * RANGE_RECORD_BYTES);
      if (record.every((value) => value === 0)) continue;
      const result = decodeRangeRecord(record, longitude, latitude, index, catalog, profile, presentation);
      await this.#confirmPointerID(index.indexId, signal);
      return result;
    }
    if (allowNearbyLand) {
      for (const candidate of nearbyDisplayCells(longitude, latitude, Boolean(range.regional))) {
        try {
          const nearby = await this.loadRangePoint(snapshot, candidate.longitude, candidate.latitude, profile, presentation, signal);
          return { ...nearby, lon: longitude, lat: latitude, sourceCell: candidate };
        } catch (error) {
          if (!(error instanceof ForecastDataError) || error.code !== 'unavailable') throw error;
        }
      }
    }
    throw new ForecastDataError('unavailable', 'There is no complete weather record here.');
  }

  async loadDailyPoint(product: DayForecastProduct, longitude: number, latitude: number, profile: Profile, presentation: ForecastPresentation, signal?: AbortSignal): Promise<DailyPoint> {
    const preferred = forecastTierForPoint(product, longitude, latitude);
    for (const catalog of [preferred, ...product.tiers.filter((tier) => tier !== preferred && tier.tier === 'global')]) {
      const point = await this.#dailyPoint(catalog, longitude, latitude, profile, presentation, signal);
      if (!point) continue;
      await this.#confirmPointerID(product.indexId, signal);
      return { ...point, releaseId: product.indexId };
    }
    throw new ForecastDataError('unavailable', 'Detailed forecast coverage is incomplete here.');
  }

  async loadPeriodEvidence(snapshot: ForecastSnapshot, longitude: number, latitude: number, profile: Profile, presentation: ForecastPresentation, signal?: AbortSignal): Promise<readonly DailyPoint[]> {
    const { index, range } = snapshot;
    const location = xyzLocation(longitude, latitude, 5);
    const withinRegionalCoverage = range.regional && declaredTile(range.regional, location.id);
    const points: DailyPoint[] = [];
    const loadDay = async (day: ForecastIndex['days'][number]): Promise<DailyPoint> => {
      const detail = withinRegionalCoverage ? day.tiers.find(({ tier }) => tier === 'europe-detail') : undefined;
      const global = day.tiers.find(({ tier }) => tier === 'global')!;
      for (const reference of detail ? [detail, global] : [global]) {
        const catalog = await this.#daily(index, reference, signal);
        const point = await this.#dailyPoint(catalog, longitude, latitude, profile, presentation, signal);
        if (point) return { ...point, releaseId: index.indexId };
      }
      throw new ForecastDataError('unavailable', 'Detailed forecast coverage is incomplete here.');
    };
    for (let offset = 0; offset < index.days.length; offset += 4) {
      points.push(...await Promise.all(index.days.slice(offset, offset + 4).map(loadDay)));
    }
    await this.#confirmPointerID(index.indexId, signal);
    return Object.freeze(points);
  }

  async #dailyPoint(catalog: DailyForecastCatalog, longitude: number, latitude: number, profile: Profile, presentation: ForecastPresentation, signal?: AbortSignal): Promise<DailyPoint | undefined> {
    const record = await this.#dailyRecord(catalog, longitude, latitude, signal);
    return record ? decodeDailyRecord(record, longitude, latitude, catalog, profile, presentation) : undefined;
  }

  scalarSourceForOutputTile(product: RangeForecastCatalog | DayForecastProduct, field: ForecastField, output: ScalarTileID): ScalarTileSource | undefined {
    if (!FIELDS.includes(field) || !tileID(output)) return undefined;
    if ('period' in product) {
      const regional = product.regional;
      if (output.z === 5 && regional && declaredTile(regional, output)) return scalarSource(regional, regional, field, output);
      return scalarSource(product, product, field, output);
    }
    const global = product.tiers.find(({ tier }) => tier === 'global');
    const detail = product.tiers.find(({ tier }) => tier === 'europe-detail');
    if (!global) throw new ForecastDataError('invalid');
    if (output.z === 5 && detail && declaredTile(detail, output)) {
      const primary = scalarSource(detail, detail, field, output);
      const fallback = scalarSource(global, global, field, output);
      return primary && fallback ? Object.freeze({ ...primary, fallback }) : undefined;
    }
    return scalarSource(global, global, field, output);
  }

  async #snapshot(signal?: AbortSignal): Promise<ForecastSnapshot> {
    const pointer = await this.#pointer(signal); const index = await this.#index(pointer, signal); const global = await this.#range(index, index.range, signal);
    const regional = index.regionalRange ? await this.#range(index, index.regionalRange, signal) : undefined;
    const range = regional ? Object.freeze({ ...global, regional }) : global;
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
    const cached = this.#json.get(key) as ForecastIndex | undefined; if (cached) { touch(this.#json, key, cached, 32); return cached; }
    const bytes = await this.#jsonBytes(`/forecast/${pointer.current.path}`, INDEX_BYTES, true, signal);
    if (await digest(bytes) !== pointer.current.sha256) throw new ForecastDataError('invalid');
    const index = parseIndex(parseJSON(bytes), pointer.current.indexId); insert(this.#json, key, index, 32); return index;
  }

  async #range(index: ForecastIndex, reference: RangeReference, signal?: AbortSignal): Promise<RangeForecastCatalog> {
    const key = `${reference.catalogPath}:${reference.catalogSha256}`;
    const cached = this.#json.get(key) as RangeForecastCatalog | undefined; if (cached) { touch(this.#json, key, cached, 32); return cached; }
    const bytes = await this.#jsonBytes(`/forecast/${reference.catalogPath}`, reference.catalogBytes, true, signal);
    if (bytes.byteLength !== reference.catalogBytes || await digest(bytes) !== reference.catalogSha256) throw new ForecastDataError('invalid');
    const root = `/forecast/${reference.catalogPath.slice(0, reference.catalogPath.lastIndexOf('/'))}`;
    const range = parseRange(parseJSON(bytes), index, reference, root); insert(this.#json, key, range, 32); return range;
  }

  async #daily(index: ForecastIndex, reference: TierReference, signal?: AbortSignal): Promise<DailyForecastCatalog> {
    const key = `${reference.catalogPath}:${reference.catalogSha256}`;
    const cached = this.#json.get(key) as DailyForecastCatalog | undefined; if (cached) { touch(this.#json, key, cached, 32); return cached; }
    const bytes = await this.#jsonBytes(`/forecast/${reference.catalogPath}`, reference.catalogBytes, true, signal);
    if (bytes.byteLength !== reference.catalogBytes || await digest(bytes) !== reference.catalogSha256) throw new ForecastDataError('invalid');
    const root = `/forecast/${reference.catalogPath.slice(0, reference.catalogPath.lastIndexOf('/'))}`;
    const catalog = parseDaily(parseJSON(bytes), index, reference, root); insert(this.#json, key, catalog, 32); return catalog;
  }

  async #dailyRecord(catalog: DailyForecastCatalog, lon: number, lat: number, signal?: AbortSignal): Promise<Uint8Array | undefined> {
    const selected = dailyPointBlock(catalog, lon, lat);
    if (!selected) return undefined;
    const { location, block } = selected;
    const bytes = await this.#block(`${catalog.root}/${block.path}`, block.bytes, block.sha256, DAILY_POINT_BLOCK_MEDIA, signal);
    validateDailyPointBlockHeader(bytes, catalog, block, block.payloadSha256);
    const local = (location.globalY % 64) * 64 + location.globalX % 64;
    const start = DAILY_POINT_BLOCK_HEADER_BYTES + local * DAILY_RECORD_BYTES;
    const record = bytes.subarray(start, start + DAILY_RECORD_BYTES);
    return record.every((value) => value === 0) ? undefined : record;
  }

  async #block(url: string, bytes: number, checksum: string, media: string, signal?: AbortSignal): Promise<Uint8Array> {
    const key = `${url}:${checksum}`, cached = this.#blocks.get(key); if (cached) return touch(this.#blocks, key, cached, 16);
    let response: Response; try { response = await this.#fetcher(url, { signal, cache: 'force-cache', headers: { Range: `bytes=0-${bytes - 1}` } }); } catch { throw new ForecastDataError('unavailable'); }
    if (response.status === 404 || response.status === 409 || response.status === 410) throw new ForecastDataError('invalid');
    if (response.status !== 206 || response.headers.get('Content-Type')?.toLowerCase() !== media || response.headers.get('Content-Range') !== `bytes 0-${bytes - 1}/${bytes}`) throw new ForecastDataError('invalid');
    const body = await readBoundedResponse(response, 'forecast point block', bytes);
    if (body.byteLength !== bytes || await digest(body) !== checksum) throw new ForecastDataError('invalid');
    insert(this.#blocks, key, body, 16); return body;
  }

  async #jsonBytes(path: string, maximum: number, immutable: boolean, signal?: AbortSignal): Promise<Uint8Array> {
    let response: Response; try { response = await this.#fetcher(path, { signal, cache: immutable ? 'force-cache' : 'no-cache' }); } catch { throw new ForecastDataError('unavailable'); }
    if (response.status === 404 || response.status === 409 || response.status === 410) throw new ForecastDataError(immutable ? 'invalid' : 'unavailable');
    if (!response.ok) throw new ForecastDataError('unavailable');
    if (response.headers.get('Content-Type')?.split(';')[0].trim().toLowerCase() !== 'application/json') throw new ForecastDataError('invalid');
    const bytes = await readBoundedResponse(response, 'forecast JSON', maximum);
    if (bytes.byteLength < 1) throw new ForecastDataError('invalid');
    return bytes;
  }

  async #confirm(pointer: Pointer, signal?: AbortSignal) { const after = await this.#pointer(signal); if (after.current.indexId !== pointer.current.indexId || after.current.sha256 !== pointer.current.sha256) throw new ForecastDataError('stale'); }
  async #confirmPointerID(indexId: string, signal?: AbortSignal) { if ((await this.#pointer(signal)).current.indexId !== indexId) throw new ForecastDataError('stale'); }
}

function parseIndex(value: unknown, indexId: string): ForecastIndex {
  const row = exact(value, ['version', 'productContract', 'indexId', 'firstDate', 'lastDate', 'tierSelection', 'days', 'range', 'regionalRange'], ['regionalRange']);
  if (row.version !== 3 || row.productContract !== 'nice-weather-forecast-run-v7' || identifier(row.indexId) !== indexId) throw new ForecastDataError('invalid');
  const selection = exact(row.tierSelection, ['method', 'priority', 'blend']);
  if (selection.method !== 'choose-one-complete-tier-before-colour' || selection.blend !== false || JSON.stringify(selection.priority) !== '["europe-detail","global"]') throw new ForecastDataError('invalid');
  const days = list(row.days).map((entry) => {
    const day = exact(entry, ['date', 'tiers']); const dateValue = date(day.date);
    const tiers = list(day.tiers).map((tier) => parseTier(tier, dateValue, indexId));
    if (tiers.length < 1 || tiers.length > 2 || tiers[tiers.length - 1].tier !== 'global' || (tiers.length === 2 && tiers[0].tier !== 'europe-detail')) throw new ForecastDataError('invalid');
    return Object.freeze({ date: dateValue, tiers: Object.freeze(tiers) });
  });
  if (days.length !== 14 || days.some((day, i) => day.date !== addDays(days[0].date, i))) throw new ForecastDataError('invalid');
  const range = parseRangeReference(row.range, indexId, 'global');
  const regionalRange = row.regionalRange === undefined ? undefined : parseRangeReference(row.regionalRange,indexId,'europe-detail');
  if (Boolean(regionalRange) !== days.some(({ tiers }) => tiers.some(({ tier }) => tier === 'europe-detail'))) throw new ForecastDataError('invalid');
  const firstDate = date(row.firstDate), lastDate = date(row.lastDate);
  if (firstDate !== days[0].date || lastDate !== days[13].date) throw new ForecastDataError('invalid');
  return Object.freeze({ version: 3, productContract: 'nice-weather-forecast-run-v7', indexId, firstDate, lastDate, days: Object.freeze(days), range, regionalRange });
}

function parseTier(value: unknown, dateValue: string, indexId: string): TierReference {
  const row = exact(value, ['releaseId', 'date', 'provider', 'tier', 'product', 'runUtc', 'nativeResolutionKm', 'sourceEvidenceSha256', 'catalogPath', 'catalogBytes', 'catalogSha256']);
  const tier = row.tier === 'global' ? 'global' : row.tier === 'europe-detail' ? 'europe-detail' : fail();
  const provider = text(row.provider), product = text(row.product), runUtc = timestamp(row.runUtc), resolution = positive(row.nativeResolutionKm);
  if (row.date !== dateValue || (tier === 'global' ? provider !== 'ecmwf-ifs' || resolution !== 28 : provider !== 'dwd-icon-eu' || resolution !== 7)) throw new ForecastDataError('invalid');
  const catalogPath = safePath(row.catalogPath);
  if (catalogPath !== `runs/${indexId}/forecast-days/${dateValue}/${tier}/catalog.json`) throw new ForecastDataError('invalid');
  return Object.freeze({ releaseId: identifier(row.releaseId), date: dateValue, provider, tier, product, runUtc, nativeResolutionKm: resolution, sourceEvidenceSha256: sha(row.sourceEvidenceSha256), catalogPath, catalogBytes: boundedInteger(row.catalogBytes, 1, DAILY_CATALOG_BYTES), catalogSha256: sha(row.catalogSha256) });
}

function parseRangeReference(value: unknown, indexId: string, tier: ForecastTier): RangeReference {
  const row = exact(value, ['period', 'provider', 'tier', 'product', 'runUtc', 'nativeResolutionKm', 'staleAfterHours', 'catalogPath', 'catalogBytes', 'catalogSha256']);
  const catalogPath = safePath(row.catalogPath);
  if (row.period !== 'next-14-days' || row.provider !== 'ecmwf-ifs' || row.tier !== tier || row.nativeResolutionKm !== 28 || !supportedStaleAfterHours(row.staleAfterHours) || catalogPath !== `runs/${indexId}/forecast-ranges/next-14-days/${tier}/catalog.json`) throw new ForecastDataError('invalid');
  return Object.freeze({ period: 'next-14-days', provider: 'ecmwf-ifs', tier, product: text(row.product), runUtc: timestamp(row.runUtc), nativeResolutionKm: 28, staleAfterHours: row.staleAfterHours, catalogPath, catalogBytes: boundedInteger(row.catalogBytes, 1, RANGE_CATALOG_BYTES), catalogSha256: sha(row.catalogSha256) });
}

function parseDaily(value: unknown, index: ForecastIndex, reference: TierReference, root: string): DailyForecastCatalog {
  const row = exact(value, ['version', 'releaseId', 'localDate', 'provider', 'tier', 'grid', 'evidence', 'land', 'scalarMaxZoom', 'scores', 'coverage', 'dailyPointBlocks', 'scalarCoverage', 'scalarTiles']);
  if (row.version !== 4 || row.releaseId !== reference.releaseId || row.localDate !== reference.date || row.provider !== reference.provider || row.tier !== reference.tier) throw new ForecastDataError('invalid');
  const gridRow = exact(row.grid, ['projection', 'west', 'east', 'south', 'north', 'rows', 'columns', 'cellDegrees', 'cellRegistration']);
  const grid = Object.freeze({ west: finite(gridRow.west), east: finite(gridRow.east), south: finite(gridRow.south), north: finite(gridRow.north), rows: positiveInteger(gridRow.rows), columns: positiveInteger(gridRow.columns) });
  const expected = reference.tier === 'global' ? [-180, 180, -90, 90, 720, 1440, 3] : [-24, 45, 29, 71.5, 680, 1104, 5];
  if (gridRow.projection !== 'EPSG:4326' || gridRow.cellRegistration !== 'cell-centre' || [grid.west, grid.east, grid.south, grid.north, grid.rows, grid.columns, row.scalarMaxZoom].some((value, i) => value !== expected[i])) throw new ForecastDataError('invalid');
  const evidenceRow = exact(row.evidence, ['product', 'runUtc', 'nativeResolutionKm', 'sourceEvidenceSha256']);
  const evidence = Object.freeze({ product: text(evidenceRow.product), runUtc: timestamp(evidenceRow.runUtc), nativeResolutionKm: positive(evidenceRow.nativeResolutionKm), sourceEvidenceSha256: sha(evidenceRow.sourceEvidenceSha256) });
  if (evidence.product !== reference.product || evidence.runUtc !== reference.runUtc || evidence.nativeResolutionKm !== reference.nativeResolutionKm || evidence.sourceEvidenceSha256 !== reference.sourceEvidenceSha256) throw new ForecastDataError('invalid');
  const landRow = exact(row.land, ['authority', 'dataset', 'version', 'sourceArchiveSha256', 'maskSha256', 'derivationContract']);
  if (landRow.authority !== 'Natural Earth' || landRow.dataset !== 'ne_10m_land' || landRow.version !== '5.1.1' || landRow.derivationContract !== 'web-mercator-centre-on-land-v1') throw new ForecastDataError('invalid');
  const land = Object.freeze({ authority: 'Natural Earth' as const, dataset: 'ne_10m_land' as const, version: '5.1.1' as const, sourceArchiveSha256: sha(landRow.sourceArchiveSha256), maskSha256: sha(landRow.maskSha256), derivationContract: 'web-mercator-centre-on-land-v1' as const });
  parseScores(row.scores, false, reference.tier);
  const dailyPointBlocks = Object.freeze(list(row.dailyPointBlocks).map((item) => parseDailyPointBlock(item, reference.date, reference.tier)));
  const scalarCoverage = Object.freeze(list(row.scalarCoverage).map(parseCoverage));
  const scalarTiles = Object.freeze(list(row.scalarTiles).map(parseScalar));
  validateScalarCollections(scalarCoverage, scalarTiles, row.scalarMaxZoom as 3 | 5);
  validateDailyPointBlocks(dailyPointBlocks, scalarCoverage, row.scalarMaxZoom as 3 | 5);
  return Object.freeze({ version: 4, releaseId: reference.releaseId, localDate: reference.date, provider: reference.provider, tier: reference.tier, grid, evidence, land, scalarMaxZoom: row.scalarMaxZoom as 3 | 5, dailyPointBlocks, scalarCoverage, scalarTiles, root });
}

function parseRange(value: unknown, index: ForecastIndex, reference: RangeReference, root: string): RangeForecastCatalog {
  const row = exact(value, ['version', 'productContract', 'releaseId', 'period', 'dates', 'source', 'grid', 'scores', 'pointBlockLayout', 'inputs', 'regionalInputs', 'coverage', 'pointBlocks', 'scalarCoverage', 'scalarTiles'], ['regionalInputs']);
  if (row.version !== 1 || row.productContract !== 'nice-weather-forecast-range-v3' || row.releaseId !== index.indexId || row.period !== 'next-14-days') throw new ForecastDataError('invalid');
  const dates = list(row.dates).map(date); if (JSON.stringify(dates) !== JSON.stringify(index.days.map(({ date }) => date))) throw new ForecastDataError('invalid');
  const sourceRow = exact(row.source, ['provider', 'tier', 'product', 'runUtc', 'nativeResolutionKm', 'staleAfterHours']);
  if (sourceRow.provider !== 'ecmwf-ifs' || sourceRow.tier !== 'global' || sourceRow.nativeResolutionKm !== 28 || !supportedStaleAfterHours(sourceRow.staleAfterHours) || sourceRow.staleAfterHours !== reference.staleAfterHours || timestamp(sourceRow.runUtc) !== reference.runUtc) throw new ForecastDataError('invalid');
  const source = Object.freeze({ provider: 'ecmwf-ifs' as const, tier: 'global' as const, product: text(sourceRow.product), runUtc: reference.runUtc, nativeResolutionKm: 28 as const, staleAfterHours: 36 as const });
  if (source.product !== reference.product) throw new ForecastDataError('invalid');
  const maximumZoom = reference.tier === 'europe-detail' ? 5 : 3;
  const grid = exact(row.grid, ['projection', 'zoom', 'tileSize', 'rows', 'columns']); if (grid.projection !== 'EPSG:3857-XYZ' || grid.zoom !== maximumZoom || grid.tileSize !== 256 || grid.rows !== 256 * 2 ** maximumZoom || grid.columns !== 256 * 2 ** maximumZoom) throw new ForecastDataError('invalid');
  parseScores(row.scores, true);
  const layout = exact(row.pointBlockLayout, ['rows', 'columns', 'headerBytes', 'recordBytes', 'mediaType']); if (layout.rows !== 64 || layout.columns !== 64 || layout.headerBytes !== 256 || layout.recordBytes !== 44 || layout.mediaType !== RANGE_MEDIA) throw new ForecastDataError('invalid');
  const inputs = list(row.inputs).map((entry) => {
    const input = exact(entry, ['date', 'path', 'bytes', 'sha256']);
    return { date: date(input.date), path: safePath(input.path), bytes: boundedInteger(input.bytes, 1, DAILY_CATALOG_BYTES), sha256: sha(input.sha256) };
  });
  const expectedInputs = index.days.map((day) => {
    const global = day.tiers.find(({ tier }) => tier === 'global');
    if (!global) throw new ForecastDataError('invalid');
    return { date: day.date, path: global.catalogPath.replace(`runs/${index.indexId}/`, ''), bytes: global.catalogBytes, sha256: global.catalogSha256 };
  });
  if (JSON.stringify(inputs) !== JSON.stringify(expectedInputs)) throw new ForecastDataError('invalid');
  const regionalInputs = row.regionalInputs === undefined ? [] : list(row.regionalInputs).map((entry) => { const input=exact(entry,['date','path','bytes','sha256']); return { date:date(input.date),path:safePath(input.path),bytes:boundedInteger(input.bytes,1,DAILY_CATALOG_BYTES),sha256:sha(input.sha256) }; });
  const expectedRegional = reference.tier === 'europe-detail' ? index.days.flatMap((day) => day.tiers.filter(({ tier }) => tier === 'europe-detail').map((tier) => ({ date:day.date,path:tier.catalogPath.replace(`runs/${index.indexId}/`,''),bytes:tier.catalogBytes,sha256:tier.catalogSha256 }))) : [];
  if (JSON.stringify(regionalInputs) !== JSON.stringify(expectedRegional)) throw new ForecastDataError('invalid');
  const pointBlocks = Object.freeze(list(row.pointBlocks).map((item) => { const block = exact(item, ['row', 'column', 'omitted', 'path', 'bytes', 'sha256', 'payloadSha256', 'dataRecords', 'noDataRecords'], ['path', 'bytes', 'sha256', 'payloadSha256']); return Object.freeze({ row: boundedInteger(block.row, 0, 4 * 2 ** maximumZoom - 1), column: boundedInteger(block.column, 0, 4 * 2 ** maximumZoom - 1), omitted: boolean(block.omitted), ...(block.omitted ? {} : { path: safePath(block.path), bytes: boundedInteger(block.bytes, RANGE_HEADER_BYTES + RANGE_RECORD_BYTES, 200_000), sha256: sha(block.sha256), payloadSha256: sha(block.payloadSha256) }) }); }));
  const scalarCoverage = Object.freeze(list(row.scalarCoverage).map(parseRangeCoverage)); const scalarTiles = Object.freeze(list(row.scalarTiles).map(parseScalar));
  validateScalarCollections(scalarCoverage, scalarTiles, maximumZoom);
  return Object.freeze({ version: 1, productContract: 'nice-weather-forecast-range-v3', releaseId: index.indexId, period: 'next-14-days', dates: Object.freeze(dates), source, pointBlocks, scalarCoverage, scalarTiles, root, tier:reference.tier, scalarMaxZoom:maximumZoom });
}

function supportedStaleAfterHours(value: unknown): value is 36 { return value === 36; }

function parseScores(value: unknown, range = false, tier?: ForecastTier) {
  const fields = exact(value, range ? ['unit', 'noDataValue', 'mediaType', 'profiles', 'fields'] : ['unit', 'noDataValue', 'fallbackValue', 'profiles', 'fields'], range || tier === 'global' ? ['fallbackValue'] : []);
  if (fields.unit !== 'canonical-score-0-100' || fields.noDataValue !== 255 || (range && fields.mediaType !== SCALAR_MEDIA) || (tier === 'europe-detail' ? fields.fallbackValue !== 254 : fields.fallbackValue !== undefined) || JSON.stringify(fields.profiles) !== '["nice","cycling"]') throw new ForecastDataError('invalid');
  const contracts = list(fields.fields).map((entry) => { const row = exact(entry, ['id', 'profile']); return [text(row.id), text(row.profile)]; });
  const expected = [['thermal', 'shared'], ['dryness', 'shared'], ['sun', 'shared'], ['nice_score', 'nice'], ['nice_wind', 'nice'], ['cycling_score', 'cycling'], ['cycling_wind', 'cycling']];
  if (JSON.stringify(contracts) !== JSON.stringify(expected)) throw new ForecastDataError('invalid');
}

function parseDailyPointBlock(value: unknown, period: string, tier: ForecastTier): ForecastDailyPointBlock { const row = exact(value, ['zoom', 'x', 'y', 'path', 'bytes', 'sha256', 'payloadSha256', 'mediaType', 'dataRecords', 'noDataRecords']); const result = { zoom: boundedInteger(row.zoom, 0, 5), x: nonNegativeInteger(row.x), y: nonNegativeInteger(row.y), path: safePath(row.path), bytes: boundedInteger(row.bytes, 1, DAILY_POINT_BLOCK_BYTES), sha256: sha(row.sha256), payloadSha256: sha(row.payloadSha256), mediaType: text(row.mediaType), dataRecords: boundedInteger(row.dataRecords, 1, 4096), noDataRecords: boundedInteger(row.noDataRecords, 0, 4095) }; if (result.path !== `points/${period}/${tier}/${result.zoom}/${result.x}/${result.y}.nwt` || result.bytes !== DAILY_POINT_BLOCK_BYTES || result.mediaType !== DAILY_POINT_BLOCK_MEDIA || result.x >= 2 ** (result.zoom + 2) || result.y >= 2 ** (result.zoom + 2) || result.dataRecords + result.noDataRecords !== 4096) throw new ForecastDataError('invalid'); return Object.freeze(result); }
function parseCoverage(value: unknown): ForecastCoverage { const row = exact(value, ['zoom', 'x', 'y', 'omitted', 'dataRecords', 'fallbackRecords', 'noDataRecords']); const result = { zoom: boundedInteger(row.zoom, 0, 5), x: nonNegativeInteger(row.x), y: nonNegativeInteger(row.y), omitted: boolean(row.omitted), dataRecords: boundedInteger(row.dataRecords, 0, 65_536), fallbackRecords: boundedInteger(row.fallbackRecords, 0, 65_536), noDataRecords: boundedInteger(row.noDataRecords, 0, 65_536) }; if (result.dataRecords + result.fallbackRecords + result.noDataRecords !== 65_536 || result.omitted !== (result.dataRecords + result.fallbackRecords === 0)) throw new ForecastDataError('invalid'); return Object.freeze(result); }
function parseRangeCoverage(value: unknown): ForecastCoverage { const row = exact(value, ['zoom', 'x', 'y', 'omitted', 'dataRecords', 'noDataRecords']); const result = { zoom: boundedInteger(row.zoom, 0, 5), x: nonNegativeInteger(row.x), y: nonNegativeInteger(row.y), omitted: boolean(row.omitted), dataRecords: boundedInteger(row.dataRecords, 0, 65_536), fallbackRecords: 0, noDataRecords: boundedInteger(row.noDataRecords, 0, 65_536) }; if (result.dataRecords + result.noDataRecords !== 65_536 || result.omitted !== (result.dataRecords === 0)) throw new ForecastDataError('invalid'); return Object.freeze(result); }
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

function validateDailyPointBlocks(blocks: readonly ForecastDailyPointBlock[], coverage: readonly ForecastCoverage[], maximumZoom: number) {
  const keys = blocks.map(({ zoom, x, y }) => `${zoom}/${x}/${y}`);
  if (new Set(keys).size !== blocks.length || blocks.some(({ zoom }) => zoom !== maximumZoom)) throw new ForecastDataError('invalid');
  const expected = new Map(coverage.filter(({ dataRecords }) => dataRecords > 0).map(({ zoom, x, y, dataRecords }) => [`${zoom}/${x}/${y}`, dataRecords]));
  if (!blocks.length && expected.size > 0) throw new ForecastDataError('invalid');
  const actual = new Map<string, number>();
  for (const { zoom, x, y, dataRecords } of blocks) {
    const parent = `${zoom}/${Math.floor(x / 4)}/${Math.floor(y / 4)}`;
    actual.set(parent, (actual.get(parent) ?? 0) + dataRecords);
  }
  if (actual.size !== expected.size || [...actual].some(([key, count]) => expected.get(key) !== count)) throw new ForecastDataError('invalid');
}

function declaredTile(catalog: Readonly<{ scalarCoverage: readonly ForecastCoverage[] }>, tile: ScalarTileID): boolean {
  let available = scalarCoverageIndexes.get(catalog.scalarCoverage);
  if (!available) {
    available = new Set(catalog.scalarCoverage.filter((item) => !item.omitted).map(({ zoom, x, y }) => `${zoom}/${x}/${y}`));
    scalarCoverageIndexes.set(catalog.scalarCoverage, available);
  }
  return available.has(`${tile.z}/${tile.x}/${tile.y}`);
}

export function forecastDetailDeclared(product: DayForecastProduct | RangeForecastCatalog, tile: ScalarTileID): boolean {
  const detail = 'tiers' in product ? product.tiers.find(({ tier }) => tier === 'europe-detail') : product.regional;
  return Boolean(detail && tile.z === 5 && declaredTile(detail, tile));
}

function scalarSource(catalog: Readonly<{ root: string; tier?: ForecastTier; scalarTiles: readonly ForecastScalarTile[]; scalarMaxZoom?: 3 | 5 }>, _identity: unknown, field: ForecastField, output: ScalarTileID): ScalarTileSource | undefined {
  const maximum = catalog.scalarMaxZoom ?? 3;
  const sourceZoom = Math.min(output.z, maximum), factor = 2 ** (output.z - sourceZoom);
  const x = Math.floor(output.x / factor), y = Math.floor(output.y / factor);
  let index = scalarTileIndexes.get(catalog.scalarTiles);
  if (!index) {
    index = new Map(catalog.scalarTiles.map((item) => [`${item.field}/${item.zoom}/${item.x}/${item.y}`, item]));
    scalarTileIndexes.set(catalog.scalarTiles, index);
  }
  const tile = index.get(`${field}/${sourceZoom}/${x}/${y}`);
  if (!tile) return undefined;
  const childX = output.x % factor, childY = output.y % factor;
  return Object.freeze({
    key: `${catalog.root}/${tile.path}:${tile.sha256}:${output.z}/${output.x}/${output.y}`,
    url: `${catalog.root}/${tile.path}`, sha256: tile.sha256,
    ...(catalog.tier === 'europe-detail' ? { allowFallback: true } : {}),
    ...(factor === 1 ? {} : { textureBounds: [childX / factor, childY / factor, (childX + 1) / factor, (childY + 1) / factor] as const }),
  });
}

export function forecastTierForPoint(product: DayForecastProduct, lon: number, lat: number): DailyForecastCatalog {
  const detail = product.tiers.find(({ tier }) => tier === 'europe-detail');
  if (detail && dailyPointBlock(detail, lon, lat)) return detail;
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

function dailyPointBlock(catalog: DailyForecastCatalog, longitude: number, latitude: number) {
  const location = xyzLocation(longitude, latitude, catalog.scalarMaxZoom);
  const x = Math.floor(location.globalX / 64), y = Math.floor(location.globalY / 64);
  const block = catalog.dailyPointBlocks.find((item) => item.zoom === catalog.scalarMaxZoom && item.x === x && item.y === y);
  return block ? { location, block } : undefined;
}

function validateRangeHeader(bytes: Uint8Array, catalog: RangeForecastCatalog, row: number, column: number, payloadSHA: string) {
  if (bytes.byteLength < RANGE_HEADER_BYTES || ascii(bytes, 0, 8) !== 'NWFRANGE') throw new ForecastDataError('invalid');
  const view = dataView(bytes); if (view.getUint16(8, true) !== 1 || view.getUint16(10, true) !== 256 || view.getUint16(12, true) !== 44 || view.getUint16(14, true) !== 64 || view.getUint16(16, true) !== 64 || bytes[18] !== catalog.scalarMaxZoom || bytes[19] !== 1 || view.getUint32(20, true) !== row || view.getUint32(24, true) !== column || view.getUint32(28, true) !== 256 * 2 ** catalog.scalarMaxZoom || view.getUint32(32, true) !== 256 * 2 ** catalog.scalarMaxZoom || !allZero(bytes.subarray(39, 64)) || hex(bytes.subarray(192, 224)) !== payloadSHA || !allZero(bytes.subarray(224, 256))) throw new ForecastDataError('invalid');
  const releaseLength = bytes[36], periodLength = bytes[37], tierLength = bytes[38];
  if (releaseLength > 64 || periodLength > 32 || tierLength > 32 || ascii(bytes, 64, 64 + releaseLength) !== catalog.releaseId || ascii(bytes, 128, 128 + periodLength) !== 'next-14-days' || ascii(bytes, 160, 160 + tierLength) !== catalog.tier || !allZero(bytes.subarray(64 + releaseLength, 128)) || !allZero(bytes.subarray(128 + periodLength, 160)) || !allZero(bytes.subarray(160 + tierLength, 192))) throw new ForecastDataError('invalid');
}

function validateDailyPointBlockHeader(bytes: Uint8Array, catalog: DailyForecastCatalog, block: ForecastDailyPointBlock, payloadSHA: string) {
  if (bytes.byteLength !== DAILY_POINT_BLOCK_BYTES || ascii(bytes, 0, 8) !== 'NWFXYZPT') throw new ForecastDataError('invalid'); const view = dataView(bytes);
  const releaseLength = bytes[26], periodLength = bytes[27], tierLength = bytes[28];
  if (view.getUint16(8, true) !== 2 || view.getUint16(10, true) !== DAILY_POINT_BLOCK_HEADER_BYTES || view.getUint16(12, true) !== DAILY_RECORD_BYTES || view.getUint16(14, true) !== DAILY_POINT_BLOCK_SIDE || bytes[16] !== block.zoom || bytes[17] !== 1 || view.getUint32(18, true) !== block.x || view.getUint32(22, true) !== block.y || releaseLength > 64 || periodLength > 32 || tierLength > 32 || !allZero(bytes.subarray(29, 32)) || ascii(bytes, 32, 32 + releaseLength) !== catalog.releaseId || ascii(bytes, 96, 96 + periodLength) !== catalog.localDate || ascii(bytes, 128, 128 + tierLength) !== catalog.tier || !allZero(bytes.subarray(32 + releaseLength, 96)) || !allZero(bytes.subarray(96 + periodLength, 128)) || !allZero(bytes.subarray(128 + tierLength, 160)) || hex(bytes.subarray(160, 192)) !== payloadSHA) throw new ForecastDataError('invalid');
}

function decodeRangeRecord(bytes: Uint8Array, lon: number, lat: number, index: ForecastIndex, catalog: RangeForecastCatalog, profile: Profile, presentation: ForecastPresentation): ForecastWindowPoint {
  if (bytes.byteLength !== 44 || bytes.every((value) => value === 0)) throw new ForecastDataError('unavailable', 'There is no complete weather record here.');
  if (bytes[43] !== 1 || bytes.subarray(0, 43).some((value) => value > 100)) throw new ForecastDataError('invalid');
  const offset = profile === 'nice' ? 3 : 9, dayOffset = profile === 'nice' ? 15 : 29, score = bytes[offset];
  const scores = Array.from(bytes.subarray(dayOffset, dayOffset + 14)); const legend = presentation.legend;
  return { kind: 'range', releaseId: index.indexId, lat, lon, periodLabel: 'Next 14 days', score, category: rating(score, legend), components: components(bytes[0], bytes[1], bytes[2], bytes[offset + 2], legend), window: { startDate: index.firstDate, endDate: index.lastDate, dayCount: 14, niceDays: bytes[offset + 3], awfulDays: bytes[offset + 4], medianScore: bytes[offset + 1], longestPoorRun: bytes[offset + 5] }, days: scores.map((value, i) => ({ date: catalog.dates[i], score: value, category: rating(value, legend) })), source: { basis: 'forecast-window', detailLabel: catalog.tier === 'europe-detail' ? 'European detail, then worldwide forecast' : 'Worldwide forecast', resolutionKm: 28, artifactGeneratedAt: presentation.generatedAt } };
}

function decodeDailyRecord(bytes: Uint8Array, lon: number, lat: number, catalog: DailyForecastCatalog, profile: Profile, presentation: ForecastPresentation): DailyPoint {
  if (bytes.byteLength !== 30) throw new ForecastDataError('invalid'); const view = dataView(bytes), flags = bytes[20];
  if ((flags & 1) === 0 || (flags & ~0x7f) !== 0 || bytes[21] !== 0) throw new ForecastDataError('invalid');
  if (bytes.subarray(22, 29).some((value) => value > 100)) throw new ForecastDataError('invalid');
  const dew = view.getInt16(6, true), gust = view.getUint16(12, true), solar = view.getUint16(14, true), wet = view.getUint16(16, true), cloud = bytes[18], humidity = bytes[19];
  const offset = profile === 'nice' ? 25 : 27, score = bytes[offset], temperature = view.getInt16(0, true) / 10;
  const pointComponents = components(bytes[22], bytes[23], bytes[24], bytes[offset + 1], presentation.legend);
  const temperatureNotice = bytes[29] >= 160 ? 'cold' as const : bytes[29] >= 80 ? 'hot' as const : undefined;
  return { kind: 'daily', releaseId: catalog.releaseId, lat, lon, periodLabel: new Intl.DateTimeFormat('en-GB', { weekday: 'long', day: 'numeric', month: 'short', timeZone: 'UTC' }).format(new Date(`${catalog.localDate}T12:00:00Z`)), score, category: rating(score, presentation.legend), components: pointComponents, summary: dailySummary(temperature, bytes[29]), temperatureNotice, metrics: { temperatureC: temperature, minTempC: view.getInt16(2, true) / 10, maxTempC: view.getInt16(4, true) / 10, rainMm: view.getUint16(8, true) / 10, windKmh: view.getUint16(10, true) / 10, dewPointC: flags & 2 ? dew / 10 : undefined, gustKmh: flags & 4 ? gust / 10 : undefined, solarEnergyMJm2: flags & 8 ? solar / 100 : undefined, cloudPct: flags & 16 ? cloud : undefined, humidityPct: flags & 32 ? humidity : undefined, wetTimeFraction: flags & 64 ? wet / 1000 : undefined }, source: { basis: 'forecast', detailLabel: catalog.tier === 'europe-detail' ? 'Detailed forecast' : 'Worldwide forecast', providerLabel: catalog.tier === 'europe-detail' ? 'European detail' : 'Worldwide forecast', resolutionKm: catalog.evidence.nativeResolutionKm, temporalResolutionHours: catalog.tier === 'europe-detail' ? 1 : 6, artifactGeneratedAt: presentation.generatedAt, runAt: catalog.evidence.runUtc, validFrom: `${catalog.localDate}T00:00:00Z`, validTo: `${addDays(catalog.localDate, 1)}T00:00:00Z` } };
}

function components(comfort: number, dryness: number, sun: number, wind: number, legend: readonly LegendCategory[]) { const value = (score: number) => ({ value: score, category: rating(score, legend) }); return { comfort: value(comfort), dryness: value(dryness), sun: value(sun), wind: value(wind) }; }
function rating(score: number, legend: readonly LegendCategory[]) { const found = legend.find(({ min, max }) => score >= min && score <= max); if (!found) throw new ForecastDataError('invalid'); return found.label; }
export function dailySummary(temperature: number, code: number): string {
  if (!Number.isInteger(code) || code < 0 || code >= 240) throw new ForecastDataError('invalid');
  const thermal = Math.floor(code / 80), rest = code % 80, wind = Math.floor(rest / 16), sky = Math.floor((rest % 16) / 4), rain = rest % 4;
  const parts = [...(thermal ? [['too hot', 'too cold'][thermal - 1]] : []), ['dry', 'brief rain', 'some rain', 'wet'][rain], ['overcast', 'mixed cloud', 'bright', 'sunny'][sky], ['light wind', 'breezy', 'windy', 'strong gusts', 'severe gusts'][wind]];
  const roundedTemperature = temperature < 0 ? -Math.round(-temperature) : Math.round(temperature);
  return `${roundedTemperature}°C · ${parts.join(' · ')}`;
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

function nearbyDisplayCells(longitude: number, latitude: number, regional: boolean) {
  const candidates: { longitude:number;latitude:number;distanceKm:number }[]=[];
  for (const zoom of regional ? [5,3] : [3]) {
    const location=xyzLocation(longitude,latitude,zoom), size=256*2**zoom;
    for (let y=-1;y<=1;y++) for(let x=-1;x<=1;x++) {
      if (x===0 && y===0) continue;
      const gx=(location.globalX+x+size)%size,gy=location.globalY+y;
      if(gy<0 || gy>=size) continue;
      const lon=(gx+0.5)/size*360-180,lat=Math.atan(Math.sinh(Math.PI*(1-2*(gy+0.5)/size)))*180/Math.PI;
      const radians=Math.PI/180,deltaLat=(lat-latitude)*radians,deltaLon=(lon-longitude)*radians;
      const a=Math.sin(deltaLat/2)**2+Math.cos(latitude*radians)*Math.cos(lat*radians)*Math.sin(deltaLon/2)**2;
      const distanceKm=6371*2*Math.atan2(Math.sqrt(a),Math.sqrt(1-a));
      if(distanceKm<=30) candidates.push({longitude:lon,latitude:lat,distanceKm});
    }
  }
  return candidates.sort((a,b)=>a.distanceKm-b.distanceKm);
}
