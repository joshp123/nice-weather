// Written by AI on 2026.10.17 by model gpt-6.1-sol.
import { bandOf, decodeDay, judgeDay, NICE_SCORE, type DayJudgement, type Taste } from './taste';
import type { Cells, PeriodData, SampleGroup } from './data';
import type { Period, PointVerdict, TypicalWeather } from './index';
import { SHIFT_LEVELS, SHIFT_MINIMUM, SHIFT_STEP } from './shaders';

export type PointContribution = Readonly<{ group: number; cell: number; level: number; weight: number }>;
type MutableTypicalWeather = { -readonly [Key in keyof TypicalWeather]: number };
type CellJudgement = { -readonly [Key in keyof DayJudgement]: DayJudgement[Key] } & { niceDayShare: number; typical: MutableTypicalWeather };

const typicalNames = ['feelsInSunAt14C', 'feelsInShadeAt21C', 'dewPointC', 'rainMmPerDay', 'wetDayShare', 'windKmh', 'sunnyShare', 'daylightHours'] as const;
function emptyTypical(): MutableTypicalWeather {
  return { feelsInSunAt14C: 0, feelsInShadeAt21C: 0, dewPointC: 0, rainMmPerDay: 0, wetDayShare: 0, windKmh: 0, sunnyShare: 0, daylightHours: 0 };
}

const partNames = ['warmth', 'evening', 'rainFactor', 'windFactor', 'humidityFactor', 'sunFactor'] as const;
const wrap = (column: number, width: number) => (column % width + width) % width;
const clamp = (value: number, minimum: number, maximum: number) => Math.max(minimum, Math.min(maximum, value));
/** Search the 5 × 5 neighbourhood centred on the rounded fine coordinate; longitude wraps. */
export function nearestLandFineIndex(latitude: number, longitude: number, offsets: readonly Uint8Array[]): number | null {
  if (!Number.isFinite(latitude) || !Number.isFinite(longitude) || latitude < -60 || latitude > 90) return null;
  const x = wrap((longitude + 180) * 4, 1440), y = (90 - latitude) * 4;
  const centreColumn = Math.round(x), centreRow = Math.round(y);
  let nearest: number | null = null, nearestDistance = Infinity;
  for (let deltaRow = -2; deltaRow <= 2; deltaRow++) {
    const row = centreRow + deltaRow;
    if (row < 0 || row > 600) continue;
    for (let deltaColumn = -2; deltaColumn <= 2; deltaColumn++) {
      const unwrappedColumn = centreColumn + deltaColumn;
      const index = row * 1440 + wrap(unwrappedColumn, 1440);
      if (offsets.some(month => month[index] === 255)) continue;
      const distance = (row - y) ** 2 + (unwrappedColumn - x) ** 2;
      if (distance < nearestDistance) { nearest = index; nearestDistance = distance; }
    }
  }
  return nearest;
}

/** The selected land point's own offset, then the map's level and cell interpolation, without its final spline. */
export function pointContributions(latitude: number, longitude: number, cells: Cells, data: PeriodData): PointContribution[] | null {
  const fineIndex = nearestLandFineIndex(latitude, longitude, [...data.offsets.values()]);
  if (fineIndex === null) return null;
  const column = fineIndex % 1440, row = Math.floor(fineIndex / 1440);
  const cellX = column / 4 - 0.5, cellY = row / 4 - 0.5;
  const cellBaseX = Math.floor(cellX), cellBaseY = Math.floor(cellY);
  const fractionX = cellX - cellBaseX, fractionY = cellY - cellBaseY;
  const weights = new Map<number, number>();
  for (let groupIndex = 0; groupIndex < data.groups.length; groupIndex++) {
    const group = data.groups[groupIndex];
    const shift = (data.offsets.get(group.month)![fineIndex] - 128) / 10;
    const level = clamp((shift - SHIFT_MINIMUM) / SHIFT_STEP, 0, SHIFT_LEVELS - 1);
    const lower = Math.min(Math.floor(level), SHIFT_LEVELS - 2);
    for (let cellRow = 0; cellRow < 2; cellRow++) {
      for (let cellColumn = 0; cellColumn < 2; cellColumn++) {
        const cell = cells.ordinals[clamp(cellBaseY + cellRow, 0, 179) * 360 + wrap(cellBaseX + cellColumn, 360)];
        const spatialWeight = (cellColumn ? fractionX : 1 - fractionX) * (cellRow ? fractionY : 1 - fractionY) * group.weight;
        for (let upper = 0; upper < 2; upper++) {
          const weight = spatialWeight * (upper ? level - lower : 1 - (level - lower));
          if (weight === 0) continue;
          const key = (groupIndex * cells.count + cell) * SHIFT_LEVELS + lower + upper;
          weights.set(key, (weights.get(key) ?? 0) + weight);
        }
      }
    }
  }
  return Array.from(weights, ([key, weight]) => ({
    group: Math.floor(key / SHIFT_LEVELS / cells.count),
    cell: Math.floor(key / SHIFT_LEVELS) % cells.count,
    level: key % SHIFT_LEVELS, weight,
  }));
}

function judgeCell(group: SampleGroup, cell: number, level: number, taste: Taste): CellJudgement {
  const result: CellJudgement = { score: 0, warmth: 0, evening: 0, rainFactor: 0, windFactor: 0, humidityFactor: 0, sunFactor: 0, niceDayShare: 0, typical: emptyTypical() };
  for (let sample = group.firstDay; sample < group.firstDay + group.dayCount; sample++) {
    const day = decodeDay(group.records.bytes, (cell * group.records.samples + sample) * 15);
    const feltOffset = SHIFT_MINIMUM + level * SHIFT_STEP;
    const judgement = judgeDay(day, taste, feltOffset);
    const typical: TypicalWeather = {
      feelsInSunAt14C: day.feelsInSun[2] + feltOffset,
      feelsInShadeAt21C: day.feelsInShade[3] + feltOffset,
      dewPointC: day.dewPoint, rainMmPerDay: day.rainMm,
      wetDayShare: day.rainMm >= 1 ? 1 : 0, windKmh: day.windKmh,
      sunnyShare: day.sunnyHours / day.daylightHours, daylightHours: day.daylightHours,
    };
    for (const name of typicalNames) result.typical[name] += typical[name] / group.dayCount;
    result.score += judgement.score / group.dayCount;
    result.niceDayShare += (judgement.score >= NICE_SCORE ? 1 : 0) / group.dayCount;
    for (const part of partNames) result[part] += judgement[part] / group.dayCount;
  }
  return result;
}

export class PointSampler {
  private cachedCells = new Map<number, CellJudgement>();
  constructor(private cells: Cells, private data: PeriodData, private period: Period, private taste: Taste) {}

  judge(latitude: number, longitude: number): PointVerdict | null {
    const contributions = pointContributions(latitude, longitude, this.cells, this.data);
    if (!contributions) return null;
    let meanScore = 0, niceDayShare = 0;
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
      niceDayShare += judgement.niceDayShare * contribution.weight;
      for (const part of partNames) parts[part] += judgement[part] * contribution.weight;
    }
    return {
      meanScore, band: bandOf(meanScore), niceDayShare,
      niceDayCount: this.period.kind === 'forecast' || this.period.kind === 'forecast-day' ? niceDayShare * this.data.sampleDays : null,
      sampleDays: this.data.sampleDays, parts, typical,
    };
  }
}

/** Read the selected land point before the map's final B-spline filter. */
export function readFineScore(values: Float32Array, latitude: number, longitude: number, offsets: readonly Uint8Array[]): number {
  const index = nearestLandFineIndex(latitude, longitude, offsets);
  return index === null ? NaN : values[index];
}
