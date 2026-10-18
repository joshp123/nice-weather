// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// The one-line answer at the top of the map: where it is nicest in the chosen period, as on Ryan Hall's map.
import { type Period, type WeatherEngine } from '../engine';
import { button, el } from './dom';
import { periodPhrase, type ForecastIndex } from './periods';
import { type NamedPlace } from './places';
import { countryCode, countryFlag } from './countries';

import { CONTINENT_POPULATION, WORLD_POPULATION, inOutlookRegion, rankPlaces, rankingSeparationKM } from './ranking';

type ViewBounds = Readonly<{ getWest(): number; getEast(): number; getSouth(): number; getNorth(): number }>;
export type HeadlinePlace = NamedPlace & Readonly<{ score: number; niceDayCount: number; perfectDayCount: number; region?: string; slug?: string | null; population?: number }>;

export function nicestPlaces(engine: WeatherEngine, places: readonly (NamedPlace & { slug?: string; region?: string; population?: number })[], bounds?: ViewBounds, limit = 3, minimumPopulation?: number): HeadlinePlace[] {
  const region = bounds ? viewportRegion(bounds) : 'the world';
  const population = minimumPopulation ?? (region === 'the world' ? WORLD_POPULATION : CONTINENT_POPULATION);
  const countries = REGION_COUNTRIES[region];
  const visible = places.filter(place => {
    if ((place.population ?? 0) < population || !inOutlookRegion(place, region)) return false;
    if (countries && !countries.has(countryCode(place))) return false;
    if (!bounds) return true;
    if (place.latitude < bounds.getSouth() || place.latitude > bounds.getNorth()) return false;
    const span = bounds.getEast() - bounds.getWest();
    return span >= 360 || ((place.longitude - bounds.getWest()) % 360 + 360) % 360 <= span;
  });
  const scores = engine.scorePoints(visible, population > 0 ? 'all' : undefined);
  const counts = engine.countPoints(visible, population > 0 ? 'all' : undefined);
  const candidates = visible.map((place, index) => ({ ...place, score: scores[index], niceDayCount: counts[index * 3], perfectDayCount: counts[index * 3 + 1] }));
  const visibleWidthKM = bounds ? 6371 * Math.PI / 180 * (bounds.getEast() - bounds.getWest()) * Math.cos((bounds.getSouth() + bounds.getNorth()) / 2 * Math.PI / 180) : Infinity;
  return rankPlaces(candidates.filter(place => place.niceDayCount > 0), population, limit, place => {
    const verdict = engine.judgePoint(place.latitude, place.longitude);
    return verdict ? { ...place, score: verdict.meanScore, niceDayCount: verdict.niceDayCount, perfectDayCount: verdict.perfectDayCount } : null;
  }, rankingSeparationKM(visibleWidthKM));
}

const REGION_COUNTRIES: Record<string, ReadonlySet<string>> = Object.fromEntries(Object.entries({
  'Europe': 'AL AD AT BY BE BA BG HR CY CZ DK EE FO FI FR DE GI GR GG VA HU IS IE IM IT JE XK LV LI LT LU MT MD MC ME NL MK NO PL PT RO RU SM RS SK SI ES SE CH UA GB TR',
  'Africa': 'DZ AO BJ BW BF BI CV CM CF TD KM CG CD CI DJ EG GQ ER SZ ET GA GM GH GN GW KE LS LR LY MG MW ML MR MU MA YT MZ NA NE NG RE RW SH ST SN SC SL SO ZA SS SD TZ TG TN UG EH ZM ZW',
  'the Middle East': 'AE BH EG IR IQ IL JO KW LB OM PS QA SA SY TR YE',
  'North America': 'AG AI AW BS BB BZ BM BQ CA KY CR CU CW DM DO SV GL GD GP GT HT HN JM MQ MX MS NI PA PR BL KN LC MF PM VC SX TT TC US VG VI',
  'South America': 'AR BO BR CL CO EC FK GF GY PY PE SR UY VE',
  'Asia': 'AF AM AZ BH BD BT BN KH CN GE HK IN ID IR IQ IL JP JO KZ KW KG LA LB MO MY MV MN MM NP KP OM PK PS PH QA SA SG KR LK SY TW TJ TH TL TM AE UZ VN YE RU TR',
  'Oceania': 'AS AU CK FJ PF GU KI MH FM NR NC NZ NU NF MP PW PG PN WS SB TK TO TV VU WF',
}).map(([region, codes]) => [region, new Set(codes.split(' '))]));

/** Name a region only when all of it is in view. */
export function viewportRegion(bounds: ViewBounds): string {
  if (bounds.getWest() <= -32 && bounds.getEast() >= 40 && bounds.getSouth() <= 27 && bounds.getNorth() >= 72 && bounds.getWest() >= -45 && bounds.getEast() <= 55 && bounds.getSouth() >= 20 && bounds.getNorth() <= 83) return 'Europe';
  return 'this map';
}

function headlineLead(period: Period, forecast: ForecastIndex | null, region: string): string {
  return `Nicest ${region === 'this map' ? 'on this map' : `in ${region}`} ${periodPhrase(period, forecast)}: `;
}

export function headlineSentence(period: Period, forecast: ForecastIndex | null, nicest: readonly HeadlinePlace[], region = 'the world'): string {
  if (nicest.length === 0) return `No nice days ${region === 'this map' ? 'on this map' : `in ${region}`} ${periodPhrase(period, forecast)}.`;
  const names = nicest.map(place => `${countryFlag(place)} ${place.name}`.trim());
  const last = names.pop();
  return `${headlineLead(period, forecast, region)}${names.length ? `${names.join(', ')} and ` : ''}${last}.`;
}

export function placeSentence(period: Period, forecast: ForecastIndex | null, places: readonly HeadlinePlace[], select: (place: HeadlinePlace) => void, region = 'the world'): DocumentFragment {
  const fragment = document.createDocumentFragment();
  fragment.append(headlineLead(period, forecast, region));
  places.slice(0, 3).forEach((place, index, list) => {
    if (index) fragment.append(index === list.length - 1 ? ' and ' : ', ');
    fragment.append(button('inline-place', `${countryFlag(place)} ${place.name}`.trim(), () => select(place)));
  });
  fragment.append('.');
  return fragment;
}

export type Headline = Readonly<{ element: HTMLElement; set(sentence: string, note?: { text: string; warning: boolean } | null, content?: DocumentFragment): void }>;

export function createHeadline(): Headline {
  const sentence = el('p', 'headline-sentence');
  const note = el('p', 'headline-note');
  const element = el('header', 'headline', el('h1', 'wordmark', 'Nice!'), el('div', 'headline-copy', sentence, note));
  return {
    element,
    set(text, noteText = null, content) {
      sentence.replaceChildren(content ?? text);
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
