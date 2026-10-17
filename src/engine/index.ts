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
  /** Current period records are loading. Every judgePoint call returns null while true, including cached-period queries. */
  readonly loading: boolean;
  /** Cold period: paints its default score image, then loads records/offsets in the background. Cached records draw immediately without waiting for an image. Resolves after records and a frame; rejects on record failure. Latest call wins. */
  setPeriod(period: Period): Promise<void>;
  /** Reuses loaded data. Resolves after a frame for this taste; while records load a changed taste clears the default image and waits. Before the first period, stores taste and resolves immediately. */
  setTaste(taste: Taste): Promise<RedrawTiming>;
  /** Synchronous; null while loading, if unavailable, or if no fine land point within two grid steps. Optional period uses loaded files without fetching or selecting. Longitude wraps; latitude range −60..90°. */
  judgePoint(latitude: number, longitude: number, period?: Period): PointVerdict | null;
  /** Nearest-land fine-point scores in input order; NaN means missing. Default taste reads the score image, including while records load (integer precision). Other tastes cache one GPU readback per redraw. */
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
 * Place scores use the nearest fine land point's own offset, interpolating
 * the neighbouring cells and felt-shift levels. Only the map applies the final B-spline.
 */
export function createWeatherEngine(map: MapLibreMap, options: EngineOptions = {}): WeatherEngine {
  return new BrowserWeatherEngine(map, options);
}
