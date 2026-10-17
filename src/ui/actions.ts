// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// Everything a visitor can do. main.ts implements these; the components only call them.
import type { Period } from '../engine';
import type { SearchResult } from './api';
import type { SliderId } from './state';

export type Actions = Readonly<{
  setPeriod(period: Period): void;
  /** While a slider is dragged: the map redraws, the link waits. */
  setSlider(id: SliderId, value: number): void;
  /** When the drag ends: the link updates. */
  commitSliders(): void;
  resetSliders(): void;
  selectPoint(latitude: number, longitude: number): void;
  selectSearchResult(result: SearchResult): void;
  locate(): void;
  closePlace(): void;
  toggleTaste(): void;
  toggleShare(): void;
}>;
