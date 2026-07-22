import type { LegendCategory, PaletteStop } from './data/types';

export type WeatherPresentation = Readonly<{
  legend: readonly LegendCategory[];
  palette: readonly PaletteStop[];
}>;

// Scores are immutable data. This small presentation contract gives those
// scores one stable meaning without depending on an unrelated legacy release.
export const WEATHER_PRESENTATION: WeatherPresentation = Object.freeze({
  legend: Object.freeze([
    { label: 'Awful', color: '#22385C', min: 0, max: 24 },
    { label: 'Poor', color: '#3A5C78', min: 25, max: 39 },
    { label: 'Mixed', color: '#5F7E7F', min: 40, max: 54 },
    { label: 'Good', color: '#859D80', min: 55, max: 69 },
    { label: 'Very good', color: '#AFB87C', min: 70, max: 84 },
    { label: 'Excellent', color: '#E4C46B', min: 85, max: 100 },
  ]),
  palette: Object.freeze([
    { score: 0, color: '#22385C' }, { score: 24, color: '#365270' },
    { score: 25, color: '#3A5C78' }, { score: 39, color: '#537484' },
    { score: 40, color: '#5F7E7F' }, { score: 54, color: '#849082' },
    { score: 55, color: '#859D80' }, { score: 69, color: '#9DB07F' },
    { score: 70, color: '#AFB87C' }, { score: 84, color: '#D0C375' },
    { score: 85, color: '#E4C46B' }, { score: 100, color: '#F6C867' },
  ]),
});
