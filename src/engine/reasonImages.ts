// Written by AI on 2026.10.26 by model GPT-6.
import { type WeatherData, FORECAST_DAYS, periodWord } from './data';
import { loadGreyscaleImage } from './greyscaleImage';
import type { Period } from './index';

export const IMAGE_REASONS = ['cold', 'hot', 'wet', 'windy', 'muggy', 'grey'] as const;
export type PixelReason = Readonly<{ reason: typeof IMAGE_REASONS[number]; severity: 0 | 1 | 2 | 3 }>;

export class ReasonImage {
  constructor(readonly bytes: Uint8Array, readonly path: string) {}

  pixel(index: number): PixelReason | null {
    const value = this.bytes[index];
    return value === undefined || value === 255 ? null : { reason: IMAGE_REASONS[value >> 2], severity: (value & 3) as PixelReason['severity'] };
  }

  /** A 1° weather cell's centre on the 0.25° image grid, matching the candidate reason lattice. */
  cell(row: number, column: number): PixelReason | null {
    if (!Number.isInteger(row) || !Number.isInteger(column) || row < 0 || row >= 180 || column < 0 || column >= 360) return null;
    return this.pixel((row * 4 + 2) * 1440 + column * 4 + 2);
  }

  point(latitude: number, longitude: number): PixelReason | null {
    if (!Number.isFinite(latitude) || !Number.isFinite(longitude) || latitude < -60 || latitude > 90) return null;
    const column = Math.round(((longitude + 180) % 360 + 360) % 360 * 4) % 1440;
    return this.pixel(Math.round((90 - latitude) * 4) * 1440 + column);
  }
}

declare module './index' {
  interface WeatherEngine {
    /** Current default-taste reasons, loaded on demand without day records; null if the period/taste changed while loading. */
    reasonImage(): Promise<ReasonImage | null>;
  }
}

const sourceImages = new WeakMap<WeatherData, Map<string, Promise<ReasonImage>>>();

/** These two internal transport calls deliberately reuse WeatherData's run pin and release routing.
 * Keep them here while the integration is restricted to this file and the small engine hook. */
export async function loadReasonImage(source: WeatherData, period: Period): Promise<ReasonImage> {
  let path: string;
  if (period.kind === 'forecast' || period.kind === 'forecast-day') {
    if (period.kind === 'forecast-day' && (!Number.isInteger(period.day) || period.day < 0 || period.day >= FORECAST_DAYS)) throw new RangeError(`Forecast day must be 0..${FORECAST_DAYS - 1}`);
    const index = await source['forecastIndex']();
    path = `forecast/${encodeURIComponent(index.run)}/reasons-typical-day-${period.kind === 'forecast' ? 'all' : `day-${String(period.day).padStart(2, '0')}`}.png`;
  } else path = `climate/reasons-typical-day-${periodWord(period)}.png`;
  let images = sourceImages.get(source);
  if (!images) sourceImages.set(source, images = new Map());
  let request = images.get(path);
  if (!request) {
    request = loadGreyscaleImage(source['url'](path)).then(bytes => {
      if (bytes.length !== 1440 * 721) throw new Error(`${path}: expected the 1440 × 721 fine grid`);
      return new ReasonImage(bytes, path);
    }).catch(error => { images.delete(path); throw error; });
    images.set(path, request);
  }
  return request;
}
