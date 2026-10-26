// Written by AI (GPT-6.1 Sol), 2026-10-19.
export type BandName = 'Awful'|'Poor'|'Fair'|'Nice'|'Beautiful'|'Perfect';
export const AWFUL_MAXIMUM: number;
export const SCORE_BANDS: readonly Readonly<{name:BandName;minimum:number}>[];
export const SCORE_THRESHOLDS: readonly number[];
export function bandOf(score:number):BandName;
