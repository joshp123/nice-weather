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
  private active = 0;
  private queue: {key:string;load:() => void}[] = [];
  private queueTimer?: ReturnType<typeof setTimeout>;
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
      cached = { complete: false, judgements: new Map() };
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
