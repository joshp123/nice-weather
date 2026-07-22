import type { Profile } from './types';
import { readBoundedResponse } from './boundedResponse';

const SCHEMA = 'nice-best-weather-world-v1';

export type BestWeatherPlace = {
  geonameId: number;
  name: string;
  admin1?: string;
  country: string;
  countryCode: string;
  latitude: number;
  longitude: number;
  score: number;
  niceDays: number;
};

export type BestWeather = {
  forecastIndexId: string;
  placeReleaseId: string;
  profile: Profile;
  startDate: string;
  endDate: string;
  forecastRunUtc: string;
  results: BestWeatherPlace[];
};

export type BestWeatherForecast = {
  indexId: string;
  firstDate: string;
  lastDate: string;
  runUtc: string;
};

export async function loadBestWeather(forecast: BestWeatherForecast, profile: Profile, signal?: AbortSignal): Promise<BestWeather> {
  const { indexId: forecastIndexId } = forecast;
  const pointerResponse = await fetch('/best-weather/current.json', { signal, cache: 'no-cache' });
  if (!pointerResponse.ok) throw new Error('Best weather is unavailable.');
  const pointer = exactObject(parseJson(await readBoundedResponse(pointerResponse, 'best-weather pointer', 1_024)), ['schema', 'forecastIndexId', 'placeReleaseId', 'url', 'sha256']);
  if (pointer.schema !== SCHEMA || pointer.forecastIndexId !== forecastIndexId || typeof pointer.placeReleaseId !== 'string' || typeof pointer.url !== 'string' || !/^\/best-weather\/releases\/[A-Za-z0-9._-]+\.json$/.test(pointer.url) || !/^[a-f0-9]{64}$/.test(pointer.sha256)) {
    throw new Error('Best weather could not be read.');
  }
  const placePointerResponse = await fetch('/places/current.json', { signal, cache: 'no-cache' });
  if (!placePointerResponse.ok) throw new Error('Best weather is unavailable.');
  const placePointer = exactObject(parseJson(await readBoundedResponse(placePointerResponse, 'place index pointer', 1_024)), ['releaseId', 'manifestSha256']);
  if (placePointer.releaseId !== pointer.placeReleaseId || typeof placePointer.manifestSha256 !== 'string' || !/^[a-f0-9]{64}$/.test(placePointer.manifestSha256)) throw new Error('Best weather could not be read.');
  const response = await fetch(pointer.url, { signal });
  if (!response.ok) throw new Error('Best weather is unavailable.');
  const bytes = await readBoundedResponse(response, 'best-weather release', 32 * 1024);
  if (bytes.byteLength < 1 || await sha256(bytes) !== pointer.sha256) throw new Error('Best weather could not be read.');
  const value = exactObject(parseJson(bytes), ['schema', 'forecastIndexId', 'placeReleaseId', 'startDate', 'endDate', 'forecastRunUtc', 'profiles']);
  const profiles = exactObject(value.profiles, ['nice', 'cycling']);
  if (value.schema !== SCHEMA || value.forecastIndexId !== forecastIndexId || value.placeReleaseId !== pointer.placeReleaseId || value.startDate !== forecast.firstDate || value.endDate !== forecast.lastDate || value.forecastRunUtc !== forecast.runUtc || !date(value.startDate) || !date(value.endDate) || !rfc3339(value.forecastRunUtc) || daysBetween(value.startDate, value.endDate) !== 13 || !Array.isArray(profiles.nice) || !Array.isArray(profiles.cycling) || profiles.nice.length !== 6 || profiles.cycling.length !== 6) {
    throw new Error('Best weather could not be read.');
  }
  const parsed = { nice: profiles.nice.map(parsePlace), cycling: profiles.cycling.map(parsePlace) };
  for (const entries of Object.values(parsed)) {
    const identities = new Set(entries.map(({ geonameId }) => geonameId));
    if (identities.size !== entries.length || entries.some((item, index) => index > 0 && item.score > entries[index - 1].score)) throw new Error('Best weather could not be read.');
  }
  const results = parsed[profile];
  return { forecastIndexId, placeReleaseId: value.placeReleaseId, profile, startDate: value.startDate, endDate: value.endDate, forecastRunUtc: value.forecastRunUtc, results };
}

function parsePlace(value: unknown): BestWeatherPlace {
  const object = objectValue(value);
  const data = exactObject(object, object.admin1 === undefined
    ? ['geonameId', 'name', 'country', 'countryCode', 'latitude', 'longitude', 'score', 'niceDays']
    : ['geonameId', 'name', 'admin1', 'country', 'countryCode', 'latitude', 'longitude', 'score', 'niceDays']);
  const strings = ['name', 'country', 'countryCode'] as const;
  const numbers = ['geonameId', 'latitude', 'longitude', 'score', 'niceDays'] as const;
  if (strings.some((key) => typeof data[key] !== 'string' || data[key].trim() === '') || numbers.some((key) => typeof data[key] !== 'number' || !Number.isFinite(data[key])) || data.admin1 !== undefined && typeof data.admin1 !== 'string') {
    throw new Error('Best weather could not be read.');
  }
  if (!Number.isInteger(data.geonameId) || data.geonameId <= 0 || data.latitude < -90 || data.latitude > 90 || data.longitude < -180 || data.longitude > 180 || !Number.isInteger(data.score) || data.score < 0 || data.score > 100 || !Number.isInteger(data.niceDays) || data.niceDays < 0 || data.niceDays > 14) throw new Error('Best weather could not be read.');
  return data as unknown as BestWeatherPlace;
}

function objectValue(value: unknown): Record<string, any> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error('Best weather could not be read.');
  return value as Record<string, any>;
}

function exactObject(value: unknown, keys: readonly string[]): Record<string, any> {
  const result = objectValue(value);
  const actual = Object.keys(result).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) throw new Error('Best weather could not be read.');
  return result;
}

function date(value: unknown): value is string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  return new Date(`${value}T00:00:00Z`).toISOString().slice(0, 10) === value;
}

function rfc3339(value: unknown): value is string {
  return typeof value === 'string'
    && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(value)
    && !Number.isNaN(Date.parse(value));
}

function daysBetween(start: string, end: string): number {
  return (Date.parse(`${end}T00:00:00Z`) - Date.parse(`${start}T00:00:00Z`)) / 86_400_000;
}

function parseJson(bytes: Uint8Array): unknown {
  try {
    return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as unknown;
  } catch {
    throw new Error('Best weather could not be read.');
  }
}

async function sha256(value: Uint8Array): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', value as BufferSource);
  return Array.from(new Uint8Array(digest), (item) => item.toString(16).padStart(2, '0')).join('');
}
