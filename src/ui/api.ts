// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// What the Sites Worker answers (hosting/sites/worker.mjs). The browser draws; the Worker names places and keeps the typical numbers.
import type { ForecastIndex } from './periods';
import type { Place } from './state';

/** One month of a place's typical year, from twenty years of real days. */
export type MonthFacts = Readonly<{
  month: number;
  highC: number;
  feelsAt21C: number;
  dewPointC: number;
  wetDaysPercent: number;
  sunnyPercent: number;
  windKmh: number;
  /** At the default taste; present once the Worker publishes scores. */
  score?: number;
  niceDayShare?: number;
}>;

export type PlaceDetails = Place & Readonly<{ months: readonly MonthFacts[] | null; overviewMonths?: readonly MonthFacts[] | null }>;
export type SearchResult = Readonly<{ name: string; region: string; latitude: number; longitude: number; slug?: string | null }>;

async function json<T>(url: string): Promise<T> {
  const response = await fetch(url);
  if (!response.ok) throw new Error(`${url}: HTTP ${response.status}`);
  return response.json() as Promise<T>;
}

export const fetchForecastIndex = (dataUrl: string) => json<ForecastIndex>(`${dataUrl}/forecast/current.json`);

export async function fetchPlace(latitude: number, longitude: number): Promise<PlaceDetails> {
  const details = await json<Omit<PlaceDetails, 'slug'> & { slug?: string | null }>(`/api/place?lat=${latitude}&lon=${longitude}`);
  return { ...details, slug: details.slug ?? null };
}

export const searchPlaces = (query: string) => json<SearchResult[]>(`/api/search?q=${encodeURIComponent(query)}`);

export async function resolveSlug(slug: string): Promise<Place | null> {
  const response = await fetch(`/api/slug/${slug.split('/').map(encodeURIComponent).join('/')}`);
  if (response.status === 404) return null;
  if (!response.ok) throw new Error(`/api/slug/${slug}: HTTP ${response.status}`);
  const place = (await response.json()) as Omit<Place, 'slug'> & { slug?: string };
  return { ...place, slug: place.slug ?? slug };
}
