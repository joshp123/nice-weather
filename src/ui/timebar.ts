// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// The bar along the bottom: when (the one control used most), what the colours mean, taste and sharing,
// and the small print.
import { BANDS, bandOf, type Month, type Period, type Season } from '../engine';
import { bands } from '../palette';
import type { Actions } from './actions';
import { button, el, swatch } from './dom';
import { MONTH_NAMES, MONTH_SHORT, daysInPeriod, dayChipLabel, forecastDate, isForecast, monthsOfPeriod, periodTitle, periodWord, upcomingForecastDays, type ForecastIndex } from './periods';
import './timepicker.css';


export type TimeBarView = Readonly<{
  period: Period;
  forecast: ForecastIndex | null;
  tasteOpen: boolean;
  tasteIsDefault: boolean;
  shareOpen: boolean;
  theme: 'dark' | 'light';
  home?: Readonly<{ name: string; latitude: number }>;
  monthScores?: readonly number[];
  dayScores?: readonly number[];
  niceDayCount?: number;
  countedDays?: number;
  countBasis?: 'sample' | 'forecast';
  scorePlaceName?: string;
  stretchLabel?: string;
  pickerOpen?: boolean;
  togglePicker?: () => void;
  playing?: boolean;
  playYear?: () => void;
}>;

export function bandColour(score: number): string {
  for (let index = bands.length - 1; index >= 0; index--) if (score >= bands[index].minimumScore) return opaque(bands[index].colour);
  return opaque(bands[0].colour);
}

/** The palette carries an alpha for the map; the interface uses the solid colour. */
export const opaque = (colour: string) => colour.slice(0, 7);

/** The band as it looks on the map: its translucent colour over the land, so the legend matches the colours seen. */
export function colourOnLand(colour: string): string {
  const channel = (index: number) => parseInt(colour.slice(1 + index * 2, 3 + index * 2), 16);
  const alpha = colour.length === 9 ? channel(3) / 255 : 1;
  const land = getComputedStyle(document.documentElement).getPropertyValue('--map-land').trim();
  const blended = [0, 1, 2].map(index => Math.round(channel(index) * alpha + parseInt(land.slice(1 + index * 2, 3 + index * 2), 16) * (1 - alpha)));
  return `rgb(${blended.join(', ')})`;
}

function segmented<T extends string>(options: readonly (readonly [T, string])[], selected: T, onSelect: (value: T) => void, label: string): HTMLElement {
  const group = el('div', 'segmented');
  group.setAttribute('role', 'radiogroup');
  group.setAttribute('aria-label', label);
  for (const [value, text] of options) {
    const node = button(value === selected ? 'selected' : '', text, () => onSelect(value));
    node.setAttribute('role', 'radio');
    node.setAttribute('aria-checked', String(value === selected));
    group.append(node);
  }
  return group;
}

const SVG_NAMESPACE = 'http://www.w3.org/2000/svg';

function svgElement<Tag extends keyof SVGElementTagNameMap>(tag: Tag, attributes: Record<string, string | number>): SVGElementTagNameMap[Tag] {
  const node = document.createElementNS(SVG_NAMESPACE, tag);
  for (const [name, value] of Object.entries(attributes)) node.setAttribute(name, String(value));
  return node;
}

function dialPoint(angle: number, radius: number): [number, number] {
  const radians = (angle - 90) * Math.PI / 180;
  return [120 + radius * Math.cos(radians), 120 + radius * Math.sin(radians)];
}

function dialArc(startAngle: number, endAngle: number, radius: number): string {
  const [startX, startY] = dialPoint(startAngle, radius);
  const [endX, endY] = dialPoint(endAngle, radius);
  return `M${startX} ${startY} A${radius} ${radius} 0 ${endAngle - startAngle > 180 ? 1 : 0} 1 ${endX} ${endY}`;
}

function dialText(className: string, text: string, x: number, y: number): SVGTextElement {
  const node = svgElement('text', { class: className, x, y, 'text-anchor': 'middle', 'dominant-baseline': 'central' });
  node.textContent = text;
  return node;
}

function yearDial(view: TimeBarView, actions: Actions): SVGSVGElement {
  const selectedMonths = monthsOfPeriod(view.period, view.forecast);
  const dial = svgElement('svg', { class: 'year-dial', viewBox: '0 0 240 240', role: 'group', 'aria-label': 'Choose months. Choose a first month, then a last month for a stretch.' });
  MONTH_SHORT.forEach((label, index) => {
    const month = (index + 1) as Month;
    const selected = selectedMonths.includes(month);
    const value = view.monthScores?.[index];
    const score = value !== undefined && Number.isFinite(value) ? value : undefined;
    const chooseMonth = () => {
      actions.setPeriod(view.period.kind === 'month' && view.period.month !== month
        ? { kind: 'months', startMonth: view.period.month, endMonth: month }
        : { kind: 'month', month });
    };
    const group = svgElement('g', {
      class: `year-dial-month${selected ? ' selected' : ''}`,
      role: 'button', tabindex: 0, 'aria-pressed': String(selected),
      'aria-label': `${MONTH_NAMES[index]}${score === undefined ? '' : `: ${Math.round(score)} out of 100, ${bandOf(score)}`}`,
      'data-month': month,
    });
    group.onclick = chooseMonth;
    group.onkeydown = event => {
      if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); chooseMonth(); }
      const offset = event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 0;
      if (offset) {
        event.preventDefault();
        const adjacentMonth = ((index + offset + 12) % 12) + 1;
        dial.querySelector<SVGGElement>(`[data-month="${adjacentMonth}"]`)?.focus();
      }
    };
    const start = index * 30;
    group.append(svgElement('path', { class: 'year-dial-target', d: dialArc(start + 2, start + 28, 91), fill: 'none', stroke: 'transparent', 'stroke-width': 42 }));
    group.append(svgElement('path', { class: 'year-dial-band', d: dialArc(start + 7, start + 23, 87), fill: 'none', stroke: score === undefined ? 'var(--border)' : bandColour(score), 'stroke-width': 18, 'stroke-linecap': 'round' }));
    const [labelX, labelY] = dialPoint(start + 15, 113);
    group.append(dialText('year-dial-label', label, labelX, labelY));
    dial.append(group);
  });
  if (selectedMonths.length === 12) {
    dial.append(svgElement('circle', { class: 'year-dial-selection', cx: 120, cy: 120, r: 71, fill: 'none' }));
  } else if (selectedMonths.length) {
    const start = (selectedMonths[0] - 1) * 30 + 4;
    const end = start + selectedMonths.length * 30 - 8;
    dial.append(svgElement('path', { class: 'year-dial-selection', d: dialArc(start, end, 71), fill: 'none' }));
  }
  const days = daysInPeriod(view.period, view.forecast);
  dial.append(dialText('year-dial-place', view.scorePlaceName ?? 'Your stretch', 120, 99));
  dial.append(dialText('year-dial-count', String(Math.round(view.niceDayCount ?? days)), 120, 124));
  dial.append(dialText('year-dial-caption', view.niceDayCount === undefined ? 'days selected' : `of ${view.countedDays ?? daysInPeriod(view.period, view.forecast)} days nice`, 120, 149));
  return dial;
}

function onBandColour(score: number): string {
  return score < 40 ? 'var(--on-low-band)' : 'var(--on-high-band)';
}

function forecastCalendar(view: TimeBarView, actions: Actions): HTMLElement {
  const calendar = el('div', 'forecast-calendar');
  if (!view.forecast) {
    calendar.append(el('p', 'picker-help', 'Loading the next 14 days…'));
    return calendar;
  }
  const availableDays = upcomingForecastDays(view.forecast).slice(0, 14);
  const grid = el('div', 'forecast-calendar-grid');
  grid.setAttribute('aria-label', 'Choose a forecast day');
  for (const day of availableDays) {
    const date = forecastDate(view.forecast, day);
    const weekday = date.toLocaleDateString('en-GB', { weekday: 'short' }).slice(0, 2);
    const label = `${weekday} ${date.getDate()}`;
    const selected = view.period.kind === 'forecast' || (view.period.kind === 'forecast-day' && view.period.day === day);
    const value = view.dayScores?.[day];
    const score = value !== undefined && Number.isFinite(value) ? value : undefined;
    const dayButton = button(`forecast-calendar-day${selected ? ' selected' : ''}`, '', () => actions.setPeriod({ kind: 'forecast-day', day }), `${dayChipLabel(view.forecast!, day)}${score === undefined ? '' : `: ${Math.round(score)} out of 100, ${bandOf(score)}`}`);
    dayButton.dataset.day = String(day);
    dayButton.setAttribute('aria-pressed', String(selected));
    dayButton.append(el('span', 'forecast-calendar-weekday', weekday), el('b', 'forecast-calendar-date', date.getDate()));
    dayButton.title = label;
    if (score !== undefined) {
      dayButton.style.backgroundColor = bandColour(score);
      dayButton.style.color = onBandColour(score);
    }
    grid.append(dayButton);
  }
  const start = forecastDate(view.forecast, availableDays[0] ?? 0);
  calendar.append(el('p', 'calendar-date-range', `${start.toLocaleDateString('en-GB', { month: 'long', year: 'numeric' })}${view.scorePlaceName ? ` in ${view.scorePlaceName}` : ''}`), grid);
  return calendar;
}

function periodShortcuts(view: TimeBarView, actions: Actions): HTMLElement {
  const southern = (view.home?.latitude ?? 1) < 0;
  const seasons: readonly (readonly [string, Season])[] = southern
    ? [['Summer', 'Dec-Feb'], ['Autumn', 'Mar-May'], ['Winter', 'Jun-Aug'], ['Spring', 'Sep-Nov']]
    : [['Winter', 'Dec-Feb'], ['Spring', 'Mar-May'], ['Summer', 'Jun-Aug'], ['Autumn', 'Sep-Nov']];
  const shortcuts = el('div', 'period-shortcuts');
  const word = periodWord(view.period);
  const addShortcut = (label: string, period: Period) => {
    const shortcut = button(`period-shortcut${word === periodWord(period) ? ' selected' : ''}`, label, () => actions.setPeriod(period));
    shortcut.setAttribute('aria-pressed', String(word === periodWord(period)));
    shortcuts.append(shortcut);
  };
  addShortcut('Whole year', { kind: 'year' });
  addShortcut(southern ? 'Your winter, Jun to Aug' : 'Your winter, Nov to Mar', southern ? { kind: 'season', season: 'Jun-Aug' } : { kind: 'months', startMonth: 11, endMonth: 3 });
  for (const [label, season] of seasons) addShortcut(label, { kind: 'season', season });
  if (view.playYear) {
    const play = button(`period-shortcut play-year${view.playing ? ' selected' : ''}`, view.playing ? 'Ⅱ Pause the year' : '▷ Play the year', view.playYear);
    play.setAttribute('aria-pressed', String(Boolean(view.playing)));
    shortcuts.append(play);
  }
  return shortcuts;
}

export function renderTimePicker(view: TimeBarView, actions: Actions): HTMLElement {
  const panel = el('section', 'time-picker-panel');
  panel.id = 'time-picker';
  panel.hidden = view.pickerOpen === false;
  panel.setAttribute('aria-label', 'Choose when');
  const close = view.togglePicker ? button('close-button picker-close', '×', view.togglePicker, 'Close time picker') : null;
  panel.append(el('div', 'time-picker-heading', el('h2', '', 'When is nice?'), close));
  panel.append(segmented<'forecast' | 'year'>(
    [['forecast', periodTitle({ kind: 'forecast' }, view.forecast)], ['year', 'Typical year']],
    isForecast(view.period) ? 'forecast' : 'year',
    kind => actions.setPeriod(kind === 'forecast' ? { kind: 'forecast' } : { kind: 'year' }), 'Time',
  ));
  if (isForecast(view.period)) {
    panel.append(forecastCalendar(view, actions));
    if (view.period.kind === 'forecast-day') panel.append(button('period-shortcut', `All ${view.forecast?.days ?? 14} days`, () => actions.setPeriod({ kind: 'forecast' })));
    panel.append(el('p', 'picker-help', 'Choose a day to see where to go.'));
  } else {
    panel.append(el('div', 'year-dial-row', yearDial(view, actions), el('div', 'year-dial-description',
      el('h3', '', periodTitle(view.period, view.forecast)),
      el('p', 'picker-help', view.period.kind === 'month' ? 'Now choose a last month for a longer stretch, or leave it here.' : 'Choose a first month, then a last. Your stretch can cross New Year.'),
      view.scorePlaceName ? el('p', 'picker-help', `The colours show ${view.scorePlaceName} through the year.`) : null,
    )), periodShortcuts(view, actions));
  }
  const method = el('a', '', 'How it works'); method.href = '/method';
  const credits = el('a', '', 'Credits'); credits.href = '/method#credits';
  panel.append(el('div', 'picker-links', method, credits));
  return panel;
}

function legend(view: TimeBarView): HTMLElement {
  const scale = el('div', 'legend');
  for (const [index, band] of BANDS.entries()) {
    const from = index === 0 ? '' : String(band.minimum);
    scale.append(el('span', 'legend-step', swatch(colourOnLand(bands[index].colour), 'legend-colour'), el('span', 'legend-text', band.name, from ? el('span', 'legend-from', ` ${from}`) : null)));
  }
  const caption = el('span', 'legend-caption', view.period.kind === 'forecast-day' ? 'Colours and label numbers: score out of 100' : `Label numbers: out of ${daysInPeriod(view.period, view.forecast)} days (${view.stretchLabel ?? periodTitle(view.period, view.forecast)})`);
  return el('div', 'legend-block', caption, scale);
}

export function renderTimeBar(view: TimeBarView, actions: Actions): HTMLElement {
  const period = button('period-trigger', el('span', '', periodTitle(view.period, view.forecast)), view.togglePicker ?? (() => {}), 'Choose when');
  period.append(el('span', 'period-trigger-arrow', '⌄'));
  period.setAttribute('aria-expanded', String(view.pickerOpen !== false));
  period.setAttribute('aria-controls', 'time-picker');
  const taste = button(`bar-button${view.tasteOpen ? ' active' : ''}${view.tasteIsDefault ? '' : ' changed'}`, el('span', 'button-label', 'Your taste'), actions.toggleTaste);
  const tasteIcon = el('span', 'button-icon'); tasteIcon.innerHTML = '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M3 6h18M3 12h18M3 18h18"/><circle cx="8" cy="6" r="2" fill="currentColor"/><circle cx="16" cy="12" r="2" fill="currentColor"/><circle cx="10" cy="18" r="2" fill="currentColor"/></svg>'; taste.append(tasteIcon);
  taste.setAttribute('aria-label', 'Your taste');
  if (!view.tasteIsDefault) taste.title = 'Your taste differs from the default';
  taste.setAttribute('aria-expanded', String(view.tasteOpen));
  const share = button(`bar-button${view.shareOpen ? ' active' : ''}`, el('span', 'button-label', 'Make a poster'), actions.toggleShare);
  const shareIcon = el('span', 'button-icon'); shareIcon.innerHTML = '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M12 16V3m-5 5 5-5 5 5M5 13v8h14v-8"/></svg>'; share.append(shareIcon);
  share.setAttribute('aria-label', 'Make a poster');
  share.setAttribute('aria-expanded', String(view.shareOpen));
  const method = el('a', 'small-link', 'How it works');
  method.href = '/method';
  const credits = el('a', 'small-link', 'Credits');
  credits.href = '/method#credits';
  const bar = el('nav', 'bar panel',
    el('div', 'bar-row bar-time', period, el('div', 'bar-actions', taste, share)),
    el('div', 'bar-row bar-meta', legend(view), el('div', 'small-print-row', method, credits)),
  );
  bar.append(renderTimePicker(view, actions));
  return bar;
}
