import type { LegendCategory, WeatherManifest } from './data/types';

export function ratingBandLabel(category: LegendCategory): string {
  return `${category.min}\u2013${category.max}`;
}

export function forecastDetailNote(manifest: WeatherManifest): string {
  const detailed = manifest.forecast.days.filter((day) => day.kind === 'detailed');
  const outlook = manifest.forecast.days.filter((day) => day.kind === 'outlook');
  const detailedResolution = detailed[0]?.resolutionKm;
  const outlookResolution = outlook[0]?.resolutionKm;

  if (detailed.length && outlook.length && detailedResolution && outlookResolution) {
    return `The first ${detailed.length} days use a detailed forecast at about ${Math.round(detailedResolution)} km. The later outlook is about ${Math.round(outlookResolution)} km.`;
  }
  return `Forecast detail is about ${Math.round(manifest.forecast.window.resolutionKm)} km.`;
}
