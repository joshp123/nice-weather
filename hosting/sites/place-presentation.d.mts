// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import type { TypicalWeather } from '../../src/engine';
export type PlaceReason = Readonly<{ key: string; label: string; figure: number; unit: string; value: string; detail: string }>;
export function humidityWord(dewPointC: number): string;
export function placeReasons(typical: TypicalWeather, singleDay?: boolean): PlaceReason[];
