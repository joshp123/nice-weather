// Written by AI on 2026.10.16 by model claude opus 5.5; colour helpers added on 2026.10.18.
// Score bands use the colours in design-tokens.css, shared by the map and interface.
// Everywhere outside the map, a band shows as it looks on the map: its colour over the land.

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

/** A band's colour as it looks on the map (its translucent colour over the land), used everywhere. */
export function bandColour(score: number): string {
  for (let index = bands.length - 1; index >= 0; index--) if (score >= bands[index].minimumScore) return colourOnLand(bands[index].colour);
  return colourOnLand(bands[0].colour);
}

function channels(colour: string): number[] {
  const rgb = /rgb\((\d+), (\d+), (\d+)\)/.exec(colour);
  return rgb ? rgb.slice(1).map(Number) : [0, 1, 2].map(index => parseInt(colour.slice(1 + index * 2, 3 + index * 2), 16));
}

/** Dark or light text, whichever reads better on a colour; returns the token, or its value for canvases. */
export function textOnColour(colour: string, asValue = false): string {
  const linear = channels(colour).map(value => { const channel = value / 255; return channel <= 0.03928 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4; });
  const luminance = 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
  const darkText = (luminance + 0.05) / 0.05 > 1.05 / (luminance + 0.05);
  const token = darkText ? '--on-light-band' : '--on-dark-band';
  return asValue ? design.getPropertyValue(token).trim() : `var(${token})`;
}

export const textOnBand = (score: number): string => textOnColour(bandColour(score));

/** The band as it looks on the map: its translucent colour over the land, so the key matches the colours seen. */
export function colourOnLand(colour: string): string {
  const channel = (index: number) => parseInt(colour.slice(1 + index * 2, 3 + index * 2), 16);
  const alpha = colour.length === 9 ? channel(3) / 255 : 1;
  const land = getComputedStyle(document.documentElement).getPropertyValue('--map-land').trim();
  const blended = [0, 1, 2].map(index => Math.round(channel(index) * alpha + parseInt(land.slice(1 + index * 2, 3 + index * 2), 16) * (1 - alpha)));
  return `rgb(${blended.join(', ')})`;
}
