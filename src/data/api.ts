import type {
  DailyPoint, ForecastWindow, ForecastWindowPoint, LegendCategory, Period, PointRequest,
  PointWeather, WeatherImage, WeatherManifest,
} from './types';

const MANIFEST_VERSION = 5;
const PRODUCT_CONTRACT = 'nice-weather-v11';

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

export async function loadPoint(request: PointRequest, signal?: AbortSignal, expectedWindow?: Pick<ForecastWindow, 'startDate' | 'endDate' | 'dayCount'>): Promise<PointWeather> {
  const params = new URLSearchParams({
    release: request.releaseId,
    lat: request.lat.toFixed(5),
    lon: request.lon.toFixed(5),
    period: request.period,
    profile: request.profile,
  });
  const response = await fetch(`/api/point?${params}`, { signal });
  if (!response.ok) {
    if (response.status === 409 && await isStaleReleaseResponse(response)) throw new StaleReleaseError();
    if (response.status === 404) throw new Error('There is no complete weather record here.');
    throw new Error('Weather details could not be loaded.');
  }
  let point: PointWeather;
  try {
    const payload = await response.json();
    const envelope = object(payload, 'point response');
    if (envelope.periodId !== request.period || envelope.profile !== request.profile) throw new Error('Point response does not match its request.');
    point = parsePoint(payload);
    if (point.kind === 'range' && expectedWindow && (point.window.startDate !== expectedWindow.startDate || point.window.endDate !== expectedWindow.endDate || point.window.dayCount !== expectedWindow.dayCount)) {
      throw new Error('Forecast-window point does not match the manifest.');
    }
  } catch (error) {
    console.error('[nice-weather] Invalid point response.', error);
    throw new Error('Weather details could not be read.');
  }
  if (point.releaseId !== request.releaseId) throw new StaleReleaseError();
  return point;
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

  return {
    version: 5,
    releaseId: releaseId(data.releaseId, 'releaseId'),
    productContract: PRODUCT_CONTRACT,
    dataStatus: data.dataStatus,
    generatedAt: timestamp(data.generatedAt, 'generatedAt'),
    bounds: [number(bounds.west, 'west'), number(bounds.south, 'south'), number(bounds.east, 'east'), number(bounds.north, 'north')],
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

function forecastWindow(value: unknown, days: Period[]): ForecastWindow {
  const row = exactObject(value, ['id', 'label', 'startDate', 'endDate', 'dayCount', 'gridResolutionKm', 'profiles'], 'forecast window');
  if (row.id !== 'next-14-days' || row.dayCount !== 14) throw new Error('Invalid forecast window.');
  const profiles = exactObject(row.profiles, ['nice', 'cycling'], 'forecast window profiles');
  const profile = (value: unknown, label: string) => {
    const asset = exactObject(value, ['gridUrl', 'gridSha256', 'image'], label);
    assetPath(text(asset.gridUrl, `${label} grid URL`), `${label} grid URL`, 'grids');
    checksum(asset.gridSha256, `${label} grid checksum`);
    return { image: weatherImage(asset.image, `${label} image`) };
  };
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
    profiles: { nice: profile(profiles.nice, 'nice window'), cycling: profile(profiles.cycling, 'cycling window') },
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
    const allowed = ['id', 'label', 'source', 'nativeResolutionKm', 'temporalResolutionHours', 'images', 'lenses', 'gridUrl', 'gridSha256', basis === 'forecast' ? 'date' : 'month', basis === 'forecast' ? 'kind' : 'month'];
    const row = exactObject(item, [...new Set(allowed)], `${basis} period ${index + 1}`);
    const source = exactObject(row.source, ['providerId', 'providerLabel', 'runAt', 'validFrom', 'validTo'], `${basis} source`);
    const images = exactObject(row.images, ['nice', 'cycling'], `${basis} images`);
    const lenses = exactObject(row.lenses, ['comfort', 'dry', 'bright', 'wind-nice', 'wind-cycling'], `${basis} lenses`);
    assetPath(text(row.gridUrl, `${basis} grid URL`), `${basis} grid URL`, 'grids');
    checksum(row.gridSha256, `${basis} grid checksum`);
    const label = text(row.label, `${basis} label`);
    const period: Period = {
      id: text(row.id, `${basis} id`),
      label,
      shortLabel: basis === 'forecast' ? shortDate(date(row.date, 'forecast date')) : label.slice(0, 3),
      resolutionKm: positiveNumber(row.nativeResolutionKm, 'nativeResolutionKm'),
      temporalResolutionHours: positiveInteger(row.temporalResolutionHours, 'temporalResolutionHours'),
      providerLabel: text(source.providerLabel, 'providerLabel'),
      sourceRunAt: timestamp(source.runAt, 'source runAt'),
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
    } else {
      const month = positiveInteger(row.month, 'climate month');
      if (month > 12) throw new Error('Invalid climate month.');
      period.month = month;
    }
    return period;
  });
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

function parsePoint(value: unknown): PointWeather {
  const raw = object(value, 'point weather');
  return raw.window === undefined ? parseDailyPoint(value) : parseRangePoint(value);
}

function pointBase(data: Record<string, unknown>) {
  const location = exactObject(data.location, ['lat', 'lon'], 'point location');
  const score = exactObject(data.score, ['value', 'category', 'components'], 'point score');
  const components = exactObject(score.components, ['comfort', 'dryness', 'sun', 'wind'], 'score components');
  return {
    releaseId: releaseId(data.releaseId, 'point releaseId'),
    lat: number(location.lat, 'latitude'), lon: number(location.lon, 'longitude'),
    periodLabel: text(data.periodLabel, 'period label'), score: boundedScore(score.value, 'score'), category: text(score.category, 'score category'),
    components: {
      comfort: componentScore(components.comfort, 'comfort'), dryness: componentScore(components.dryness, 'dryness'),
      sun: componentScore(components.sun, 'sun'), wind: componentScore(components.wind, 'wind'),
    },
  };
}

function parseRangePoint(value: unknown): ForecastWindowPoint {
  const data = exactObject(value, ['releaseId', 'periodId', 'periodLabel', 'profile', 'location', 'score', 'window', 'source'], 'forecast-window point');
  if (data.periodId !== 'next-14-days') throw new Error('Invalid forecast-window period.');
  const window = exactObject(data.window, ['startDate', 'endDate', 'dayCount', 'niceDays', 'awfulDays', 'medianScore', 'longestPoorRun'], 'forecast-window evidence');
  const dayCount = positiveInteger(window.dayCount, 'window day count');
  if (dayCount !== 14) throw new Error('Invalid forecast-window day count.');
  const niceDays = integer(window.niceDays, 'nice days');
  const awfulDays = integer(window.awfulDays, 'awful days');
  const longestPoorRun = integer(window.longestPoorRun, 'longest poor run');
  const minimumPoorRun = Math.ceil(awfulDays / (dayCount - awfulDays + 1));
  if (niceDays < 0 || awfulDays < 0 || niceDays + awfulDays > dayCount || longestPoorRun < minimumPoorRun || longestPoorRun > dayCount - niceDays) throw new Error('Invalid forecast-window evidence.');
  const source = exactObject(data.source, ['basis', 'detailLabel', 'resolutionKm', 'artifactGeneratedAt'], 'forecast-window source');
  if (source.basis !== 'forecast-window') throw new Error('Invalid forecast-window source.');
  return {
    ...pointBase(data), kind: 'range',
    window: {
      startDate: date(window.startDate, 'window startDate'), endDate: date(window.endDate, 'window endDate'), dayCount,
      niceDays, awfulDays, medianScore: boundedScore(window.medianScore, 'median score'), longestPoorRun,
    },
    source: {
      basis: 'forecast-window', detailLabel: text(source.detailLabel, 'source detail label'),
      resolutionKm: positiveNumber(source.resolutionKm, 'source resolution'), artifactGeneratedAt: timestamp(source.artifactGeneratedAt, 'source generatedAt'),
    },
  };
}

function parseDailyPoint(value: unknown): DailyPoint {
  const data = exactObject(value, ['releaseId', 'periodId', 'periodLabel', 'profile', 'location', 'summary', 'score', 'weather', 'source'], 'daily point');
  const weather = exactObject(data.weather, ['temperatureC', 'minTempC', 'maxTempC', 'rainMm', 'wetTimeFraction', 'solarEnergyMJm2', 'cloudPct', 'windKmh', 'gustKmh', 'humidityPct', 'dewPointC'], 'point weather metrics', ['wetTimeFraction', 'solarEnergyMJm2', 'cloudPct', 'gustKmh', 'humidityPct', 'dewPointC']);
  const source = exactObject(data.source, ['basis', 'detailLabel', 'providerLabel', 'resolutionKm', 'temporalResolutionHours', 'artifactGeneratedAt', 'runAt', 'validFrom', 'validTo', 'referencePeriod'], 'point source', ['runAt', 'validFrom', 'validTo', 'referencePeriod']);
  if (source.basis !== 'forecast' && source.basis !== 'climate') throw new Error('Invalid source basis.');
  const basis = source.basis;
  const runAt = optionalTimestamp(source.runAt, 'source runAt');
  const validFrom = optionalTimestamp(source.validFrom, 'source validFrom');
  const validTo = optionalTimestamp(source.validTo, 'source validTo');
  const referencePeriod = optionalText(source.referencePeriod, 'source referencePeriod');
  if (basis === 'forecast' && (!runAt || !validFrom || !validTo)) throw new Error('Invalid forecast source timing.');
  if (basis === 'climate' && !referencePeriod) throw new Error('Invalid climate reference period.');
  return {
    ...pointBase(data), kind: 'daily', summary: text(data.summary, 'summary'),
    metrics: {
      temperatureC: number(weather.temperatureC, 'temperature'), minTempC: number(weather.minTempC, 'minimum temperature'), maxTempC: number(weather.maxTempC, 'maximum temperature'),
      rainMm: number(weather.rainMm, 'rain'), wetTimeFraction: optionalNumber(weather.wetTimeFraction, 'wetTimeFraction'),
      solarEnergyMJm2: optionalNumber(weather.solarEnergyMJm2, 'solarEnergyMJm2'), cloudPct: optionalNumber(weather.cloudPct, 'cloudPct'),
      windKmh: number(weather.windKmh, 'wind'), gustKmh: optionalNumber(weather.gustKmh, 'gustKmh'), humidityPct: optionalNumber(weather.humidityPct, 'humidityPct'), dewPointC: optionalNumber(weather.dewPointC, 'dewPointC'),
    },
    source: {
      basis, detailLabel: text(source.detailLabel, 'source detail label'), providerLabel: text(source.providerLabel, 'source provider label'),
      resolutionKm: positiveNumber(source.resolutionKm, 'source resolution'), temporalResolutionHours: positiveInteger(source.temporalResolutionHours, 'source temporal resolution'),
      artifactGeneratedAt: timestamp(source.artifactGeneratedAt, 'source artifactGeneratedAt'), runAt, validFrom, validTo, referencePeriod,
    },
  };
}

function componentScore(value: unknown, label: string): { value: number; category: string } {
  const component = exactObject(value, ['value', 'category'], `${label} component`);
  return { value: boundedScore(component.value, `${label} score`), category: text(component.category, `${label} category`) };
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
function positiveNumber(value: unknown, label: string): number { const parsed = number(value, label); if (parsed <= 0) throw new Error(`Invalid ${label}.`); return parsed; }
function boundedScore(value: unknown, label: string): number { const parsed = integer(value, label); if (parsed < 0 || parsed > 100) throw new Error(`Invalid ${label}.`); return parsed; }
function optionalNumber(value: unknown, label: string): number | undefined { return value === undefined ? undefined : number(value, label); }
function optionalText(value: unknown, label: string): string | undefined { return value === undefined ? undefined : text(value, label); }
function timestamp(value: unknown, label: string): string { const parsed = text(value, label); if (Number.isNaN(Date.parse(parsed))) throw new Error(`Invalid ${label}.`); return parsed; }
function optionalTimestamp(value: unknown, label: string): string | undefined { return value === undefined ? undefined : timestamp(value, label); }
function date(value: unknown, label: string): string { const parsed = text(value, label); if (!/^\d{4}-\d{2}-\d{2}$/.test(parsed) || new Date(`${parsed}T00:00:00Z`).toISOString().slice(0, 10) !== parsed) throw new Error(`Invalid ${label}.`); return parsed; }
function color(value: unknown, label: string): string { const parsed = text(value, label); if (!/^#[0-9A-Fa-f]{6}$/.test(parsed)) throw new Error(`Invalid ${label}.`); return parsed; }
function checksum(value: unknown, label: string): string { const parsed = text(value, label); if (!/^[0-9a-f]{64}$/.test(parsed)) throw new Error(`Invalid ${label}.`); return parsed; }
function releaseId(value: unknown, label: string): string { const parsed = text(value, label); if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(parsed)) throw new Error(`Invalid ${label}.`); return parsed; }

function assetPath(value: string, label: string, root: 'images' | 'grids'): string {
  const path = value.replace(/^\//, '');
  let decoded: string;
  try { decoded = decodeURIComponent(path); } catch { throw new Error(`Invalid ${label}.`); }
  const segments = decoded.split('/');
  if (segments[0] !== root || segments.length < 2 || segments.some((segment) => segment === '' || segment === '.' || segment === '..') || decoded.includes('\\')) throw new Error(`Invalid ${label}.`);
  return path;
}

function imagePath(value: string, label: string): string { return assetPath(value, label, 'images'); }

async function isStaleReleaseResponse(response: Response): Promise<boolean> {
  try { const body = object(await response.json(), 'error response'); return object(body.error, 'error').code === 'stale_release'; } catch { return false; }
}
