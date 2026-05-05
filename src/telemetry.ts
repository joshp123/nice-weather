type TelemetryValue = string | number | boolean | null | undefined;
type TelemetryFields = Record<string, TelemetryValue>;

const PREFIX = 'nice-weather-finder';

export function telemetry(event: string, fields: TelemetryFields = {}) {
  console.info(`${PREFIX}:${event}`, JSON.stringify(cleanFields(fields)));
}

export function telemetryError(event: string, fields: TelemetryFields = {}) {
  console.error(`${PREFIX}:${event}`, JSON.stringify(cleanFields(fields)));
}

export function installBrowserTelemetry() {
  const state = window as typeof window & { __niceWeatherTelemetryInstalled?: boolean };
  if (state.__niceWeatherTelemetryInstalled) return;
  state.__niceWeatherTelemetryInstalled = true;

  telemetry('app-started', { href: window.location.href });

  window.addEventListener('error', (event) => {
    telemetryError('window-error', {
      message: event.message,
      source: event.filename,
      line: event.lineno,
      column: event.colno,
    });
  });

  window.addEventListener('unhandledrejection', (event) => {
    telemetryError('unhandled-rejection', {
      message: event.reason instanceof Error ? event.reason.message : String(event.reason),
    });
  });

  document.addEventListener('visibilitychange', () => {
    telemetry('visibility-changed', { state: document.visibilityState });
  });

  installResourceTelemetry();
}

function installResourceTelemetry() {
  if (!('PerformanceObserver' in window)) {
    telemetry('resource-observer-unavailable');
    return;
  }

  const totals = {
    forecastTile: metric(),
    manifest: metric(),
    appAsset: metric(),
    osmTile: metric(),
  };
  const seen = new Set<string>();
  let flushTimer: number | undefined;

  const observer = new PerformanceObserver((list) => {
    for (const rawEntry of list.getEntries()) {
      const entry = rawEntry as PerformanceResourceTiming;
      const category = resourceCategory(entry.name);
      if (!category) continue;

      const key = `${entry.name}:${entry.startTime}`;
      if (seen.has(key)) continue;
      seen.add(key);

      const target = totals[category];
      target.count += 1;
      target.transferBytes += Math.max(0, entry.transferSize || entry.decodedBodySize || 0);
      target.slowestMs = Math.max(target.slowestMs, entry.duration);

      if (flushTimer === undefined) {
        flushTimer = window.setTimeout(() => {
          flushTimer = undefined;
          telemetry('resource-summary', {
            forecastTileCount: totals.forecastTile.count,
            forecastTileKB: toKB(totals.forecastTile.transferBytes),
            forecastTileSlowestMs: Math.round(totals.forecastTile.slowestMs),
            manifestCount: totals.manifest.count,
            manifestKB: toKB(totals.manifest.transferBytes),
            appAssetCount: totals.appAsset.count,
            appAssetKB: toKB(totals.appAsset.transferBytes),
            osmTileCount: totals.osmTile.count,
            osmTileKB: totals.osmTile.transferBytes > 0 ? toKB(totals.osmTile.transferBytes) : null,
          });
        }, 400);
      }
    }
  });

  observer.observe({ type: 'resource', buffered: true });
}

function metric() {
  return { count: 0, transferBytes: 0, slowestMs: 0 };
}

function resourceCategory(url: string): 'forecastTile' | 'manifest' | 'appAsset' | 'osmTile' | null {
  if (url.includes('/artifacts/tiles/forecast/')) return 'forecastTile';
  if (url.endsWith('/artifacts/manifest.json')) return 'manifest';
  if (url.includes('/assets/')) return 'appAsset';
  if (url.includes('tile.openstreetmap.org')) return 'osmTile';
  return null;
}

function cleanFields(fields: TelemetryFields) {
  return Object.fromEntries(
    Object.entries({
      at: new Date().toISOString(),
      ...fields,
    }).filter(([, value]) => value !== undefined),
  );
}

function toKB(bytes: number) {
  return Math.round(bytes / 1024);
}
