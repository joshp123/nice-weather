import type { LegendCategory, WeatherManifest } from './data/types';

// Display copy only. Scoring remains in internal/weather/score.go; these
// values are never used to calculate or validate a browser-side score.
export const PROFILE_WEIGHT_NOTES = {
  nice: 'Comfort 45% · Dry 25% · Bright 20% · Calm 10%',
  cycling: 'Comfort 35% · Dry 30% · Bright 10% · Calm 25%',
} as const;

export const DRY_SCORE_NOTE = 'Dry uses whichever is worse: rain amount or wet daytime duration.';

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
