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
  | Readonly<{ kind: 'year' }>;
export type Point = Readonly<{ latitude: number; longitude: number }>;
/** Physical averages of the same shifted, spatially interpolated days as meanScore. */
export type TypicalWeather = Readonly<{
  feelsInSunAt14C: number;
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
  meanScore: number;
  band: BandName;
  /** 0..1, from the same representative days and interpolation as meanScore. */
  niceDayShare: number;
  /** Forecast: expected count out of 1 or 14. Climate: null; samples are representative, not a calendar. */
  niceDayCount: number | null;
  sampleDays: number;
  parts: Omit<DayJudgement, 'score'>;
  /** Always supplied by this engine; optional for existing interface consumers. */
  typical?: TypicalWeather;
}>;
export type RedrawTiming = Readonly<{ milliseconds: number; period: Period; revision: number }>;
export type EngineOptions = Readonly<{
  /** Defaults to /data/v6. Override only for isolated synthetic-data debugging. */
  dataUrl?: string;
  layerId?: string;
  onRedraw?: (timing: RedrawTiming) => void;
}>;
export interface WeatherEngine {
  /** Add once with map.addLayer(engine.layer, 'sea'), after the style loads. WebGL2 required. */
  readonly layer: CustomLayerInterface;
  /** Fetches only this period's records and offsets; resolves after its first completed GPU frame. Latest call wins. */
  setPeriod(period: Period): Promise<void>;
  /** Reuses loaded data. Resolves after GPU completion, including the next MapLibre frame. */
  setTaste(taste: Taste): Promise<RedrawTiming>;
  /** Synchronous; optional period uses already-loaded data without selecting it or fetching. null if unavailable or south of 60°S. Longitude wraps; water uses nearest land. */
  judgePoint(latitude: number, longitude: number, period?: Period): PointVerdict | null;
  /** Same scores as judgePoint within GPU rounding, in input order; NaN means missing. After setTaste/setPeriod resolves, this cheaply reads and caches the GPU grid for city labels. */
  scorePoints(points: readonly Point[]): Float32Array;
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
 * Climate months have equal weight (eight real representative days each).
 * Fine-grid scores interpolate the neighbouring cells and felt-shift levels;
 * the card uses those identical weights, including the smooth B-spline filter.
 */
export function createWeatherEngine(map: MapLibreMap, options: EngineOptions = {}): WeatherEngine {
  return new BrowserWeatherEngine(map, options);
}
