import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { bestWeatherAvailability, clearSearchAfterMapSelection, dayCountLabel, forecastRangeMeta, LayerLegend, LENSES, periodRailHasMore, periodStatus, placeSearchPrompt, rainDisplay, rangeLensAsset, revealFocusedPeriod, revealSelectedPeriod, ScoreRamp, temperatureRange, wetDaytimeDuration } from './App';
import type { DailyPoint, WeatherImage, WeatherManifest } from './data/types';

describe('map controls', () => {
  it('keeps Overall one click away in the complete five-lens instrument', () => {
    expect(LENSES).toEqual([
      { id: 'overall', label: 'Overall score' },
      { id: 'comfort', label: 'Comfort' },
      { id: 'dry', label: 'Dry' },
      { id: 'bright', label: 'Bright' },
      { id: 'wind', label: 'Calm' },
    ]);

    const markup = renderToStaticMarkup(createElement(LayerLegend, {
      manifest: { palette: [{ score: 0, color: '#000' }, { score: 100, color: '#fff' }], legend, forecast: { days: [], window: { resolutionKm: 28 } } } as unknown as WeatherManifest,
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
    expect(markup).toContain('Choose map lens and explain scores. Active: Comfort');
    expect(markup).toContain('role="group" aria-label="Map lens"');
    expect(markup.match(/aria-pressed=/g)).toHaveLength(5);
    expect(markup).not.toContain('role="radio"');
    expect(markup).toContain('>Overall score<');
    expect(markup).toContain('aria-label="Map lens and score guide"');
    expect(markup).toContain('<details class="scoreGuide"><summary>How scores work</summary>');
    expect(markup).not.toContain('<details class="scoreGuide" open="">');
    for (const band of legend) expect(markup).toContain(`>${band.label}<`);
    expect(markup).not.toContain('>Scores<');
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

  it('keeps exact profile weights and the dry rule inside the legend guide', () => {
    const markup = renderToStaticMarkup(createElement(LayerLegend, {
      manifest: {
        palette: [{ score: 0, color: '#000' }, { score: 100, color: '#fff' }],
        legend,
        forecast: { days: [], window: { resolutionKm: 28 } },
      } as unknown as WeatherManifest,
      view: 'range', lens: 'overall', panel: 'lenses', windEnabled: false, windFrameIndex: 0, windStatus: 'idle',
      onPanelChange: vi.fn(), onChange: vi.fn(), onWindEnabledChange: vi.fn(), onWindFrameChange: vi.fn(),
    }));

    expect(markup).toContain('Warm &amp; sunny');
    expect(markup).toContain('Comfort 45% · Dry 25% · Bright 20% · Calm 10%');
    expect(markup).toContain('Comfort 35% · Dry 30% · Bright 10% · Calm 25%');
    expect(markup).toContain('Dry uses whichever is worse: rain amount or wet daytime duration.');
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

  it('fully reveals a day or month when keyboard focus reaches it', () => {
    const scrollIntoView = vi.fn();
    revealFocusedPeriod({ scrollIntoView } as unknown as Pick<HTMLElement, 'scrollIntoView'>);
    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: 'auto', block: 'nearest', inline: 'nearest' });
  });

  it('keeps the score direction visible without opening score theory', () => {
    const markup = renderToStaticMarkup(createElement(ScoreRamp, {
      manifest: { palette: [{ score: 0, color: '#000000' }, { score: 100, color: '#ffffff' }] } as WeatherManifest,
    }));
    expect(markup).toContain('Score colours run from Awful to Excellent');
    expect(markup).toContain('<b>Awful</b>');
    expect(markup).toContain('<b>Excellent</b>');
    expect(markup).toContain('linear-gradient(90deg, #000000 0%, #ffffff 100%)');
  });

  it('shows the rail cue only while more periods continue to the right', () => {
    expect(periodRailHasMore({ scrollWidth: 720, clientWidth: 320, scrollLeft: 0 })).toBe(true);
    expect(periodRailHasMore({ scrollWidth: 720, clientWidth: 320, scrollLeft: 400 })).toBe(false);
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

  it('uses an honest Europe search prompt in every view', () => {
    expect(placeSearchPrompt('range')).toBe('Search Europe');
    expect(placeSearchPrompt('daily')).toBe('Search Europe');
    expect(placeSearchPrompt('climate')).toBe('Search Europe');
  });

  it('keeps source honesty outside the control labels without repeating the range dates', () => {
    const manifest = {
      dataStatus: 'live',
      forecast: {
        updatedAt: '2020-07-20T08:00:00Z', staleAfterHours: 12,
        window: { startDate: '2026-07-20', endDate: '2026-08-02', resolutionKm: 28 },
        days: [
          ...Array.from({ length: 5 }, () => ({ kind: 'detailed', resolutionKm: 7 })),
          ...Array.from({ length: 9 }, () => ({ kind: 'outlook', resolutionKm: 28 })),
        ],
      },
      climate: { period: '1981–2010' },
    } as WeatherManifest;
    expect(periodStatus(manifest, 'range')).toEqual({ primary: 'Forecast out of date', meta: '5 days detailed at 7 km · 9 days outlook at 28 km', stale: true });
    expect(forecastRangeMeta(manifest)).toBe('5 days detailed at 7 km · 9 days outlook at 28 km');
    expect(periodStatus(manifest, 'range').meta).not.toContain('20 Jul');
    expect(periodStatus(manifest, 'climate')).toEqual({ primary: 'Monthly average', meta: '1981–2010', stale: false });
  });

  it('falls back to the declared range detail when no daily split is present', () => {
    const manifest = { forecast: { window: { resolutionKm: 18 }, days: [] } } as unknown as WeatherManifest;
    expect(forecastRangeMeta(manifest)).toBe('Forecast detail · about 18 km');
  });

  it('suppresses ranked destinations when the forecast is stale', () => {
    const manifest = {
      forecast: { updatedAt: '2026-07-20T08:00:00Z', staleAfterHours: 12 },
    } as WeatherManifest;
    expect(bestWeatherAvailability(manifest, new Date('2026-07-20T19:59:00Z'))).toEqual({ allowed: true, message: '' });
    expect(bestWeatherAvailability(manifest, new Date('2026-07-20T20:01:00Z'))).toEqual({ allowed: false, message: 'The forecast is out of date. Search for a place instead.' });
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
    const app = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8');
    expect(css).toContain('.pointHeading h2 { margin: 0; overflow-wrap: anywhere;');
    expect(css).toContain('.weatherFacts dd { margin: var(--space-1) 0 0; overflow-wrap: anywhere;');
    expect(css).toContain('.searchPopover .bestPlace strong, .searchPopover .bestPlace small { overflow: visible; text-overflow: clip; white-space: normal; }');
    expect(css).not.toContain('.phoneLensControl');
    for (const label of ['14 days', 'By day', 'Climate', 'Overall']) expect(app).toContain(`'${label}'`);
    expect(app).toContain('>Warm + sunny</span>');
    expect(app).not.toContain('shortProfileLabel');
  });

  it('keeps the selected-place action fixed outside the scrolling evidence', () => {
    const css = readFileSync(new URL('./styles.css', import.meta.url), 'utf8');
    expect(css).toContain('.pointPanel:focus { outline: 0; }');
    expect(css).toContain('.pointPanel:focus-visible { box-shadow: var(--shadow-surface), inset 0 0 0 2px var(--focus); }');
    expect(css).toContain('.pointPanel { top: auto; right: var(--space-2); bottom: 88px; left: var(--space-2); display: flex;');
    expect(css).toContain('.pointPanel.expanded .pointPanelBody { margin: 0 calc(-1 * var(--space-4)); padding: 0 var(--space-4) var(--space-3); overflow-y: auto;');
    expect(css).toContain('.sheetExpand { display: block; flex: 0 0 var(--control-height);');
    expect(css).toContain('.periodRailWrap[data-has-before="true"]::before, .periodRailWrap[data-has-more="true"]::after { opacity: 1; }');
    expect(css).not.toContain('.periodRailCue');
  });

  it('uses one compact control system and only two permanent phone surfaces', () => {
    const css = readFileSync(new URL('./styles.css', import.meta.url), 'utf8');
    expect(css).toContain('--control-height: 44px;');
    expect(css).toContain('--meta-height: 24px;');
    expect(css).toContain('--radius-surface: 12px;');
    expect(css).toContain('--radius-inner: 8px;');
    expect(css).toContain('.controlSurface { border: 1px solid var(--hairline);');
    expect(css).toContain('.cockpit { position: absolute; z-index: 13; right: 50%; bottom: 14px; width: 480px;');
    expect(css).toContain('.compactControlLabel { display: none; }');
    expect(css).toContain('.wideControlLabel { display: none; }');
    expect(css).toContain('.compactControlLabel { display: inline; }');
    expect(css).toContain('.mapStatus .statusSeparator, .mapStatus .statusMeta { display: none; }');
    expect(css).toContain('background: rgba(255, 253, 247, .9);');
    expect(css).toContain('.periodRail button { flex: 1 0 52px; min-width: 52px; height: var(--control-height); padding: var(--space-1); border: 0; border-radius: var(--radius-inner); background: transparent; color: var(--muted); scroll-margin-inline: 28px;');
    expect(css).toContain('.toolbarTrigger { display: flex; align-items: center; justify-content: center;');
    expect(css).not.toContain('.cockpitMain');
    expect(css).not.toContain('.cockpitMeta');
    expect(css).toContain('.maplibregl-ctrl-top-left, .maplibregl-ctrl-top-right { display: none !important; }');
    expect(css).toContain('.wordmark, .sampleFlag { display: none; }');
    expect(css).not.toContain('.legendInstrument');
    expect(css).not.toContain('.legendBands');
    expect(css).not.toContain('.mapAction');
  });
});

const legend: WeatherManifest['legend'] = [
  { label: 'Awful', color: '#343b53', min: 0, max: 24 },
  { label: 'Poor', color: '#5e6382', min: 25, max: 39 },
  { label: 'Mixed', color: '#97949c', min: 40, max: 54 },
  { label: 'Good', color: '#c9a47f', min: 55, max: 69 },
  { label: 'Very good', color: '#e18952', min: 70, max: 84 },
  { label: 'Excellent', color: '#f06031', min: 85, max: 100 },
];
