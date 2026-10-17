// Written by AI on 2026.10.16 by model claude opus 5.5.
// Score bands use the colours in design-tokens.css, shared by the map and interface.

export type Band = Readonly<{ name: string; minimumScore: number; colour: string }>;

// Low to high, one ramp: deep red for awful, brick for poor, slate for fair,
// then blue, green and gold for the good bands (Josh: "red/black should be
// awful and maybe golden for perfect, but pleasing colours"). Every band is
// visible, so "awful" never looks like missing data.
// Colours are #rrggbbaa; the alpha sets how strongly a band covers the land.
const design = getComputedStyle(document.documentElement);
export const bands: readonly Band[] = [
  { name: 'Awful', minimumScore: 0 },
  { name: 'Poor', minimumScore: 25 },
  { name: 'Fair', minimumScore: 40 },
  { name: 'Nice', minimumScore: 55 },
  { name: 'Beautiful', minimumScore: 70 },
  { name: 'Perfect', minimumScore: 85 },
].map(band => ({ ...band, colour: design.getPropertyValue(`--band-${band.name.toLowerCase()}`).trim() }));
