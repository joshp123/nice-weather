// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// Share the poster and its sentence together. Link previews use the same poster template.
import type maplibregl from 'maplibre-gl';
import type { Period, Point, PointVerdict } from '../engine';
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
  /** What the poster names: the chosen place, or the places in the answer. Its map always shows them. */
  subjects: readonly Point[];
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
    // Beyond the world's edge the map is transparent; on the poster it is sea, not black.
    const draw = copy.getContext('2d')!;
    draw.fillStyle = getComputedStyle(document.documentElement).getPropertyValue('--map-sea').trim();
    draw.fillRect(0, 0, copy.width, copy.height);
    draw.drawImage(source, 0, 0);
    return copy;
  } finally {
    map.setPixelRatio(screenPixelRatio);
  }
}

/** Move the camera so the places the poster names fit its wide map frame (zooming out past the usual limit if a
 *  phone is too narrow for them); returns the way back. */
function frameSubjects(map: maplibregl.Map, subjects: readonly Point[], aspect: number): () => void {
  const before = { center: map.getCenter(), zoom: map.getZoom() }, minimumZoom = map.getMinZoom();
  if (!subjects.length) return () => {};
  const { clientWidth: width, clientHeight: height } = map.getContainer();
  const frameWidth = Math.min(width, height * aspect), frameHeight = frameWidth / aspect;
  const margin = Math.min(frameWidth, frameHeight) * 0.15;
  const across = (width - frameWidth) / 2 + margin, down = (height - frameHeight) / 2 + margin;
  const longitudes = subjects.map(place => place.longitude), latitudes = subjects.map(place => place.latitude);
  map.setMinZoom(0);
  const camera = map.cameraForBounds([[Math.min(...longitudes), Math.min(...latitudes)], [Math.max(...longitudes), Math.max(...latitudes)]],
    { padding: { top: down, bottom: down, left: across, right: across }, maxZoom: before.zoom });
  if (camera) map.jumpTo(camera);
  return () => { map.setMinZoom(minimumZoom); map.jumpTo(before); };
}

/** The middle of the named places on screen, where the poster's crop is centred. */
function subjectsCentre(map: maplibregl.Map, subjects: readonly Point[]): { x: number; y: number } | null {
  if (!subjects.length) return null;
  const points = subjects.map(place => map.project([place.longitude, place.latitude]));
  const xs = points.map(point => point.x), ys = points.map(point => point.y);
  return { x: (Math.min(...xs) + Math.max(...xs)) / 2, y: (Math.min(...ys) + Math.max(...ys)) / 2 };
}

/** Draw the same control-free template used by link previews. */
export async function drawShareCard(_width: number, _height: number, context: ShareContext): Promise<Blob> {
  const { shareImagePng, SHARE_MAP } = await import('../../hosting/sites/share-template.mjs');
  const restoreCamera = frameSubjects(context.map, context.subjects, SHARE_MAP.width / SHARE_MAP.height);
  let mapImage: HTMLCanvasElement, focus: { x: number; y: number } | null;
  try {
    mapImage = await captureMap(context.map);
    focus = subjectsCentre(context.map, context.subjects);
  } finally {
    restoreCamera();
  }
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
      ...(focus ? { focusX: focus.x * mapImage.width / context.map.getContainer().clientWidth, focusY: focus.y * mapImage.height / context.map.getContainer().clientHeight } : {}) },
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
