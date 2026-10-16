// Written by AI on 2026.10.16 by model claude opus 5.5.
// What the Sites Worker sends. The browser only reads and draws; Sites does the work.
import type { Grid } from './weatherLayer';

/** forecast/current.json: the live run's manifest, as written by `niceweather forecast`. */
export type ForecastIndex = Readonly<{
  runId: string;
  /** ISO time of the ECMWF model run. */
  runTime: string;
  dates: readonly string[];
  grid: Grid;
}>;

export type ClimateIndex = Readonly<{
  /** For example "2006-2025". */
  years: string;
  grid: Grid;
}>;

export type DayWeather = Readonly<{
  date: string;
  scores: Readonly<{ nice: number; cycling: number; comfort: number; dry: number; clear: number; calmNice: number; calmCycling: number }>;
  temperatureC: number;
  dewPointC: number;
  rainMm: number;
  wetPercent: number;
  cloudPercent: number;
  windKph: number;
  gustKph: number | null;
}>;

export type MonthClimate = Readonly<{
  month: number;
  /** Share of real days 2006-2025 rated Nice or better, 0-100, per profile. */
  niceChance: Readonly<{ nice: number; cycling: number }>;
  awfulChance: Readonly<{ nice: number; cycling: number }>;
  medianScore: Readonly<{ nice: number; cycling: number }>;
  /** Daytime (10:00-20:00) temperature: typical, a cool day (1 in 10 cooler), a warm day (1 in 10 warmer). */
  temperatureC: number;
  coolDayC: number;
  warmDayC: number;
  highC: number;
  dewPointC: number;
  muggyDaysPercent: number;
  rainFreeDaysPercent: number;
  cloudPercent: number;
  clearDaysPercent: number;
  windKph: number;
}>;

export type PlaceWeather = Readonly<{
  name: string;
  region: string;
  latitude: number;
  longitude: number;
  days: readonly DayWeather[] | null;
  months: readonly MonthClimate[] | null;
}>;

export type SearchResult = Readonly<{ name: string; region: string; latitude: number; longitude: number }>;

async function json<T>(url: string): Promise<T> {
  const response = await fetch(url);
  if (!response.ok) throw new Error(`${url}: HTTP ${response.status}`);
  return response.json() as Promise<T>;
}

export const fetchForecastIndex = () => json<ForecastIndex>('/data/forecast/current.json');
export const fetchClimateIndex = async (): Promise<ClimateIndex> => {
  const index = await json<{ years: string; grid: Grid & { resolutionDegrees: number } }>('/data/climate/current.json');
  return { years: index.years.replace('-', '–'), grid: { ...index.grid, resolution: index.grid.resolutionDegrees } };
};
export const fetchPlace = (latitude: number, longitude: number) =>
  json<PlaceWeather>(`/api/place?lat=${latitude.toFixed(3)}&lon=${longitude.toFixed(3)}`);
export const searchPlaces = (query: string) => json<SearchResult[]>(`/api/search?q=${encodeURIComponent(query)}`);
