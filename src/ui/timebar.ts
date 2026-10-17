// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// The bar along the bottom: when (the one control used most), what the colours mean, taste and sharing,
// and the small print.
import { BANDS, type Period } from '../engine';
import { bands } from '../palette';
import type { Actions } from './actions';
import { button, el, swatch } from './dom';
import { MONTH_SHORT, SEASONS, SEASON_LABEL, dayChipLabel, isForecast, periodWord, samePeriod, upcomingForecastDays, type ForecastIndex } from './periods';

export const METHODOLOGY_LINE = 'Plz read the methodology b4 posting a screenshot on Twitter pwning me';

export type TimeBarView = Readonly<{
  period: Period;
  forecast: ForecastIndex | null;
  /** The chosen place's score for a period, when known, for a dot on that chip. */
  scoreFor: (period: Period) => number | null;
  tasteOpen: boolean;
  tasteIsDefault: boolean;
  shareOpen: boolean;
  theme: 'dark' | 'light';
}>;

export function bandColour(score: number): string {
  for (let index = bands.length - 1; index >= 0; index--) if (score >= bands[index].minimumScore) return opaque(bands[index].colour);
  return opaque(bands[0].colour);
}

/** The palette carries an alpha for the map; the interface uses the solid colour. */
export const opaque = (colour: string) => colour.slice(0, 7);

const LAND = { dark: [0x36, 0x3b, 0x43], light: [0xd5, 0xd9, 0xdf] } as const;

/** The band as it looks on the map: its translucent colour over the land, so the legend matches the colours seen. */
export function colourOnLand(colour: string, theme: 'dark' | 'light'): string {
  const channel = (index: number) => parseInt(colour.slice(1 + index * 2, 3 + index * 2), 16);
  const alpha = colour.length === 9 ? channel(3) / 255 : 1;
  const blended = [0, 1, 2].map(index => Math.round(channel(index) * alpha + LAND[theme][index] * (1 - alpha)));
  return `rgb(${blended.join(', ')})`;
}

function chip(label: string, selected: boolean, score: number | null, onSelect: () => void): HTMLButtonElement {
  const dot = el('span', 'chip-dot');
  if (score !== null) dot.style.background = bandColour(score);
  else dot.classList.add('empty');
  const node = button(selected ? 'chip selected' : 'chip', '', onSelect);
  node.append(dot, label);
  node.setAttribute('aria-pressed', String(selected));
  return node;
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

function periodChips(view: TimeBarView, actions: Actions): HTMLElement {
  const chips = el('div', 'chips');
  const add = (label: string, period: Period) => chips.append(chip(label, samePeriod(view.period, period), view.scoreFor(period), () => actions.setPeriod(period)));
  if (isForecast(view.period)) {
    add(`All ${view.forecast?.days ?? 14} days`, { kind: 'forecast' });
    if (view.forecast) for (const day of upcomingForecastDays(view.forecast)) add(dayChipLabel(view.forecast, day), { kind: 'forecast-day', day });
  } else {
    add('Year', { kind: 'year' });
    for (const season of SEASONS) add(SEASON_LABEL[season], { kind: 'season', season });
    for (let month = 1; month <= 12; month++) add(MONTH_SHORT[month - 1], { kind: 'month', month: month as 1 });
  }
  return chips;
}

function legend(theme: 'dark' | 'light'): HTMLElement {
  const scale = el('div', 'legend');
  for (const [index, band] of BANDS.entries()) {
    const from = index === 0 ? '' : String(band.minimum);
    scale.append(el('span', 'legend-step', swatch(colourOnLand(bands[index].colour, theme), 'legend-colour'), el('span', 'legend-text', band.name, from ? el('span', 'legend-from', ` ${from}`) : null)));
  }
  const caption = el('span', 'legend-caption', 'Score out of 100');
  return el('div', 'legend-block', caption, scale);
}

export function renderTimeBar(view: TimeBarView, actions: Actions): HTMLElement {
  const timeKind = segmented<'forecast' | 'year'>(
    [['forecast', `Next ${view.forecast?.days ?? 14} days`], ['year', 'Typical year']],
    isForecast(view.period) ? 'forecast' : 'year',
    kind => actions.setPeriod(kind === 'forecast' ? { kind: 'forecast' } : { kind: 'year' }),
    'Time',
  );
  const taste = button(`bar-button${view.tasteOpen ? ' active' : ''}${view.tasteIsDefault ? '' : ' changed'}`, 'Taste', actions.toggleTaste);
  if (!view.tasteIsDefault) taste.title = 'Your taste differs from the default';
  taste.setAttribute('aria-expanded', String(view.tasteOpen));
  const share = button(`bar-button${view.shareOpen ? ' active' : ''}`, 'Share', actions.toggleShare);
  share.setAttribute('aria-expanded', String(view.shareOpen));
  const chips = periodChips(view, actions);
  chips.dataset.period = periodWord(view.period);

  const method = el('a', 'small-link', 'How it works');
  method.href = '/method';
  const methodology = el('a', 'small-link methodology', METHODOLOGY_LINE);
  methodology.href = '/method';
  const built = el('span', 'small-print', `Built ${__BUILD_DATE__} · ${__BUILD_COMMIT__}`);

  return el('nav', 'bar panel',
    el('div', 'bar-row bar-time', timeKind, chips, el('div', 'bar-actions', taste, share)),
    el('div', 'bar-row bar-meta', legend(view.theme), el('div', 'small-print-row', method, methodology, built)),
  );
}
