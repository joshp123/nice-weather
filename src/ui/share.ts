// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// Sharing, as on moneymap: copy the link, copy an image of the map with the place and its score, post on X,
// or hand it to the phone's share sheet. The same card is what a pasted link shows as its preview.
import type maplibregl from 'maplibre-gl';
import type { Period, PointVerdict } from '../engine';
import { button, el } from './dom';
import { bandSentence, dayCountsSentence, periodPhrase, type ForecastIndex } from './periods';
import type { Place } from './state';

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
  return `Where the weather is nice ${periodPhrase(period, forecast)}. ${context.headline}`.trim();
}

/** Copy the map after its next render so the poster includes the latest weather. */
async function captureMap(map: maplibregl.Map): Promise<HTMLCanvasElement> {
  if (!map.areTilesLoaded()) await new Promise<void>(resolve => { map.once('idle', () => resolve()); map.triggerRepaint(); });
  await new Promise<void>(resolve => { map.once('render', () => requestAnimationFrame(() => resolve())); map.triggerRepaint(); });
  const source = map.getCanvas();
  const copy = document.createElement('canvas'); copy.width = source.width; copy.height = source.height;
  copy.getContext('2d')!.drawImage(source, 0, 0);
  return copy;
}

/** Capture the screen layout itself, including its type, map, key and place comparisons. */
export async function drawShareCard(width: number, height: number, context: ShareContext): Promise<Blob> {
  await document.fonts.ready;
  const mapImage = await captureMap(context.map);
  const app = document.getElementById('app')!;
  const bounds = app.getBoundingClientRect();
  const clone = app.cloneNode(true) as HTMLElement;
  const originalElements = [app, ...app.querySelectorAll('*')];
  const cloneElements = [clone, ...clone.querySelectorAll('*')];
  originalElements.forEach((original, index) => {
    const target = cloneElements[index] as HTMLElement | SVGElement;
    const style = getComputedStyle(original);
    target.setAttribute('style', Array.from(style).map(name => `${name}:${style.getPropertyValue(name)}`).join(';'));
    if (original instanceof HTMLCanvasElement) {
      if (original === context.map.getCanvas()) { target.remove(); return; }
      const bitmap = document.createElement('img');
      bitmap.setAttribute('style', target.getAttribute('style')!);
      bitmap.src = original === context.map.getCanvas() ? mapImage.toDataURL() : original.toDataURL();
      bitmap.width = original.clientWidth; bitmap.height = original.clientHeight;
      target.replaceWith(bitmap);
    }
  });
  clone.querySelector('.poster-slot')?.remove();
  clone.style.background = 'transparent';
  const mapClone = clone.querySelector<HTMLElement>('.map');
  if (mapClone) mapClone.style.background = 'transparent';
  clone.style.position = 'relative'; clone.style.width = `${bounds.width}px`; clone.style.height = `${bounds.height}px`;
  // Fonts are embedded so the SVG is self-contained, including when Safari loads it as an image.
  let fonts = '';
  for (const sheet of Array.from(document.styleSheets)) {
    let rules: CSSRuleList;
    try { rules = sheet.cssRules; } catch { continue; }
    for (const rule of Array.from(rules)) {
      if (!(rule instanceof CSSFontFaceRule)) continue;
      let text = rule.cssText;
      const source = /url\(["']?([^"')]+)["']?\)/.exec(text);
      if (source) {
        const blob = await fetch(new URL(source[1], sheet.href ?? location.href)).then(response => response.blob());
        const encoded = await new Promise<string>(resolve => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result)); reader.readAsDataURL(blob); });
        text = text.replace(source[0], `url(${encoded})`);
      }
      fonts += text;
    }
  }
  const markup = new XMLSerializer().serializeToString(clone);
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${bounds.width}" height="${bounds.height}"><foreignObject width="100%" height="100%"><div xmlns="http://www.w3.org/1999/xhtml"><style>${fonts}</style>${markup}</div></foreignObject></svg>`;
  const bitmap = new Image();
  const ready = new Promise<void>((resolve, reject) => { bitmap.onload = () => resolve(); bitmap.onerror = () => reject(new Error('The poster could not be rendered')); });
  bitmap.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
  await ready;
  const canvas = document.createElement('canvas'); canvas.width = width; canvas.height = height;
  const draw = canvas.getContext('2d')!;
  draw.fillStyle = getComputedStyle(app).backgroundColor; draw.fillRect(0, 0, width, height);
  const mapBounds = context.map.getContainer().getBoundingClientRect();
  draw.save();
  draw.beginPath();
  draw.roundRect(mapBounds.x * width / bounds.width, mapBounds.y * height / bounds.height, mapBounds.width * width / bounds.width, mapBounds.height * height / bounds.height, parseFloat(getComputedStyle(context.map.getContainer()).borderRadius) * width / bounds.width); draw.clip();
  draw.fillStyle = getComputedStyle(context.map.getContainer()).backgroundColor;
  draw.fillRect(mapBounds.x * width / bounds.width, mapBounds.y * height / bounds.height, mapBounds.width * width / bounds.width, mapBounds.height * height / bounds.height);
  draw.drawImage(mapImage, mapBounds.x * width / bounds.width, mapBounds.y * height / bounds.height, mapBounds.width * width / bounds.width, mapBounds.height * height / bounds.height);
  draw.restore();
  draw.drawImage(bitmap, 0, 0, width, height);
  return new Promise((resolve, reject) => canvas.toBlob(blob => blob ? resolve(blob) : reject(new Error('The poster could not be saved')), 'image/png'));
}

export async function renderPoster(context: ShareContext, close: () => void): Promise<HTMLElement> {
  const blob = await drawShareCard(innerWidth, innerHeight, context);
  const url = URL.createObjectURL(blob);
  const preview = el('img', 'poster-preview'); preview.src = url; preview.alt = 'Your Nice Weather poster, exactly as shown on the map';
  const dismiss = () => { URL.revokeObjectURL(url); close(); };
  const namedPlaces = Array.from(document.querySelectorAll<HTMLElement>('#app .inline-place')).map(element => ({ element, bounds: element.getBoundingClientRect() })).filter(entry => entry.bounds.width && entry.bounds.height);
  const artwork = el('div', 'poster-artwork', preview);
  for (const { element, bounds } of namedPlaces) {
    const hotspot = button('poster-place-link', '', () => { dismiss(); element.click(); }, `Open ${element.textContent}`);
    hotspot.style.left = `${bounds.x / innerWidth * 100}%`; hotspot.style.top = `${bounds.y / innerHeight * 100}%`;
    hotspot.style.width = `${bounds.width / innerWidth * 100}%`; hotspot.style.height = `${bounds.height / innerHeight * 100}%`;
    artwork.append(hotspot);
  }
  const menu = renderShareMenu(() => context, () => Promise.resolve(blob));
  const save = button('menu-button', 'Download image', () => download(blob, 'nice-weather.png'));
  return el('section', 'poster-overlay', el('h2', 'poster-title', 'Your weather, as a poster.'), artwork,
    el('div', 'poster-toolbar panel', menu, save, button('menu-button', 'Back to the map', dismiss)));
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

export function renderShareMenu(context: () => ShareContext, capture?: () => Promise<Blob>): HTMLElement {
  const status = el('p', 'share-status');
  status.setAttribute('role', 'status');
  const say = (text: string) => { status.textContent = text; };

  const copyLink = button('menu-button', 'Copy link', async () => {
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

  const copyImage = button('menu-button', 'Copy image', async () => {
    const current = context();
    // Safari only accepts a promise made inside the click; the same PNG downloads when the clipboard refuses.
    const image = capture ? capture() : drawShareCard(innerWidth, innerHeight, current);
    copyImage.disabled = true;
    say('Copying image…');
    try {
      if ('ClipboardItem' in window && navigator.clipboard?.write) {
        try {
          await navigator.clipboard.write([new ClipboardItem({ 'image/png': image })]);
          say('Image copied. Paste it into your post.');
          return;
        } catch {
          // Fall through to the download.
        }
      }
      download(await image, 'nice-weather.png');
      say('Image downloaded.');
    } catch (error) {
      say(`Could not make the image: ${error instanceof Error ? error.message : String(error)}`);
    } finally {
      copyImage.disabled = false;
    }
  });

  const post = el('a', 'menu-button', 'Post on X');
  post.target = '_blank';
  post.rel = 'noopener';
  const current = context();
  post.href = `https://x.com/intent/tweet?${new URLSearchParams({ text: shareText(current), url: current.link })}`;

  const items = [copyLink, copyImage, post];
  if (typeof navigator.share === 'function') {
    items.push(button('menu-button', 'Share with another app…', async () => {
      const current = context();
      try {
        // Call in the tap: awaiting an image first loses Safari's user activation.
        await navigator.share({ text: shareText(current), url: current.link });
        say('Shared.');
      } catch (error) {
        if (!(error instanceof Error && error.name === 'AbortError')) say('Could not open the share sheet. Use Copy link instead.');
      }
    }));
  }
  return el('div', 'share-menu panel', ...items, status);
}
