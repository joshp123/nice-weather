// Written by AI on 2026.10.17 by model gpt-6.1-sol.
import type { CustomLayerInterface, Map as MapLibreMap } from 'maplibre-gl';
import type { BandName, DayJudgement, Taste } from './taste';
import { BrowserWeatherEngine } from './weatherEngine';
export { BANDS, DEFAULT_TASTE, SLIDERS, bandOf, tasteFromSliders } from './taste';
export type { Taste, BandName, DayJudgement } from './taste';

export type Month = 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12;
export type Season = 'Dec-Feb' | 'Mar-May' | 'Jun-Aug' | 'Sep-Nov';
export type Period =
  | Readonly<{ kind: 'forecast-day'; day: number }>
  | Readonly<{ kind: 'forecast' }>
  | Readonly<{ kind: 'month'; month: Month }>
  | Readonly<{ kind: 'season'; season: Season }>
  | Readonly<{ kind: 'months'; startMonth: Month; endMonth: Month }>
  | Readonly<{ kind: 'year' }>;
export type Point = Readonly<{ latitude: number; longitude: number }>;
/** Physical averages of the same shifted days used for the typical-day judgement. */
export type TypicalWeather = Readonly<{
  feelsInSunAt14C: number;
  feelsInShadeAt15C?: number;
  feelsInShadeAt21C: number;
  dewPointC: number;
  rainMmPerDay: number;
  /** Share in 0..1 of days with at least 1 mm rain. */
  wetDayShare: number;
  windKmh: number;
  /** Mean daily sunnyHours / daylightHours, in 0..1. */
  sunnyShare: number;
  daylightHours: number;
}>;
export type PointVerdict = Readonly<{
  /** Typical-day score; retained field name for app integration. */
  meanScore: number;
  band: BandName;
  /** 0..1, from the same representative day scores as meanScore. */
  niceDayShare: number;
  /** Expected count from representative days, weighted by real month lengths. */
  niceDayCount: number;
  perfectDayShare: number;
  awfulDayShare: number;
  perfectDayCount: number;
  awfulDayCount: number;
  totalDays: number;
  sampleDays: number;
  parts: Omit<DayJudgement, 'score'>;
  typical: TypicalWeather;
}>;
export type RedrawTiming = Readonly<{ milliseconds: number; period: Period; revision: number }>;
export type EngineOptions = Readonly<{
  /** Published weather files, normally /data/v6. */
  dataUrl?: string;
  /** Completed visible update, including clearing the default image if taste moves before records arrive. */
  onRedraw?: (timing: RedrawTiming) => void;
  /** Fires synchronously when current-period record loading changes. Image paint may precede completion. */
  onLoadingChange?: (loading: boolean) => void;
}>;
export interface WeatherEngine {
  /** Add once with map.addLayer(engine.layer, 'sea'), after the style loads. WebGL2 required. */
  readonly layer: CustomLayerInterface;
  /** Current period or exact-place data are loading. Overview-map records can finish before place data. */
  readonly loading: boolean;
  /** Cold period: paints its default score image, then loads records/offsets in the background. Cached records draw immediately without waiting for an image. Resolves after records and a frame; rejects on record failure. Latest call wins. */
  setPeriod(period: Period): Promise<void>;
  /** Reuses loaded data. Resolves after a frame for this taste; while records load a changed taste clears the default image and waits. Before the first period, stores taste and resolves immediately. */
  setTaste(taste: Taste): Promise<RedrawTiming>;
  /** Load the year for the dial without changing the visible map. */
  prepareYear(): Promise<void>;
  /** Synchronous; null until data arrive or where unavailable. With the fine-place manifest, climate queries load the exact 30 arc-second pixel and replay; no overview fallback. Other periods do not select a map period. */
  judgePoint(latitude: number, longitude: number, period?: Period): PointVerdict | null;
  /** Named-place scores in input order; NaN means pending or missing. Fine climate uses the same exact-pixel judgement as the card and the loaded native pixel area on the map. */
  scorePoints(points: readonly Point[], scope?: 'all'): Float32Array;
  /** Packed nice, perfect and awful day counts for each point. */
  countPoints(points: readonly Point[], scope?: 'all'): Float32Array;
  /** Releases GPU resources. Remove the layer first, or call this before removing the map. */
  destroy(): void;
}

/**
 * const engine = createWeatherEngine(map);
 * map.addLayer(engine.layer, 'sea');
 * await engine.setPeriod({ kind: 'month', month: 11 });
 * await engine.setTaste(DEFAULT_TASTE);
 * const malaga = engine.judgePoint(36.72, -4.42);
 *
 * Climate months count their real calendar days, represented by eight sampled days each.
 * An installed fine-place manifest selects exact-pixel climate replay for cards and named places.
 * Without it the map and card share the full-verdict B-spline field. Every grid point first uses the same temporal typical-day rule.
 */
export function createWeatherEngine(map: MapLibreMap, options: EngineOptions = {}): WeatherEngine {
  return new BrowserWeatherEngine(map, options);
}
