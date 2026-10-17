// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import { countryCode } from './countries';

export type RankedPlace = Readonly<{ name: string; latitude: number; longitude: number; score: number; niceDayCount: number; perfectDayCount: number; population?: number; slug?: string | null; region?: string }>;
export const WORLD_POPULATION = 300000;
export const CONTINENT_POPULATION = 50000;
const SEPARATION_KM = 250;
const NEARBY_KM = 50;
const SIMILAR_SCORE = 5;

export function mostNiceDaysFirst(a: RankedPlace, b: RankedPlace): number {
  return Math.round(b.niceDayCount) - Math.round(a.niceDayCount) || Math.round(b.perfectDayCount) - Math.round(a.perfectDayCount)
    || (b.population ?? 0) - (a.population ?? 0) || ((a.slug ?? a.name) < (b.slug ?? b.name) ? -1 : (a.slug ?? a.name) > (b.slug ?? b.name) ? 1 : 0);
}

function distanceKM(a: RankedPlace, b: RankedPlace): number {
  const radians = Math.PI / 180;
  const squared = Math.sin((b.latitude - a.latitude) * radians / 2) ** 2 + Math.cos(a.latitude * radians) * Math.cos(b.latitude * radians) * Math.sin((b.longitude - a.longitude) * radians / 2) ** 2;
  return 6371 * 2 * Math.asin(Math.sqrt(Math.min(1, squared)));
}

export function rankPlaces<T extends RankedPlace>(places: readonly T[], minimumPopulation: number, limit = 3, resolve?: (place: T) => T | null): T[] {
  const candidates = places.filter(place => (place.population ?? 0) >= minimumPopulation);
  const chosen: T[] = [], winners: T[] = [];
  for (const winner of candidates.filter(place => Number.isFinite(place.score) && Number.isFinite(place.niceDayCount)).sort(mostNiceDaysFirst)) {
    let named = winner;
    for (const candidate of candidates) {
      if ((candidate.population ?? 0) < (named.population ?? 0) || distanceKM(winner, candidate) > NEARBY_KM) continue;
      const numbered = Number.isFinite(candidate.score) ? candidate : resolve?.(candidate);
      if (!numbered || Math.abs(numbered.score - winner.score) > SIMILAR_SCORE) continue;
      if ((numbered.population ?? 0) > (named.population ?? 0) || numbered.population === named.population && (numbered.slug ?? numbered.name) < (named.slug ?? named.name)) named = numbered;
    }
    if (chosen.some((place, index) => distanceKM(winner, winners[index]) < SEPARATION_KM || distanceKM(named, place) < SEPARATION_KM)) continue;
    chosen.push(named); winners.push(winner);
    if (chosen.length === limit) break;
  }
  return chosen.sort(mostNiceDaysFirst);
}

const EUROPE_CODES = new Set('AL AD AT BY BE BA BG HR CY CZ DK EE FO FI FR DE GI GR GG VA HU IS IE IM IT JE XK LV LI LT LU MT MD MC ME NL MK NO PL PT RO RU SM RS SK SI ES SE CH UA GB TR'.split(' '));
const EUROPEAN_TURKEY = [[25,42.5],[29.3,42.5],[29.12,41.23],[29.075,41.12],[29.035,41.045],[29.005,41.025],[28.985,41],[28.98,40.98],[28.6,40.8],[27.5,40.4],[26.75,40.35],[26.4,40.2],[26.17,40.03],[25.8,39.8],[25,39.8]];

export function inOutlookRegion(place: Pick<RankedPlace, 'latitude' | 'longitude' | 'region' | 'slug'>, region: string): boolean {
  const country = countryCode(place), { latitude, longitude } = place;
  if (country === 'PS' || country === 'IL') return false;
  if (region === 'the USA') return country === 'US' && (latitude >= 24 && latitude <= 72 && longitude >= -180 && longitude <= -66 || latitude >= 18 && latitude <= 23 && longitude >= -161 && longitude <= -154);
  if (region !== 'Europe') return true;
  if (latitude < 27 || latitude > 72 || longitude < -32 || longitude > 40 || !EUROPE_CODES.has(country)) return false;
  if (country === 'ES' && latitude < 36) return longitude >= -18.5 && longitude <= -13 && latitude <= 30;
  if (country !== 'TR') return true;
  let inside = false, previous = EUROPEAN_TURKEY.at(-1)!;
  for (const current of EUROPEAN_TURKEY) {
    if ((current[1] > latitude) !== (previous[1] > latitude) && longitude < (previous[0] - current[0]) * (latitude - current[1]) / (previous[1] - current[1]) + current[0]) inside = !inside;
    previous = current;
  }
  return inside;
}
