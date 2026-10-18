// Written by AI on 2026.10.17 by model gpt-6.1-sol.
import { bandOf, decodeDay, judgeDay, type DayJudgement, type Taste } from './taste';
import type { Cells, PeriodData, SampleGroup } from './data';
import type { Period, PointVerdict, TypicalWeather } from './index';
import { SHIFT_LEVELS, SHIFT_MINIMUM, SHIFT_STEP } from './shaders';
import { countDayScores } from '../../hosting/sites/day-counts.mjs';

export type PointContribution = Readonly<{ group: number; cell: number; level: number; weight: number }>;
type MutableTypicalWeather = { -readonly [Key in keyof TypicalWeather]-?: number };
type CellJudgement = { -readonly [Key in keyof DayJudgement]: DayJudgement[Key] } & { days: number[]; typical: MutableTypicalWeather };

const typicalNames = ['feelsInSunAt14C', 'feelsInShadeAt15C', 'feelsInShadeAt21C', 'dewPointC', 'rainMmPerDay', 'wetDayShare', 'windKmh', 'sunnyShare', 'daylightHours'] as const;
function emptyTypical(): MutableTypicalWeather {
  return { feelsInSunAt14C: 0, feelsInShadeAt15C: 0, feelsInShadeAt21C: 0, dewPointC: 0, rainMmPerDay: 0, wetDayShare: 0, windKmh: 0, sunnyShare: 0, daylightHours: 0 };
}

const partNames = ['warmth', 'evening', 'rainFactor', 'windFactor', 'humidityFactor', 'sunFactor'] as const;
const wrap = (column: number, width: number) => (column % width + width) % width;
const clamp = (value: number, minimum: number, maximum: number) => Math.max(minimum, Math.min(maximum, value));
/** The map's normalized 4 × 4 B-spline footprint, excluding missing land. */
export function mapPointWeights(latitude: number, longitude: number, valid: (index: number) => boolean): { index: number; weight: number }[] {
  if (!Number.isFinite(latitude) || !Number.isFinite(longitude) || latitude < -60 || latitude > 90) return [];
  const x = wrap((longitude + 180) * 4, 1440), y = (90 - latitude) * 4;
  const weights = (fraction: number) => [(1 - fraction) ** 3, 3 * fraction ** 3 - 6 * fraction ** 2 + 4, -3 * fraction ** 3 + 3 * fraction ** 2 + 3 * fraction + 1, fraction ** 3].map(value => value / 6);
  const horizontal = weights(x - Math.floor(x)), vertical = weights(y - Math.floor(y));
  const points: { index: number; weight: number }[] = [];
  let landWeight = 0;
  for (let row = 0; row < 4; row++) for (let column = 0; column < 4; column++) {
    const index = clamp(Math.floor(y) + row - 1, 0, 720) * 1440 + wrap(Math.floor(x) + column - 1, 1440);
    if (!valid(index)) continue;
    const weight = horizontal[column] * vertical[row];
    points.push({ index, weight });
    landWeight += weight;
  }
  return points.map(point => ({ index: point.index, weight: point.weight / landWeight }));
}

function pointContributions(fineIndex: number, cells: Cells, data: PeriodData): PointContribution[] {
  const column = fineIndex % 1440, row = Math.floor(fineIndex / 1440);
  const cell = cells.ordinals[clamp(Math.floor(row / 4), 0, 179) * 360 + Math.floor(column / 4)];
  const contributions: PointContribution[] = [];
  data.groups.forEach((group, groupIndex) => {
    const shift = (data.offsets.get(group.month)![fineIndex] - 128) / 10;
    const level = clamp((shift - SHIFT_MINIMUM) / SHIFT_STEP, 0, SHIFT_LEVELS - 1);
    const lower = Math.min(Math.floor(level), SHIFT_LEVELS - 2);
    for (let upper = 0; upper < 2; upper++) {
      const weight = group.weight * (upper ? level - lower : 1 - (level - lower));
      if (weight > 0) contributions.push({ group: groupIndex, cell, level: lower + upper, weight });
    }
  });
  return contributions;
}

function judgeCell(group: SampleGroup, cell: number, level: number, taste: Taste): CellJudgement {
  const result: CellJudgement = { score: 0, warmth: 0, evening: 0, rainFactor: 0, windFactor: 0, humidityFactor: 0, sunFactor: 0, days: [], typical: emptyTypical() };
  for (let sample = group.firstDay; sample < group.firstDay + group.dayCount; sample++) {
    const day = decodeDay(group.records.bytes, (cell * group.records.samples + sample) * 15);
    const feltOffset = SHIFT_MINIMUM + level * SHIFT_STEP;
    const judgement = judgeDay(day, taste, feltOffset);
    const typical: MutableTypicalWeather = {
      feelsInSunAt14C: day.feelsInSun[2] + feltOffset,
      feelsInShadeAt15C: day.feelsInShade[1] + feltOffset,
      feelsInShadeAt21C: day.feelsInShade[3] + feltOffset,
      dewPointC: day.dewPoint, rainMmPerDay: day.rainMm,
      wetDayShare: day.rainMm >= 1 ? 1 : 0, windKmh: day.windKmh,
      sunnyShare: day.sunnyHours / day.daylightHours, daylightHours: day.daylightHours,
    };
    for (const name of typicalNames) result.typical[name] += typical[name] / group.dayCount;
    result.days.push(judgement.score);
    result.score += judgement.score / group.dayCount;
    for (const part of partNames) result[part] += judgement[part] / group.dayCount;
  }
  return result;
}

export class PointSampler {
  private cachedCells = new Map<number, CellJudgement>();
  private cachedGridPoints = new Map<number, PointVerdict>();
  private cachedPoints = new Map<string, PointVerdict | null>();
  constructor(private cells: Cells, private data: PeriodData, private period: Period, private taste: Taste) {}

  judge(latitude: number, longitude: number): PointVerdict | null {
    const key = `${latitude},${longitude}`;
    if (this.cachedPoints.has(key)) return this.cachedPoints.get(key)!;
    const footprint = mapPointWeights(latitude, longitude, index => [...this.data.offsets.values()].every(offsets => offsets[index] !== 255));
    if (!footprint.length) { this.cachedPoints.set(key, null); return null; }
    const points = footprint.map(point => ({ verdict: this.judgeGridPoint(point.index), weight: point.weight }));
    const average = (read: (verdict: PointVerdict) => number) => points.reduce((sum, point) => sum + read(point.verdict) * point.weight, 0);
    const meanScore = canonicalScore(average(point => point.meanScore));
    const typical = emptyTypical();
    for (const name of typicalNames) typical[name] = average(point => point.typical[name] ?? 0);
    const parts = { warmth: 0, evening: 0, rainFactor: 0, windFactor: 0, humidityFactor: 0, sunFactor: 0 };
    for (const name of partNames) parts[name] = average(point => point.parts[name]);
    const dayScores = points[0].verdict.dayScores!.map((_, day) => canonicalScore(average(point => point.dayScores![day])));
    const verdict = {
      meanScore, band: bandOf(meanScore), niceDayShare: average(point => point.niceDayShare), perfectDayShare: average(point => point.perfectDayShare), awfulDayShare: average(point => point.awfulDayShare),
      ...countDayScores(dayScores, this.period.kind === 'forecast' || this.period.kind === 'forecast-day' ? 'forecast' : 'sample'),
      totalDays: this.data.totalDays, sampleDays: this.data.sampleDays, parts, typical,
    };
    this.cachedPoints.set(key, verdict);
    return verdict;
  }

  private judgeGridPoint(index: number): PointVerdict {
    const cached = this.cachedGridPoints.get(index);
    if (cached) return cached;
    const contributions = pointContributions(index, this.cells, this.data);
    let meanScore = 0;
    const dayScores = this.data.groups.map(group => new Float64Array(group.dayCount));
    const typical = emptyTypical();
    const parts = { warmth: 0, evening: 0, rainFactor: 0, windFactor: 0, humidityFactor: 0, sunFactor: 0 };
    for (const contribution of contributions) {
      const key = (contribution.group * this.cells.count + contribution.cell) * SHIFT_LEVELS + contribution.level;
      let judgement = this.cachedCells.get(key);
      if (!judgement) {
        judgement = judgeCell(this.data.groups[contribution.group], contribution.cell, contribution.level, this.taste);
        this.cachedCells.set(key, judgement);
      }
      for (const name of typicalNames) typical[name] += judgement.typical[name] * contribution.weight;
      meanScore += judgement.score * contribution.weight;
      for (let day = 0; day < judgement.days.length; day++) dayScores[contribution.group][day] += judgement.days[day] * contribution.weight / this.data.groups[contribution.group].weight;
      for (const part of partNames) parts[part] += judgement[part] * contribution.weight;
    }
    const shares = [1, 0, 0, 0, 0, 0, 0];
    const thresholds = [0, 25, 40, 55, 70, 85, 100];
    let singleDayScore = 0;
    for (let group = 0; group < dayScores.length; group++) for (const interpolatedScore of dayScores[group]) {
      const score = canonicalScore(interpolatedScore);
      singleDayScore = score;
      const weight = this.data.groups[group].weight / dayScores[group].length;
      for (let threshold = 1; threshold < thresholds.length; threshold++) if (score >= thresholds[threshold]) shares[threshold] += weight;
    }
    meanScore = this.data.totalDays > 1 ? typicalDayScore(shares) : singleDayScore;
    const niceDayShare = shares[3], perfectDayShare = shares[5], awfulDayShare = Math.max(0, 1 - shares[1]);
    const verdict = {
      meanScore, band: bandOf(meanScore), niceDayShare, perfectDayShare, awfulDayShare,
      ...countDayScores(dayScores.flatMap(scores => Array.from(scores, canonicalScore)), this.period.kind === 'forecast' || this.period.kind === 'forecast-day' ? 'forecast' : 'sample'),
      totalDays: this.data.totalDays, sampleDays: this.data.sampleDays, parts, typical,
    };
    this.cachedGridPoints.set(index, verdict);
    return verdict;
  }
}

/** The same filtered score field used by the map fragment shader. */
export function readMapPoint(values: ArrayLike<number>, latitude: number, longitude: number, stride = 1, channel = 0): number {
  const points = mapPointWeights(latitude, longitude, index => values[index * stride] <= 100.5);
  if (!points.length) return NaN;
  return points.reduce((score, point) => score + values[point.index * stride + channel] * point.weight, 0);
}

const canonicalScore = (score: number): number => Math.round(score * 10000) / 10000;

/** Threshold shares locate the median's band; interpolation keeps map colours continuous. */
export function typicalDayScore(shares: readonly number[]): number {
  const thresholds = [0, 25, 40, 55, 70, 85, 100];
  for (let index = 5; index >= 0; index--) {
    if (shares[index] < 0.5 - 1e-9) continue;
    if (index === 5) return canonicalScore(Math.max(85, 85 + (shares[5] - 0.5) * 30));
    return canonicalScore(Math.max(thresholds[index], thresholds[index] + (thresholds[index + 1] - thresholds[index]) * (shares[index] - 0.5) / (shares[index] - shares[index + 1])));
  }
  return 0;
}
