// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// Share the poster and its sentence together. Link previews use the same poster template.
import type maplibregl from 'maplibre-gl';
import type { Period, PointVerdict } from '../engine';
import { button, el } from './dom';
import { bandSentence, dayCountsSentence, periodPhrase, type ForecastIndex } from './periods';
import type { Place } from './state';
import { countryFlag } from './countries';
import { shareImage } from '../../hosting/sites/share-image.mjs';
import { bands, colourOnLand } from '../palette';

export type ShareContext = Readonly<{
  map: maplibregl.Map;
  place: Place | null;
  verdict: PointVerdict | null;
  period: Period;
  forecast: ForecastIndex | null;
  headline: string;
  /** The absolute link to this view. */
  link: string;
}>;

/** One sentence that stands on its own in a post. */
export function shareText(context: ShareContext): string {
  const { place, verdict, period, forecast } = context;
  if (place?.name && verdict) {
    const days = dayCountsSentence(period, forecast, verdict);
    return `${place.name} ${periodPhrase(period, forecast)}: ${bandSentence(period, verdict.band)}, ${Math.floor(verdict.meanScore)} out of 100.${days ? ` ${days}.` : ''}`;
  }
  return context.headline;
}

/** Copy the map after its next render so the poster includes the latest weather. */
async function captureMap(map: maplibregl.Map): Promise<HTMLCanvasElement> {
  if (!map.areTilesLoaded()) await new Promise<void>(resolve => { map.once('idle', () => resolve()); map.triggerRepaint(); });
  const screenPixelRatio = map.getPixelRatio();
  const exportPixelRatio = Math.max(screenPixelRatio, 1200 / map.getContainer().clientWidth);
  try {
    map.setPixelRatio(exportPixelRatio);
    await new Promise<void>(resolve => { map.once('render', () => requestAnimationFrame(() => resolve())); map.triggerRepaint(); });
    const source = map.getCanvas();
    const copy = document.createElement('canvas'); copy.width = source.width; copy.height = source.height;
    copy.getContext('2d')!.drawImage(source, 0, 0);
    return copy;
  } finally {
    map.setPixelRatio(screenPixelRatio);
  }
}

/** Draw the same control-free template used by link previews. */
export async function drawShareCard(_width: number, _height: number, context: ShareContext): Promise<Blob> {
  const [{ shareImagePng }, mapImage] = await Promise.all([
    import('../../hosting/sites/share-template.mjs'), captureMap(context.map),
  ]);
  const source = mapImage.getContext('2d')!.getImageData(0, 0, mapImage.width, mapImage.height).data;
  const pixels = new Uint8Array(mapImage.width * mapImage.height * 3);
  for (let index = 0; index < pixels.length / 3; index++) pixels.set(source.subarray(index * 4, index * 4 + 3), index * 3);
  const { place, verdict, period, forecast } = context;
  let dates = periodPhrase(period, forecast);
  if (forecast && (period.kind === 'forecast' || period.kind === 'forecast-day')) {
    const date = (day: number) => {
      const value = new Date(`${forecast.firstDate}T12:00:00Z`); value.setUTCDate(value.getUTCDate() + day);
      return value.toLocaleDateString('en-GB', { day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' });
    };
    dates = period.kind === 'forecast-day' ? date(period.day) : `${date(0)} to ${date(forecast.days - 1)}`;
  } else dates = `${dates[0].toUpperCase()}${dates.slice(1)}, typical weather`;
  const png = await shareImagePng({
    title: place ? `${countryFlag(place)} ${place.name}`.trim() : context.headline,
    dates,
    numbers: verdict ? { score: verdict.meanScore, niceDayCount: verdict.niceDayCount, perfectDayCount: verdict.perfectDayCount, totalDays: verdict.totalDays, countedDays: verdict.countedDays, countBasis: verdict.countBasis } : undefined,
    detail: 'Colours show the typical day. Find your nice weather.',
    map: { pixels, width: mapImage.width, height: mapImage.height,
      ...(place ? {focusX: context.map.project([place.longitude, place.latitude]).x * mapImage.width / context.map.getContainer().clientWidth,
        focusY: context.map.project([place.longitude, place.latitude]).y * mapImage.height / context.map.getContainer().clientHeight} : {}) },
    dark: matchMedia('(prefers-color-scheme: dark)').matches,
    bandColours: bands.map(band => colourOnLand(band.colour).match(/\d+/g)!.map(Number) as [number, number, number]),
  });
  return new Blob([png as BlobPart], { type: 'image/png' });
}

export async function renderPoster(context: ShareContext, close: () => void): Promise<HTMLElement> {
  const blob = await drawShareCard(1200, 630, context);
  const url = URL.createObjectURL(blob);
  const preview = el('img', 'poster-preview'); preview.src = url; preview.alt = 'Nice! weather map with the place, figures and dates';
  const dismiss = () => { URL.revokeObjectURL(url); close(); };
  const artwork = el('div', 'poster-artwork', preview);
  const { shareHeadingLinks } = await import('../../hosting/sites/share-template.mjs');
  const title = context.place ? `${countryFlag(context.place)} ${context.place.name}`.trim() : context.headline;
  const namedElements = new Map<string, HTMLElement | null>(Array.from(document.querySelectorAll<HTMLElement>('#app .inline-place')).map(element => [element.textContent?.trim() ?? '', element]));
  if (context.place) namedElements.set(title, null);
  for (const bounds of shareHeadingLinks(title, Array.from(namedElements.keys()))) {
    const placeLink = button('poster-place-link', '', () => { dismiss(); if (bounds.label !== title || !context.place) namedElements.get(bounds.label)?.click(); }, `Open ${bounds.label}`);
    placeLink.style.left = `${bounds.x / 1200 * 100}%`; placeLink.style.top = `${bounds.y / 630 * 100}%`;
    placeLink.style.width = `${bounds.width / 1200 * 100}%`; placeLink.style.height = `${bounds.height / 630 * 100}%`;
    artwork.append(placeLink);
  }
  const menu = renderShareMenu(() => context, blob);
  const overlay = el('section', 'poster-overlay', artwork, el('div', 'poster-toolbar panel', menu, button('button', 'Close', dismiss)));
  overlay.setAttribute('role', 'dialog');
  overlay.setAttribute('aria-label', 'Share this map');
  return overlay;
}

function download(blob: Blob, name: string): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  document.body.append(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

export function renderShareMenu(context: () => ShareContext, image: Blob): HTMLElement {
  const status = el('p', 'share-status');
  status.setAttribute('role', 'status');
  const say = (text: string) => { status.textContent = text; };

  const copyLink = button('button', 'Copy link', async () => {
    try {
      await navigator.clipboard.writeText(context().link);
      say('Link copied.');
    } catch {
      const field = el('input', 'copy-link-fallback');
      field.value = context().link;
      field.readOnly = true;
      field.setAttribute('aria-label', 'Link to this view');
      status.replaceChildren('Copy this link:', field);
      field.focus();
      field.select();
      if (document.execCommand('copy')) say('Link copied.');
    }
  });

  const share = button('button', 'Share image', async () => {
    const current = context();
    share.disabled = true;
    try {
      const result = await shareImage(image, `${shareText(current)}\n${current.link}`, 'nice-weather.png');
      say(result === 'shared' ? 'Shared.' : 'Image and sentence copied. Paste them into your post.');
    } catch (error) {
      if (!(error instanceof Error && error.name === 'AbortError')) {
        say('Could not share the image. Download it below; the sentence is ready to copy.');
        const sentence = el('textarea', 'copy-link-fallback');
        sentence.value = `${shareText(current)}\n${current.link}`;
        sentence.readOnly = true;
        sentence.setAttribute('aria-label', 'Sentence to share with the image');
        status.append(sentence);
      }
    } finally {
      share.disabled = false;
    }
  });
  const save = button('button', 'Download image', () => download(image, 'nice-weather.png'));
  return el('div', 'share-menu', share, save, copyLink, status);
}
