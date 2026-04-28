import type { Basis, Manifest, Profile } from './types';

export async function loadManifest(): Promise<Manifest> {
  const startedAt = performance.now();
  const response = await fetch('/artifacts/manifest.json');
  if (!response.ok) {
    throw new Error(`Failed to load manifest: ${response.status}`);
  }
  const manifest = (await response.json()) as Manifest;
  validateManifest(manifest);
  console.info('nice-weather-finder:manifest-loaded', JSON.stringify({
    layers: manifest.layers.length,
    elapsedMs: Math.round(performance.now() - startedAt),
  }));
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
