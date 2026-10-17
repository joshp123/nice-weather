// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// What the visitor is looking at, and the link that reopens it.
//   /                      the world, next 14 days
//   /july                  the world in July
//   /portugal/lisbon/july  a place and a period
//   /@38.72,-9.14,6z       any point, with the map zoom
//   ?perfectFrom=21        a slider moved from its default
import { DEFAULT_PERIOD, periodFromWord, periodWord, samePeriod } from './periods';
import { SLIDERS, type Period } from '../engine';

export type SliderId = (typeof SLIDERS)[number]['id'];
export type SliderValues = Readonly<Record<SliderId, number>>;
export type Place = Readonly<{ name: string; region: string; latitude: number; longitude: number; slug: string | null }>;
export type Camera = Readonly<{ center: readonly [number, number]; zoom: number }>;

export type AppState = Readonly<{
  period: Period;
  sliders: SliderValues;
  place: Place | null;
}>;

export const DEFAULT_SLIDERS: SliderValues = Object.fromEntries(SLIDERS.map(slider => [slider.id, slider.default])) as SliderValues;

export function slidersAreDefault(sliders: SliderValues): boolean {
  return SLIDERS.every(slider => sliders[slider.id] === slider.default);
}

export type LinkTarget =
  | Readonly<{ kind: 'slug'; slug: string }>
  | Readonly<{ kind: 'point'; latitude: number; longitude: number; zoom: number | null }>
  | null;

export type ParsedLink = Readonly<{ period: Period | null; sliders: Partial<SliderValues>; target: LinkTarget }>;

export function parseLink(url: URL): ParsedLink {
  const parts = url.pathname.split('/').filter(Boolean).map(decodeURIComponent);
  let target: LinkTarget = null;
  let period: Period | null = null;
  const first = parts[0];
  if (first?.startsWith('@')) {
    const match = /^@(-?\d+(?:\.\d+)?),(-?\d+(?:\.\d+)?)(?:,(\d+(?:\.\d+)?)z)?$/.exec(first);
    if (match) target = { kind: 'point', latitude: Number(match[1]), longitude: Number(match[2]), zoom: match[3] ? Number(match[3]) : null };
    period = parts[1] ? periodFromWord(parts[1]) : null;
  } else if (first) {
    period = periodFromWord(first);
    if (!period) {
      const hasCountry = Boolean(parts[1] && !periodFromWord(parts[1]));
      target = { kind: 'slug', slug: hasCountry ? `${first}/${parts[1]}` : first };
      period = periodFromWord(parts[hasCountry ? 2 : 1] ?? '');
    }
  }
  const sliders: Partial<Record<SliderId, number>> = {};
  for (const slider of SLIDERS) {
    const raw = url.searchParams.get(slider.id);
    if (raw === null) continue;
    const value = Number(raw);
    if (Number.isFinite(value)) sliders[slider.id] = Math.min(slider.max, Math.max(slider.min, value));
  }
  return { period, sliders, target };
}

export function buildLink(state: AppState, zoom: number): string {
  const word = samePeriod(state.period, DEFAULT_PERIOD) ? '' : periodWord(state.period);
  let path = '/';
  if (state.place?.slug) path = `/${state.place.slug}`;
  else if (state.place) path = `/@${state.place.latitude.toFixed(2)},${state.place.longitude.toFixed(2)},${zoom.toFixed(zoom % 1 ? 1 : 0)}z`;
  if (word) path = path === '/' ? `/${word}` : `${path}/${word}`;
  const query = new URLSearchParams();
  for (const slider of SLIDERS) if (state.sliders[slider.id] !== slider.default) query.set(slider.id, String(state.sliders[slider.id]));
  const search = query.toString();
  return search ? `${path}?${search}` : path;
}

// The last view, kept on this device so the map reopens where it was left (Josh: "remember my last view").
const SAVED_VIEW_KEY = 'nice-weather-view-v6';
type SavedView = { period: string; camera: Camera; sliders?: SliderValues; place?: Place | null };

export function loadSavedView(): { period: Period | null; camera: Camera | null; sliders?: SliderValues; place?: Place | null } {
  try {
    const saved = JSON.parse(localStorage.getItem(SAVED_VIEW_KEY) ?? 'null') as SavedView | null;
    if (!saved) return { period: null, camera: null };
    return { period: periodFromWord(saved.period), camera: saved.camera, sliders: saved.sliders, place: saved.place };
  } catch {
    return { period: null, camera: null };
  }
}

export function saveView(state: AppState, camera: Camera): void {
  const saved: SavedView = { period: periodWord(state.period), camera, sliders: state.sliders, place: state.place };
  try { localStorage.setItem(SAVED_VIEW_KEY, JSON.stringify(saved)); } catch { /* Private browsing can disable storage. */ }
}
