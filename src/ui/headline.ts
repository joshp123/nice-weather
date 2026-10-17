// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// The one-line answer at the top of the map: where it is nicest in the chosen period, as on Ryan Hall's map.
import { bandOf, type Period, type WeatherEngine } from '../engine';
import { el } from './dom';
import { kilometresBetween } from './map';
import { periodPhrase, type ForecastIndex } from './periods';
import { HEADLINE_PLACES, type NamedPlace } from './places';

const MINIMUM_SPREAD_KM = 1500;

/** The three nicest well-known places, far enough apart to be three answers rather than one coast. */
export function nicestPlaces(engine: WeatherEngine): (NamedPlace & { score: number })[] {
  const scores = engine.scorePoints(HEADLINE_PLACES);
  const ranked = HEADLINE_PLACES.map((place, index) => ({ ...place, score: scores[index] }))
    .filter(place => Number.isFinite(place.score))
    .sort((a, b) => b.score - a.score);
  const chosen: (NamedPlace & { score: number })[] = [];
  for (const place of ranked) {
    if (chosen.some(other => kilometresBetween(place, other) < MINIMUM_SPREAD_KM)) continue;
    chosen.push(place);
    if (chosen.length === 3) break;
  }
  return chosen;
}

export function headlineSentence(period: Period, forecast: ForecastIndex | null, nicest: readonly (NamedPlace & { score: number })[]): string {
  if (nicest.length === 0) return '';
  const names = nicest.map(place => place.name);
  const list = names.length === 1 ? names[0] : `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`;
  const bands = new Set(nicest.map(place => bandOf(place.score)));
  const allSame = bands.size === 1 ? `, all ${[...bands][0]}` : '';
  const phrase = periodPhrase(period, forecast);
  return `Nicest ${phrase}: ${list}${allSame}.`;
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
