type TelemetryDetail = Record<string, string | number | boolean | undefined>;
type TelemetryEvent = TelemetryDetail & { event: string };

export function telemetry(event: string, detail: TelemetryDetail = {}) {
  const payload: TelemetryEvent = { event, ...detail };
  const telemetryWindow = window as Window & { __niceWeatherTelemetry?: TelemetryEvent[] };
  const buffer = telemetryWindow.__niceWeatherTelemetry ??= [];
  buffer.push(payload);
  if (buffer.length > 250) buffer.splice(0, buffer.length - 250);
  console.info(`nice-weather:${event}`, detail);
  window.dispatchEvent(new CustomEvent('nice-weather:telemetry', { detail: payload }));
}

export function reportResourceSummary() {
  const resources = performance.getEntriesByType('resource') as PerformanceResourceTiming[];
  const weatherImages = resources.filter((entry) => /\/images\/(forecast|climate)\//.test(entry.name));
  const baseMap = resources.filter((entry) => entry.name.includes('openfreemap'));
  telemetry('resource-summary', {
    requests: resources.length,
    transferBytes: sum(resources, 'transferSize'),
    weatherImages: weatherImages.length,
    weatherBytes: sum(weatherImages, 'transferSize'),
    baseMapRequests: baseMap.length,
    baseMapBytes: sum(baseMap, 'transferSize'),
  });
}

function sum(entries: PerformanceResourceTiming[], field: 'transferSize'): number {
  return entries.reduce((total, entry) => total + (entry[field] || 0), 0);
}
