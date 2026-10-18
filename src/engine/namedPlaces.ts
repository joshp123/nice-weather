// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import { createNamedPlaceClient, namedPlaceNumbers } from '../../hosting/sites/named-place.mjs';
import type { Period, Point, PointVerdict } from './index';

type NamedPlaceClient = ReturnType<typeof createNamedPlaceClient>;

/** Default-taste days for the places in view; arbitrary points keep the overview model. */
export class NamedPlaces {
  private client: NamedPlaceClient;
  private judgements = new Map<string, PointVerdict | null>();
  private requestedFacts = new Set<string>();
  private completedFacts = new Set<string>();
  private requestedDays = new Set<string>();
  private configurationReady = false;
  private destroyed = false;

  get enabled(): boolean { return this.client.enabled; }

  constructor(dataUrl: string, private changed: () => void) {
    this.client = createNamedPlaceClient(new URL('/api/named', new URL(dataUrl, window.location.href)).href);
    void this.client.ready().catch(error => console.warn('Named place data unavailable; using the overview', error)).finally(() => {
      this.configurationReady = true;
      if (!this.destroyed) this.changed();
    });
  }

  prepare(points: readonly Point[], period: Period): void {
    if (this.destroyed) return;
    const kind = period.kind === 'forecast' || period.kind === 'forecast-day' ? 'forecast' : 'climate';
    const missing = points.filter(point => {
      const key = `${point.latitude},${point.longitude}:${kind}`;
      if (this.requestedDays.has(key)) return false;
      this.requestedDays.add(key);
      return true;
    });
    if (!missing.length) return;
    void this.client.ensure(missing, period).catch(error => console.warn('Named places in view unavailable; using the overview', error)).finally(() => {
      if (!this.destroyed) this.changed();
    });
  }

  pending(latitude: number, longitude: number, period: Period): boolean {
    return !this.configurationReady || this.client.pending(latitude, longitude, period);
  }

  pendingJudgement(latitude: number, longitude: number, period: Period): boolean {
    const key = `${latitude},${longitude}:${period.kind === 'forecast' || period.kind === 'forecast-day' ? 'forecast' : 'climate'}`;
    return this.pending(latitude, longitude, period) || this.enabled && this.requestedFacts.has(key) && !this.completedFacts.has(key);
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
    if (this.destroyed) return null;
    const key = `${latitude},${longitude}:${period.kind === 'forecast' || period.kind === 'forecast-day' ? 'forecast' : 'climate'}`;
    if (!this.requestedFacts.has(key)) {
      this.requestedFacts.add(key);
      void this.client.load(latitude, longitude, period).catch(error => console.warn('Named place facts unavailable; using the overview', error)).finally(() => {
        this.completedFacts.add(key);
        if (!this.destroyed) this.changed();
      });
    }
    const row = this.client.find(latitude, longitude, period);
    if (!row || period.kind !== 'forecast' && period.kind !== 'forecast-day' && !row.months) return null;
    return this.numbers(latitude, longitude, period);
  }

  destroy(): void {
    this.destroyed = true;
    this.judgements.clear();
    this.requestedFacts.clear();
    this.completedFacts.clear();
    this.requestedDays.clear();
  }
}
