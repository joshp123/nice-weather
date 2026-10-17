// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// Sharing, as on moneymap: copy the link, copy an image of the map with the place and its score, post on X,
// or hand it to the phone's share sheet. The same card is what a pasted link shows as its preview.
import type maplibregl from 'maplibre-gl';
import type { Period, PointVerdict } from '../engine';
import { button, el } from './dom';
import { niceDaysSentence, periodPhrase, periodTitle, type ForecastIndex } from './periods';
import type { Place } from './state';
import { bandColour } from './timebar';

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
    const days = niceDaysSentence(period, forecast, verdict.niceDayShare, verdict.niceDayCount);
    return `${place.name} ${periodPhrase(period, forecast)}: ${verdict.band}, ${Math.round(verdict.meanScore)} out of 100.${days ? ` ${days}.` : ''}`;
  }
  return `Where the weather is nice ${periodPhrase(period, forecast)}. ${context.headline}`.trim();
}

/** A copy of the map as drawn right now. MapLibre keeps no drawing buffer, so the copy is taken inside a render. */
function captureMap(map: maplibregl.Map): Promise<HTMLCanvasElement> {
  return new Promise(resolve => {
    map.once('render', () => {
      const source = map.getCanvas();
      const copy = document.createElement('canvas');
      copy.width = source.width;
      copy.height = source.height;
      copy.getContext('2d')!.drawImage(source, 0, 0);
      resolve(copy);
    });
    map.triggerRepaint();
  });
}

function fitText(context: CanvasRenderingContext2D, text: string, maxWidth: number): string {
  if (context.measureText(text).width <= maxWidth) return text;
  let shortened = text;
  while (shortened.length > 1 && context.measureText(`${shortened}…`).width > maxWidth) shortened = shortened.slice(0, -1);
  return `${shortened.trimEnd()}…`;
}

/** The share card: the map behind, the place and its verdict in front, the link and the small print. */
export async function drawShareCard(width: number, height: number, context: ShareContext): Promise<Blob> {
  await document.fonts.load('600 40px Barlow').catch(() => undefined);
  const [snapshot] = await Promise.all([captureMap(context.map)]);
  const canvas = document.createElement('canvas');
  canvas.width = width;
  canvas.height = height;
  const draw = canvas.getContext('2d')!;

  // The map, cropped to the card's shape around the place (or the middle of the view).
  const map = context.map;
  const pixelRatio = snapshot.width / map.getContainer().clientWidth;
  const focus = context.place ? map.project([context.place.longitude, context.place.latitude]) : { x: map.getContainer().clientWidth / 2, y: map.getContainer().clientHeight / 2 };
  let cropWidth = snapshot.width;
  let cropHeight = (cropWidth * height) / width;
  if (cropHeight > snapshot.height) { cropHeight = snapshot.height; cropWidth = (cropHeight * width) / height; }
  const cropLeft = Math.min(Math.max(focus.x * pixelRatio - cropWidth / 2, 0), snapshot.width - cropWidth);
  const cropTop = Math.min(Math.max(focus.y * pixelRatio - cropHeight / 2, 0), snapshot.height - cropHeight);
  draw.drawImage(snapshot, cropLeft, cropTop, cropWidth, cropHeight, 0, 0, width, height);

  // A dark band along the bottom for the words, whatever the map's colours.
  const unit = height / 630;
  const bandHeight = 215 * unit;
  const gradient = draw.createLinearGradient(0, height - bandHeight, 0, height);
  gradient.addColorStop(0, 'rgba(8, 10, 14, 0)');
  gradient.addColorStop(0.45, 'rgba(8, 10, 14, 0.78)');
  gradient.addColorStop(1, 'rgba(8, 10, 14, 0.9)');
  draw.fillStyle = gradient;
  draw.fillRect(0, height - bandHeight, width, bandHeight);
  const topGradient = draw.createLinearGradient(0, 0, 0, 90 * unit);
  topGradient.addColorStop(0, 'rgba(8, 10, 14, 0.55)');
  topGradient.addColorStop(1, 'rgba(8, 10, 14, 0)');
  draw.fillStyle = topGradient;
  draw.fillRect(0, 0, width, 90 * unit);

  const margin = 48 * unit;
  draw.textBaseline = 'alphabetic';
  draw.fillStyle = '#ffffff';
  draw.font = `600 ${26 * unit}px Barlow, sans-serif`;
  draw.fillText('Nice Weather', margin, margin + 8 * unit);
  draw.textAlign = 'right';
  draw.font = `500 ${22 * unit}px Barlow, sans-serif`;
  draw.fillStyle = 'rgba(255,255,255,0.85)';
  draw.fillText(periodTitle(context.period, context.forecast), width - margin, margin + 8 * unit);
  draw.textAlign = 'left';

  const { place, verdict } = context;
  let textLeft = margin;
  const titleBaseline = height - 112 * unit;
  if (place?.name && verdict) {
    const badge = 84 * unit;
    const badgeTop = titleBaseline - badge + 14 * unit;
    draw.fillStyle = bandColour(verdict.meanScore);
    draw.beginPath();
    draw.roundRect(margin, badgeTop, badge, badge, 14 * unit);
    draw.fill();
    draw.fillStyle = '#ffffff';
    draw.textAlign = 'center';
    draw.font = `600 ${44 * unit}px Barlow, sans-serif`;
    draw.fillText(String(Math.round(verdict.meanScore)), margin + badge / 2, badgeTop + badge * 0.69);
    draw.textAlign = 'left';
    textLeft = margin + badge + 24 * unit;
    draw.font = `600 ${54 * unit}px Barlow, sans-serif`;
    draw.fillText(fitText(draw, place.name, width - textLeft - margin), textLeft, titleBaseline);
    draw.font = `500 ${26 * unit}px Barlow, sans-serif`;
    draw.fillStyle = 'rgba(255,255,255,0.92)';
    const days = niceDaysSentence(context.period, context.forecast, verdict.niceDayShare, verdict.niceDayCount);
    draw.fillText(fitText(draw, `${verdict.band} · ${Math.round(verdict.meanScore)} out of 100${days ? ` · ${days}` : ''}`, width - textLeft - margin), textLeft, titleBaseline + 40 * unit);
  } else {
    draw.font = `600 ${46 * unit}px Barlow, sans-serif`;
    draw.fillText(fitText(draw, `Where the weather is nice ${periodPhrase(context.period, context.forecast)}`, width - 2 * margin), margin, titleBaseline);
    draw.font = `500 ${26 * unit}px Barlow, sans-serif`;
    draw.fillStyle = 'rgba(255,255,255,0.92)';
    draw.fillText(fitText(draw, context.headline, width - 2 * margin), margin, titleBaseline + 40 * unit);
  }

  draw.textAlign = 'right';
  draw.fillStyle = '#ffffff';
  draw.font = `600 ${20 * unit}px Barlow, sans-serif`;
  draw.fillText(context.link.replace(/^https?:\/\//, ''), width - margin, height - margin + 10 * unit);

  return new Promise((resolve, reject) => canvas.toBlob(blob => (blob ? resolve(blob) : reject(new Error('Could not make the image'))), 'image/png'));
}

function canShareFiles(): boolean {
  if (typeof navigator.canShare !== 'function') return false;
  try {
    return navigator.canShare({ files: [new File([new Uint8Array(8)], 'nice-weather.png', { type: 'image/png' })] });
  } catch {
    return false;
  }
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

export function renderShareMenu(context: () => ShareContext, onDone: () => void): HTMLElement {
  const status = el('p', 'share-status');
  status.setAttribute('role', 'status');
  const say = (text: string) => { status.textContent = text; };

  const copyLink = button('menu-button', 'Copy link', async () => {
    try {
      await navigator.clipboard.writeText(context().link);
      say('Link copied.');
    } catch {
      say(context().link);
    }
  });

  const copyImage = button('menu-button', 'Copy image', async () => {
    const current = context();
    // Safari only accepts a promise made inside the click; the same PNG downloads when the clipboard refuses.
    const image = drawShareCard(1200, 630, current);
    copyImage.disabled = true;
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
  post.onmousedown = post.ontouchstart = () => {
    const current = context();
    post.href = `https://x.com/intent/tweet?${new URLSearchParams({ text: shareText(current), url: current.link })}`;
  };
  post.onclick = () => { if (!post.href) post.onmousedown!(new MouseEvent('mousedown')); };

  const items = [copyLink, copyImage, post];
  if (canShareFiles()) {
    items.push(button('menu-button', 'Share…', async () => {
      const current = context();
      try {
        const file = new File([await drawShareCard(1200, 630, current)], 'nice-weather.png', { type: 'image/png' });
        await navigator.share({ files: [file], text: shareText(current), url: current.link });
        onDone();
      } catch (error) {
        if (!(error instanceof Error && error.name === 'AbortError')) say('Could not open the share sheet.');
      }
    }));
  }
  return el('div', 'share-menu panel', ...items, status);
}
