// Written by AI on 2026.10.18 by model gpt-6.1-sol.
import { createFinePlaceClient, placeNumbers } from '../../hosting/sites/fine-place.mjs';
import type { Period, PointVerdict } from './index';
import type { Taste } from './taste';

type PlacePacket = Awaited<ReturnType<ReturnType<typeof createFinePlaceClient>['load']>>;
type CachedPlace = { latitude: number; longitude: number; packet?: PlacePacket; complete: boolean; judgements: Map<string, PointVerdict | null> };

/** Exact-pixel climate queries and loaded pixel areas for the map. */
export class FinePlaces {
  private loader: ReturnType<typeof createFinePlaceClient>;
  private places = new Map<string, CachedPlace>();
  private pending = 0;
  private active = 0;
  private queue: {key:string;load:() => void}[] = [];
  private queueTimer?: ReturnType<typeof setTimeout>;
  private notification?: number;
  private destroyed = false;
  private availability: 'checking' | 'inactive' | 'active' = 'checking';

  constructor(dataUrl: string, private changed: () => void) {
    const endpoint = new URL('/api/fine/replay', new URL(dataUrl, window.location.href)).href;
    this.loader = createFinePlaceClient(endpoint);
    void this.loader.enabled().then(enabled => {
      this.availability = enabled ? 'active' : 'inactive';
    }).catch(error => {
      this.availability = 'active';
      console.warn('Fine place availability could not be checked', error);
    }).finally(() => this.notify());
  }

  get enabled(): boolean { return this.availability === 'active'; }
  get loading(): boolean { return this.availability === 'checking' || this.pending > 0; }

  judge(latitude: number, longitude: number, period: Period, taste: Taste, background = false): PointVerdict | null {
    if (this.destroyed || this.availability !== 'active') return null;
    if (!Number.isFinite(latitude) || !Number.isFinite(longitude) || latitude < -90 || latitude > 90) return null;
    const key = `${latitude},${longitude}`;
    let cached = this.places.get(key);
    if (cached) {
      this.places.delete(key); this.places.set(key, cached);
      if (!background) {
        const queued = this.queue.findIndex(job => job.key === key);
        if (queued >= 0) this.queue.unshift(this.queue.splice(queued, 1)[0]);
      }
    }
    if (!cached) {
      cached = { latitude, longitude, complete: false, judgements: new Map() };
      this.places.set(key, cached);
      this.pending++;
      this.notify();
      const entry = cached;
      const load = () => {
        this.active++;
        void this.loader.load(latitude, longitude).then(packet => {
          entry.packet = packet;
        }).catch(error => console.warn('Exact-pixel place data unavailable', error))
          .finally(() => {
            entry.complete = true; this.pending--; this.active--;
            let retained = [...this.places.values()].filter(place => place.complete).length;
            for (const [address, place] of this.places) if (retained > 512 && place.complete) { this.places.delete(address); retained--; }
            this.drain(); this.notify();
          });
      };
      if (background) this.queue.push({key,load}); else this.queue.unshift({key,load});
      if (this.queueTimer === undefined) this.queueTimer = setTimeout(() => { this.queueTimer = undefined; this.drain(); }, 0);
    }
    if (!cached.complete || !cached.packet) return null;
    const judgementKey = `${JSON.stringify(period)}:${taste.warmShift},${taste.heatShift},${taste.eveningShift},${taste.humidityShift},${taste.windShift},${taste.rainScale}`;
    if (!cached.judgements.has(judgementKey)) {
      cached.judgements.set(judgementKey, placeNumbers(cached.packet, period, taste));
      if (cached.judgements.size > 24) cached.judgements.delete(cached.judgements.keys().next().value!);
    }
    return cached.judgements.get(judgementKey) ?? null;
  }

  /** Loaded native pixels are also the map's exact-data field, not only card values. */
  mapPixels(period: Period, taste: Taste): { row: number; column: number; score: number }[] {
    if (this.availability !== 'active') return [];
    const pixels = new Map<string, { row: number; column: number; score: number }>();
    for (const cached of [...this.places.values()]) {
      if (!cached.complete || !cached.packet) continue;
      const verdict = this.judge(cached.latitude, cached.longitude, period, taste);
      if (!verdict) continue;
      const { row, column } = cached.packet.pixel;
      pixels.set(`${row},${column}`, { row, column, score: verdict.meanScore });
    }
    return [...pixels.values()];
  }

  private drain(): void {
    while (!this.destroyed && this.active < 4 && this.queue.length) this.queue.shift()!.load();
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
    if (this.queueTimer !== undefined) clearTimeout(this.queueTimer);
    this.queue = [];
    this.places.clear();
  }
}
