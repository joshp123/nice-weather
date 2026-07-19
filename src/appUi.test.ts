import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { clearSearchAfterMapSelection, dayCountLabel, LayerLegend, LENSES, rainDisplay, rangeLensAsset, revealSelectedPeriod } from './App';
import type { DailyPoint, WeatherImage, WeatherManifest } from './data/types';

describe('map controls', () => {
  it('keeps Overall one click away in the complete five-lens instrument', () => {
    expect(LENSES).toEqual([
      { id: 'overall', label: 'Overall' },
      { id: 'comfort', label: 'Comfort' },
      { id: 'dry', label: 'Dry' },
      { id: 'bright', label: 'Bright' },
      { id: 'wind', label: 'Calm' },
    ]);

    const markup = renderToStaticMarkup(createElement(LayerLegend, {
      manifest: { palette: [{ score: 0, color: '#000' }, { score: 100, color: '#fff' }] } as WeatherManifest,
      view: 'range',
      lens: 'comfort',
      panel: 'lenses',
      windEnabled: false,
      windFrameIndex: 0,
      windStatus: 'idle',
      onPanelChange: vi.fn(),
      onChange: vi.fn(),
      onWindEnabledChange: vi.fn(),
      onWindFrameChange: vi.fn(),
    }));
    expect(markup).toContain('Choose map lens. Active: Comfort');
    expect(markup.match(/role="radio"/g)).toHaveLength(5);
    expect(markup).toContain('>Overall<');
    expect(markup).toContain('aria-label="Explain weather scores"');
  });

  it('uses canonical range lens images rather than inventing a browser score', () => {
    const image = (url: string) => ({ url } as WeatherImage);
    const window = {
      profiles: { nice: { image: image('nice-overall') }, cycling: { image: image('cycling-overall') } },
      lenses: {
        comfort: image('comfort'), dry: image('dry'), bright: image('bright'),
        'wind-nice': image('nice-calm'), 'wind-cycling': image('cycling-calm'),
      },
    } as WeatherManifest['forecast']['window'];

    expect(rangeLensAsset(window, 'nice', 'overall').url).toBe('nice-overall');
    expect(rangeLensAsset(window, 'nice', 'comfort').url).toBe('comfort');
    expect(rangeLensAsset(window, 'cycling', 'wind').url).toBe('cycling-calm');
  });

  it('brings a newly selected day or month back into the visible rail', () => {
    const scrollIntoView = vi.fn();
    const container = {
      querySelectorAll: () => [
        { dataset: { periodId: '2026-07-20' }, scrollIntoView: vi.fn() },
        { dataset: { periodId: '2026-08-02' }, scrollIntoView },
      ],
    } as unknown as Pick<HTMLElement, 'querySelectorAll'>;

    revealSelectedPeriod(container, '2026-08-02');

    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: 'smooth', block: 'nearest', inline: 'center' });
  });
});

describe('clear weather copy', () => {
  it('states that legacy climate rain is a daily monthly average', () => {
    const climate = { metrics: { rainMm: 2.25 }, source: { basis: 'climate' } } as DailyPoint;
    const forecast = { metrics: { rainMm: 2.25 }, source: { basis: 'forecast' } } as DailyPoint;

    expect(rainDisplay(climate)).toEqual({ label: 'Rain · daily average', value: '2.3 mm/day' });
    expect(rainDisplay(forecast)).toEqual({ label: 'Rain', value: '2.3 mm' });
  });

  it('uses the correct singular day label', () => {
    expect(dayCountLabel(1)).toBe('1 day');
    expect(dayCountLabel(14)).toBe('14 days');
  });

  it('clears a stale place query only after an external map selection', () => {
    const clear = vi.fn();
    clearSearchAfterMapSelection(0, clear);
    expect(clear).not.toHaveBeenCalled();
    clearSearchAfterMapSelection(1, clear);
    expect(clear).toHaveBeenCalledOnce();
  });
});

describe('phone readability', () => {
  it('allows primary place names and weather values to wrap instead of ellipsizing', () => {
    const css = readFileSync(new URL('./styles.css', import.meta.url), 'utf8');
    expect(css).toContain('.pointHeading h2 { margin: 0; overflow-wrap: anywhere;');
    expect(css).toContain('.weatherFacts dd { margin: 4px 0 0; overflow-wrap: anywhere;');
    expect(css).toContain('.searchPopover .bestPlace strong, .searchPopover .bestPlace small { overflow: visible; text-overflow: clip; white-space: normal; }');
  });
});
