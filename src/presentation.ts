import type { PointWeather } from './data/types';

export function pointSourceLabel(source: PointWeather['source']): string {
  if (source.basis === 'forecast-window') return `${source.detailLabel} · about ${Math.round(source.resolutionKm)} km`;
  if (source.basis === 'climate') return `Typical month · ${source.referencePeriod} · ${source.providerLabel} · about ${Math.round(source.resolutionKm)} km`;
  const detail = source.temporalResolutionHours === 1 ? 'hourly detail' : `up to ${source.temporalResolutionHours}-hour detail`;
  return `${source.detailLabel} · ${source.providerLabel} · about ${Math.round(source.resolutionKm)} km · ${detail}`;
}
