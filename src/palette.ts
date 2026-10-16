// Written by AI on 2026.10.16 by model claude opus 5.5.
// The one place that turns a 0-100 score into a word and a colour.
// Thresholds match weather.Rating in internal/weather/score.go.

export type Band = Readonly<{ name: string; minimumScore: number; colour: string }>;

// Low to high. Brightness carries the meaning: bad weather darkens the land,
// "Fair" leaves the plain land showing, and good weather lights it up, up to
// gold for Perfect. Good places stand out; bad places recede (Ryan Hall's principle).
// Colours are #rrggbbaa; the alpha sets how strongly a band covers the land.
const palettes = {
  dusk: [
    { name: 'Awful', minimumScore: 0, colour: '#170d118c' },
    { name: 'Poor', minimumScore: 25, colour: '#5a2a3aa8' },
    { name: 'Fair', minimumScore: 40, colour: 'transparent' },
    { name: 'Nice', minimumScore: 55, colour: '#3b9edbd6' },
    { name: 'Beautiful', minimumScore: 70, colour: '#55c27ad6' },
    { name: 'Perfect', minimumScore: 85, colour: '#f5c542e0' },
  ],
  ember: [
    { name: 'Awful', minimumScore: 0, colour: '#2a0b10d0' },
    { name: 'Poor', minimumScore: 25, colour: '#7a2630b0' },
    { name: 'Fair', minimumScore: 40, colour: 'transparent' },
    { name: 'Nice', minimumScore: 55, colour: '#6fb7e8d6' },
    { name: 'Beautiful', minimumScore: 70, colour: '#7ccf5bd6' },
    { name: 'Perfect', minimumScore: 85, colour: '#ffd23fe0' },
  ],
} satisfies Record<string, Band[]>;

export type PaletteName = keyof typeof palettes;

const requestedPalette = new URLSearchParams(location.search).get('palette');
export const bands: readonly Band[] =
  palettes[(requestedPalette && requestedPalette in palettes ? requestedPalette : 'dusk') as PaletteName];

export function bandForScore(score: number): Band {
  for (let index = bands.length - 1; index >= 0; index--) {
    if (score >= bands[index].minimumScore) return bands[index];
  }
  return bands[0];
}

export const NICE_DAY_MINIMUM_SCORE = 55;
