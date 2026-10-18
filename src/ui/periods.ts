// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// The periods a visitor can look at, their words in links, and how they are named on screen.
import type { Month, Period, PointVerdict, Season } from '../engine';

/** /data/v6/forecast/current.json */
export type ForecastIndex = Readonly<{ run: string; firstDate: string; days: number }>;

export const MONTH_NAMES = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'] as const;
export const MONTH_SHORT = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'] as const;
export const SEASONS: readonly Season[] = ['Dec-Feb', 'Mar-May', 'Jun-Aug', 'Sep-Nov'];
export const SEASON_LABEL: Record<Season, string> = { 'Dec-Feb': 'Winter, Dec to Feb', 'Mar-May': 'Spring, Mar to May', 'Jun-Aug': 'Summer, Jun to Aug', 'Sep-Nov': 'Autumn, Sep to Nov' };
const SEASON_SENTENCE: Record<Season, string> = { 'Dec-Feb': 'December to February', 'Mar-May': 'March to May', 'Jun-Aug': 'June to August', 'Sep-Nov': 'September to November' };
const SEASON_DAYS: Record<Season, number> = { 'Dec-Feb': 90, 'Mar-May': 92, 'Jun-Aug': 92, 'Sep-Nov': 91 };
const MONTH_DAYS = [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];

export const DEFAULT_PERIOD: Period = { kind: 'forecast' };

export function isForecast(period: Period): boolean {
  return period.kind === 'forecast' || period.kind === 'forecast-day';
}

export function samePeriod(a: Period, b: Period): boolean {
  return periodWord(a) === periodWord(b);
}

/** The date or period in a shared link. */
export function periodWord(period: Period): string {
  switch (period.kind) {
    case 'forecast': return 'next-14-days';
    case 'forecast-day': return period.date ?? `day-${period.day}`;
    case 'month': return MONTH_NAMES[period.month - 1].toLowerCase();
    case 'season': return period.season.toLowerCase();
    case 'months': return `${MONTH_SHORT[period.startMonth - 1].toLowerCase()}-${MONTH_SHORT[period.endMonth - 1].toLowerCase()}`;
    case 'year': return 'year';
  }
}

export function periodFromWord(word: string): Period | null {
  if (word === 'next-14-days') return { kind: 'forecast' };
  if (word === 'year') return { kind: 'year' };
  if (/^\d{4}-\d{2}-\d{2}$/.test(word)) return { kind: 'forecast-day', day: 0, date: word };
  const month = MONTH_NAMES.findIndex(name => name.toLowerCase() === word);
  if (month >= 0) return { kind: 'month', month: (month + 1) as Month };
  const season = SEASONS.find(name => name.toLowerCase() === word);
  if (season) return { kind: 'season', season };
  const run = /^([a-z]{3})-([a-z]{3})$/.exec(word);
  if (run) {
    const startMonth = MONTH_SHORT.findIndex(month => month.toLowerCase() === run[1]) + 1;
    const endMonth = MONTH_SHORT.findIndex(month => month.toLowerCase() === run[2]) + 1;
    if (startMonth && endMonth) return startMonth === endMonth ? { kind: 'month', month: startMonth as Month } : { kind: 'months', startMonth: startMonth as Month, endMonth: endMonth as Month };
  }
  return null;
}

/** The local date of forecast day `day`. */
export function forecastDate(forecast: ForecastIndex, day: number): Date {
  const date = new Date(`${forecast.firstDate}T12:00:00`);
  date.setDate(date.getDate() + day);
  return date;
}

export function datedForecastPeriod(forecast: ForecastIndex, day: number): Period {
  const date = forecastDate(forecast, day);
  const word = `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
  return { kind: 'forecast-day', day, date: word };
}

function daysFromToday(date: Date, today = new Date()): number {
  const start = new Date(today.getFullYear(), today.getMonth(), today.getDate(), 12);
  return Math.round((date.getTime() - start.getTime()) / 86_400_000);
}

/** "Today", "Tomorrow", "Sun 19". */
export function dayChipLabel(forecast: ForecastIndex, day: number): string {
  const date = forecastDate(forecast, day);
  return date.toLocaleDateString('en-GB', { weekday: 'short', day: 'numeric' });
}

/** "today", "tomorrow", "on Sunday 19 October". */
function dayPhrase(forecast: ForecastIndex, day: number): string {
  const date = forecastDate(forecast, day);
  const difference = daysFromToday(date);
  if (difference === 0) return 'today';
  if (difference === 1) return 'tomorrow';
  return `on ${date.toLocaleDateString('en-GB', { weekday: 'long', day: 'numeric', month: 'long' })}`;
}

/** The period as it reads in a sentence after "nice": "in the next 14 days", "today", "in November", "all year". */
export function periodPhrase(period: Period, forecast: ForecastIndex | null): string {
  switch (period.kind) {
    case 'forecast': return forecast && daysFromToday(forecastDate(forecast, 0)) !== 0 ? `in the ${forecast.days}-day forecast` : `in the next ${forecast?.days ?? 14} days`;
    case 'forecast-day': return forecast ? dayPhrase(forecast, period.day) : 'that day';
    case 'month': return `in ${MONTH_NAMES[period.month - 1]}`;
    case 'season': return `from ${SEASON_SENTENCE[period.season]}`;
    case 'months': return `from ${MONTH_NAMES[period.startMonth - 1]} to ${MONTH_NAMES[period.endMonth - 1]}`;
    case 'year': return 'all year';
  }
}

/** The period as a title: "Next 14 days", "Sunday 19 October", "November", "December to February", "The whole year". */
export function periodTitle(period: Period, forecast: ForecastIndex | null): string {
  switch (period.kind) {
    case 'forecast': return forecast && daysFromToday(forecastDate(forecast, 0)) !== 0 ? `${forecast.days}-day forecast` : `Next ${forecast?.days ?? 14} days`;
    case 'forecast-day': {
      if (!forecast) return 'One day';
      const date = forecastDate(forecast, period.day);
      const difference = daysFromToday(date);
      const long = date.toLocaleDateString('en-GB', { weekday: 'long', day: 'numeric', month: 'long' });
      return difference === 0 ? `Today, ${long}` : difference === 1 ? `Tomorrow, ${long}` : long;
    }
    case 'month': return MONTH_NAMES[period.month - 1];
    case 'season': return SEASON_SENTENCE[period.season];
    case 'months': return `${MONTH_NAMES[period.startMonth - 1]} to ${MONTH_NAMES[period.endMonth - 1]}`;
    case 'year': return 'The whole year';
  }
}

/** Show every day used by the forecast total, including its explicitly dated first day. */
export function upcomingForecastDays(forecast: ForecastIndex): number[] {
  return Array.from({ length: forecast.days }, (_, day) => day);
}

export function daysInPeriod(period: Period, forecast: ForecastIndex | null): number {
  switch (period.kind) {
    case 'forecast': return forecast?.days ?? 14;
    case 'forecast-day': return 1;
    case 'month': return MONTH_DAYS[period.month - 1];
    case 'season': return SEASON_DAYS[period.season];
    case 'months': return monthsOfPeriod(period, forecast).reduce((days, month) => days + MONTH_DAYS[month - 1], 0);
    case 'year': return 365;
  }
}

/** Counts refer to the selected stretch, including all days within its months. */
export function dayCountsSentence(period: Period, forecast: ForecastIndex | null, verdict: PointVerdict): string {
  if (period.kind === 'forecast-day') return '';
  const days = verdict.countedDays ?? daysInPeriod(period, forecast);
  return `Nice or better on ${verdict.niceDayCount} of ${days} days, perfect on ${verdict.perfectDayCount}, awful on ${verdict.awfulDayCount}`;
}

export function bandSentence(period: Period, band: string): string {
  return period.kind === 'forecast-day' ? band : `${band} most days`;
}

/** The months a period draws on, for the typical numbers in the card. */
export function monthsOfPeriod(period: Period, forecast: ForecastIndex | null): Month[] {
  switch (period.kind) {
    case 'month': return [period.month];
    case 'months': {
      const months: Month[] = [];
      let month = period.startMonth;
      while (true) {
        months.push(month);
        if (month === period.endMonth) return months;
        month = (month % 12 + 1) as Month;
      }
    }
    case 'season': return { 'Dec-Feb': [12, 1, 2], 'Mar-May': [3, 4, 5], 'Jun-Aug': [6, 7, 8], 'Sep-Nov': [9, 10, 11] }[period.season] as Month[];
    case 'year': return [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12];
    case 'forecast':
    case 'forecast-day': {
      if (!forecast) return [];
      const first = forecastDate(forecast, period.kind === 'forecast-day' ? period.day : 0).getMonth() + 1;
      const last = forecastDate(forecast, period.kind === 'forecast-day' ? period.day : forecast.days - 1).getMonth() + 1;
      return first === last ? [first as Month] : [first as Month, last as Month];
    }
  }
}
