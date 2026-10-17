// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// The place card: the answer first (score, band, nice days), then why, in real units, then the year at a glance.
import type { Period, PointVerdict } from '../engine';
import type { Actions } from './actions';
import type { PlaceDetails } from './api';
import { button, closeIcon, el } from './dom';
import { MONTH_NAMES, MONTH_SHORT, dayChipLabel, isForecast, niceDaysSentence, periodTitle, upcomingForecastDays, type ForecastIndex } from './periods';
import type { Place } from './state';
import { bandColour } from './timebar';

export type CardView = Readonly<{
  place: Place;
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

function row(label: string, value: string, detail: string, effectText: string | null = null): HTMLElement {
  const penalty = effectText ? el('dd', 'why-effect', effectText) : null;
  if (penalty) penalty.title = 'Reduction in the score from this factor';
  return el('div', 'why-row', el('dt', '', label),
    el('dd', '', el('span', 'why-value', value), el('span', 'why-detail', detail)), penalty);
}

function why(verdict: PointVerdict): HTMLElement {
  const typical = verdict.typical;
  if (!typical) return el('p', 'muted', 'Weather figures are unavailable.');
  const parts = verdict.parts;
  return el('dl', 'why',
    row('Afternoon in sun', `${round(typical.feelsInSunAt14C)} °C feels-like`, afternoonWords(typical.feelsInSunAt14C)),
    row('Walk home at 21:00', `${round(typical.feelsInShadeAt21C)} °C feels-like`, eveningWords(typical.feelsInShadeAt21C)),
    row('Rain, 10:00–21:00', `${typical.rainMmPerDay.toFixed(1)} mm/day`, `${typical.rainMmPerDay < 0.5 ? 'Mostly dry' : typical.rainMmPerDay < 2 ? 'Some rain' : 'Wet'} · ${round(typical.wetDayShare * 100)}% wet days`, effect(parts.rainFactor)),
    row('Wind', `${round(typical.windKmh)} km/h`, typical.windKmh <= 15 ? 'Light wind' : typical.windKmh <= 25 ? 'Breezy' : typical.windKmh <= 40 ? 'Windy' : 'Strong wind', effect(parts.windFactor)),
    row('Humidity', `${typical.dewPointC.toFixed(1)} °C dew point`, humidityWord(typical.dewPointC), effect(parts.humidityFactor)),
    row('Sun', `${round(typical.sunnyShare * 100)}% of daylight`, typical.sunnyShare >= 0.8 ? 'Mostly sunny' : typical.sunnyShare >= 0.4 ? 'Sun and cloud' : 'Mostly cloudy', effect(parts.sunFactor)),
  );
}

function monthStrip(view: CardView, actions: Actions): HTMLElement | null {
  const months = view.details?.months;
  if (!months || months.length !== 12) return null;
  const strip = el('div', 'strip month-strip');
  let anyScore = false;
  for (const facts of months) {
    const month = facts.month as 1;
    const score = view.monthScores?.[month - 1] ?? (view.tasteIsDefault ? facts.score ?? null : null);
    if (score !== null) anyScore = true;
    const bar = el('span', 'strip-bar');
    if (score !== null) bar.style.background = bandColour(score);
    const selected = view.period.kind === 'month' && view.period.month === month;
    const cell = button(`strip-cell${selected ? ' selected' : ''}`, '', () => actions.setPeriod({ kind: 'month', month }));
    cell.append(bar, el('span', 'strip-label', MONTH_SHORT[month - 1]), el('span', 'strip-value', `${round(facts.highC)}°`));
    cell.title = `${MONTH_NAMES[month - 1]}: ${score !== null ? `${round(score)} out of 100, ` : ''}typical high ${round(facts.highC)} °C`;
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
    cell.append(bar, el('span', 'strip-label', label), el('span', 'strip-value', score === null ? '' : String(round(score))));
    cell.title = `${label}: ${score === null ? 'no score' : `${round(score)} out of 100`}`;
    strip.append(cell);
  }
  return el('div', 'strip-block', el('p', 'section-label', 'Day by day: score out of 100'), strip);
}

export function renderCard(view: CardView, actions: Actions): HTMLElement {
  const close = button('icon-button', '', actions.closePlace, 'Close');
  close.innerHTML = closeIcon;
  const name = view.details?.name ?? view.place.name;
  const region = view.details?.region ?? view.place.region;
  const header = el('header', 'card-header', el('div', '', el('h2', '', name || 'Finding the name…'), region ? el('p', 'muted', region) : null), close);

  const verdict = view.verdict;
  if (!verdict) {
    return el('section', 'card panel', header, el('p', 'muted', view.problem ?? (view.loading ? 'Loading weather for this period…' : 'No weather data for this spot.')));
  }
  const score = el('span', 'score-badge', String(round(verdict.meanScore)));
  score.style.background = bandColour(verdict.meanScore);
  const niceDays = niceDaysSentence(view.period, view.forecast, verdict.niceDayShare, verdict.niceDayCount);
  const answer = el('div', 'verdict', score,
    el('div', 'verdict-text', el('strong', 'band-word', verdict.band), el('p', 'verdict-days', niceDays || periodTitle(view.period, view.forecast))));
  const periodLine = el('p', 'card-period', isForecast(view.period) ? periodTitle(view.period, view.forecast) : `${periodTitle(view.period, view.forecast)}, in a typical year`);

  const strip = isForecast(view.period) ? dayStrip(view, actions) : null;
  return el('section', 'card panel', header, answer, periodLine, why(verdict), strip, monthStrip(view, actions));
}
