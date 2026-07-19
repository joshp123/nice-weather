import type {
  DailyPoint, ForecastWindowPoint, LegendCategory, Period, PointAsset, PointRequest,
  PointWeather, WeatherImage, WeatherManifest,
} from './types';

const MANIFEST_VERSION = 8;
const PRODUCT_CONTRACT = 'nice-weather-v17';
const POINT_HEADER_BYTES = 128;
const MAX_MERCATOR_LATITUDE = 85.0511287798066;
const MAPLIBRE_TILE_EXTENT = 8192;
const DENSE_FORECAST_SCORE_COLS = 1104;
const pointHeaderCache = new Map<string, ParsedPointHeader>();
let pointCacheRelease = '';

export class StaleReleaseError extends Error {
  readonly code = 'stale_release';

  constructor(message = 'Weather data changed. Refreshing the forecast.') {
    super(message);
    this.name = 'StaleReleaseError';
  }
}

export async function loadManifest(signal?: AbortSignal): Promise<WeatherManifest> {
  const response = await fetch('/manifest.json', { signal });
  if (!response.ok) throw new Error('The forecast is not available right now.');
  try {
    return parseManifest(await response.json());
  } catch (error) {
    console.error('[nice-weather] Invalid weather manifest.', error);
    throw new Error('The forecast could not be read.');
  }
}

export async function loadPoint(request: PointRequest, manifest: WeatherManifest, signal?: AbortSignal): Promise<PointWeather> {
  if (request.releaseId !== manifest.releaseId) throw new StaleReleaseError();
  if (pointCacheRelease !== manifest.releaseId) {
    pointHeaderCache.clear();
    pointCacheRelease = manifest.releaseId;
  }
  const target = pointTarget(request, manifest);
  try {
    const bytes = await loadPointRecord(request, target.asset, signal);
    const displayBytes = target.display ? await loadPointRecord(request, target.display, signal) : undefined;
    return target.kind === 'range'
      ? decodeRangePoint(bytes, request, manifest)
      : decodeDailyPoint(bytes, request, manifest, target.period!, target.kind === 'climate', displayBytes);
  } catch (error) {
    if (error instanceof StaleReleaseError || (error instanceof Error && error.message === 'There is no complete weather record here.')) throw error;
    console.error('[nice-weather] Invalid static point asset.', error);
    throw new Error('Weather details could not be read.');
  }
}

async function loadPointRecord(request: PointRequest, asset: PointAsset, signal?: AbortSignal): Promise<Uint8Array> {
  const url = pointAssetUrl(request.releaseId, asset);
  const header = pointHeaderCache.get(url) ?? await loadPointHeader(url, asset, signal);
  pointHeaderCache.set(url, header);
  const index = pointIndex(header, request.lon, request.lat);
  if (index === undefined) throw new Error('There is no complete weather record here.');
  const start = POINT_HEADER_BYTES + index * header.recordBytes;
  if (!Number.isSafeInteger(start) || start + header.recordBytes > header.bytes) throw new Error('Point record offset is unsafe.');
  return rangeBytes(url, start, start + header.recordBytes - 1, header.bytes, signal);
}

export function imageAssetUrl(release: string, image: WeatherImage): string {
  const safeRelease = releaseId(release, 'releaseId');
  const hashAt = image.url.indexOf('#');
  const fragment = hashAt >= 0 ? image.url.slice(hashAt) : '';
  const withoutFragment = hashAt >= 0 ? image.url.slice(0, hashAt) : image.url;
  const queryAt = withoutFragment.indexOf('?');
  const path = imagePath(queryAt >= 0 ? withoutFragment.slice(0, queryAt) : withoutFragment, 'image URL');
  const query = new URLSearchParams(queryAt >= 0 ? withoutFragment.slice(queryAt + 1) : '');
  query.set('v', image.sha256);
  return `/releases/${encodeURIComponent(safeRelease)}/${path}?${query.toString()}${fragment}`;
}

function parseManifest(value: unknown): WeatherManifest {
  const data = exactObject(value, ['version', 'releaseId', 'productContract', 'dataStatus', 'generatedAt', 'region', 'profiles', 'legend', 'palette', 'forecast', 'climate'], 'manifest', ['climate']);
  if (data.version !== MANIFEST_VERSION) throw new Error(`Unsupported manifest version ${String(data.version)}.`);
  if (data.productContract !== PRODUCT_CONTRACT) throw new Error(`Unsupported product contract ${String(data.productContract)}.`);
  if (data.dataStatus !== 'live' && data.dataStatus !== 'sample') throw new Error('Invalid manifest dataStatus.');
  const region = exactObject(data.region, ['id', 'label', 'bounds'], 'region');
  const bounds = exactObject(region.bounds, ['west', 'east', 'south', 'north'], 'region bounds');
  const profiles = array(data.profiles, 'profiles').map((item, index) => exactObject(item, ['id', 'label'], `profile ${index + 1}`));
  if (profiles.length !== 2 || profiles[0].id !== 'nice' || profiles[1].id !== 'cycling') throw new Error('Invalid manifest profiles.');
  const forecast = exactObject(data.forecast, ['updatedAt', 'staleAfterHours', 'window', 'days'], 'forecast');
  const days = periodList(forecast.days, 'forecast');
  if (days.length !== 14) throw new Error('The forecast must contain 14 days.');
  if (new Set(days.map((day) => day.id)).size !== days.length) throw new Error('Forecast period IDs must be unique.');
  const windURLs = days.flatMap((day) => day.windFrames?.map((frame) => frame.url) ?? []);
  if (new Set(windURLs).size !== windURLs.length) throw new Error('Forecast wind asset paths must be unique.');
  if (days.some((day, index) => day.date !== addUtcDays(days[0].date!, index))) throw new Error('Forecast days must be consecutive.');
  const window = forecastWindow(forecast.window, days);
  let climate: WeatherManifest['climate'];
  if (data.climate !== undefined) {
    const row = exactObject(data.climate, ['label', 'referencePeriod', 'months'], 'climate');
    const months = periodList(row.months, 'climate');
    if (months.length !== 12) throw new Error('Typical weather must contain 12 months.');
    if (new Set(months.map((month) => month.id)).size !== months.length) throw new Error('Typical-weather period IDs must be unique.');
    if (months.some((month, index) => month.month !== index + 1)) throw new Error('Typical weather months must run from January to December.');
    climate = { label: text(row.label, 'climate label'), period: text(row.referencePeriod, 'climate referencePeriod'), months };
  }
	const manifestBounds = { west: number(bounds.west, 'west'), east: number(bounds.east, 'east'), south: number(bounds.south, 'south'), north: number(bounds.north, 'north') };
	for (const period of [...days, ...(climate?.months ?? [])]) validatePointImageParity(period.displayPoint ?? period.point, [...Object.values(period.images), ...Object.values(period.lenses)], manifestBounds, `${period.id} assets`);
	validatePointImageParity(window.point, [...Object.values(window.profiles).map((profile) => profile.image), ...Object.values(window.lenses)], manifestBounds, 'forecast window assets');
	const outlooks = days.filter((day) => day.kind === 'outlook');
	if (outlooks.length === 0 || outlooks.some((day) => !day.displayPoint || !samePointGeometry(day.displayPoint, window.point))) throw new Error('Forecast window point geometry does not match its outlook display planes.');
	if (data.dataStatus === 'live') {
		const radians = (degrees: number) => degrees * Math.PI / 180;
		const projectedHeight = Math.asinh(Math.tan(radians(manifestBounds.north))) - Math.asinh(Math.tan(radians(manifestBounds.south)));
		const longitudeSpan = radians(manifestBounds.east - manifestBounds.west);
		const rows = Math.max(1, Math.round(DENSE_FORECAST_SCORE_COLS * projectedHeight / longitudeSpan));
		if (window.point.geometry.cols !== DENSE_FORECAST_SCORE_COLS || window.point.geometry.rows !== rows) throw new Error('Live forecast window does not use the canonical dense score geometry.');
	}

  return {
    version: 8,
    releaseId: releaseId(data.releaseId, 'releaseId'),
    productContract: PRODUCT_CONTRACT,
    dataStatus: data.dataStatus,
    generatedAt: timestamp(data.generatedAt, 'generatedAt'),
    bounds: [manifestBounds.west, manifestBounds.south, manifestBounds.east, manifestBounds.north],
    forecast: {
      updatedAt: timestamp(forecast.updatedAt, 'forecast updatedAt'),
      staleAfterHours: positiveInteger(forecast.staleAfterHours, 'staleAfterHours'),
      window,
      days,
    },
    climate,
    legend: legendList(data.legend),
    palette: paletteList(data.palette),
  };
}

function forecastWindow(value: unknown, days: Period[]): WeatherManifest['forecast']['window'] {
  const row = exactObject(value, ['id', 'label', 'startDate', 'endDate', 'dayCount', 'gridResolutionKm', 'point', 'profiles', 'lenses'], 'forecast window');
  if (row.id !== 'next-14-days' || row.dayCount !== 14) throw new Error('Invalid forecast window.');
  const profiles = exactObject(row.profiles, ['nice', 'cycling'], 'forecast window profiles');
  const lenses = exactObject(row.lenses, ['comfort', 'dry', 'bright', 'wind-nice', 'wind-cycling'], 'forecast window lenses');
  const profile = (value: unknown, label: string) => {
    const asset = exactObject(value, ['image'], label);
    return { image: weatherImage(asset.image, `${label} image`) };
  };
  const parsedProfiles = { nice: profile(profiles.nice, 'nice window'), cycling: profile(profiles.cycling, 'cycling window') };
  const parsedLenses = {
    comfort: weatherImage(lenses.comfort, 'window comfort image'),
    dry: weatherImage(lenses.dry, 'window dry image'),
    bright: weatherImage(lenses.bright, 'window bright image'),
    'wind-nice': weatherImage(lenses['wind-nice'], 'window nice calm image'),
    'wind-cycling': weatherImage(lenses['wind-cycling'], 'window cycling calm image'),
  };
  const imageURLs = [...Object.values(parsedProfiles).map((item) => item.image.url), ...Object.values(parsedLenses).map((item) => item.url)];
  if (new Set(imageURLs).size !== imageURLs.length) throw new Error('Forecast window image paths must be unique.');
  const startDate = date(row.startDate, 'window startDate');
  const endDate = date(row.endDate, 'window endDate');
  if (startDate !== days[0].date || endDate !== days[days.length - 1].date) throw new Error('Forecast window dates do not match its days.');
  return {
    id: 'next-14-days',
    label: text(row.label, 'window label'),
    shortLabel: '14 days',
    startDate,
    endDate,
    dayCount: 14,
    resolutionKm: positiveNumber(row.gridResolutionKm, 'window resolution'),
    point: pointAsset(row.point, 'forecast-window', 'forecast window point'),
    profiles: parsedProfiles,
    lenses: parsedLenses,
  };
}

function paletteList(value: unknown): Array<{ score: number; color: string }> {
  const stops = array(value, 'palette stops');
  if (stops.length !== 12) throw new Error('Invalid palette stops.');
  const parsed = stops.map((item, index) => {
    const stop = exactObject(item, ['score', 'color'], `palette stop ${index + 1}`);
    return { score: number(stop.score, 'palette score'), color: color(stop.color, 'palette color') };
  });
  if (parsed[0].score !== 0 || parsed[parsed.length - 1].score !== 100 || parsed.some((stop, index) => index > 0 && stop.score <= parsed[index - 1].score)) {
    throw new Error('Invalid palette stops.');
  }
  return parsed;
}

function periodList(value: unknown, basis: 'forecast' | 'climate'): Period[] {
  return array(value, `${basis} periods`).map((item, index) => {
		const allowed = ['id', 'label', 'source', 'nativeResolutionKm', 'temporalResolutionHours', 'images', 'lenses', 'point', basis === 'forecast' ? 'date' : 'month', basis === 'forecast' ? 'kind' : 'month', ...(basis === 'forecast' ? ['windFrames', 'displayPoint'] : [])];
		const row = exactObject(item, [...new Set(allowed)], `${basis} period ${index + 1}`, basis === 'forecast' ? ['displayPoint'] : []);
    const source = exactObject(row.source, ['providerId', 'providerLabel', 'runAt', 'validFrom', 'validTo'], `${basis} source`);
    const images = exactObject(row.images, ['nice', 'cycling'], `${basis} images`);
    const lenses = exactObject(row.lenses, ['comfort', 'dry', 'bright', 'wind-nice', 'wind-cycling'], `${basis} lenses`);
    const label = text(row.label, `${basis} label`);
    const temporalResolutionHours = positiveInteger(row.temporalResolutionHours, 'temporalResolutionHours');
    const sourceValidFrom = timestamp(source.validFrom, 'source validFrom');
    const sourceValidTo = timestamp(source.validTo, 'source validTo');
    const period: Period = {
      id: text(row.id, `${basis} id`),
      label,
      shortLabel: basis === 'forecast' ? shortDate(date(row.date, 'forecast date')) : label.slice(0, 3),
      resolutionKm: positiveNumber(row.nativeResolutionKm, 'nativeResolutionKm'),
      temporalResolutionHours,
      providerLabel: text(source.providerLabel, 'providerLabel'),
			sourceRunAt: timestamp(source.runAt, 'source runAt'),
			sourceValidFrom,
			sourceValidTo,
			point: pointAsset(row.point, basis === 'forecast' ? 'forecast-day' : 'legacy-climate-month', `${basis} point`),
      images: { nice: weatherImage(images.nice, `${basis} nice image`), cycling: weatherImage(images.cycling, `${basis} cycling image`) },
      lenses: {
        comfort: weatherImage(lenses.comfort, `${basis} comfort lens`),
        dry: weatherImage(lenses.dry, `${basis} dry lens`),
        bright: weatherImage(lenses.bright, `${basis} bright lens`),
        'wind-nice': weatherImage(lenses['wind-nice'], `${basis} nice wind lens`),
        'wind-cycling': weatherImage(lenses['wind-cycling'], `${basis} cycling wind lens`),
      },
    };
    if (basis === 'forecast') {
      period.date = date(row.date, 'forecast date');
      if (row.kind !== 'detailed' && row.kind !== 'outlook') throw new Error('Invalid forecast kind.');
      period.kind = row.kind;
			if (row.kind === 'outlook') {
				if (row.displayPoint === undefined) throw new Error('Outlook requires dense display scores.');
				period.displayPoint = pointAsset(row.displayPoint, 'display-score', `${basis} display score`);
			} else if (row.displayPoint !== undefined) throw new Error('Detailed forecast must remain on its native score plane.');
      period.windFrames = windFrames(row.windFrames, sourceValidFrom, sourceValidTo);
    } else {
      const month = positiveInteger(row.month, 'climate month');
      if (month > 12) throw new Error('Invalid climate month.');
      period.month = month;
    }
    return period;
  });
}

function windFrames(value: unknown, validFrom: string, validTo: string): Period['windFrames'] {
  const frames = array(value, 'wind frames').map((item, index) => {
    const row = exactObject(item, ['validAt', 'url', 'sha256', 'clippedComponents'], `wind frame ${index + 1}`);
    const url = assetPath(text(row.url, 'wind URL'), 'wind URL', 'wind');
    const sha256 = checksum(row.sha256, 'wind checksum');
    if (!url.endsWith('.nww.gz')) throw new Error('Invalid wind asset format.');
    if (!url.includes(sha256)) throw new Error('Wind asset URL does not contain its checksum.');
    return {
      validAt: timestamp(row.validAt, 'wind validAt'),
      url,
      sha256,
      clippedComponents: nonNegativeInteger(row.clippedComponents, 'wind clipped components'),
    };
  });
  if (frames.length < 2 || new Set(frames.map((frame) => frame.url)).size !== frames.length) throw new Error('Invalid wind frame catalogue.');
  const first = Date.parse(validFrom); const last = Date.parse(validTo);
  frames.forEach((frame, index) => {
    const current = Date.parse(frame.validAt);
    if (current < first || current > last || (index > 0 && current <= Date.parse(frames[index - 1].validAt))) throw new Error('Invalid wind frame timing.');
  });
  return frames;
}

function addUtcDays(value: string, offset: number): string {
  const result = new Date(`${value}T00:00:00Z`);
  result.setUTCDate(result.getUTCDate() + offset);
  return result.toISOString().slice(0, 10);
}

function weatherImage(value: unknown, label: string): WeatherImage {
  const row = exactObject(value, ['url', 'sha256', 'width', 'height', 'projection'], label);
  if (row.projection !== 'web-mercator') throw new Error(`${label} uses an unsupported projection.`);
  return {
    url: assetPath(text(row.url, `${label} URL`), `${label} URL`, 'images'),
    sha256: checksum(row.sha256, `${label} checksum`),
    width: positiveInteger(row.width, `${label} width`),
    height: positiveInteger(row.height, `${label} height`),
    projection: 'web-mercator',
  };
}

function pointAsset(value: unknown, expectedKind: PointAsset['kind'], label: string): PointAsset {
  const row = exactObject(value, ['url', 'sha256', 'payloadSha256', 'bytes', 'kind', 'headerBytes', 'recordBytes', 'projection', 'geometry'], label);
  const geometryRow = exactObject(row.geometry, ['bounds', 'quad', 'rows', 'cols'], `${label} geometry`);
  const boundsRow = exactObject(geometryRow.bounds, ['west', 'east', 'south', 'north'], `${label} bounds`);
  const quadRow = exactObject(geometryRow.quad, ['west', 'east', 'north', 'south'], `${label} quad`);
  const url = assetPath(text(row.url, `${label} URL`), `${label} URL`, 'points');
  const sha256 = checksum(row.sha256, `${label} checksum`);
  const payloadSha256 = checksum(row.payloadSha256, `${label} payload checksum`);
  const rows = positiveInteger(geometryRow.rows, `${label} rows`);
  const cols = positiveInteger(geometryRow.cols, `${label} columns`);
  const recordBytes: 8 | 30 | 44 = expectedKind === 'forecast-window' ? 44 : expectedKind === 'display-score' ? 8 : 30;
  const bytes = positiveInteger(row.bytes, `${label} bytes`);
  if (row.kind !== expectedKind || row.headerBytes !== POINT_HEADER_BYTES || row.recordBytes !== recordBytes || row.projection !== 'web-mercator-image') throw new Error(`Invalid ${label} layout.`);
  if (!url.endsWith('.nwp') || !url.includes(sha256)) throw new Error(`Invalid ${label} identity.`);
  if (!Number.isSafeInteger(rows * cols) || !Number.isSafeInteger(POINT_HEADER_BYTES + rows * cols * recordBytes) || bytes !== POINT_HEADER_BYTES + rows * cols * recordBytes) throw new Error(`Invalid ${label} size.`);
  const bounds = {
    west: number(boundsRow.west, `${label} west`), east: number(boundsRow.east, `${label} east`),
    south: number(boundsRow.south, `${label} south`), north: number(boundsRow.north, `${label} north`),
  };
  const quad = {
    west: number(quadRow.west, `${label} quad west`), east: number(quadRow.east, `${label} quad east`),
    north: number(quadRow.north, `${label} quad north`), south: number(quadRow.south, `${label} quad south`),
  };
  if (bounds.west < -180 || bounds.east > 180 || bounds.south < -MAX_MERCATOR_LATITUDE || bounds.north > MAX_MERCATOR_LATITUDE || bounds.west >= bounds.east || bounds.south >= bounds.north || quad.west < 0 || quad.east > 1 || quad.north < 0 || quad.south > 1 || quad.west >= quad.east || quad.north >= quad.south) throw new Error(`Invalid ${label} geometry.`);
  const canonicalQuad = canonicalPointQuad(bounds);
  if (quad.west !== canonicalQuad.west || quad.east !== canonicalQuad.east || quad.north !== canonicalQuad.north || quad.south !== canonicalQuad.south) throw new Error(`Invalid ${label} MapLibre quad.`);
  return { url, sha256, payloadSha256, bytes, kind: expectedKind, headerBytes: 128, recordBytes, projection: 'web-mercator-image', geometry: { bounds, quad, rows, cols } };
}

function canonicalPointQuad(bounds: PointAsset['geometry']['bounds']): PointAsset['geometry']['quad'] {
  const x = (longitude: number) => (longitude + 180) / 360;
  const y = (latitude: number) => 0.5 - Math.asinh(Math.tan(latitude * Math.PI / 180)) / (2 * Math.PI);
  const west = x(bounds.west); const east = x(bounds.east);
  const north = y(bounds.north); const south = y(bounds.south);
  const span = Math.max(east - west, south - north);
  const scale = 2 ** Math.max(0, Math.floor(-Math.log2(span))) * MAPLIBRE_TILE_EXTENT;
  const snap = (value: number) => Math.round(value * scale) / scale;
  return { west: snap(west), east: snap(east), north: snap(north), south: snap(south) };
}

function validatePointImageParity(asset: PointAsset, images: WeatherImage[], bounds: PointAsset['geometry']['bounds'], label: string): void {
  if (!sameBounds(asset.geometry.bounds, bounds) || images.some((image) => image.width !== asset.geometry.cols || image.height !== asset.geometry.rows)) throw new Error(`Invalid ${label} geometry.`);
}

function samePointGeometry(left: PointAsset, right: PointAsset): boolean {
  return left.geometry.rows === right.geometry.rows && left.geometry.cols === right.geometry.cols && sameBounds(left.geometry.bounds, right.geometry.bounds) &&
    left.geometry.quad.west === right.geometry.quad.west && left.geometry.quad.east === right.geometry.quad.east && left.geometry.quad.north === right.geometry.quad.north && left.geometry.quad.south === right.geometry.quad.south;
}

function sameBounds(left: PointAsset['geometry']['bounds'], right: PointAsset['geometry']['bounds']): boolean {
  return left.west === right.west && left.east === right.east && left.south === right.south && left.north === right.north;
}

function legendList(value: unknown): LegendCategory[] {
  const categories = array(value, 'legend categories');
  if (categories.length !== 6) throw new Error('Invalid legend categories.');
  let next = 0;
  return categories.map((item, index) => {
    const row = exactObject(item, ['label', 'color', 'min', 'max'], `legend category ${index + 1}`);
    const result = { label: text(row.label, 'legend label'), color: color(row.color, 'legend color'), min: integer(row.min, 'legend min'), max: integer(row.max, 'legend max') };
    if (result.min !== next || result.max < result.min || result.max > 100) throw new Error('Invalid legend ranges.');
    next = result.max + 1;
    if (index === categories.length - 1 && next !== 101) throw new Error('Invalid legend ranges.');
    return result;
  });
}

type ParsedPointHeader = PointAsset & { recordBytes: 8 | 30 | 44 };

function pointTarget(request: PointRequest, manifest: WeatherManifest): { asset: PointAsset; display?: PointAsset; kind: 'range' | 'forecast' | 'climate'; period?: Period } {
  if (request.period === manifest.forecast.window.id) return { asset: manifest.forecast.window.point, kind: 'range' };
  const forecast = manifest.forecast.days.find((period) => period.id === request.period);
  if (forecast) return { asset: forecast.point, display: forecast.displayPoint, kind: 'forecast', period: forecast };
  const climate = manifest.climate?.months.find((period) => period.id === request.period);
  if (climate) return { asset: climate.point, kind: 'climate', period: climate };
  throw new Error('There is no complete weather record here.');
}

function pointAssetUrl(release: string, asset: PointAsset): string {
  return `/releases/${encodeURIComponent(releaseId(release, 'releaseId'))}/${asset.url}?v=${asset.sha256}`;
}

async function loadPointHeader(url: string, asset: PointAsset, signal?: AbortSignal): Promise<ParsedPointHeader> {
  const bytes = await rangeBytes(url, 0, POINT_HEADER_BYTES - 1, asset.bytes, signal);
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  if (new TextDecoder().decode(bytes.subarray(0, 8)) !== 'NWFPOINT' || view.getUint16(8, true) !== 1 || view.getUint16(10, true) !== POINT_HEADER_BYTES) throw new Error('Unsupported point asset header.');
  const kindCode = view.getUint8(14);
  const kind = kindCode === 1 ? 'forecast-day' : kindCode === 2 ? 'forecast-window' : kindCode === 3 ? 'legacy-climate-month' : kindCode === 5 ? 'display-score' : undefined;
  if (!kind || kind !== asset.kind || view.getUint8(15) !== 1 || view.getUint16(12, true) !== asset.recordBytes) throw new Error('Point asset header does not match its manifest.');
  const geometry = {
    rows: view.getUint32(16, true), cols: view.getUint32(20, true),
    bounds: { west: view.getFloat64(24, true), east: view.getFloat64(32, true), south: view.getFloat64(40, true), north: view.getFloat64(48, true) },
    quad: { west: view.getFloat64(56, true), east: view.getFloat64(64, true), north: view.getFloat64(72, true), south: view.getFloat64(80, true) },
  };
  if (geometry.rows !== asset.geometry.rows || geometry.cols !== asset.geometry.cols || JSON.stringify(geometry.bounds) !== JSON.stringify(asset.geometry.bounds) || JSON.stringify(geometry.quad) !== JSON.stringify(asset.geometry.quad)) throw new Error('Point asset geometry does not match its manifest.');
  if (hex(bytes.subarray(88, 120)) !== asset.payloadSha256 || bytes.subarray(120, 128).some((value) => value !== 0)) throw new Error('Point asset header checksum or reserved bytes are invalid.');
  const expectedBytes = POINT_HEADER_BYTES + geometry.rows * geometry.cols * asset.recordBytes;
  if (!Number.isSafeInteger(expectedBytes) || expectedBytes !== asset.bytes) throw new Error('Point asset size is unsafe.');
  return asset;
}

async function rangeBytes(url: string, start: number, end: number, total: number, signal?: AbortSignal): Promise<Uint8Array> {
  const response = await fetch(url, { signal, headers: { Range: `bytes=${start}-${end}` } });
  if (response.status === 404 || response.status === 409 || response.status === 410) throw new StaleReleaseError();
  if (response.status !== 206) throw new Error(`Point range returned HTTP ${response.status}.`);
  const contentType = response.headers.get('Content-Type')?.trim().toLowerCase();
  if (contentType !== 'application/octet-stream' || response.headers.has('Content-Encoding')) throw new Error('Point range has unsafe representation headers.');
  if (response.headers.get('Content-Range') !== `bytes ${start}-${end}/${total}`) throw new Error('Point range does not match the requested file offsets.');
  const wanted = end - start + 1;
  const contentLength = response.headers.get('Content-Length');
  if (contentLength !== String(wanted)) throw new Error('Point range length header is invalid.');
  const bytes = new Uint8Array(await response.arrayBuffer());
  if (bytes.byteLength !== wanted) throw new Error('Point range body has the wrong length.');
  return bytes;
}

function pointIndex(header: ParsedPointHeader, lon: number, lat: number): number | undefined {
  if (!Number.isFinite(lon) || !Number.isFinite(lat) || lon < -180 || lon > 180 || lat < -MAX_MERCATOR_LATITUDE || lat > MAX_MERCATOR_LATITUDE) return undefined;
  const x = (lon + 180) / 360;
  const radians = lat * Math.PI / 180;
  const y = (1 - Math.log(Math.tan(radians) + 1 / Math.cos(radians)) / Math.PI) / 2;
  const quad = header.geometry.quad;
  if (x < quad.west || x > quad.east || y < quad.north || y > quad.south) return undefined;
  let col = Math.floor((x - quad.west) / (quad.east - quad.west) * header.geometry.cols);
  let row = Math.floor((y - quad.north) / (quad.south - quad.north) * header.geometry.rows);
  if (col === header.geometry.cols) col--;
  if (row === header.geometry.rows) row--;
  const index = row * header.geometry.cols + col;
  return row < 0 || col < 0 || row >= header.geometry.rows || col >= header.geometry.cols || !Number.isSafeInteger(index) ? undefined : index;
}

function decodeDailyPoint(bytes: Uint8Array, request: PointRequest, manifest: WeatherManifest, period: Period, climate: boolean, display?: Uint8Array): DailyPoint {
  if (bytes.length !== 30 || bytes.every((value) => value === 0)) throw new Error('There is no complete weather record here.');
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const flags = bytes[20];
  if ((flags & 1) === 0 || (flags & ~0x7f) !== 0 || bytes[21] !== 0) throw new Error('Invalid daily point flags.');
  const values = [bytes[22], bytes[23], bytes[24], bytes[25], bytes[26], bytes[27], bytes[28]];
  if (values.some((value) => value > 100)) throw new Error('Daily point score exceeds 100.');
  const dewPoint = view.getInt16(6, true); const gust = view.getUint16(12, true); const solar = view.getUint16(14, true);
  const wet = view.getUint16(16, true); const cloud = bytes[18]; const humidity = bytes[19];
  if (((flags & 2) === 0 && dewPoint !== 0) || ((flags & 4) === 0 && gust !== 0) || ((flags & 8) === 0 && solar !== 0) || ((flags & 16) === 0 && cloud !== 0) || ((flags & 32) === 0 && humidity !== 0) || ((flags & 64) === 0 && wet !== 0) || wet > 1000 || cloud > 100 || humidity > 100 || ((flags & 0x18) === 0) || ((flags & 0x22) === 0)) throw new Error('Invalid daily point weather fields.');
  const scoreBytes = display ?? bytes.subarray(22, 30);
	if (display && (display.length !== 8 || display[7] !== 1 || display.subarray(0, 7).some((value) => value > 100))) throw new Error('Invalid display-score point record.');
  const profileOffset = request.profile === 'nice' ? 3 : 5;
  const score = scoreBytes[profileOffset]; const wind = scoreBytes[profileOffset + 1];
  const components = scoreComponents(scoreBytes[0], scoreBytes[1], scoreBytes[2], wind, manifest.legend);
  const summaryParts = decodeSummary(bytes[29]);
  const temperature = view.getInt16(0, true) / 10;
  const source = climate ? {
    basis: 'climate' as const, detailLabel: 'Typical weather', providerLabel: period.providerLabel, resolutionKm: period.resolutionKm,
    temporalResolutionHours: period.temporalResolutionHours, artifactGeneratedAt: manifest.generatedAt, referencePeriod: manifest.climate!.period,
  } : {
    basis: 'forecast' as const, detailLabel: period.kind === 'detailed' ? 'Detailed forecast' : 'Outlook', providerLabel: period.providerLabel,
    resolutionKm: period.resolutionKm, temporalResolutionHours: period.temporalResolutionHours, artifactGeneratedAt: manifest.generatedAt,
    runAt: period.sourceRunAt, validFrom: period.sourceValidFrom, validTo: period.sourceValidTo,
  };
  return {
    kind: 'daily', releaseId: manifest.releaseId, lat: request.lat, lon: request.lon, periodLabel: period.label,
    score, category: rating(score, manifest.legend), components, summary: `${goRound(temperature)}°C · ${summaryParts.join(' · ')}`,
    metrics: {
      temperatureC: temperature, minTempC: view.getInt16(2, true) / 10, maxTempC: view.getInt16(4, true) / 10,
      rainMm: view.getUint16(8, true) / 10, windKmh: view.getUint16(10, true) / 10,
      dewPointC: flags & 2 ? dewPoint / 10 : undefined, gustKmh: flags & 4 ? gust / 10 : undefined,
      solarEnergyMJm2: flags & 8 ? solar / 100 : undefined, cloudPct: flags & 16 ? cloud : undefined,
      humidityPct: flags & 32 ? humidity : undefined, wetTimeFraction: flags & 64 ? wet / 1000 : undefined,
    }, source,
  };
}

function decodeRangePoint(bytes: Uint8Array, request: PointRequest, manifest: WeatherManifest): ForecastWindowPoint {
  if (bytes.length !== 44 || bytes.every((value) => value === 0)) throw new Error('There is no complete weather record here.');
  if (bytes[43] !== 1 || bytes.subarray(0, 43).some((value) => value > 100)) throw new Error('Invalid forecast-window point record.');
  const offset = request.profile === 'nice' ? 3 : 9;
  const dayOffset = request.profile === 'nice' ? 15 : 29;
  const scores = Array.from(bytes.subarray(dayOffset, dayOffset + 14));
  const calculated = summarizeScores(scores, manifest.legend);
  const score = bytes[offset]; const medianScore = bytes[offset + 1]; const wind = bytes[offset + 2];
  const niceDays = bytes[offset + 3]; const awfulDays = bytes[offset + 4]; const longestPoorRun = bytes[offset + 5];
  if (score !== calculated.score || medianScore !== calculated.median || niceDays !== calculated.niceDays || awfulDays !== calculated.awfulDays || longestPoorRun !== calculated.longestPoorRun) throw new Error('Forecast-window summary does not match its daily scores.');
  const window = manifest.forecast.window;
  return {
    kind: 'range', releaseId: manifest.releaseId, lat: request.lat, lon: request.lon, periodLabel: window.label,
    score, category: rating(score, manifest.legend), components: scoreComponents(bytes[0], bytes[1], bytes[2], wind, manifest.legend),
    window: { startDate: window.startDate, endDate: window.endDate, dayCount: 14, niceDays, awfulDays, medianScore, longestPoorRun },
    days: scores.map((value, index) => ({ date: manifest.forecast.days[index].date!, score: value, category: rating(value, manifest.legend) })),
    source: { basis: 'forecast-window', detailLabel: '14-day forecast', resolutionKm: window.resolutionKm, artifactGeneratedAt: manifest.generatedAt },
  };
}

function summarizeScores(scores: number[], legend: LegendCategory[]) {
	const niceMinimum = legend[3].min;
	const awfulMaximum = legend[0].max;
	const poorMaximum = legend[1].max;
  const score = Math.round(scores.reduce((sum, value) => sum + value, 0) / scores.length);
  const ordered = [...scores].sort((a, b) => a - b);
  const median = Math.round((ordered[6] + ordered[7]) / 2);
  let niceDays = 0; let awfulDays = 0; let poorRun = 0; let longestPoorRun = 0;
  for (const value of scores) {
    if (value >= niceMinimum) niceDays++;
    if (value <= awfulMaximum) awfulDays++;
    poorRun = value <= poorMaximum ? poorRun + 1 : 0;
    longestPoorRun = Math.max(longestPoorRun, poorRun);
  }
  return { score, median, niceDays, awfulDays, longestPoorRun };
}

function scoreComponents(comfort: number, dryness: number, sun: number, wind: number, legend: LegendCategory[]) {
  const component = (value: number) => ({ value, category: rating(value, legend) });
  return { comfort: component(comfort), dryness: component(dryness), sun: component(sun), wind: component(wind) };
}

function rating(score: number, legend: LegendCategory[]): string {
  if (!Number.isInteger(score) || score < 0 || score > 100) throw new Error('Invalid point score.');
  const band = legend.find((item) => score >= item.min && score <= item.max);
  if (!band) throw new Error('Point score has no legend category.');
  return band.label;
}

function decodeSummary(code: number): string[] {
  if (!Number.isInteger(code) || code < 0 || code >= 240) throw new Error('Invalid point summary code.');
  const thermal = Math.floor(code / 80); let remainder = code % 80;
  const wind = Math.floor(remainder / 16); remainder %= 16;
  const sun = Math.floor(remainder / 4); const rain = remainder % 4;
  const parts = thermal === 0 ? [] : [['too hot', 'too cold'][thermal - 1]];
  return [...parts, ['dry', 'brief rain', 'some rain', 'wet'][rain], ['overcast', 'mixed cloud', 'bright', 'sunny'][sun], ['light wind', 'breezy', 'windy', 'strong gusts', 'severe gusts'][wind]];
}

function goRound(value: number): number { return value < 0 ? -Math.floor(-value + 0.5) : Math.floor(value + 0.5); }
function hex(bytes: Uint8Array): string { return Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join(''); }

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

function shortDate(value: string): string {
  return new Intl.DateTimeFormat('en-GB', { weekday: 'short', day: 'numeric', timeZone: 'UTC' }).format(new Date(`${value}T12:00:00Z`));
}

function exactObject(value: unknown, fields: readonly string[], label: string, optional: readonly string[] = []): Record<string, unknown> {
  const data = object(value, label);
  const allowed = new Set(fields);
  if (Object.keys(data).some((field) => !allowed.has(field)) || fields.some((field) => !optional.includes(field) && !(field in data))) throw new Error(`Invalid ${label} fields.`);
  return data;
}

function object(value: unknown, label: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error(`Invalid ${label}.`);
  return value as Record<string, unknown>;
}
function array(value: unknown, label: string): unknown[] { if (!Array.isArray(value)) throw new Error(`Invalid ${label}.`); return value; }
function text(value: unknown, label: string): string { if (typeof value !== 'string' || value.length === 0 || value.trim() !== value) throw new Error(`Invalid ${label}.`); return value; }
function number(value: unknown, label: string): number { if (typeof value !== 'number' || !Number.isFinite(value)) throw new Error(`Invalid ${label}.`); return value; }
function integer(value: unknown, label: string): number { const parsed = number(value, label); if (!Number.isSafeInteger(parsed)) throw new Error(`Invalid ${label}.`); return parsed; }
function positiveInteger(value: unknown, label: string): number { const parsed = integer(value, label); if (parsed < 1) throw new Error(`Invalid ${label}.`); return parsed; }
function nonNegativeInteger(value: unknown, label: string): number { const parsed = integer(value, label); if (parsed < 0) throw new Error(`Invalid ${label}.`); return parsed; }
function positiveNumber(value: unknown, label: string): number { const parsed = number(value, label); if (parsed <= 0) throw new Error(`Invalid ${label}.`); return parsed; }
function boundedScore(value: unknown, label: string): number { const parsed = integer(value, label); if (parsed < 0 || parsed > 100) throw new Error(`Invalid ${label}.`); return parsed; }
function timestamp(value: unknown, label: string): string { const parsed = text(value, label); if (Number.isNaN(Date.parse(parsed))) throw new Error(`Invalid ${label}.`); return parsed; }
function date(value: unknown, label: string): string { const parsed = text(value, label); if (!/^\d{4}-\d{2}-\d{2}$/.test(parsed) || new Date(`${parsed}T00:00:00Z`).toISOString().slice(0, 10) !== parsed) throw new Error(`Invalid ${label}.`); return parsed; }
function color(value: unknown, label: string): string { const parsed = text(value, label); if (!/^#[0-9A-Fa-f]{6}$/.test(parsed)) throw new Error(`Invalid ${label}.`); return parsed; }
function checksum(value: unknown, label: string): string { const parsed = text(value, label); if (!/^[0-9a-f]{64}$/.test(parsed)) throw new Error(`Invalid ${label}.`); return parsed; }
function releaseId(value: unknown, label: string): string { const parsed = text(value, label); if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(parsed)) throw new Error(`Invalid ${label}.`); return parsed; }

function assetPath(value: string, label: string, root: 'images' | 'points' | 'wind'): string {
  const path = value.replace(/^\//, '');
  let decoded: string;
  try { decoded = decodeURIComponent(path); } catch { throw new Error(`Invalid ${label}.`); }
  const segments = decoded.split('/');
  if (segments[0] !== root || segments.length < 2 || segments.some((segment) => segment === '' || segment === '.' || segment === '..') || decoded.includes('\\')) throw new Error(`Invalid ${label}.`);
  return path;
}

function imagePath(value: string, label: string): string { return assetPath(value, label, 'images'); }
