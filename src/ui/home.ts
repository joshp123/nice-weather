// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import type { Place } from './state';
import { searchPlaces } from './api';
import type { Period } from '../engine';

const city = (name: string, region: string, latitude: number, longitude: number): Place => ({ name, region, latitude, longitude, slug: null });
const TIME_ZONE_HOMES: Record<string, Place> = {
  'Europe/Amsterdam': city('Rotterdam', 'Netherlands', 51.9225, 4.47917),
  'Europe/London': city('London', 'United Kingdom', 51.50853, -0.12574),
  'Europe/Paris': city('Paris', 'France', 48.85341, 2.3488),
  'Europe/Berlin': city('Berlin', 'Germany', 52.52437, 13.41053),
  'Europe/Athens': city('Athens', 'Greece', 37.98376, 23.72784),
  'America/New_York': city('New York', 'United States', 40.71427, -74.00597),
  'America/Los_Angeles': city('San Francisco', 'United States', 37.77493, -122.41942),
  'America/Chicago': city('Chicago', 'United States', 41.85003, -87.65005),
  'America/Toronto': city('Toronto', 'Canada', 43.70643, -79.39864),
  'Australia/Sydney': city('Sydney', 'Australia', -33.86785, 151.20732),
  'Australia/Melbourne': city('Melbourne', 'Australia', -37.814, 144.96332),
  'Australia/Perth': city('Perth', 'Australia', -31.95224, 115.8614),
  'Pacific/Auckland': city('Auckland', 'New Zealand', -36.84853, 174.76349),
  'Asia/Singapore': city('Singapore', 'Singapore', 1.28967, 103.85007),
  'Asia/Tokyo': city('Tokyo', 'Japan', 35.6895, 139.69171),
  'Asia/Hong_Kong': city('Hong Kong', 'Hong Kong', 22.27832, 114.17469),
  'Africa/Johannesburg': city('Cape Town', 'South Africa', -33.92584, 18.42322),
  'America/Santiago': city('Santiago', 'Chile', -33.45694, -70.64827),
};
export function timeZoneHome(): Place | null {
  return TIME_ZONE_HOMES[Intl.DateTimeFormat().resolvedOptions().timeZone] ?? null;
}
export async function inferredHome(): Promise<Place | null> {
  const mapped = timeZoneHome();
  if (mapped) return mapped;
  const name = Intl.DateTimeFormat().resolvedOptions().timeZone.split('/').at(-1)?.replaceAll('_', ' ');
  if (!name) return null;
  const found = (await searchPlaces(name)).find(place => place.name.toLowerCase() === name.toLowerCase());
  return found ? { ...found, slug: found.slug ?? null } : null;
}
export async function requestHome(): Promise<Place | null> {
  const response = await fetch('/api/home', { cache: 'no-store' });
  return response.ok ? response.json() : null;
}
export function winterPeriod(home: Place | null): Period {
  return home && home.latitude < 0 ? { kind: 'season', season: 'Jun-Aug' } : { kind: 'months', startMonth: 11, endMonth: 3 };
}
export function isHomeWinter(period: Period, home: Place | null): boolean {
  return home?.latitude !== undefined && (home.latitude < 0
    ? period.kind === 'season' && period.season === 'Jun-Aug'
    : period.kind === 'months' && period.startMonth === 11 && period.endMonth === 3);
}
export function regionalComparison(home: Place | null): Place {
  if (!home) return TIME_ZONE_HOMES['Europe/London'];
  let primary = 'Europe/London';
  let alternative = 'Europe/Paris';
  if (home.longitude < -30) {
    primary = home.longitude < -105 ? 'America/Los_Angeles' : 'America/New_York';
    alternative = home.longitude < -105 ? 'America/New_York' : 'America/Los_Angeles';
  } else if (home.longitude > 110 && home.latitude < 0) {
    primary = 'Australia/Sydney';
    alternative = 'Australia/Melbourne';
  } else if (home.longitude > 60) {
    primary = 'Asia/Tokyo';
    alternative = 'Asia/Hong_Kong';
  }
  const comparison = TIME_ZONE_HOMES[primary];
  const comparisonIsHome = Math.abs(home.latitude - comparison.latitude) < .5 && Math.abs(home.longitude - comparison.longitude) < .5;
  return TIME_ZONE_HOMES[comparisonIsHome ? alternative : primary];
}
