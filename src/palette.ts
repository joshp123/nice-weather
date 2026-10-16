// Written by AI on 2026.10.16 by model claude opus 5.5.
// The one place that turns a 0-100 score into a word and a colour.
// Thresholds match weather.Rating in internal/weather/score.go.

export type Band = Readonly<{ name: string; minimumScore: number; colour: string }>;

// Low to high, one ramp: deep red for awful, brick for poor, slate for fair,
// then blue, green and gold for the good bands (Josh: "red/black should be
// awful and maybe golden for perfect, but pleasing colours"). Every band is
// visible, so "awful" never looks like missing data.
// Colours are #rrggbbaa; the alpha sets how strongly a band covers the land.
export const bands: readonly Band[] = [
  { name: 'Awful', minimumScore: 0, colour: '#4a122299' },
  { name: 'Poor', minimumScore: 25, colour: '#9c4040b3' },
  { name: 'Fair', minimumScore: 40, colour: '#6f7b8a99' },
  { name: 'Nice', minimumScore: 55, colour: '#3b9edbdc' },
  { name: 'Beautiful', minimumScore: 70, colour: '#55c27adc' },
  { name: 'Perfect', minimumScore: 85, colour: '#f5c542e6' },
];

export function bandForScore(score: number): Band {
  for (let index = bands.length - 1; index >= 0; index--) {
    if (score >= bands[index].minimumScore) return bands[index];
  }
  return bands[0];
}

export const NICE_DAY_MINIMUM_SCORE = 55;
