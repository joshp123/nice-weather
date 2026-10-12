type TelemetryDetail = Record<string, string | number | boolean | undefined>;
type TelemetryEvent = TelemetryDetail & { event: string };

export function telemetry(event: string, detail: TelemetryDetail = {}) {
  const payload: TelemetryEvent = { event, ...detail };
  const telemetryWindow = window as Window & { __niceWeatherTelemetry?: TelemetryEvent[] };
  const buffer = telemetryWindow.__niceWeatherTelemetry ??= [];
  buffer.push(payload);
  if (buffer.length > 250) buffer.splice(0, buffer.length - 250);
  console.info(`nice-weather:${event} ${JSON.stringify(detail)}`);
}

export function reportResourceSummary() {
  const resources = performance.getEntriesByType('resource') as PerformanceResourceTiming[];
  const weatherTiles = resources.filter((entry) => /\/(forecast|climate)\/.*\.nws(?:\?|$)/.test(entry.name));
  const pointBlocks = resources.filter((entry) => /\/(forecast|climate)\/.*\.(?:nwt|nwr|nwc|nwd)(?:\?|$)/.test(entry.name));
  const baseMap = resources.filter((entry) => entry.name.includes('openfreemap'));
  telemetry('resource-summary', {
    requests: resources.length,
    transferBytes: sum(resources, 'transferSize'),
    weatherTiles: weatherTiles.length,
    weatherBytes: sum(weatherTiles, 'transferSize'),
    weatherDecodedBytes: sum(weatherTiles, 'decodedBodySize'),
    pointRequests: pointBlocks.length,
    pointBytes: sum(pointBlocks, 'transferSize'),
    baseMapRequests: baseMap.length,
    baseMapBytes: sum(baseMap, 'transferSize'),
  });
}

function sum(entries: PerformanceResourceTiming[], field: 'transferSize' | 'decodedBodySize'): number {
  return entries.reduce((total, entry) => total + (entry[field] || 0), 0);
}
