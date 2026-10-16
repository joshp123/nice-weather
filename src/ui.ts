// Written by AI on 2026.10.16 by model claude opus 5.5.
// The controls and the place card. Everything is plain DOM built from app state.
import type { ClimateIndex, ForecastIndex, MonthClimate, PlaceWeather, SearchResult } from './api';
import { bands, bandForScore, NICE_DAY_MINIMUM_SCORE } from './palette';
import { factorsForDay, summaryForDay } from './explain';
import type { AppState, Lens, Profile, View } from './state';

type Child = Node | string | null | false | undefined;

export function element<K extends keyof HTMLElementTagNameMap>(tag: K, className?: string, ...children: Child[]): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className) node.className = className;
  for (const child of children) if (child) node.append(child);
  return node;
}

function segmented<T extends string>(options: readonly (readonly [T, string])[], selected: T, onSelect: (value: T) => void, label: string): HTMLElement {
  const group = element('div', 'segmented');
  group.setAttribute('role', 'radiogroup');
  group.setAttribute('aria-label', label);
  for (const [value, text] of options) {
    const button = element('button', value === selected ? 'selected' : '', text);
    button.setAttribute('role', 'radio');
    button.setAttribute('aria-checked', String(value === selected));
    button.onclick = () => onSelect(value);
    group.append(button);
  }
  return group;
}

const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const longMonths = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];

export function dayLabel(isoDate: string, today = new Date()): string {
  const date = new Date(`${isoDate}T12:00:00`);
  const difference = Math.floor((date.getTime() - new Date(today.toDateString()).getTime()) / 86_400_000);
  if (difference === 0) return 'Today';
  if (difference === 1) return 'Tomorrow';
  return date.toLocaleDateString('en-GB', { weekday: 'short', day: 'numeric' });
}

function longDayLabel(isoDate: string): string {
  return new Date(`${isoDate}T12:00:00`).toLocaleDateString('en-GB', { weekday: 'long', day: 'numeric', month: 'long' });
}

export type Actions = Readonly<{
  setView: (view: View) => void;
  setDay: (day: string) => void;
  setMonth: (month: number) => void;
  setProfile: (profile: Profile) => void;
  setLens: (lens: Lens) => void;
  closePlace: () => void;
  choosePlace: (latitude: number, longitude: number) => void;
  showHelp: () => void;
}>;

/** The bottom control dock: time, preference, lens and legend. */
export function renderDock(state: AppState, forecast: ForecastIndex | null, actions: Actions, place: PlaceWeather | null): HTMLElement {
  const viewControl = segmented<View>([['forecast', 'Next 15 days'], ['year', 'Year']], state.view, actions.setView, 'Time');
  const profileControl = segmented<Profile>([['nice', 'Nice'], ['cycling', 'Cycling']], state.profile, actions.setProfile, 'Preference');

  const chips = element('div', 'chips');
  if (state.view === 'forecast' && forecast) {
    const niceDays = place?.days?.filter((day) => day.scores[state.profile] >= NICE_DAY_MINIMUM_SCORE).length;
    chips.append(chip('Overview', niceDays === undefined ? null : `${niceDays} nice`, state.day === 'fortnight', () => actions.setDay('fortnight'), null));
    for (const date of forecast.dates) {
      const day = place?.days?.find((candidate) => candidate.date === date);
      const score = day?.scores[state.profile];
      chips.append(chip(dayLabel(date), score === undefined ? null : bandForScore(score).name, state.day === date, () => actions.setDay(date), score ?? null));
    }
  } else {
    for (let month = 1; month <= 12; month++) {
      const climate = place?.months?.[month - 1];
      const chance = climate?.niceChance[state.profile];
      chips.append(chip(months[month - 1], null, state.month === month, () => actions.setMonth(month), chance ?? null));
    }
  }

  const fortnight = state.view === 'forecast' && state.day === 'fortnight';
  const lensControl = fortnight
    ? null
    : segmented<Lens>([['overall', 'Overall'], ['comfort', 'Comfort'], ['dry', 'Dry'], ['clear', 'Clear sky'], ['calm', 'Calm']], state.lens, actions.setLens, 'Show');

  const dayCount = forecast?.dates.length ?? 14;
  const caption = fortnight ? `Nice days out of ${dayCount}` : state.view === 'year' ? (state.lens === 'overall' ? 'Share of days that are nice' : 'Share of days that are good') : 'How nice the day is';
  const scale = element('div', 'legend-scale');
  for (const band of bands) {
    const colour = element('span', 'bar-colour');
    colour.style.background = band.colour;
    const text = fortnight ? daysRange(band.minimumScore, dayCount) : state.view === 'year' ? percentRange(band.minimumScore) : band.name;
    scale.append(element('span', 'legend-step', colour, text));
  }
  const help = element('button', 'help-button', 'How scores work');
  help.onclick = actions.showHelp;
  const legend = element('div', 'legend', element('span', 'legend-caption', caption), scale, help);

  return element('nav', 'dock panel',
    element('div', 'dock-row', viewControl, chips),
    element('div', 'dock-row secondary', element('div', 'dock-controls', profileControl, lensControl), legend));
}

/** How a score is made, in plain words. */
export function renderHelp(actions: Actions): HTMLElement {
  const close = element('button', 'icon-button close', '×');
  close.setAttribute('aria-label', 'Close');
  close.onclick = actions.closePlace;
  const item = (name: string, text: string) => element('li', '', element('strong', '', name), ` ${text}`);
  return element('section', 'card panel help',
    element('header', 'card-header', element('h2', '', 'How scores work'), close),
    element('p', '', 'We look at the weather from 10:00 to 20:00 local time. Four things count:'),
    element('ul', 'help-list',
      item('Comfort:', 'temperature and humidity. 22–27 °C is best. Below 16 °C, very hot or very humid air scores low.'),
      item('Dry:', 'how much rain falls, and for how long.'),
      item('Clear sky:', 'how much of the sky is cloud.'),
      item('Calm:', 'how strong the wind is.')),
    element('p', '', 'The overall score mixes the four. Warmth counts most. One very bad result limits the overall score, so a warm, wet day is not nice.'),
    element('p', '', 'Cycling counts rain and wind more, so it is stricter.'),
    element('p', '', 'Each day gets a score from 0 to 100: Awful, Poor, Fair, Nice, Beautiful or Perfect. The overview and the year view show how many days are Nice or better.'),
    element('p', 'muted', 'Forecast: ECMWF, about 28 km. Typical year: real days from 2006 to 2025 (ERA5).'));
}

function daysRange(minimumScore: number, dayCount: number): string {
  const index = bands.findIndex((band) => band.minimumScore === minimumScore);
  const from = Math.ceil((minimumScore / 100) * dayCount);
  const to = index === bands.length - 1 ? dayCount : Math.ceil((bands[index + 1].minimumScore / 100) * dayCount) - 1;
  return from === to ? `${from}` : `${from}–${to}`;
}

function percentRange(minimumScore: number): string {
  const index = bands.findIndex((band) => band.minimumScore === minimumScore);
  const to = index === bands.length - 1 ? 100 : bands[index + 1].minimumScore - 1;
  return `${minimumScore}–${to}%`;
}

/** A bar filled to a 0-100 score in its band's colour. */
function meter(score: number): HTMLElement {
  const fill = element('span', 'meter-fill');
  const colour = bandForScore(score).colour;
  fill.style.width = `${Math.max(3, score)}%`;
  fill.style.background = colour;
  const bar = element('span', 'meter', fill);
  bar.setAttribute('aria-hidden', 'true');
  return bar;
}

function swatch(colour: string): HTMLElement {
  const node = element('span', 'swatch');
  node.style.background = colour;
  return node;
}

function chip(label: string, detail: string | null, selected: boolean, onSelect: () => void, score: number | null): HTMLElement {
  const button = element('button', selected ? 'chip selected' : 'chip', element('span', 'chip-label', label));
  if (detail) button.append(element('span', 'chip-detail', score === null ? null : swatch(bandForScore(score).colour), detail));
  button.onclick = onSelect;
  return button;
}

/** The place card: one answer first, then the reasons. */
export function renderCard(state: AppState, place: PlaceWeather, climate: ClimateIndex | null, actions: Actions): HTMLElement {
  const close = element('button', 'icon-button close', '×');
  close.setAttribute('aria-label', 'Close');
  close.onclick = actions.closePlace;
  const header = element('header', 'card-header', element('div', '', element('h2', '', place.name), element('p', 'muted', place.region)), close);
  const body = state.view === 'year' ? yearBody(state, place, climate, actions) : forecastBody(state, place);
  return element('section', 'card panel', header, body);
}

function answer(score: number, heading: string, sentence: string): HTMLElement {
  const band = bandForScore(score);
  return element('div', 'answer',
    element('div', 'answer-line', swatch(band.colour), element('strong', '', band.name), element('span', 'score', String(Math.round(score)))),
    element('p', 'answer-heading muted', heading),
    element('p', 'answer-sentence', sentence));
}

function forecastBody(state: AppState, place: PlaceWeather): HTMLElement {
  const days = place.days;
  if (!days || days.length === 0) return element('p', 'muted', 'No forecast for this spot. Choose a place on land.');
  if (state.day === 'fortnight') {
    const isNice = (day: (typeof days)[number]) => day.scores[state.profile] >= NICE_DAY_MINIMUM_SCORE;
    const nice = days.filter(isNice).length;
    let spell: typeof days = [];
    let current: typeof days = [];
    for (const day of days) {
      current = isNice(day) ? [...current, day] : [];
      if (current.length > spell.length) spell = current;
    }
    const share = Math.round((nice / days.length) * 100);
    const spellText = spell.length > 1
      ? `Longest nice spell: ${spell.length} days, ${dayLabel(spell[0].date)} to ${dayLabel(spell.at(-1)!.date)}.`
      : 'No two nice days in a row.';
    return element('div', 'answer',
      element('div', 'answer-line', swatch(bandForScore(share).colour), element('strong', '', `${nice} of ${days.length} days`), element('span', 'muted', 'Nice or better')),
      element('p', 'answer-sentence', spellText));
  }
  const day = days.find((candidate) => candidate.date === state.day) ?? days[0];
  const factors = element('dl', 'factors');
  for (const factor of factorsForDay(day, state.profile)) {
    factors.append(element('div', 'factor',
      element('dt', '', factor.label),
      element('dd', '', element('span', 'factor-value', factor.value), element('span', 'muted', factor.detail)),
      meter(factor.score)));
  }
  return element('div', '', answer(day.scores[state.profile], longDayLabel(day.date), summaryForDay(day, state.profile)), factors);
}

function yearBody(state: AppState, place: PlaceWeather, climate: ClimateIndex | null, actions: Actions): HTMLElement {
  const months12 = place.months;
  if (!months12) return element('p', 'muted', 'No climate record for this spot. Choose a place on land.');
  const month: MonthClimate = months12[state.month - 1];
  const chance = month.niceChance[state.profile];
  const chart = element('div', 'month-chart');
  for (const entry of months12) {
    const bar = element('span', 'bar');
    bar.style.height = `${Math.max(3, entry.niceChance[state.profile])}%`;
    const colour = bandForScore(entry.niceChance[state.profile]).colour;
    bar.style.background = colour;
    const column = element('button', entry.month === state.month ? 'month selected' : 'month', element('span', 'bar-track', bar), element('span', 'month-label', months[entry.month - 1][0]));
    column.title = `${longMonths[entry.month - 1]}: ${entry.niceChance[state.profile]}% of days nice`;
    column.onclick = () => actions.setMonth(entry.month);
    chart.append(column);
  }
  // The three nicest months that are nice at least half the time, in calendar order.
  const bestMonths = [...months12]
    .filter((entry) => entry.niceChance[state.profile] >= 50)
    .sort((left, right) => right.niceChance[state.profile] - left.niceChance[state.profile])
    .slice(0, 3)
    .sort((left, right) => left.month - right.month)
    .map((entry) => longMonths[entry.month - 1]);
  const bestMonthsSentence = bestMonths.length ? `Best months: ${bestMonths.join(', ')}.` : 'No month is usually nice here.';
  const round = Math.round;
  const typical = element('dl', 'factors',
    fact('Temperature', `${round(month.temperatureC)} °C`, `Cool day ${round(month.coolDayC)} °C, warm day ${round(month.warmDayC)} °C`),
    fact('Humidity', `dew point ${round(month.dewPointC)} °C`, month.muggyDaysPercent >= 5 ? `Muggy on ${round(month.muggyDaysPercent)}% of days` : 'Rarely muggy'),
    fact('Rain', `${round(month.rainFreeDaysPercent)}% dry days`, `Rain on ${100 - round(month.rainFreeDaysPercent)}% of days`),
    fact('Sky', `${round(month.cloudPercent)}% cloud`, `Clear on ${round(month.clearDaysPercent)}% of days`),
    fact('Wind', `${round(month.windKph)} km/h`, 'Typical daytime wind'));
  const awful = month.awfulChance[state.profile];
  return element('div', '',
    element('div', 'answer',
      element('div', 'answer-line', swatch(bandForScore(chance).colour), element('strong', '', `${chance}% nice`)),
      element('p', 'answer-heading muted', `A typical ${longMonths[state.month - 1]}`),
      element('p', 'answer-sentence', `${chance}% of days are Nice or better${awful > 0 ? `, ${awful}% are Awful` : ''}. ${bestMonthsSentence}`)),
    typical,
    element('p', 'section-label muted', 'Nice days, month by month'),
    chart,
    climate ? element('p', 'footnote muted', `Real days ${climate.years}, 10:00–20:00 local time.`) : null);
}

function fact(label: string, value: string, detail: string): HTMLElement {
  return element('div', 'factor', element('dt', '', label), element('dd', '', element('span', 'factor-value', value), element('span', 'muted', detail)));
}

/** "Updated this morning" style freshness for the forecast. Old forecasts say so plainly. */
export function forecastStatus(forecast: ForecastIndex, now = new Date()): { text: string; stale: boolean } {
  const run = new Date(forecast.runTime);
  const hours = (now.getTime() - run.getTime()) / 3_600_000;
  const when = run.toLocaleString('en-GB', { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
  if (hours > 36) return { text: `Forecast is ${Math.round(hours / 24)} days old (${when})`, stale: true };
  return { text: `Updated ${when}`, stale: false };
}

export function renderSearch(onChoose: (result: SearchResult) => void, onLocate: () => void, search: (query: string) => Promise<SearchResult[]>): HTMLElement {
  const input = element('input', 'search-input');
  input.type = 'search';
  input.placeholder = 'Search a place';
  input.setAttribute('aria-label', 'Search a place');
  const results = element('ul', 'search-results');
  let latest = 0;
  let timer: number | undefined;
  input.oninput = () => {
    window.clearTimeout(timer);
    const query = input.value.trim();
    if (query.length < 2) { results.replaceChildren(); return; }
    timer = window.setTimeout(async () => {
      const request = ++latest;
      const found = await search(query);
      if (request !== latest) return;
      results.replaceChildren(...found.slice(0, 7).map((result) => {
        const item = element('li', '', element('button', '', element('span', '', result.name), element('span', 'muted', result.region)));
        item.onclick = () => { input.value = result.name; results.replaceChildren(); onChoose(result); };
        return item;
      }));
    }, 120);
  };
  input.onkeydown = (event) => {
    if (event.key === 'Enter') (results.querySelector('button') as HTMLButtonElement | null)?.click();
    if (event.key === 'Escape') results.replaceChildren();
  };
  const locate = element('button', 'icon-button');
  locate.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M21 3 3 10.5l7.2 2.3L12.5 20z"/></svg>';
  locate.setAttribute('aria-label', 'Use my location');
  locate.onclick = onLocate;
  return element('div', 'search panel', input, locate, results);
}
