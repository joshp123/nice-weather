import { describe, expect, it } from 'vitest';
import type { WeatherManifest } from './data/types';
import { DRY_SCORE_NOTE, forecastDetailNote, PROFILE_WEIGHT_NOTES, ratingBandLabel } from './scoreGuide';

describe('score guide copy', () => {
  it('states both profiles exact component weights and the dry rule', () => {
    expect(PROFILE_WEIGHT_NOTES).toEqual({
      nice: 'Comfort 45% · Dry 25% · Clear sky 20% · Calm 10%',
      cycling: 'Comfort 35% · Dry 30% · Clear sky 10% · Calm 25%',
    });
    expect(DRY_SCORE_NOTE).toBe('Dry uses whichever is worse: rain amount or wet daytime duration.');
  });

  it('prints inclusive rating thresholds', () => {
    expect(ratingBandLabel({ label: 'Poor', color: '#000000', min: 15, max: 29 })).toBe('15\u201329');
  });

  it('states the honest detailed and outlook split from the manifest', () => {
    const manifest = {
      forecast: {
        window: { resolutionKm: 28 },
        days: [
          ...Array.from({ length: 5 }, () => ({ kind: 'detailed', resolutionKm: 7 })),
          ...Array.from({ length: 9 }, () => ({ kind: 'outlook', resolutionKm: 28 })),
        ],
      },
    } as unknown as WeatherManifest;
    expect(forecastDetailNote(manifest)).toBe('The first 5 days use a detailed forecast at about 7 km. The later outlook is about 28 km.');
  });

  it('falls back to the window resolution when the forecast has one detail level', () => {
    const manifest = { forecast: { window: { resolutionKm: 11 }, days: [] } } as unknown as WeatherManifest;
    expect(forecastDetailNote(manifest)).toBe('Forecast detail is about 11 km.');
  });
});
