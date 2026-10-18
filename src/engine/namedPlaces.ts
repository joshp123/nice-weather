// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import { createNamedPlaceClient, namedPlaceNumbers } from '../../hosting/sites/named-place.mjs';
import type { Period, PointVerdict } from './index';

type NamedPlaceClient = ReturnType<typeof createNamedPlaceClient>;

/** One compact default-taste catalogue; arbitrary points keep the overview model. */
export class NamedPlaces {
  private client: NamedPlaceClient;
  private judgements = new Map<string, PointVerdict | null>();
  private requestedFacts = new Set<string>();
  private destroyed = false;

  get enabled(): boolean { return this.client.enabled; }

  constructor(dataUrl: string, private changed: () => void) {
    this.client = createNamedPlaceClient(new URL('/api/named', new URL(dataUrl, window.location.href)).href);
    void this.client.ready().then(() => {
      if (!this.destroyed) this.changed();
    }).catch(error => console.warn('Named place data unavailable; using the overview', error));
  }

  numbers(latitude: number, longitude: number, period: Period): PointVerdict | null {
    if (this.destroyed) return null;
    const row = this.client.find(latitude, longitude, period);
    if (!row) return null;
    const key = `${latitude},${longitude}:${JSON.stringify(period)}:${Boolean(row.months)}`;
    if (!this.judgements.has(key)) {
      this.judgements.set(key, namedPlaceNumbers(row, period));
      if (this.judgements.size > 24000) this.judgements.delete(this.judgements.keys().next().value!);
    }
    return this.judgements.get(key) ?? null;
  }

  judge(latitude: number, longitude: number, period: Period): PointVerdict | null {
    const row = this.client.find(latitude, longitude, period);
    if (!row || this.destroyed) return null;
    if (period.kind === 'forecast' || period.kind === 'forecast-day' || row.months) return this.numbers(latitude, longitude, period);
    const key = `${latitude},${longitude}`;
    if (!this.requestedFacts.has(key)) {
      this.requestedFacts.add(key);
      void this.client.load(latitude, longitude).then(() => {
        if (!this.destroyed) this.changed();
      }).catch(error => console.warn('Named place facts unavailable; using the overview', error));
    }
    return null;
  }

  destroy(): void {
    this.destroyed = true;
    this.judgements.clear();
    this.requestedFacts.clear();
  }
}
