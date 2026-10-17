// Written by AI on 2026.10.18 by model gpt-6.1-sol.
import { createFinePlaceLoader, placeNumbers } from '../../hosting/sites/fine-place.mjs';
import type { Period, PointVerdict } from './index';
import type { Taste } from './taste';

type PlacePacket = Awaited<ReturnType<ReturnType<typeof createFinePlaceLoader>['load']>>;
type CachedPlace = { packet?: PlacePacket; complete: boolean; judgements: Map<string, PointVerdict | null> };

/** Exact-pixel climate queries; the overview map keeps its existing data source. */
export class FinePlaces {
  private loader: ReturnType<typeof createFinePlaceLoader>;
  private places = new Map<string, CachedPlace>();
  private pending = 0;
  private notification?: number;
  private destroyed = false;
  private availability: 'checking' | 'inactive' | 'active' = 'checking';

  constructor(dataUrl: string, private changed: () => void) {
    const dataRoot = dataUrl.replace(/\/[^/]*$/, '');
    const readBytes = async (key: string): Promise<Uint8Array | null> => {
      const path = key.startsWith('v6/') ? `${dataUrl}/${key.slice(3)}` : `${dataRoot}/${key}`;
      const response = await fetch(path);
      if (response.status === 404) return null;
      if (!response.ok) throw new Error(`Fine place data: HTTP ${response.status}`);
      return new Uint8Array(await response.arrayBuffer());
    };
    this.loader = createFinePlaceLoader(readBytes);
    void this.loader.enabled().then(enabled => {
      this.availability = enabled ? 'active' : 'inactive';
    }).catch(error => {
      this.availability = 'active';
      console.warn('Fine place availability could not be checked', error);
    }).finally(() => this.notify());
  }

  get enabled(): boolean { return this.availability !== 'inactive'; }
  get loading(): boolean { return this.availability === 'checking' || this.pending > 0; }

  judge(latitude: number, longitude: number, period: Period, taste: Taste): PointVerdict | null {
    if (this.destroyed || this.availability !== 'active') return null;
    if (!Number.isFinite(latitude) || !Number.isFinite(longitude) || latitude < -90 || latitude > 90) return null;
    const key = `${latitude},${longitude}`;
    let cached = this.places.get(key);
    if (!cached) {
      cached = { complete: false, judgements: new Map() };
      this.places.set(key, cached);
      this.pending++;
      this.notify();
      const entry = cached;
      void this.loader.load(latitude, longitude).then(packet => {
        entry.packet = packet;
      }).catch(error => console.warn('Exact-pixel place data unavailable', { latitude, longitude, error }))
        .finally(() => { entry.complete = true; this.pending--; this.notify(); });
    }
    if (!cached.complete || !cached.packet) return null;
    const judgementKey = `${JSON.stringify(period)}:${taste.warmShift},${taste.heatShift},${taste.eveningShift},${taste.humidityShift},${taste.windShift},${taste.rainScale}`;
    if (!cached.judgements.has(judgementKey)) cached.judgements.set(judgementKey, placeNumbers(cached.packet, period, taste));
    return cached.judgements.get(judgementKey) ?? null;
  }

  private notify(): void {
    if (this.destroyed || this.notification !== undefined) return;
    this.notification = requestAnimationFrame(() => {
      this.notification = undefined;
      if (!this.destroyed) this.changed();
    });
  }

  destroy(): void {
    this.destroyed = true;
    if (this.notification !== undefined) cancelAnimationFrame(this.notification);
    this.places.clear();
  }
}
