// Written by AI on 2026.10.17 by model gpt-6.1-sol.
import { bandOf, decodeDay, judgeDay, NICE_SCORE, type DayJudgement, type Taste } from './taste';
import type { Cells, PeriodData, SampleGroup } from './data';
import type { Period, PointVerdict } from './index';
import { SHIFT_LEVELS, SHIFT_MINIMUM, SHIFT_STEP } from './shaders';

export type PointContribution = Readonly<{ group: number; cell: number; level: number; weight: number }>;
type CellJudgement = { -readonly [Key in keyof DayJudgement]: DayJudgement[Key] } & { niceDayShare: number };

const partNames = ['warmth', 'evening', 'rainFactor', 'windFactor', 'humidityFactor', 'sunFactor'] as const;
const wrap = (column: number, width: number) => (column % width + width) % width;
const clamp = (value: number, minimum: number, maximum: number) => Math.max(minimum, Math.min(maximum, value));
function splineWeights(t: number): number[] {
  return [(1 - t) ** 3 / 6, (3 * t ** 3 - 6 * t * t + 4) / 6, (-3 * t ** 3 + 3 * t * t + 3 * t + 1) / 6, t ** 3 / 6];
}

/** The same spline, cell and felt-shift weights used by the two rendering passes. */
export function pointContributions(latitude: number, longitude: number, cells: Cells, data: PeriodData): PointContribution[] | null {
  if (!Number.isFinite(latitude) || !Number.isFinite(longitude) || latitude < -60 || latitude > 90) return null;
  const x = wrap((longitude + 180) * 4, 1440), y = (90 - latitude) * 4;
  const baseX = Math.floor(x), baseY = Math.floor(y);
  const wx = splineWeights(x - baseX), wy = splineWeights(y - baseY);
  const weights = new Map<number, number>();
  for (let groupIndex = 0; groupIndex < data.groups.length; groupIndex++) {
    const group = data.groups[groupIndex];
    const offsets = data.offsets.get(group.month)!;
    for (let fineRow = 0; fineRow < 4; fineRow++) {
      for (let fineColumn = 0; fineColumn < 4; fineColumn++) {
        const column = wrap(baseX + fineColumn - 1, 1440);
        const row = clamp(baseY + fineRow - 1, 0, 720);
        const value = offsets[row * 1440 + column];
        const shift = value === 255 ? 0 : (value - 128) / 10;
        const level = clamp((shift - SHIFT_MINIMUM) / SHIFT_STEP, 0, SHIFT_LEVELS - 1);
        const lower = Math.min(Math.floor(level), SHIFT_LEVELS - 2);
        const cellX = column / 4 - 0.5, cellY = row / 4 - 0.5;
        const cellBaseX = Math.floor(cellX), cellBaseY = Math.floor(cellY);
        const fractionX = cellX - cellBaseX, fractionY = cellY - cellBaseY;
        for (let cellRow = 0; cellRow < 2; cellRow++) {
          for (let cellColumn = 0; cellColumn < 2; cellColumn++) {
            const cell = cells.ordinals[clamp(cellBaseY + cellRow, 0, 179) * 360 + wrap(cellBaseX + cellColumn, 360)];
            const spatialWeight = wx[fineColumn] * wy[fineRow] * (cellColumn ? fractionX : 1 - fractionX) * (cellRow ? fractionY : 1 - fractionY) * group.weight;
            for (let upper = 0; upper < 2; upper++) {
              const weight = spatialWeight * (upper ? level - lower : 1 - (level - lower));
              if (weight === 0) continue;
              const key = (groupIndex * cells.count + cell) * SHIFT_LEVELS + lower + upper;
              weights.set(key, (weights.get(key) ?? 0) + weight);
            }
          }
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
  const result: CellJudgement = { score: 0, warmth: 0, evening: 0, rainFactor: 0, windFactor: 0, humidityFactor: 0, sunFactor: 0, niceDayShare: 0 };
  for (let sample = group.firstDay; sample < group.firstDay + group.dayCount; sample++) {
    const day = decodeDay(group.records.bytes, (cell * group.records.samples + sample) * 15);
    const judgement = judgeDay(day, taste, SHIFT_MINIMUM + level * SHIFT_STEP);
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
    const parts = { warmth: 0, evening: 0, rainFactor: 0, windFactor: 0, humidityFactor: 0, sunFactor: 0 };
    for (const contribution of contributions) {
      const key = (contribution.group * this.cells.count + contribution.cell) * SHIFT_LEVELS + contribution.level;
      let judgement = this.cachedCells.get(key);
      if (!judgement) {
        judgement = judgeCell(this.data.groups[contribution.group], contribution.cell, contribution.level, this.taste);
        this.cachedCells.set(key, judgement);
      }
      meanScore += judgement.score * contribution.weight;
      niceDayShare += judgement.niceDayShare * contribution.weight;
      for (const part of partNames) parts[part] += judgement[part] * contribution.weight;
    }
    return {
      meanScore, band: bandOf(meanScore), niceDayShare,
      niceDayCount: this.period.kind === 'forecast' || this.period.kind === 'forecast-day' ? niceDayShare * this.data.sampleDays : null,
      sampleDays: this.data.sampleDays, parts,
    };
  }
}

/** Samples the GPU's resolved fine grid with the displayed B-spline. */
export function readFineScore(values: Float32Array, latitude: number, longitude: number): number {
  if (!Number.isFinite(latitude) || !Number.isFinite(longitude) || latitude < -60 || latitude > 90) return NaN;
  const x = wrap((longitude + 180) * 4, 1440), y = (90 - latitude) * 4;
  const baseX = Math.floor(x), baseY = Math.floor(y);
  const wx = splineWeights(x - baseX), wy = splineWeights(y - baseY);
  let score = 0;
  for (let row = 0; row < 4; row++) {
    for (let column = 0; column < 4; column++) {
      score += values[clamp(baseY + row - 1, 0, 720) * 1440 + wrap(baseX + column - 1, 1440)] * wx[column] * wy[row];
    }
  }
  return score;
}
