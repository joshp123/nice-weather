import type { Basis, ForecastHorizon, Manifest, Profile } from './types';
import { telemetry } from '../telemetry';

export async function loadManifest(): Promise<Manifest> {
  const startedAt = performance.now();
  const response = await fetch('/artifacts/manifest.json');
  if (!response.ok) {
    throw new Error(`Failed to load manifest: ${response.status}`);
  }
  const manifest = (await response.json()) as Manifest;
  validateManifest(manifest);
  telemetry('manifest-loaded', {
    layers: manifest.layers.length,
    horizons: manifest.sources.forecast.horizons.length,
    places: manifest.anchors.length,
    elapsedMs: Math.round(performance.now() - startedAt),
  });
  return manifest;
}

export function findLayer(manifest: Manifest, basis: Basis, profile: Profile, horizon: string) {
  const layer = manifest.layers.find(
    (candidate) =>
      candidate.basis === basis && candidate.profile === profile && candidate.horizon === horizon,
  );
  if (!layer) {
    throw new Error(`Missing layer for ${basis}/${profile}/${horizon}`);
  }
  return layer;
}

export function horizonsFor(manifest: Manifest, basis: Basis): string[] {
  return basis === 'forecast' ? manifest.sources.forecast.horizons : (manifest.sources.climatology?.horizons ?? []);
}

export function forecastHorizonDetail(manifest: Manifest, horizon: string): ForecastHorizon | undefined {
  return manifest.sources.forecast.horizonDetails?.find((detail) => detail.id === horizon);
}

export type ForecastFreshness = {
  state: 'fresh' | 'stale' | 'unknown';
  ageHours?: number;
  staleAfterHours?: number;
};

export function forecastFreshness(manifest: Manifest, now = new Date()): ForecastFreshness {
  const runStartedAt = manifest.sources.forecast.runStartedAt;
  if (!runStartedAt) return { state: 'unknown' };
  const runTime = new Date(runStartedAt);
  if (Number.isNaN(runTime.getTime())) return { state: 'unknown' };
  const ageHours = Math.max(0, (now.getTime() - runTime.getTime()) / 3_600_000);
  const staleAfterHours = manifest.sources.forecast.staleAfterHours ?? 12;
  return {
    state: ageHours > staleAfterHours ? 'stale' : 'fresh',
    ageHours,
    staleAfterHours,
  };
}

export function validHorizon(horizons: string[], horizon: string): string | undefined {
  return horizons.includes(horizon) ? horizon : horizons[0];
}

function validateManifest(manifest: Manifest) {
  if (manifest.version !== 1) {
    throw new Error('Unsupported manifest version');
  }
  if (!Array.isArray(manifest.layers) || manifest.layers.length === 0) {
    throw new Error('Manifest has no layers');
  }
}
