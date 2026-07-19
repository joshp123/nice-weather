import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { clearSearchAfterMapSelection, dayCountLabel, LayerLegend, LENSES, placeSearchPrompt, PointLensControl, rainDisplay, rangeLensAsset, revealSelectedPeriod, temperatureRange, wetDaytimeDuration } from './App';
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

  it('keeps exact profile weights and the dry rule inside the Scores popover', () => {
    const markup = renderToStaticMarkup(createElement(LayerLegend, {
      manifest: {
        palette: [{ score: 0, color: '#000' }, { score: 100, color: '#fff' }],
        legend: [{ label: 'Poor', color: '#000', min: 0, max: 24 }],
        forecast: { days: [], window: { resolutionKm: 28 } },
      } as unknown as WeatherManifest,
      view: 'range', lens: 'overall', panel: 'guide', windEnabled: false, windFrameIndex: 0, windStatus: 'idle',
      onPanelChange: vi.fn(), onChange: vi.fn(), onWindEnabledChange: vi.fn(), onWindFrameChange: vi.fn(),
    }));

    expect(markup).toContain('Comfort 45% · Dry 25% · Bright 20% · Calm 10%');
    expect(markup).toContain('Comfort 35% · Dry 30% · Bright 10% · Calm 25%');
    expect(markup).toContain('Dry uses whichever is worse: rain amount or wet daytime duration.');
  });

  it('keeps all five map lenses available in the compact phone place sheet', () => {
    const markup = renderToStaticMarkup(createElement(PointLensControl, { value: 'dry', onChange: vi.fn() }));
    expect(markup).toContain('class="phoneLensControl"');
    expect(markup.match(/role="radio"/g)).toHaveLength(5);
    expect(markup).toContain('>Overall<');
    expect(markup).toContain('aria-checked="true">Dry<');
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
    const climate = { metrics: { rainMm: 2.25, wetTimeFraction: .5 }, source: { basis: 'climate' } } as DailyPoint;
    const forecast = { metrics: { rainMm: 2.25 }, source: { basis: 'forecast' } } as DailyPoint;

    expect(rainDisplay(climate)).toEqual({ label: 'Rain · daily average', value: '2.3 mm/day' });
    expect(rainDisplay(forecast)).toEqual({ label: 'Rain', value: '2.3 mm' });
  });

  it('puts forecast rain amount and wet daytime duration together', () => {
    const forecast = { metrics: { rainMm: .8, wetTimeFraction: .5 }, source: { basis: 'forecast' } } as DailyPoint;

    expect(rainDisplay(forecast)).toEqual({ label: 'Rain', value: '0.8 mm · about 5 wet hours' });
    expect(rainDisplay({ metrics: { rainMm: 0, wetTimeFraction: 0 }, source: { basis: 'forecast' } } as DailyPoint)).toEqual({ label: 'Rain', value: '0.0 mm' });
    expect(wetDaytimeDuration(.01)).toBe('under ½ wet hour');
    expect(wetDaytimeDuration(.1)).toBe('about 1 wet hour');
  });

  it('makes Europe-wide best-weather discovery visible in the search prompt', () => {
    expect(placeSearchPrompt('range')).toBe('Search Europe or see best weather');
    expect(placeSearchPrompt('daily')).toBe('Search Europe');
    expect(placeSearchPrompt('climate')).toBe('Search Europe');
  });

  it('uses the correct singular day label', () => {
    expect(dayCountLabel(1)).toBe('1 day');
    expect(dayCountLabel(14)).toBe('14 days');
  });

  it('shows equal rounded minimum and maximum temperatures once', () => {
    expect(temperatureRange(27.6, 28.4)).toBe('28°');
    expect(temperatureRange(27.4, 28.4)).toBe('27–28°');
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
    expect(css).toContain('.phoneLensControl { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr));');
  });
});
