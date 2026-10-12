import type { LegendCategory, PaletteStop } from './data/types';

export type WeatherPresentation = Readonly<{
  legend: readonly LegendCategory[];
  palette: readonly PaletteStop[];
}>;

// Scores are immutable data. This small presentation contract gives those
// scores one stable meaning without depending on an unrelated legacy release.
export const WEATHER_PRESENTATION: WeatherPresentation = Object.freeze({
  legend: Object.freeze([
    { label: 'Awful', color: '#263244', min: 0, max: 24 },
    { label: 'Poor', color: '#426580', min: 25, max: 39 },
    { label: 'Mixed', color: '#56A8DD', min: 40, max: 54 },
    { label: 'Good', color: '#9BD6F3', min: 55, max: 69 },
    { label: 'Very good', color: '#83C878', min: 70, max: 84 },
    { label: 'Excellent', color: '#F2D55E', min: 85, max: 100 },
  ]),
  palette: Object.freeze([
    { score: 0, color: '#263244' }, { score: 24, color: '#263244' },
    { score: 25, color: '#426580' }, { score: 39, color: '#426580' },
    { score: 40, color: '#56A8DD' }, { score: 54, color: '#56A8DD' },
    { score: 55, color: '#9BD6F3' }, { score: 69, color: '#9BD6F3' },
    { score: 70, color: '#83C878' }, { score: 84, color: '#83C878' },
    { score: 85, color: '#F2D55E' }, { score: 100, color: '#F2D55E' },
  ]),
});
