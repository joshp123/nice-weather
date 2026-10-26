// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import type { Month, Period, PointVerdict } from './index';
import { periodWord } from './data';

/** Physical facts and unnamed point judgements stay on the Worker at the default taste. */
export class DefaultPlaces {
  private judgements = new Map<string, PointVerdict | null>();
  private pending = new Set<string>();
  private fineRequests = new Map<string, Promise<void>>();
  private notification?: number;
  private destroyed = false;

  constructor(private endpoint: string, private changed: () => void) {}

  loading(period: Period, latitude?: number, longitude?: number): boolean {
    if (latitude !== undefined && longitude !== undefined) return this.pending.has(`${periodWord(period)}:${latitude},${longitude}`);
    const prefix = `${periodWord(period)}:`;
    return [...this.pending].some(key => key.startsWith(prefix));
  }

  cached(latitude: number, longitude: number, period: Period): PointVerdict | null {
    return this.judgements.get(`${periodWord(period)}:${latitude},${longitude}`) ?? null;
  }

  judge(latitude: number, longitude: number, period: Period, fine = false): PointVerdict | null {
    if (this.destroyed || !Number.isFinite(latitude) || !Number.isFinite(longitude) || Math.abs(latitude) > 90) return null;
    const word = periodWord(period), key = `${word}:${latitude},${longitude}`;
    if (!this.judgements.has(key)) {
      this.judgements.set(key, null);
      this.pending.add(key);
      this.notify();
      const coordinate = `${latitude},${longitude}`;
      const pendingFine = fine ? this.fineRequests.get(coordinate) : undefined;
      if (pendingFine) {
        void pendingFine.finally(() => {
          if (this.judgements.get(key) === null) this.judgements.delete(key);
          this.pending.delete(key);
          this.notify();
        });
        return null;
      }
      const url = new URL(this.endpoint);
      const requestPeriod = period.kind === 'forecast-day' ? period.date ?? word : word;
      url.search = new URLSearchParams({lat: String(latitude), lon: String(longitude), period: requestPeriod}).toString();
      const request = fetch(url, { cache: 'no-cache' }).then(async response => {
        if (!response.ok) throw new Error(`Place weather unavailable: HTTP ${response.status}`);
        const place = await response.json() as {latitude: number; longitude: number; monthJudgements?: PointVerdict[]; judgement?: PointVerdict & {dayJudgements?: PointVerdict[]}};
        if (Math.round(place.latitude * 1e5) === Math.round(latitude * 1e5) && Math.round(place.longitude * 1e5) === Math.round(longitude * 1e5)) {
          this.judgements.set(key, place.judgement ?? null);
          place.judgement?.dayJudgements?.forEach((judgement, day) => {
            this.judgements.set(`${periodWord({kind: 'forecast-day', day})}:${latitude},${longitude}`, judgement);
          });
          place.monthJudgements?.forEach((judgement, month) => {
            this.judgements.set(`${periodWord({kind: 'month', month: (month + 1) as Month})}:${latitude},${longitude}`, judgement);
          });
        }
      }).catch(error => console.warn('Place weather unavailable', error)).finally(() => {
        this.pending.delete(key);
        if (fine) this.fineRequests.delete(coordinate);
        this.notify();
      });
      if (fine) this.fineRequests.set(coordinate, request);
    }
    return this.judgements.get(key) ?? null;
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
    this.judgements.clear();
    this.pending.clear();
  }
}
