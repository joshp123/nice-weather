import type { LegendCategory, Period, PointRequest, PointWeather, WeatherImage, WeatherManifest } from './types';

export class StaleReleaseError extends Error {
  readonly code = 'stale_release';

  constructor(message = 'Weather data changed. Refreshing the forecast.') {
    super(message);
    this.name = 'StaleReleaseError';
  }
}

export async function loadManifest(signal?: AbortSignal): Promise<WeatherManifest> {
  const response = await fetch('/manifest.json', { signal });
  if (!response.ok) throw new Error(`Forecast data returned ${response.status}.`);
  return parseManifest(await response.json());
}

export async function loadPoint(request: PointRequest, signal?: AbortSignal): Promise<PointWeather> {
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
    if (response.status === 404) throw new Error('Weather is not available at this point.');
    throw new Error('Weather details could not be loaded.');
  }
  const point = parsePoint(await response.json());
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
  const data = object(value, 'manifest');
  if (data.version !== 4) throw new Error('This forecast uses an unsupported data format.');
  if (data.productContract !== 'nice-weather-v7') throw new Error('This forecast uses an unsupported product contract.');
  if (data.dataStatus !== 'live' && data.dataStatus !== 'sample') throw new Error('Invalid manifest dataStatus.');
  const region = object(data.region, 'region');
  const bounds = object(region.bounds, 'region bounds');
  const profiles = array(data.profiles, 'profiles').map((item, index) => object(item, `profile ${index + 1}`));
  if (profiles.length !== 2 || profiles[0].id !== 'nice' || profiles[1].id !== 'cycling') throw new Error('Invalid manifest profiles.');
  const forecast = object(data.forecast, 'forecast');
  const climate = object(data.climate, 'climate');
  const days = periodList(forecast.days, 'forecast');
  const months = periodList(climate.months, 'climate');
  if (days.length !== 14) throw new Error('The forecast must contain 14 days.');
  if (months.length !== 12) throw new Error('Typical weather must contain 12 months.');

  return {
    version: 4,
    releaseId: releaseId(data.releaseId, 'releaseId'),
    productContract: 'nice-weather-v7',
    dataStatus: data.dataStatus,
    generatedAt: text(data.generatedAt, 'generatedAt'),
    bounds: [number(bounds.west, 'west'), number(bounds.south, 'south'), number(bounds.east, 'east'), number(bounds.north, 'north')],
    forecast: {
      updatedAt: text(forecast.updatedAt, 'forecast updatedAt'),
      staleAfterHours: number(forecast.staleAfterHours, 'staleAfterHours'),
      days,
    },
    climate: {
      period: text(climate.referencePeriod, 'climate referencePeriod'),
      months,
    },
    legend: legendList(data.legend),
    palette: paletteList(data.palette),
  };
}

function paletteList(value: unknown): Array<{ score: number; color: string }> {
  const stops = array(value, 'palette stops');
  if (stops.length !== 12) throw new Error('Invalid palette stops.');
  const parsed = stops.map((item, index) => {
    const stop = object(item, `palette stop ${index + 1}`);
    return { score: number(stop.score, 'palette score'), color: text(stop.color, 'palette color') };
  });
  if (parsed[0].score !== 0 || parsed[parsed.length - 1].score !== 100 || parsed.some((stop, index) => !/^#[0-9A-Fa-f]{6}$/.test(stop.color) || (index > 0 && stop.score <= parsed[index - 1].score))) {
    throw new Error('Invalid palette stops.');
  }
  return parsed;
}

function periodList(value: unknown, basis: 'forecast' | 'climate'): Period[] {
  return array(value, `${basis} periods`).map((item, index) => {
    const row = object(item, `${basis} period ${index + 1}`);
    const source = object(row.source, `${basis} source`);
    const images = object(row.images, `${basis} images`);
    const lenses = object(row.lenses, `${basis} lenses`);
    const label = text(row.label, `${basis} label`);
    const period: Period = {
      id: text(row.id, `${basis} id`),
      label,
      shortLabel: basis === 'forecast' ? shortDate(text(row.date, 'forecast date')) : label.slice(0, 3),
      resolutionKm: number(row.nativeResolutionKm, 'nativeResolutionKm'),
      temporalResolutionHours: number(row.temporalResolutionHours, 'temporalResolutionHours'),
      providerLabel: text(source.providerLabel, 'providerLabel'),
      sourceRunAt: text(source.runAt, 'source runAt'),
      images: {
        nice: weatherImage(images.nice, `${basis} nice image`),
        cycling: weatherImage(images.cycling, `${basis} cycling image`),
      },
      lenses: {
        comfort: weatherImage(lenses.comfort, `${basis} comfort lens`),
        dry: weatherImage(lenses.dry, `${basis} dry lens`),
        bright: weatherImage(lenses.bright, `${basis} bright lens`),
        'wind-nice': weatherImage(lenses['wind-nice'], `${basis} nice wind lens`),
        'wind-cycling': weatherImage(lenses['wind-cycling'], `${basis} cycling wind lens`),
      },
    };
    if (basis === 'forecast') {
      period.date = text(row.date, 'forecast date');
      if (row.kind !== 'detailed' && row.kind !== 'outlook') throw new Error('Invalid forecast kind.');
      period.kind = row.kind;
    } else {
      period.month = number(row.month, 'climate month');
    }
    return period;
  });
}

function weatherImage(value: unknown, label: string): WeatherImage {
  const row = object(value, label);
  if (row.projection !== 'web-mercator') throw new Error(`${label} uses an unsupported projection.`);
  return {
    url: imagePath(text(row.url, `${label} URL`), `${label} URL`),
    sha256: text(row.sha256, `${label} checksum`),
    width: positiveInteger(row.width, `${label} width`),
    height: positiveInteger(row.height, `${label} height`),
    projection: 'web-mercator',
  };
}

function imagePath(value: string, label: string): string {
  const path = value.replace(/^\//, '');
  let decoded: string;
  try {
    decoded = decodeURIComponent(path);
  } catch {
    throw new Error(`Invalid ${label}.`);
  }
  const segments = decoded.split('/');
  if (segments[0] !== 'images' || segments.length < 2 || segments.some((segment) => segment === '' || segment === '.' || segment === '..') || decoded.includes('\\')) {
    throw new Error(`Invalid ${label}.`);
  }
  return path;
}

function legendList(value: unknown): LegendCategory[] {
  const categories = array(value, 'legend categories');
  if (categories.length !== 6) throw new Error('Invalid legend categories.');
  return categories.map((item, index) => {
    const row = object(item, `legend category ${index + 1}`);
    return {
      label: text(row.label, 'legend label'),
      color: text(row.color, 'legend color'),
      min: number(row.min, 'legend min'),
      max: number(row.max, 'legend max'),
    };
  });
}

function parsePoint(value: unknown): PointWeather {
  const data = object(value, 'point weather');
  const location = object(data.location, 'point location');
  const score = object(data.score, 'point score');
  const components = object(score.components, 'score components');
  const weather = object(data.weather, 'point weather metrics');
  const source = object(data.source, 'point source');
  if (source.basis !== 'forecast' && source.basis !== 'climate') throw new Error('Invalid source basis.');
  const basis = source.basis;
  const runAt = optionalText(source.runAt, 'source runAt');
  const validFrom = optionalText(source.validFrom, 'source validFrom');
  const validTo = optionalText(source.validTo, 'source validTo');
  const referencePeriod = optionalText(source.referencePeriod, 'source referencePeriod');
  if (basis === 'forecast' && (!runAt || !validFrom || !validTo)) throw new Error('Invalid forecast source timing.');
  if (basis === 'climate' && !referencePeriod) throw new Error('Invalid climate reference period.');
  return {
    releaseId: releaseId(data.releaseId, 'point releaseId'),
    lat: number(location.lat, 'latitude'),
    lon: number(location.lon, 'longitude'),
    periodLabel: text(data.periodLabel, 'period label'),
    score: number(score.value, 'score'),
    category: text(score.category, 'score category'),
    components: {
      comfort: componentScore(components.comfort, 'comfort'),
      dryness: componentScore(components.dryness, 'dryness'),
      sun: componentScore(components.sun, 'sun'),
      wind: componentScore(components.wind, 'wind'),
    },
    summary: text(data.summary, 'summary'),
    metrics: {
      temperatureC: number(weather.temperatureC, 'temperature'),
      minTempC: number(weather.minTempC, 'minimum temperature'),
      maxTempC: number(weather.maxTempC, 'maximum temperature'),
      rainMm: number(weather.rainMm, 'rain'),
      wetTimeFraction: optionalNumber(weather.wetTimeFraction, 'wetTimeFraction'),
      solarEnergyMJm2: optionalNumber(weather.solarEnergyMJm2, 'solarEnergyMJm2'),
      cloudPct: optionalNumber(weather.cloudPct, 'cloudPct'),
      windKmh: number(weather.windKmh, 'wind'),
      gustKmh: optionalNumber(weather.gustKmh, 'gustKmh'),
      humidityPct: optionalNumber(weather.humidityPct, 'humidityPct'),
      dewPointC: optionalNumber(weather.dewPointC, 'dewPointC'),
    },
    source: {
      basis,
      detailLabel: text(source.detailLabel, 'source detail label'),
      providerLabel: text(source.providerLabel, 'source provider label'),
      resolutionKm: number(source.resolutionKm, 'source resolution'),
      temporalResolutionHours: number(source.temporalResolutionHours, 'source temporal resolution'),
      artifactGeneratedAt: text(source.artifactGeneratedAt, 'source artifactGeneratedAt'),
      runAt,
      validFrom,
      validTo,
      referencePeriod,
    },
  };
}

function componentScore(value: unknown, label: string): { value: number; category: string } {
  const component = object(value, `${label} component`);
  return { value: number(component.value, `${label} score`), category: text(component.category, `${label} category`) };
}

export function freshnessLabel(updatedAt: string, now = new Date()): string {
  const elapsedMinutes = Math.max(0, Math.round((now.getTime() - new Date(updatedAt).getTime()) / 60_000));
  if (!Number.isFinite(elapsedMinutes)) return 'Update time unknown';
  if (elapsedMinutes < 2) return 'Updated just now';
  if (elapsedMinutes < 60) return `Updated ${elapsedMinutes} minutes ago`;
  const hours = Math.round(elapsedMinutes / 60);
  if (hours < 24) return `Updated ${hours} ${hours === 1 ? 'hour' : 'hours'} ago`;
  return `Updated ${Math.round(hours / 24)} days ago`;
}

export function isStale(updatedAt: string, staleAfterHours: number, now = new Date()): boolean {
  const age = now.getTime() - new Date(updatedAt).getTime();
  return !Number.isFinite(age) || age > staleAfterHours * 3_600_000;
}

export function forecastRunStatus(runAt: string, staleAfterHours: number, now = new Date()): { stale: boolean; label: string } {
  const stale = isStale(runAt, staleAfterHours, now);
  return { stale, label: stale ? 'Forecast is out of date' : freshnessLabel(runAt, now) };
}

function shortDate(date: string): string {
  const parsed = new Date(`${date}T12:00:00Z`);
  if (Number.isNaN(parsed.getTime())) throw new Error('Invalid forecast date.');
  return new Intl.DateTimeFormat('en-GB', { weekday: 'short', day: 'numeric', timeZone: 'UTC' }).format(parsed);
}

function object(value: unknown, label: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error(`Invalid ${label}.`);
  return value as Record<string, unknown>;
}

function array(value: unknown, label: string): unknown[] {
  if (!Array.isArray(value)) throw new Error(`Invalid ${label}.`);
  return value;
}

function text(value: unknown, label: string): string {
  if (typeof value !== 'string' || value.length === 0) throw new Error(`Invalid ${label}.`);
  return value;
}

function number(value: unknown, label: string): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) throw new Error(`Invalid ${label}.`);
  return value;
}

function positiveInteger(value: unknown, label: string): number {
  const parsed = number(value, label);
  if (!Number.isInteger(parsed) || parsed < 1) throw new Error(`Invalid ${label}.`);
  return parsed;
}

function optionalNumber(value: unknown, label: string): number | undefined {
  return value === undefined ? undefined : number(value, label);
}

function optionalText(value: unknown, label: string): string | undefined {
  return value === undefined ? undefined : text(value, label);
}

function releaseId(value: unknown, label: string): string {
  const parsed = text(value, label);
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(parsed)) throw new Error(`Invalid ${label}.`);
  return parsed;
}

async function isStaleReleaseResponse(response: Response): Promise<boolean> {
  try {
    const body = object(await response.json(), 'error response');
    const error = object(body.error, 'error');
    return error.code === 'stale_release';
  } catch {
    return false;
  }
}
