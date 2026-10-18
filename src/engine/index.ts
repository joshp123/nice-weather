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
  /** Present when a named place's temperature detail comes from its 1 km pixel. */
  resolutionArcSeconds?: number;
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
  /** Reports current-period map or selected-place loading. Default views do not load day records. */
  onLoadingChange?: (loading: boolean) => void;
}>;
export interface WeatherEngine {
  /** Add once with map.addLayer(engine.layer, 'sea'), after the style loads. WebGL2 required. */
  readonly layer: CustomLayerInterface;
  /** Current-period map or requested Worker place facts are loading. */
  readonly loading: boolean;
  /** Default taste loads only its precomputed map image; other tastes load the needed records. Resolves after a completed map frame. Latest call wins. */
  setPeriod(period: Period): Promise<void>;
  /** Loads records on demand for a changed taste, then resolves after its frame. Resetting to default uses the image. Before the first period, stores taste and resolves immediately. */
  setTaste(taste: Taste): Promise<RedrawTiming>;
  /** Default dials use named data; a changed taste loads year records without selecting a period. */
  prepareYear(): Promise<void>;
  /** Prefetch current-period records without changing the visible default map or taste. */
  prepareTaste(): Promise<void>;
  /** Synchronous; default named climate cards load compact facts, other current-period cards load Worker judgements. Named numbers supply other-period dials. Changed tastes use loaded overview records. Returns null while pending or unavailable. */
  judgePoint(latitude: number, longitude: number, period?: Period): PointVerdict | null;
  /** A place's compact named numbers or Worker card facts have not arrived yet. */
  pointLoading(latitude: number, longitude: number, period?: Period): boolean;
  /** Scores in input order; named default-taste values are the same as the card. Other points and tastes use the 25 km field; NaN means pending or missing. */
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
 * A named-place catalogue selects precomputed exact-pixel climate days at the default taste.
 * The map, unnamed points and changed tastes use the full-verdict B-spline field. Every source uses the same temporal typical-day rule.
 */
export function createWeatherEngine(map: MapLibreMap, options: EngineOptions = {}): WeatherEngine {
  return new BrowserWeatherEngine(map, options);
}
