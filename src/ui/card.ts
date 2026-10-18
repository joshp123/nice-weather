// Written by AI on 2026.10.18 by model GPT-6.1 Sol, building on Claude Fable 5.1.
// The place card: the answer first (score, band, nice days), then why, in real units, then the year at a glance.
import { DEFAULT_TASTE, type Period, type PointVerdict, type Taste } from '../engine';
import { COLD_GRADE_BY_FEELS_IN_SUN, HEAT_GRADE_BY_FEELS_IN_SHADE, EVENING_GRADE_BY_FEELS_AT_21, RAIN_FACTOR_BY_MM, WIND_FACTOR_BY_KMH, HUMIDITY_FACTOR_BY_DEW_POINT, SUN_FACTOR_BY_SUNNY_SHARE, readCurve } from '../engine/taste';
import type { Actions } from './actions';
import type { PlaceDetails } from './api';
import { button, closeIcon, el } from './dom';
import { MONTH_NAMES, MONTH_SHORT, dayChipLabel, isForecast, bandSentence, monthsOfPeriod, periodTitle, upcomingForecastDays, type ForecastIndex } from './periods';
import type { Place } from './state';
import { bandColour } from './timebar';
import { countryFlag } from './countries';
import './card.css';

export type CardView = Readonly<{
  place: Place;
  taste?: Taste;
  stretchLabel?: string;
  homeComparison?: Readonly<{ place: Place; verdict: PointVerdict | null }>;
  regionalComparison?: Readonly<{ place: Place; verdict: PointVerdict | null }>;
  details: PlaceDetails | null;
  verdict: PointVerdict | null;
  period: Period;
  forecast: ForecastIndex | null;
  tasteIsDefault: boolean;
  loading: boolean;
  problem: string | null;
  /** The place's score for each forecast day, when the engine can judge other periods. */
  dayScores: readonly (number | null)[] | null;
  /** The place's score for each month at the visitor's taste, when the engine has the months loaded. */
  monthScores: readonly (number | null)[] | null;
}>;

const round = Math.round;

export function humidityWord(dewPointC: number): string {
  if (dewPointC <= 13) return 'dry';
  if (dewPointC <= 16) return 'comfortable';
  if (dewPointC <= 18) return 'humid';
  if (dewPointC <= 21) return 'muggy';
  if (dewPointC <= 24) return 'oppressive';
  return 'miserable';
}

function afternoonWords(temperatureC: number): string {
  if (temperatureC >= 31) return 'Hot';
  if (temperatureC >= 23) return 'Warm';
  if (temperatureC >= 19) return 'Mild';
  if (temperatureC >= 15) return 'Cool';
  if (temperatureC >= 9) return 'Chilly';
  return 'Cold';
}

function eveningWords(temperatureC: number): string {
  if (temperatureC >= 26) return 'Hot evening';
  if (temperatureC >= 19) return 'Warm evening';
  if (temperatureC >= 16) return 'Comfortable in a t-shirt';
  if (temperatureC >= 12) return 'Cool evening';
  if (temperatureC >= 8) return 'Chilly, bring a layer';
  return 'Cold';
}

function effect(factor: number): string | null {
  return factor >= 0.985 ? null : `−${round((1 - factor) * 100)}%`;
}

function factorScore(factor: number): number {
  return factor >= 0.97 ? 85 : factor >= 0.9 ? 70 : factor >= 0.8 ? 55 : factor >= 0.65 ? 40 : factor >= 0.45 ? 25 : 0;
}

type PhysicalScale = Readonly<{ value: number; minimum: number; maximum: number; ticks: readonly string[]; score: (value: number) => number }>;

function physicalScale(scale: PhysicalScale): HTMLElement {
  const track = el('div', 'reason-scale');
  const stops: string[] = [];
  for (let step = 0; step <= 40; step++) {
    const value = scale.minimum + (scale.maximum - scale.minimum) * step / 40;
    const colour = bandColour(scale.score(value));
    stops.push(`${colour} ${step * 2.5}%`, `${colour} ${Math.min(100, (step + 1) * 2.5)}%`);
  }
  track.style.background = `linear-gradient(90deg, ${stops.join(', ')})`;
  const marker = el('i', 'reason-marker');
  marker.style.left = `${Math.max(0, Math.min(100, (scale.value - scale.minimum) / (scale.maximum - scale.minimum) * 100))}%`;
  track.append(marker);
  track.setAttribute('aria-hidden', 'true');
  const ticks = el('div', 'reason-ticks', ...scale.ticks.map(tick => el('span', '', tick)));
  ticks.setAttribute('aria-hidden', 'true');
  return el('div', 'reason-range', track, ticks);
}

function row(label: string, value: string, detail: string, scale: PhysicalScale, effectText: string | null = null): HTMLElement {
  const penalty = effectText ? el('span', 'why-effect', effectText) : null;
  if (penalty) penalty.title = 'Reduction in the score from this factor';
  return el('div', 'why-row', el('dt', '', label),
    el('dd', '', el('span', 'why-value', value), el('span', 'why-detail', detail), penalty),
    physicalScale(scale));
}

function why(verdict: PointVerdict, taste: Taste): HTMLElement {
  const typical = verdict.typical;
  const parts = verdict.parts;
  const afternoonShade = typical.feelsInShadeAt15C;
  return el('dl', 'why physical-why',
    row('Afternoon in the sun', `${round(typical.feelsInSunAt14C)} °C feels-like`, afternoonWords(typical.feelsInSunAt14C),
      { value: typical.feelsInSunAt14C, minimum: 0, maximum: 40, ticks: ['0 °C', '20 °C', '40 °C'], score: value => readCurve(COLD_GRADE_BY_FEELS_IN_SUN, value - taste.warmShift) }),
    afternoonShade === undefined ? null : row('Afternoon in the shade', `${round(afternoonShade)} °C feels-like at 15:00`, afternoonWords(afternoonShade),
      { value: afternoonShade, minimum: 10, maximum: 45, ticks: ['10 °C', '28 °C', '45 °C'], score: value => readCurve(HEAT_GRADE_BY_FEELS_IN_SHADE, value + taste.heatShift) }),
    row('Walk home at 21:00', `${round(typical.feelsInShadeAt21C)} °C in the shade`, eveningWords(typical.feelsInShadeAt21C),
      { value: typical.feelsInShadeAt21C, minimum: 0, maximum: 40, ticks: ['0 °C', '20 °C', '40 °C'], score: value => Math.min(readCurve(EVENING_GRADE_BY_FEELS_AT_21, value - taste.eveningShift), readCurve(HEAT_GRADE_BY_FEELS_IN_SHADE, value + taste.heatShift)) }),
    row('Rain, 10:00 to 21:00', `${typical.rainMmPerDay.toFixed(1)} mm a day`, `${typical.rainMmPerDay < 0.5 ? 'Mostly dry' : typical.rainMmPerDay < 2 ? 'Some rain' : 'Wet'} · ${round(typical.wetDayShare * 100)}% of days wet`,
      { value: typical.rainMmPerDay, minimum: 0, maximum: 10, ticks: ['0 mm', '5 mm', '10 mm'], score: value => factorScore(readCurve(RAIN_FACTOR_BY_MM, value * taste.rainScale)) }, effect(parts.rainFactor)),
    row('Wind', `${round(typical.windKmh)} km/h`, typical.windKmh <= 15 ? 'Light wind' : typical.windKmh <= 25 ? 'Breezy' : typical.windKmh <= 40 ? 'Windy' : 'Strong wind',
      { value: typical.windKmh, minimum: 0, maximum: 70, ticks: ['Calm', '35 km/h', '70 km/h'], score: value => factorScore(readCurve(WIND_FACTOR_BY_KMH, value - taste.windShift)) }, effect(parts.windFactor)),
    row('Humidity', `${typical.dewPointC.toFixed(1)} °C dew point`, humidityWord(typical.dewPointC),
      { value: typical.dewPointC, minimum: 5, maximum: 30, ticks: ['5 °C · dry', '18 °C', '30 °C · sticky'], score: value => factorScore(readCurve(HUMIDITY_FACTOR_BY_DEW_POINT, value - taste.humidityShift)) }, effect(parts.humidityFactor)),
    row('Sun', `${round(typical.sunnyShare * 100)}% of daylight`, typical.sunnyShare >= 0.8 ? 'Mostly sunny' : typical.sunnyShare >= 0.4 ? 'Sun and cloud' : 'Mostly cloudy',
      { value: typical.sunnyShare * 100, minimum: 0, maximum: 100, ticks: ['0% · grey', '50%', '100% · clear'], score: value => factorScore(readCurve(SUN_FACTOR_BY_SUNNY_SHARE, value / 100)) }, effect(parts.sunFactor)),
  );
}

function comparisons(view: CardView, actions: Actions): HTMLElement | null {
  const rows = [
    [view.homeComparison, 'Home'], [view.regionalComparison, 'Compare'],
  ] as const;
  const comparison = el('div', 'card-comparisons');
  for (const [entry, label] of rows) {
    if (!entry?.verdict || entry.place.slug === view.place.slug && entry.place.name === view.place.name) continue;
    const place = button('inline-place', `${countryFlag(entry.place)} ${entry.place.name}`.trim(), () => actions.selectSearchResult(entry.place));
    comparison.append(el('p', '', `${label}: `, place, `, nice on ${round(entry.verdict.niceDayCount)} of ${round(entry.verdict.totalDays)} days ${view.stretchLabel ?? `in ${periodTitle(view.period, view.forecast).toLowerCase()}`}.`));
  }
  return comparison.childElementCount ? comparison : null;
}

function monthStrip(view: CardView, actions: Actions): HTMLElement | null {
  const months = !view.tasteIsDefault && view.details?.overviewMonths ? view.details.overviewMonths : view.details?.months;
  if (!months || months.length !== 12) return null;
  const strip = el('div', 'strip month-strip');
  let anyScore = false;
  for (const facts of months) {
    const month = facts.month as 1;
    const score = view.monthScores?.[month - 1] ?? (view.tasteIsDefault ? facts.score ?? null : null);
    if (score !== null) anyScore = true;
    const bar = el('span', 'strip-bar');
    if (score !== null) bar.style.background = bandColour(score);
    const selected = !isForecast(view.period) && monthsOfPeriod(view.period, view.forecast).includes(month);
    const cell = button(`strip-cell${selected ? ' selected' : ''}`, '', () => actions.setPeriod({ kind: 'month', month }));
    cell.append(bar, el('span', 'strip-label', MONTH_SHORT[month - 1]), el('span', 'strip-value', `${round(facts.highC)}°`));
    cell.title = `${MONTH_NAMES[month - 1]}: ${score !== null ? `${Math.floor(score)} out of 100, ` : ''}typical high ${round(facts.highC)} °C`;
    strip.append(cell);
  }
  const caption = `Month by month: typical high${anyScore ? ', coloured by score' : ''}`;
  return el('div', 'strip-block', el('p', 'section-label', caption), strip);
}

function dayStrip(view: CardView, actions: Actions): HTMLElement | null {
  if (!view.forecast || !view.dayScores) return null;
  const strip = el('div', 'strip day-strip');
  for (const day of upcomingForecastDays(view.forecast)) {
    const score = view.dayScores[day] ?? null;
    const bar = el('span', 'strip-bar');
    if (score !== null) bar.style.background = bandColour(score);
    const selected = view.period.kind === 'forecast-day' && view.period.day === day;
    const cell = button(`strip-cell${selected ? ' selected' : ''}`, '', () => actions.setPeriod({ kind: 'forecast-day', day }));
    const label = dayChipLabel(view.forecast, day);
    cell.append(bar, el('span', 'strip-label', label), el('span', 'strip-value', score === null ? '' : String(Math.floor(score))));
    cell.title = `${label}: ${score === null ? 'no score' : `${Math.floor(score)} out of 100`}`;
    strip.append(cell);
  }
  return el('div', 'strip-block', el('p', 'section-label', 'Day by day: score out of 100'), strip);
}

export function renderCard(view: CardView, actions: Actions): HTMLElement {
  const close = button('icon-button', '', actions.closePlace, 'Close');
  close.innerHTML = closeIcon;
  const name = view.details?.name ?? view.place.name;
  const region = view.details?.region ?? view.place.region;
  const header = el('header', 'card-header', el('div', '', el('h2', '', name ? `${countryFlag({ region, slug: view.place.slug })} ${name}`.trim() : 'Finding the name…'), region ? el('p', 'muted', region) : null), close);

  const verdict = view.verdict;
  if (!verdict) {
    return el('section', 'card panel', header, el('p', 'muted', view.problem ?? (view.loading ? 'Loading weather for this period…' : 'No weather data for this spot.')));
  }
  const score = el('span', 'score-badge', String(Math.floor(verdict.meanScore)));
  score.style.background = bandColour(verdict.meanScore);
  const niceDays = isForecast(view.period) && view.period.kind === 'forecast-day' ? '' : `Nice on ${round(verdict.niceDayCount)} of the ${round(verdict.totalDays)} days ${view.stretchLabel ?? (view.period.kind === 'year' ? 'of the year' : `in ${periodTitle(view.period, view.forecast).toLowerCase()}`)}. Perfect on ${round(verdict.perfectDayCount)}; awful on ${round(verdict.awfulDayCount)}.`;
  const answer = el('div', 'verdict', score,
    el('div', 'verdict-text', el('strong', 'band-word', bandSentence(view.period, verdict.band)), el('p', 'verdict-days', niceDays || periodTitle(view.period, view.forecast))));
  const periodLabel = isForecast(view.period) ? periodTitle(view.period, view.forecast) : `${periodTitle(view.period, view.forecast)}, in a typical year`;
  const periodLine = el('p', 'card-period', `${periodLabel}${verdict.resolutionArcSeconds === 30 ? '' : ' · Area estimate'}`);

  const strip = isForecast(view.period) ? dayStrip(view, actions) : null;
  return el('section', 'card panel physical-card', header, answer, periodLine, why(verdict, view.taste ?? DEFAULT_TASTE), strip, monthStrip(view, actions), comparisons(view, actions));
}
