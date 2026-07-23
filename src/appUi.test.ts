import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { bestWeatherAvailability, BestWeatherResults, BestWeatherTrigger, climateFieldFor, climateRailPeriods, climateSummary, climateUiModel, ClimateDetails, clearSearchAfterMapSelection, dayCountLabel, forecastMapTiles, forecastMapUsesDetail, forecastOutcomeCopy, forecastRangeMeta, LayerLegend, LENSES, nearbyClimateCellQualifier, periodRailHasMore, periodStatus, placeSearchPrompt, PointHeadingFor, pointWithinActiveSearchCoverage, pointWithinBounds, rainDisplay, RangeDetails, rangeLensAsset, revealFocusedPeriod, revealSelectedPeriod, scalarPalette, ScoreRamp, temperatureRange, topBestWeatherRecommendation, wetDaytimeDuration } from './App';
import type { BestWeatherPlace } from './data/bestWeather';
import type { ClimateIndex, ClimatePointEvidence } from './data/climateClient';
import { forecastTierForPoint, type DayForecastProduct } from './data/forecastClient';
import type { DailyPoint, ForecastWindowPoint, WeatherImage, WeatherManifest } from './data/types';

const detailGrid = { west: -24, east: 45, south: 29, north: 71.5, rows: 680, columns: 1104 };
const detailPointBlock = {
  row: 5, column: 8, rows: 64, columns: 64, omitted: false,
  path: 'blocks/5-8.nwf', bytes: 123_200, sha256: '0'.repeat(64), payloadSha256: '0'.repeat(64),
  mediaType: 'application/vnd.nice-weather.forecast-block',
};

describe('map controls', () => {
  it('keeps global views cheap and enables detail only inside the viewport budget', () => {
    const product = {
      tiers: [
        { tier: 'europe-detail', grid: detailGrid, pointBlocks: [detailPointBlock], scalarCoverage: [{ zoom: 5, x: 16, y: 10, omitted: false }] },
        { tier: 'global', scalarCoverage: [{ zoom: 3, x: 0, y: 0, omitted: false }] },
      ],
    } as unknown as DayForecastProduct;
    const world = { west: -180, east: 180, south: -85, north: 85 };
    const europe = { west: -15, east: 30, south: 35, north: 60 };
    const ultrawide = { west: -120, east: 120, south: -60, north: 80 };

    expect(forecastMapUsesDetail(product, world, 1.35)).toBe(false);
    expect(new Set(forecastMapTiles(product, world, 1.35).map(({ id }) => `${id.z}/${id.x}/${id.y}`)).size).toBe(64);
    expect(forecastTierForPoint(product, 10, 50).tier).toBe('europe-detail');
    expect(forecastMapUsesDetail(product, europe, 5)).toBe(true);
    expect(forecastMapTiles(product, europe, 5).every(({ id }) => id.z === 5)).toBe(true);
    expect(forecastMapTiles(product, europe, 5)).toHaveLength(20);
    expect(forecastTierForPoint(product, 10, 50).tier).toBe('europe-detail');
    expect(forecastMapUsesDetail(product, ultrawide, 4)).toBe(false);
    expect(forecastMapTiles(product, ultrawide, 4).every(({ id }) => id.z === 3)).toBe(true);
    expect(forecastMapTiles(product, ultrawide, 4).length).toBeLessThanOrEqual(64);
  });

  it('keeps a selected European judgement independent of the map texture budget', () => {
    const product = {
      tiers: [
        { tier: 'europe-detail', grid: detailGrid, pointBlocks: [detailPointBlock], scalarCoverage: [{ zoom: 5, x: 16, y: 10, omitted: false }] },
        { tier: 'global', scalarCoverage: [{ zoom: 3, x: 0, y: 0, omitted: false }] },
      ],
    } as unknown as DayForecastProduct;
    const worldwide = { west: -180, east: 180, south: -85, north: 85 };
    const scoreByTier = { global: 7, 'europe-detail': 89 } as const;

    expect(forecastMapUsesDetail(product, worldwide, 1.35)).toBe(false);
    expect(forecastMapTiles(product, worldwide, 1.35).every(({ id }) => id.z === 3)).toBe(true);
    expect(scoreByTier[forecastTierForPoint(product, 10, 50).tier]).toBe(89);
  });

  it('keeps Overall one click away in the complete five-lens instrument', () => {
    expect(LENSES).toEqual([
      { id: 'overall', label: 'Overall score' },
      { id: 'comfort', label: 'Comfort' },
      { id: 'dry', label: 'Dry' },
      { id: 'bright', label: 'Clear sky' },
      { id: 'wind', label: 'Calm' },
    ]);

    const markup = renderToStaticMarkup(createElement(LayerLegend, {
      manifest: { palette: [{ score: 0, color: '#000' }, { score: 100, color: '#fff' }], legend, forecast: { days: [], window: { resolutionKm: 28 } } } as unknown as WeatherManifest,
      view: 'range',
      profile: 'nice',
      lens: 'comfort',
      panel: 'lenses',
      onPanelChange: vi.fn(),
      onChange: vi.fn(),
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
      view: 'range', profile: 'nice', lens: 'overall', panel: 'lenses',
      onPanelChange: vi.fn(), onChange: vi.fn(),
    }));

    expect(markup).toContain('Warm &amp; sunny');
    expect(markup).toContain('Comfort 45% · Dry 25% · Clear sky 20% · Calm 10%');
    expect(markup).toContain('Comfort 35% · Dry 30% · Clear sky 10% · Calm 25%');
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

  it('reveals the selected month immediately after its rail width changes', () => {
    const scrollIntoView = vi.fn();
    const container = {
      querySelectorAll: () => [{ dataset: { periodId: 'month-12' }, scrollIntoView }],
    } as unknown as Pick<HTMLElement, 'querySelectorAll'>;

    revealSelectedPeriod(container, 'month-12', 'auto');

    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: 'auto', block: 'nearest', inline: 'center' });
    const app = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8');
    expect(app).toContain('const observer = new ResizeObserver(() => {');
    expect(app).toContain("revealSelectedPeriod(container, selected.current, 'auto');");
  });

  it('keeps the score direction visible without opening score theory', () => {
    const markup = renderToStaticMarkup(createElement(ScoreRamp, {
      manifest: { palette: [{ score: 0, color: '#000000' }, { score: 100, color: '#ffffff' }] } as WeatherManifest,
      profile: 'nice',
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

  it('states the forecast and climate place-search scopes', () => {
    expect(placeSearchPrompt('range')).toBe('Search places');
    expect(placeSearchPrompt('daily')).toBe('Search anywhere');
    expect(placeSearchPrompt('climate')).toBe('Search anywhere');
  });

  it('clears worldwide results before scheduling a worldwide search', () => {
    const app = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8');
    const clear = app.indexOf("    setResults([]);\n    setStatus('');\n    const timer = window.setTimeout");
    const worldwideSearch = app.indexOf('search.search(query, { viewport: viewportRef.current })');

    expect(clear).toBeGreaterThan(-1);
    expect(worldwideSearch).toBeGreaterThan(clear);
    expect(app).not.toContain('bounds: searchBounds');
  });

  it('opens restored-focus search interactions from pointer and input events', () => {
    const app = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8');

    expect(app).toContain('onPointerDown={engageInput}');
    expect(app).toContain('onChange={(event) => {\n        engageInput();\n        setQuery(event.target.value);');
    expect(app).toContain('if (document.activeElement === input.current) setFocused(true);');
    expect(app).toContain("telemetry('search-results', { count: matches.length, inputActive: document.activeElement === input.current });");
  });

  it('accepts worldwide forecast coordinate and current-location points', () => {
    const europe: [number, number, number, number] = [-24, 29, 45, 71.5];
    const málaga = { lat: 36.7, lon: -4.4 };
    const singapore = { lat: 1.3, lon: 103.8 };

    expect(pointWithinActiveSearchCoverage(málaga, 'range', europe)).toBe(true);
    expect(pointWithinActiveSearchCoverage(singapore, 'daily', europe)).toBe(false);
    expect(pointWithinActiveSearchCoverage(singapore, 'range')).toBe(true);
    expect(pointWithinActiveSearchCoverage(singapore, 'climate', europe)).toBe(true);
    expect(pointWithinActiveSearchCoverage(singapore, 'climate')).toBe(true);

    const app = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8');
    expect(app.match(/pointWithinActiveSearchCoverage\(/g)).toHaveLength(1);
    expect(app).not.toContain('activeSearchCoverage.current');
    expect(app).not.toContain('Current location is outside the forecast area.');
  });

  it('clears a forecast-area location notice before switching to Climate', () => {
    const app = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8');
    const handleView = app.indexOf('const handleView = (next: View) => {');
    const clearNotice = app.indexOf("setMapNotice('');", handleView);
    const setView = app.indexOf('setView(next);', handleView);

    expect(handleView).toBeGreaterThan(-1);
    expect(clearNotice).toBeGreaterThan(handleView);
    expect(setView).toBeGreaterThan(clearNotice);
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
    const climate = climateUiModel(climateIndex('preview', ['month-11']));
    expect(periodStatus(manifest, 'climate', undefined, climate)).toEqual({ primary: 'Climate preview', meta: '2006–2025 · worldwide', stale: false });
  });

  it('never presents a partial climate index as a typical year', () => {
    const preview = climateUiModel(climateIndex('preview', ['month-11']));
    expect(preview).toMatchObject({ available: true, complete: false, label: 'November climate', compactLabel: 'November', referencePeriod: '2006–2025' });
    expect(preview.periods).toEqual([{ id: 'month-11', shortLabel: 'Nov' }]);
    expect(climateRailPeriods(preview)).toEqual([]);
    expect(preview.label).not.toBe('Typical year');

    const complete = climateUiModel(climateIndex('complete', Array.from({ length: 12 }, (_, index) => `month-${String(index + 1).padStart(2, '0')}`)));
    expect(complete).toMatchObject({ available: true, complete: true, label: 'Typical year', referencePeriod: '2006–2025' });
    expect(complete.periods).toHaveLength(12);
    expect(climateRailPeriods(complete)).toHaveLength(12);
  });

  it('maps each climate lens to one canonical probability field', () => {
    expect(climateFieldFor('overall', 'nice')).toBe('nice_good');
    expect(climateFieldFor('overall', 'cycling')).toBe('cycling_good');
    expect(climateFieldFor('comfort', 'cycling')).toBe('comfort_good');
    expect(climateFieldFor('dry', 'nice')).toBe('dry_good');
    expect(climateFieldFor('bright', 'nice')).toBe('bright_good');
    expect(climateFieldFor('wind', 'nice')).toBe('nice_calm_good');
    expect(climateFieldFor('wind', 'cycling')).toBe('cycling_calm_good');
  });

  it('retains only selections that the Europe forecast can explain', () => {
    const bounds: [number, number, number, number] = [-25, 30, 45, 72];
    expect(pointWithinBounds({ lon: -4.4, lat: 36.7 }, bounds)).toBe(true);
    expect(pointWithinBounds({ lon: 103.8, lat: 1.3 }, bounds)).toBe(false);
  });

  it('uses one continuous 101-value palette and a profile-aware probability legend', () => {
    const manifest = { palette: [{ score: 0, color: '#343b53' }, { score: 100, color: '#f0b83f' }] } as WeatherManifest;
    const palette = scalarPalette(manifest);
    expect(palette).toHaveLength(101);
    expect(palette[0]).toEqual([52, 59, 83, 255]);
    expect(palette[50]).toEqual([146, 122, 73, 255]);
    expect(palette[100]).toEqual([240, 184, 63, 255]);
    const nice = renderToStaticMarkup(createElement(ScoreRamp, { manifest, probability: true, profile: 'nice' }));
    expect(nice).toContain('aria-label="Climate colours run from Rarely nice to Usually nice"');
    expect(nice).toContain('<b>Rarely nice</b>');
    expect(nice).toContain('<b>Usually nice</b>');
    const cycling = renderToStaticMarkup(createElement(ScoreRamp, { manifest, probability: true, profile: 'cycling' }));
    expect(cycling).toContain('aria-label="Climate colours run from Rarely good for cycling to Usually good for cycling"');
    expect(cycling).toContain('<b>Rarely good</b>');
    expect(cycling).toContain('<b>Usually good</b>');
  });

  it('shows profile-aware distribution evidence and all four delivered components', () => {
    const data = climateEvidence();
    const nice = renderToStaticMarkup(createElement(ClimateDetails, {
      data, point: { lat: 36.72, lon: -4.42 }, profile: 'nice', lens: 'overall', onLensChange: vi.fn(), climateModel: climateUiModel(climateIndex('preview', ['month-11'])),
    }));
    expect(climateSummary(data, 'nice')).toBe('68% nice · 9% awful · median 71');
    for (const copy of ['Nice days', 'Awful days', 'Chance of 3 poor days in a row', 'Median score', 'Comfort', 'Dry', 'Clear sky', 'Calm']) expect(nice).toContain(copy);
    for (const value of ['77%', '82%', '74%', '61%']) expect(nice).toContain(value);
    expect(nice.match(/aria-pressed=/g)).toHaveLength(4);
    expect(nice).toContain('12.3%');
    expect(nice).toContain('Recent daily climate · 2006–2025 · worldwide · about 30 km weather detail');
    expect(nice).not.toMatch(/monthly average|temperature|rain/i);

    const cycling = renderToStaticMarkup(createElement(ClimateDetails, {
      data, point: { lat: 36.72, lon: -4.42 }, profile: 'cycling', lens: 'wind', onLensChange: vi.fn(), climateModel: climateUiModel(climateIndex('preview', ['month-11'])),
    }));
    expect(climateSummary(data, 'cycling')).toBe('59% good for cycling · 13% awful · median 64');
    expect(cycling).toContain('<dt>Good cycling days</dt>');
    expect(cycling).toContain('59% good for cycling · 13% awful · median 64');
    expect(cycling).toContain('<strong>Calm</strong>');
    expect(cycling).toContain('<em>52%</em>');
    expect(cycling).toContain('aria-pressed="true"');
  });

  it('qualifies adjacent climate evidence with the real source distance only', () => {
    const exact = climateEvidence();
    expect(nearbyClimateCellQualifier(exact, { lat: 37.2, lon: -7.31667 })).toBe('');

    const adjacent = { ...exact, sourceCell: { latitude: 37.2, longitude: -7.4 }, usedAdjacentCell: true };
    expect(nearbyClimateCellQualifier(adjacent, { lat: 37.2, lon: -7.31667 })).toBe(' · nearest climate cell 7 km away');
    const rendered = renderToStaticMarkup(createElement(ClimateDetails, {
      data: adjacent, point: { lat: 37.2, lon: -7.31667 }, profile: 'nice', lens: 'overall', onLensChange: vi.fn(), climateModel: climateUiModel(climateIndex('preview', ['month-11'])),
    }));
    expect(rendered).toContain('about 30 km weather detail · nearest climate cell 7 km away');
  });

  it('names the overall climate lens for the selected profile', () => {
    const props = {
      manifest: { palette: [{ score: 0, color: '#000' }, { score: 100, color: '#fff' }], legend, forecast: { days: [], window: { resolutionKm: 28 } } } as unknown as WeatherManifest,
      view: 'climate' as const, lens: 'overall' as const, panel: 'lenses' as const,
      onPanelChange: vi.fn(), onChange: vi.fn(),
    };
    const nice = renderToStaticMarkup(createElement(LayerLegend, { ...props, profile: 'nice' }));
    expect(nice).toContain('aria-label="Choose map lens and explain scores. Active: Nice days"');
    expect(nice).toContain('>Nice days<');
    const cycling = renderToStaticMarkup(createElement(LayerLegend, { ...props, profile: 'cycling' }));
    expect(cycling).toContain('aria-label="Choose map lens and explain scores. Active: Good cycling days"');
    expect(cycling).toContain('>Good cycling days<');
    expect(cycling).toContain('Chance of 3 poor days in a row means a seven-day window contains at least three consecutive Poor or Awful days.');
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

  it('shows the current top recommendation before interaction and refreshes it by profile', () => {
    const place = (name: string, score: number): BestWeatherPlace => ({
      geonameId: score, name, country: 'Spain', countryCode: 'ES', latitude: 36.7, longitude: -4.4, score, niceDays: 10,
    });
    const nice = { status: 'ready', profile: 'nice', results: [place('Málaga', 82)] } as const;
    const cycling = { status: 'ready', profile: 'cycling', results: [place('Girona', 76)] } as const;
    expect(topBestWeatherRecommendation(nice, 'nice', true, '', false)).toMatchObject({ name: 'Málaga', score: 82 });
    expect(topBestWeatherRecommendation(cycling, 'cycling', true, '', false)).toMatchObject({ name: 'Girona', score: 76 });
    expect(topBestWeatherRecommendation(nice, 'cycling', true, '', false)).toBeUndefined();
    expect(topBestWeatherRecommendation(cycling, 'nice', true, '', false)).toBeUndefined();
    expect(topBestWeatherRecommendation(nice, 'nice', false, '', false)).toBeUndefined();
    expect(topBestWeatherRecommendation(nice, 'nice', true, 'Mal', false)).toBeUndefined();
    expect(topBestWeatherRecommendation(nice, 'nice', true, '', true)).toBeUndefined();

    const trigger = renderToStaticMarkup(createElement(BestWeatherTrigger, { recommendation: place('Málaga', 82), onOpen: vi.fn() }));
    expect(trigger).toContain('<small><span class="recommendationPrefix">Best · </span>Spain</small><strong>Málaga</strong>');
    expect(trigger).toContain('aria-label="Best weather: Málaga, Spain, score 82. Show all six recommendations."');

    const niceResults = renderToStaticMarkup(createElement(BestWeatherResults, { state: nice, onChoose: vi.fn() }));
    expect(niceResults).toContain('10/14 nice');
    expect(niceResults).toContain('10 of 14 nice');
    const cyclingResults = renderToStaticMarkup(createElement(BestWeatherResults, { state: cycling, onChoose: vi.fn() }));
    expect(cyclingResults).toContain('Best for cycling');
    expect(cyclingResults).toContain('10/14 cycling');
    expect(cyclingResults).toContain('10 of 14 good for cycling');
    expect(forecastOutcomeCopy('nice', 9)).toEqual({ daysLabel: 'Nice days', summaryLabel: '9 of 14 nice' });
    expect(forecastOutcomeCopy('cycling', 9)).toEqual({ daysLabel: 'Good cycling days', summaryLabel: '9 of 14 good for cycling' });

    const app = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8');
    expect(app).toContain('}, [bestWeatherAllowed, forecast, profile]);');
    expect(app).toContain('onOpen={openBestWeather}');
    expect(app).toContain('Show all six recommendations.');
  });

  it('labels forecast headline scores as Overall', () => {
    const markup = renderToStaticMarkup(createElement(PointHeadingFor, {
      data: { kind: 'daily', periodLabel: 'Wed 22 Jul', source: { basis: 'forecast' }, score: 82, category: 'Excellent' } as DailyPoint,
      point: { lat: 36.72, lon: -4.42, label: 'Málaga' },
    }));
    expect(markup).toContain('<strong>82</strong><span>Overall · Excellent</span>');
  });

  it('uses profile-correct language in the 14-day place evidence', () => {
    const component = { value: 72, category: 'Good' };
    const data = {
      releaseId: 'forecast-test', lat: 36.72, lon: -4.42, periodLabel: 'Next 14 days', score: 72, category: 'Good',
      kind: 'range',
      components: { comfort: component, dryness: component, sun: component, wind: component },
      window: { startDate: '2026-07-22', endDate: '2026-08-04', dayCount: 14, niceDays: 9, awfulDays: 1, medianScore: 72, longestPoorRun: 1 },
      days: [{ date: '2026-07-22', score: 72, category: 'Good' }],
      source: { basis: 'forecast-window', detailLabel: 'Worldwide forecast', resolutionKm: 28, artifactGeneratedAt: '2026-07-22T00:00:00Z' },
    } satisfies ForecastWindowPoint;
    const nice = renderToStaticMarkup(createElement(RangeDetails, { data, legend, profile: 'nice' }));
    expect(nice).toContain('<dt>Nice days</dt>');
    const cycling = renderToStaticMarkup(createElement(RangeDetails, { data, legend, profile: 'cycling' }));
    expect(cycling).toContain('<dt>Good cycling days</dt>');
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

  it('gives open discovery the phone viewport without changing desktop surfaces', () => {
    const css = readFileSync(new URL('./styles.css', import.meta.url), 'utf8');
    const phoneStart = css.indexOf('@media (max-width: 680px)');
    const narrowPhoneStart = css.indexOf('@media (max-width: 420px)');
    const desktop = css.slice(0, phoneStart);
    const phone = css.slice(phoneStart, narrowPhoneStart);
    const discoveryRule = '.appShell:has(.searchPopover) .pointPanel,\n  .appShell:has(.searchPopover) .cockpit { display: none; }';

    expect(phoneStart).toBeGreaterThan(-1);
    expect(narrowPhoneStart).toBeGreaterThan(phoneStart);
    expect(phone).toContain(discoveryRule);
    expect(desktop).not.toContain(discoveryRule);
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

function climateIndex(state: ClimateIndex['state'], periods: string[]): ClimateIndex {
  return {
    version: 1,
    indexId: 'climate-index',
    state,
    referencePeriod: { firstYear: 2006, lastYear: 2025 },
    provenance: {
      sourceEvidenceSha256: 'a'.repeat(64),
      landMaskSetSha256: 'b'.repeat(64),
      derivationContract: 'agera5-v2.0|daily-metrics-v1|extrema-order-v1|weather-score-v2|climate-distribution-v1|natural-earth-5.1.1-centre-land|2006-2025',
    },
    months: periods.map((period) => ({ period, releaseId: `release-${period}`, catalogSha256: 'c'.repeat(64) })),
  } as ClimateIndex;
}

function climateEvidence(): ClimatePointEvidence {
  const profile = (goodOrBetterPercent: number, awfulPercent: number, medianScore: number, calmGoodPercent: number) => ({
    medianScore,
    calmGoodPercent,
    poorWeekPermille: 123,
    ratingShares: { awful: awfulPercent * 10, poor: 120, mixed: 110, good: 250, veryGood: 230, excellent: 200 },
    goodOrBetterPermille: goodOrBetterPercent * 10,
    goodOrBetterPercent,
    awfulPermille: awfulPercent * 10,
    awfulPercent,
  });
  return {
    releaseId: 'climate-month-11',
    period: 'month-11',
    sourceCell: { latitude: 36.7, longitude: -4.4 },
    usedAdjacentCell: false,
    comfortGoodPercent: 77,
    dryGoodPercent: 82,
    brightGoodPercent: 74,
    nice: profile(68, 9, 71, 61),
    cycling: profile(59, 13, 64, 52),
  };
}
