// Written by AI on 2026.10.17 by model gpt-6.1-sol.
import { loadGreyscaleImage } from './greyscaleImage';
import type { Period, Month } from './index';

/** ECMWF's free forecast ends at 360 hours, so every time zone gets 14 whole local days (.agent/v6-contract.md). */
export const FORECAST_DAYS = 14;

export type Records = Readonly<{ bytes: Uint8Array; samples: number }>;
export type SampleGroup = Readonly<{ records: Records; firstDay: number; dayCount: number; month: Month; weight: number }>;
export type PeriodData = Readonly<{ groups: readonly SampleGroup[]; offsets: ReadonlyMap<Month, Uint8Array>; sampleDays: number; totalDays: number }>;
export type Cells = Readonly<{ count: number; ordinals: Uint16Array }>;

type ForecastIndex = Readonly<{ run: string; firstDate: string; days: number }>;
export type ScoreImage = Readonly<{ bytes: Uint8Array }>;
const monthWords = ['january', 'february', 'march', 'april', 'may', 'june', 'july', 'august', 'september', 'october', 'november', 'december'];

type Forecast = Readonly<{ records: Records; firstDate: string }>;

const seasons = { 'Dec-Feb': [12, 1, 2], 'Mar-May': [3, 4, 5], 'Jun-Aug': [6, 7, 8], 'Sep-Nov': [9, 10, 11] } as const;

export const MONTH_DAYS = [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31] as const;
const monthShortWords = monthWords.map(word => word.slice(0, 3));

export function climateMonths(period: Exclude<Period, { kind: 'forecast' | 'forecast-day' }>): readonly Month[] {
  if (period.kind === 'months') return Array.from({ length: (period.endMonth - period.startMonth + 12) % 12 + 1 }, (_, index) => ((period.startMonth - 1 + index) % 12 + 1) as Month);
  return period.kind === 'month' ? [period.month] : period.kind === 'season' ? seasons[period.season] : [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12];
}

function forecastGroups(period: Extract<Period, { kind: 'forecast' | 'forecast-day' }>, forecast: Forecast): SampleGroup[] {
  const firstDay = period.kind === 'forecast-day' ? period.day : 0;
  const dayCount = period.kind === 'forecast-day' ? 1 : FORECAST_DAYS;
  const groups: SampleGroup[] = [];
  for (let day = firstDay; day < firstDay + dayCount; day++) {
    const date = new Date(`${forecast.firstDate}T12:00:00Z`);
    date.setUTCDate(date.getUTCDate() + day);
    const month = (date.getUTCMonth() + 1) as Month;
    const previous = groups[groups.length - 1];
    if (previous?.month === month) groups[groups.length - 1] = { ...previous, dayCount: previous.dayCount + 1, weight: previous.weight + 1 / dayCount };
    else groups.push({ records: forecast.records, firstDay: day, dayCount: 1, month, weight: 1 / dayCount });
  }
  return groups;
}

export class WeatherData {
  private cellRequest?: Promise<Cells>;
  private forecastIndexRequest?: Promise<ForecastIndex>;
  private scoreImages = new Map<string, Promise<ScoreImage | null>>();
  private monthlyRecords = new Map<Month, Promise<Records>>();
  private monthlyOffsets = new Map<Month, Promise<Uint8Array>>();
  private forecast?: Promise<Forecast>;
  private loadedMonthlyRecords = new Map<Month, Records>();
  private loadedOffsets = new Map<Month, Uint8Array>();
  private loadedForecast?: Forecast;

  constructor(private baseUrl: string) {}

  get cells(): Promise<Cells> {
    return this.cellRequest ??= this.binary('cells.bin').then(bytes => {
      if (bytes.length === 0 || bytes.length % 4 !== 0) throw new Error('v6 cells.bin has an invalid length');
      const pairs = new Uint16Array(bytes.buffer, bytes.byteOffset, bytes.length / 2);
      return { count: bytes.length / 4, ordinals: nearestLandOrdinals(pairs) };
    }).catch(error => { this.cellRequest = undefined; throw error; });
  }

  private forecastIndex(): Promise<ForecastIndex> {
    return this.forecastIndexRequest ??= (async () => {
      const response = await fetch(`${this.baseUrl}/forecast/current.json`, { cache: 'no-cache' });
      if (!response.ok) throw new Error(`v6 forecast/current.json: HTTP ${response.status}`);
      const current = await response.json() as ForecastIndex;
      if (current.days !== FORECAST_DAYS || !/^\d{4}-\d{2}-\d{2}$/.test(current.firstDate) || !current.run) throw new Error('Invalid v6 forecast/current.json');
      return current;
    })().catch(error => { this.forecastIndexRequest = undefined; throw error; });
  }

  async scoreImage(period: Period): Promise<ScoreImage | null> {
    let path: string;
    if (period.kind === 'forecast' || period.kind === 'forecast-day') {
      if (period.kind === 'forecast-day' && (!Number.isInteger(period.day) || period.day < 0 || period.day >= FORECAST_DAYS)) throw new RangeError(`Forecast day must be 0..${FORECAST_DAYS - 1}`);
      const index = await this.forecastIndex();
      path = `forecast/${encodeURIComponent(index.run)}/scores-typical-day-${period.kind === 'forecast' ? 'all' : `day-${String(period.day).padStart(2, '0')}`}.png`;
    } else {
      const months = climateMonths(period);
      const word = months.length === 12 ? 'year' : months.length === 1 ? monthWords[months[0] - 1]
        : `${monthShortWords[months[0] - 1]}-${monthShortWords[months[months.length - 1] - 1]}`;
      path = `climate/scores-typical-day-${word}.png`;
    }
    let request = this.scoreImages.get(path);
    if (!request) {
      request = loadGreyscaleImage(`${this.baseUrl}/${path}`).then(bytes => {
        if (bytes.length !== 1440 * 721) throw new Error(`${path}: expected the 1440 × 721 fine grid`);
        return { bytes };
      }).catch(error => {
        this.scoreImages.delete(path);
        if (!String(error).endsWith('HTTP 404')) console.warn('Score image unavailable; loading weather records', error);
        return null;
      });
      this.scoreImages.set(path, request);
    }
    return request;
  }

  private async binary(path: string): Promise<Uint8Array> {
    const url = `${this.baseUrl}/${path}`;
    const response = await fetch(url);
    if (!response.ok) throw new Error(`${url}: HTTP ${response.status}`);
    return new Uint8Array(await response.arrayBuffer());
  }

  private async records(path: string, samples: number): Promise<Records> {
    const [bytes, cells] = await Promise.all([this.binary(path), this.cells]);
    if (bytes.length !== cells.count * samples * 15) throw new Error(`${path}: expected ${cells.count * samples * 15} bytes, got ${bytes.length}`);
    return { bytes, samples };
  }

  private monthRecords(month: Month): Promise<Records> {
    let request = this.monthlyRecords.get(month);
    if (!request) {
      request = this.records(`climate/month-${String(month).padStart(2, '0')}.bin`, 8).then(records => {
        this.loadedMonthlyRecords.set(month, records);
        return records;
      }).catch(error => {
        this.monthlyRecords.delete(month);
        throw error;
      });
      this.monthlyRecords.set(month, request);
    }
    return request;
  }

  private offsets(month: Month): Promise<Uint8Array> {
    let request = this.monthlyOffsets.get(month);
    if (!request) {
      request = loadGreyscaleImage(`${this.baseUrl}/climate/offsets-${String(month).padStart(2, '0')}.png`).then(bytes => {
        if (bytes.length !== 1440 * 721) throw new Error(`v6 offsets ${month}: expected the 1440 × 721 fine grid`);
        this.loadedOffsets.set(month, bytes);
        return bytes;
      }).catch(error => {
        this.monthlyOffsets.delete(month);
        throw error;
      });
      this.monthlyOffsets.set(month, request);
    }
    return request;
  }

  /** Returns only resolved files; never starts a request or changes the selected period. */
  loaded(period: Period): PeriodData | null {
    let groups: SampleGroup[];
    if (period.kind === 'forecast' || period.kind === 'forecast-day') {
      if (!this.loadedForecast || (period.kind === 'forecast-day' && (!Number.isInteger(period.day) || period.day < 0 || period.day >= FORECAST_DAYS))) return null;
      groups = forecastGroups(period, this.loadedForecast);
    } else {
      const months = climateMonths(period);
      if (months.some(month => !this.loadedMonthlyRecords.has(month))) return null;
      groups = months.map(month => ({ records: this.loadedMonthlyRecords.get(month)!, firstDay: 0, dayCount: 8, month, weight: MONTH_DAYS[month - 1] / months.reduce((sum, month) => sum + MONTH_DAYS[month - 1], 0) }));
    }
    if (groups.some(group => !this.loadedOffsets.has(group.month))) return null;
    return { groups, offsets: new Map(groups.map(group => [group.month, this.loadedOffsets.get(group.month)!])), sampleDays: groups.reduce((sum, group) => sum + group.dayCount, 0), totalDays: period.kind === 'forecast-day' ? 1 : period.kind === 'forecast' ? FORECAST_DAYS : climateMonths(period).reduce((sum, month) => sum + MONTH_DAYS[month - 1], 0) };
  }

  async load(period: Period): Promise<PeriodData> {
    let groups: SampleGroup[];
    if (period.kind === 'forecast' || period.kind === 'forecast-day') {
      if (period.kind === 'forecast-day' && (!Number.isInteger(period.day) || period.day < 0 || period.day >= FORECAST_DAYS)) throw new RangeError(`Forecast day must be 0..${FORECAST_DAYS - 1}`);
      if (!this.forecast) {
        this.forecast = (async () => {
          const current = await this.forecastIndex();
          return { records: await this.records(`forecast/${encodeURIComponent(current.run)}/days.bin`, FORECAST_DAYS), firstDate: current.firstDate };
        })().then(forecast => { this.loadedForecast = forecast; return forecast; }).catch(error => { this.forecast = undefined; throw error; });
      }
      const index = await this.forecastIndex();
      const firstDay = period.kind === 'forecast-day' ? period.day : 0;
      const count = period.kind === 'forecast-day' ? 1 : FORECAST_DAYS;
      const months = new Set<Month>();
      for (let day = firstDay; day < firstDay + count; day++) {
        const date = new Date(`${index.firstDate}T12:00:00Z`);
        date.setUTCDate(date.getUTCDate() + day);
        months.add((date.getUTCMonth() + 1) as Month);
      }
      const [forecast] = await Promise.all([this.forecast, ...[...months].map(month => this.offsets(month))]);
      groups = forecastGroups(period, forecast);
    } else {
      const months = climateMonths(period);
      if (months.some(month => !Number.isInteger(month) || month < 1 || month > 12)) throw new RangeError('Month must be 1..12');
      groups = await Promise.all(months.map(async month => {
        const [records] = await Promise.all([this.monthRecords(month), this.offsets(month)]);
        return { records, firstDay: 0, dayCount: 8, month, weight: MONTH_DAYS[month - 1] / months.reduce((sum, month) => sum + MONTH_DAYS[month - 1], 0) };
      }));
    }
    const offsets = new Map(await Promise.all(groups.map(async group => [group.month, await this.offsets(group.month)] as const)));
    return { groups, offsets, sampleDays: groups.reduce((sum, group) => sum + group.dayCount, 0), totalDays: period.kind === 'forecast-day' ? 1 : period.kind === 'forecast' ? FORECAST_DAYS : climateMonths(period).reduce((sum, month) => sum + MONTH_DAYS[month - 1], 0) };
  }
}

/** Exact Euclidean nearest land in grid coordinates, with longitude wrapping. Only runs once. */
function nearestLandOrdinals(pairs: Uint16Array): Uint16Array {
  const count = pairs.length / 2;
  const land = new Int32Array(360 * 180).fill(-1);
  for (let ordinal = 0; ordinal < count; ordinal++) {
    const row = pairs[ordinal * 2], column = pairs[ordinal * 2 + 1];
    if (row >= 180 || column >= 360) throw new Error('v6 cells.bin contains a cell outside the world grid');
    land[row * 360 + column] = ordinal;
  }
  const rowNearest = new Int32Array(360 * 180).fill(-1);
  const rowDistance = new Float32Array(360 * 180).fill(Infinity);
  for (let row = 0; row < 180; row++) {
    let nearest = -1080;
    for (let column = -360; column < 720; column++) {
      const wrapped = (column + 360) % 360;
      if (land[row * 360 + wrapped] >= 0) nearest = column;
      if (column >= 0 && column < 360 && nearest !== -1080) {
        const index = row * 360 + column;
        rowNearest[index] = land[row * 360 + (nearest + 360) % 360];
        rowDistance[index] = (column - nearest) ** 2;
      }
    }
    nearest = 1080;
    for (let column = 719; column >= -360; column--) {
      const wrapped = (column + 360) % 360;
      if (land[row * 360 + wrapped] >= 0) nearest = column;
      if (column >= 0 && column < 360 && nearest !== 1080) {
        const index = row * 360 + column;
        if ((column - nearest) ** 2 < rowDistance[index]) {
          rowNearest[index] = land[row * 360 + (nearest + 360) % 360];
          rowDistance[index] = (column - nearest) ** 2;
        }
      }
    }
  }
  const ordinals = new Uint16Array(360 * 180);
  for (let row = 0; row < 180; row++) {
    for (let column = 0; column < 360; column++) {
      const index = row * 360 + column;
      if (land[index] >= 0) { ordinals[index] = land[index]; continue; }
      let distance = Infinity;
      for (let sourceRow = 0; sourceRow < 180; sourceRow++) {
        const candidate = sourceRow * 360 + column;
        const squaredDistance = (row - sourceRow) ** 2 + rowDistance[candidate];
        if (squaredDistance < distance) { distance = squaredDistance; ordinals[index] = rowNearest[candidate]; }
      }
    }
  }
  return ordinals;
}
