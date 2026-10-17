// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// The one-line answer at the top of the map: where it is nicest in the chosen period, as on Ryan Hall's map.
import { type Period, type WeatherEngine } from '../engine';
import { el } from './dom';
import { kilometresBetween } from './map';
import { daysInPeriod, periodPhrase, type ForecastIndex } from './periods';
import { type NamedPlace } from './places';

const MINIMUM_SPREAD_KM = 250;

type ViewBounds = Readonly<{ getWest(): number; getEast(): number; getSouth(): number; getNorth(): number }>;
type HeadlinePlace = NamedPlace & Readonly<{ score: number; niceDayCount: number; perfectDayCount: number }>;

/** All named places in the current view compete by day counts, with room between the answers. */
export function nicestPlaces(engine: WeatherEngine, places: readonly (NamedPlace & { slug?: string })[], bounds?: ViewBounds): HeadlinePlace[] {
  const visible = places.filter(place => {
    if (place.slug?.startsWith('palestine/')) return false;
    if (!bounds) return true;
    if (place.latitude < bounds.getSouth() || place.latitude > bounds.getNorth()) return false;
    const span = bounds.getEast() - bounds.getWest();
    return span >= 360 || ((place.longitude - bounds.getWest()) % 360 + 360) % 360 <= span;
  });
  const scores = engine.scorePoints(visible);
  const counts = engine.countPoints(visible);
  const ranked = visible.map((place, index) => ({ ...place, score: scores[index], niceDayCount: counts[index * 3], perfectDayCount: counts[index * 3 + 1] }))
    .filter(place => Number.isFinite(place.score) && Number.isFinite(place.niceDayCount))
    .sort((a, b) => b.niceDayCount - a.niceDayCount || b.perfectDayCount - a.perfectDayCount || b.score - a.score);
  const chosen: HeadlinePlace[] = [];
  for (const place of ranked) {
    if (chosen.some(other => kilometresBetween(place, other) < MINIMUM_SPREAD_KM)) continue;
    chosen.push(place);
    if (chosen.length === 3) break;
  }
  return chosen;
}

export function headlineSentence(period: Period, forecast: ForecastIndex | null, nicest: readonly HeadlinePlace[]): string {
  if (nicest.length === 0) return '';
  const phrase = periodPhrase(period, forecast);
  if (period.kind === 'forecast-day') {
    return `Nicest ${phrase}: ${nicest.map(place => `${place.name} (${Math.floor(place.score)})`).join(', ')}.`;
  }
  const days = daysInPeriod(period, forecast);
  const list = nicest.map((place, index) => `${place.name} (${Math.round(place.niceDayCount)}${index === 0 ? ` of ${days}` : ''})`).join(', ');
  return `Most nice days ${phrase}: ${list}.`;
}

export type Headline = Readonly<{ element: HTMLElement; set(sentence: string, note?: { text: string; warning: boolean } | null): void }>;

export function createHeadline(): Headline {
  const sentence = el('p', 'headline-sentence');
  const note = el('p', 'headline-note');
  const element = el('header', 'headline panel', el('h1', 'wordmark', 'Nice!'), sentence, note);
  return {
    element,
    set(text, noteText = null) {
      sentence.textContent = text;
      sentence.hidden = !text;
      note.textContent = noteText?.text ?? '';
      note.hidden = !noteText;
      note.classList.toggle('warning', Boolean(noteText?.warning));
    },
  };
}

/** How fresh the forecast is. Old forecasts say so plainly (Josh: "if forecast is stale, must be obvious"). */
export function forecastFreshness(forecast: ForecastIndex, now = new Date()): { text: string; warning: boolean } {
  const match = /^(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})Z$/.exec(forecast.run);
  if (!match) return { text: `Forecast from ${forecast.firstDate}`, warning: false };
  const run = new Date(Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3]), Number(match[4]), Number(match[5])));
  const hours = (now.getTime() - run.getTime()) / 3_600_000;
  const when = run.toLocaleString('en-GB', { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
  if (hours > 48) return { text: `Forecast is ${Math.round(hours / 24)} days old, from ${when}`, warning: true };
  return { text: `Forecast from ${when}`, warning: false };
}
