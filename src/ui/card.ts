// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// The place card: the answer first (score, band, nice days), then why, in real units, then the year at a glance.
import type { Period, PointVerdict } from '../engine';
import type { Actions } from './actions';
import type { MonthFacts, PlaceDetails } from './api';
import { button, closeIcon, el } from './dom';
import { MONTH_NAMES, MONTH_SHORT, dayChipLabel, isForecast, monthsOfPeriod, niceDaysSentence, periodTitle, upcomingForecastDays, type ForecastIndex } from './periods';
import type { Place } from './state';
import { bandColour } from './timebar';

export type CardView = Readonly<{
  place: Place;
  details: PlaceDetails | null;
  verdict: PointVerdict | null;
  period: Period;
  forecast: ForecastIndex | null;
  tasteIsDefault: boolean;
  /** The place's score for each forecast day, when the engine can judge other periods. */
  dayScores: readonly (number | null)[] | null;
  /** The place's score for each month at the visitor's taste, when the engine has the months loaded. */
  monthScores: readonly (number | null)[] | null;
}>;

const round = Math.round;

function average(facts: readonly MonthFacts[], key: keyof MonthFacts): number {
  return facts.reduce((sum, month) => sum + (month[key] as number), 0) / facts.length;
}

export function humidityWord(dewPointC: number): string {
  if (dewPointC < 13) return 'dry';
  if (dewPointC < 16) return 'comfortable';
  if (dewPointC < 18) return 'humid';
  if (dewPointC < 21) return 'muggy';
  if (dewPointC < 24) return 'oppressive';
  return 'miserable';
}

function warmthWords(grade: number, highC: number | null): string {
  if (highC !== null && highC >= 31 && grade < 85) return 'Too hot in the afternoon';
  if (grade >= 85) return 'T-shirt weather all day';
  if (grade >= 62) return 'Warm enough most of the day';
  if (grade >= 40) return 'Cool for a t-shirt part of the day';
  if (grade >= 22) return 'Too cool most of the day';
  return 'Too cold';
}

function eveningWords(grade: number): string {
  if (grade >= 90) return 'Fine in a t-shirt';
  if (grade >= 65) return 'Cool, a t-shirt will do';
  if (grade >= 30) return 'Chilly, bring a layer';
  return 'Cold';
}

/** "−26%" for a factor of 0.74; "no effect" when it costs nothing. */
function effect(factor: number): string {
  return factor >= 0.985 ? 'no effect' : `−${round((1 - factor) * 100)}%`;
}

function row(label: string, value: string, detail: string | null, effectText: string | null): HTMLElement {
  return el('div', 'why-row',
    el('dt', '', label),
    el('dd', '', el('span', 'why-value', value), detail ? el('span', 'why-detail', detail) : null),
    el('dd', `why-effect${effectText === 'no effect' ? ' quiet' : ''}`, effectText ?? ''));
}

function why(verdict: PointVerdict, facts: readonly MonthFacts[] | null): HTMLElement {
  const typical = facts && facts.length ? {
    highC: average(facts, 'highC'), feelsAt21C: average(facts, 'feelsAt21C'), dewPointC: average(facts, 'dewPointC'),
    wetDaysPercent: average(facts, 'wetDaysPercent'), sunnyPercent: average(facts, 'sunnyPercent'), windKmh: average(facts, 'windKmh'),
  } : null;
  const parts = verdict.parts;
  return el('dl', 'why',
    row('T-shirt weather', `${round(parts.warmth)} / 100`, typical ? `${warmthWords(parts.warmth, typical.highC)} · high ${round(typical.highC)} °C` : warmthWords(parts.warmth, null), null),
    row('Walk home at 21:00', `${round(parts.evening)} / 100`, typical ? `${eveningWords(parts.evening)} · feels ${round(typical.feelsAt21C)} °C` : eveningWords(parts.evening), null),
    row('Rain', effect(parts.rainFactor), typical ? `Wet on ${round(typical.wetDaysPercent)}% of days` : null, null),
    row('Wind', effect(parts.windFactor), typical ? `${round(typical.windKmh)} km/h on average` : null, null),
    row('Humidity', effect(parts.humidityFactor), typical ? `Dew point ${round(typical.dewPointC)} °C, ${humidityWord(typical.dewPointC)}` : null, null),
    row('Sun', effect(parts.sunFactor), typical ? `Sunny ${round(typical.sunnyPercent)}% of the day` : null, null),
  );
}

function monthStrip(view: CardView, actions: Actions): HTMLElement | null {
  const months = view.details?.months;
  if (!months || months.length !== 12) return null;
  const strip = el('div', 'strip');
  let anyScore = false;
  for (const facts of months) {
    const month = facts.month as 1;
    const score = view.monthScores?.[month - 1] ?? facts.score ?? null;
    if (score !== null) anyScore = true;
    const bar = el('span', 'strip-bar');
    if (score !== null) bar.style.background = bandColour(score);
    const selected = view.period.kind === 'month' && view.period.month === month;
    const cell = button(`strip-cell${selected ? ' selected' : ''}`, '', () => actions.setPeriod({ kind: 'month', month }));
    cell.append(bar, el('span', 'strip-label', MONTH_SHORT[month - 1][0]), el('span', 'strip-value', `${round(facts.highC)}°`));
    cell.title = `${MONTH_NAMES[month - 1]}: ${score !== null ? `${round(score)} out of 100, ` : ''}typical high ${round(facts.highC)} °C`;
    strip.append(cell);
  }
  const usesDefault = anyScore && !view.monthScores && !view.tasteIsDefault;
  const caption = `Month by month: typical high${anyScore ? ', coloured by score' : ''}${usesDefault ? ' at the default taste' : ''}`;
  return el('div', 'strip-block', el('p', 'section-label', caption), strip);
}

function dayStrip(view: CardView, actions: Actions): HTMLElement | null {
  if (!view.forecast || !view.dayScores) return null;
  const strip = el('div', 'strip');
  for (const day of upcomingForecastDays(view.forecast)) {
    const score = view.dayScores[day] ?? null;
    const bar = el('span', 'strip-bar');
    if (score !== null) bar.style.background = bandColour(score);
    const selected = view.period.kind === 'forecast-day' && view.period.day === day;
    const cell = button(`strip-cell${selected ? ' selected' : ''}`, '', () => actions.setPeriod({ kind: 'forecast-day', day }));
    const label = dayChipLabel(view.forecast, day);
    cell.append(bar, el('span', 'strip-label', label === 'Today' ? 'T' : label === 'Tomorrow' ? 'Tm' : label.slice(0, 2)), el('span', 'strip-value', score === null ? '' : String(round(score))));
    cell.title = `${label}: ${score === null ? 'no score' : `${round(score)} out of 100`}`;
    strip.append(cell);
  }
  return el('div', 'strip-block', el('p', 'section-label', 'Day by day'), strip);
}

export function renderCard(view: CardView, actions: Actions): HTMLElement {
  const close = button('icon-button', '', actions.closePlace, 'Close');
  close.innerHTML = closeIcon;
  const name = view.details?.name ?? view.place.name;
  const region = view.details?.region ?? view.place.region;
  const header = el('header', 'card-header', el('div', '', el('h2', '', name || 'Finding the name…'), region ? el('p', 'muted', region) : null), close);

  const verdict = view.verdict;
  if (!verdict) {
    return el('section', 'card panel', header, el('p', 'muted', 'No weather data for this spot.'));
  }
  const score = el('span', 'score-badge', String(round(verdict.meanScore)));
  score.style.background = bandColour(verdict.meanScore);
  const niceDays = niceDaysSentence(view.period, view.forecast, verdict.niceDayShare, verdict.niceDayCount);
  const answer = el('div', 'verdict', score,
    el('div', 'verdict-text', el('strong', 'band-word', verdict.band), el('p', 'verdict-days', niceDays || periodTitle(view.period, view.forecast))));
  const periodLine = el('p', 'card-period', isForecast(view.period) ? periodTitle(view.period, view.forecast) : `${periodTitle(view.period, view.forecast)}, in a typical year`);

  const facts = view.details?.months && !isForecast(view.period) ? monthsOfPeriod(view.period, view.forecast).map(month => view.details!.months![month - 1]) : null;
  const strip = isForecast(view.period) ? dayStrip(view, actions) : null;
  return el('section', 'card panel', header, answer, periodLine, why(verdict, facts), strip, monthStrip(view, actions));
}
