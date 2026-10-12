import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { BestWeatherResults, climateFieldFor, climateRailPeriods, climateReliability, climateUiModel, ClimateDetails, ClimatePointHeading, clearSearchAfterMapSelection, dayCountLabel, forecastMapTiles, forecastMapUsesDetail, forecastOutcomeCopy, nearbyClimateCellQualifier, periodRailHasMore, placeSearchPrompt, PointHeadingFor, rainDisplay, RangeDetails, revealFocusedPeriod, revealSelectedPeriod, scalarPalette, ScoreRamp, temperatureRange, wetDaytimeDuration } from './App';
import type { BestWeatherPlace } from './data/bestWeather';
import type { ClimateIndex, ClimatePointEvidence } from './data/climateClient';
import { forecastTierForPoint, type DayForecastProduct } from './data/forecastClient';
import type { DailyPoint, ForecastWindowPoint } from './data/types';
import { WEATHER_PRESENTATION } from './weatherPresentation';

const detailGrid = { west: -24, east: 45, south: 29, north: 71.5, rows: 680, columns: 1104 };
describe('map controls', () => {

  it('keeps global views cheap and enables detail only inside the viewport budget', () => {
    const product = {
      tiers: [
        { tier: 'europe-detail', scalarMaxZoom:5, dailyPointBlocks:[{zoom:5,x:67,y:43}], grid: detailGrid, scalarCoverage: [{ zoom: 5, x: 16, y: 10, omitted: false }] },
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
        { tier: 'europe-detail', scalarMaxZoom:5, dailyPointBlocks:[{zoom:5,x:67,y:43}], grid: detailGrid, scalarCoverage: [{ zoom: 5, x: 16, y: 10, omitted: false }] },
        { tier: 'global', scalarCoverage: [{ zoom: 3, x: 0, y: 0, omitted: false }] },
      ],
    } as unknown as DayForecastProduct;
    const worldwide = { west: -180, east: 180, south: -85, north: 85 };
    const scoreByTier = { global: 7, 'europe-detail': 89 } as const;

    expect(forecastMapUsesDetail(product, worldwide, 1.35)).toBe(false);
    expect(forecastMapTiles(product, worldwide, 1.35).every(({ id }) => id.z === 3)).toBe(true);
    expect(scoreByTier[forecastTierForPoint(product, 10, 50).tier]).toBe(89);
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
  });

  it('keeps the score direction visible without opening score theory', () => {
    const markup = renderToStaticMarkup(createElement(ScoreRamp, {
      manifest: WEATHER_PRESENTATION,
      profile: 'nice',
    }));
    expect(markup).toContain('Weather suitability colours');
    expect(markup).toContain('Awful</span>');
    expect(markup).toContain('Excellent</span>');
    expect(markup).toContain('No data</span>');
  });

  it('shows the rail cue only while more periods continue to the right', () => {
    expect(periodRailHasMore({ scrollWidth: 720, clientWidth: 320, scrollLeft: 0 })).toBe(true);
    expect(periodRailHasMore({ scrollWidth: 720, clientWidth: 320, scrollLeft: 400 })).toBe(false);
  });
});

describe('clear weather copy', () => {

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

  it('exposes only the complete twelve-month climate product', () => {
    const complete = climateUiModel(climateIndex());
    expect(complete).toMatchObject({ available: true, complete: true, label: 'Typical year', referencePeriod: '2006–2025' });
    expect(complete.periods).toHaveLength(12);
    expect(climateRailPeriods(complete)).toHaveLength(12);
  });

  it('maps each climate lens to one canonical probability field', () => {
    expect(climateFieldFor('overall', 'nice')).toBe('nice_good');
    expect(climateFieldFor('overall', 'cycling')).toBe('cycling_good');
    expect(climateFieldFor('comfort', 'cycling')).toBe('comfort_good');
    expect(climateFieldFor('dry', 'nice')).toBe('dry_good');
    expect(climateFieldFor('bright', 'nice')).toBe('clear_sky_good');
    expect(climateFieldFor('wind', 'nice')).toBe('nice_calm_good');
    expect(climateFieldFor('wind', 'cycling')).toBe('cycling_calm_good');
  });

  it('shows the seasonal answer, reliability and physical evidence without a duplicate stat grid', () => {
    const data = climateEvidence();
    const climateModel = climateUiModel(climateIndex());
    const niceHeading = renderToStaticMarkup(createElement(ClimatePointHeading, { data, point: { lat: 36.72, lon: -4.42, label: 'Málaga' }, profile: 'nice', climateModel }));
    expect(niceHeading).toContain('Real November days · 2006–2025');
    expect(niceHeading).toContain('<strong>68%</strong><span>Chance of a nice November day</span>');
    const nice = renderToStaticMarkup(createElement(ClimateDetails, {
      data, point: { lat: 36.72, lon: -4.42 }, profile: 'nice', lens: 'overall', onLensChange: vi.fn(), climateModel,
    }));
    expect(climateReliability(data, 'nice')).toBe('9% awful · 12.3% chance of 3 poor days in a 7-day window');
    for (const copy of ['Comfort', 'Dry', 'Clear sky', 'Calm']) expect(nice).toContain(copy);
    for (const value of ['77%', '82%', '74%', '61%']) expect(nice).toContain(value);
    for (const evidence of [
      'Daytime temperature was 17–27°C on 80% of days · dew point 12°C typical · 90% at or below 17°C · muggy 5%',
      'Rain-free days 82% · 90% had 4.2 mm or less',
      'Cloud cover 32% typical · clear-sky days 74%',
      'Wind 9 km/h typical · 90% at or below 21 km/h',
    ]) expect(nice).toContain(evidence);
    expect(nice.match(/aria-pressed=/g)).toHaveLength(4);
    expect(nice.match(/<button/g)).toHaveLength(4);
    expect(nice).toContain('Real daily evidence · 2006–2025 · worldwide · about 28 km weather detail');
    expect(nice).not.toMatch(/Median score|median 71|climateFacts|monthly average/);

    const cyclingHeading = renderToStaticMarkup(createElement(ClimatePointHeading, { data, point: { lat: 36.72, lon: -4.42, label: 'Málaga' }, profile: 'cycling', climateModel }));
    expect(cyclingHeading).toContain('<strong>59%</strong><span>Chance of a good cycling day in November</span>');
    const cycling = renderToStaticMarkup(createElement(ClimateDetails, {
      data, point: { lat: 36.72, lon: -4.42 }, profile: 'cycling', lens: 'wind', onLensChange: vi.fn(), climateModel,
    }));
    expect(climateReliability(data, 'cycling')).toBe('13% awful · 12.3% chance of 3 poor days in a 7-day window');
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
      data: adjacent, point: { lat: 37.2, lon: -7.31667 }, profile: 'nice', lens: 'overall', onLensChange: vi.fn(), climateModel: climateUiModel(climateIndex()),
    }));
    expect(rendered).toContain('about 28 km weather detail · nearest climate cell 7 km away');
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
    const nice = renderToStaticMarkup(createElement(PointHeadingFor, { data, point: { lat: 36.72, lon: -4.42 }, profile: 'nice' }));
    expect(nice).toContain('nice days</span>');
    const cycling = renderToStaticMarkup(createElement(PointHeadingFor, { data, point: { lat: 36.72, lon: -4.42 }, profile: 'cycling' }));
    expect(cycling).toContain('cycling days</span>');
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

const legend = WEATHER_PRESENTATION.legend;

function climateIndex(): ClimateIndex {
  const periods = Array.from({ length: 12 }, (_, index) => `month-${String(index + 1).padStart(2, '0')}`);
  return {
    version: 2,
    indexId: 'climate-index',
    state: 'complete',
    referencePeriod: { firstYear: 2006, lastYear: 2025 },
    geometry: { projection: 'EPSG:4326', west: -180, east: 180, south: -90, north: 90, rows: 721, columns: 1440, cellResolutionDegrees: 0.25, cellRegistration: 'exact-era5-grid-point-centres' },
    source: {
      dataset: 'ERA5', kind: 'hourly-reanalysis-reduced-to-iana-civil-local-days', gridDegrees: 0.25,
      localDay: 'IANA civil local daytime 10:00–20:00, exact valid dates, 2006–2025',
      landAuthority: 'Natural Earth 5.1.1 source-cell-footprint intersection', landSourceSha256: 'a'.repeat(64),
      publicationAuthorityKind: 'checksum-trusted-publication-authority-v1',
      logicalCellCount: 2, datelineCellCount: 1,
      displayMaskAuthority: 'Natural Earth 5.1.1 centre-on-land at exact ERA5 0.25 degree grid points',
      displayMaskDerivation: 'test authority', displayMaskSha256: 'b'.repeat(64), displayMaskBytes: 1038240,
    },
    positiveDateline: { longitude: 180, meaning: 'independent +180 degree civil-day records; never alias to -180 degree records', canonicalColumns: 1440, overrideRecordBytes: 64 },
    probability: { unit: 'whole-percent', denominator: 'complete-real-days-in-reference-month', threshold: 'canonical-rating-good-or-better', goodMinimumScore: 55, noDataValue: 255, fields: ['comfort_good', 'dry_good', 'clear_sky_good', 'nice_good', 'nice_calm_good', 'cycling_good', 'cycling_calm_good'] },
    scalarSemantics: { maximumZoomMeaning: 'exact evaluated point value sampled only where the ERA5 grid-point centre is land', parentAggregation: 'rounded arithmetic mean of available four child texels; no-data excluded' },
    maximumZoom: 4,
    pointRecordBytes: 64,
    months: periods.map((period) => ({ period, releaseId: `release-${period}`, catalogSha256: 'c'.repeat(64) })),
  };
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
    clearSkyGoodPercent: 74,
    nice: profile(68, 9, 71, 61),
    cycling: profile(59, 13, 64, 52),
    physical: {
      daytimeTemperatureP10C: 17, daytimeTemperatureP50C: 22, daytimeTemperatureP90C: 27,
      daytimeMinimumP50C: 15, daytimeMaximumP50C: 29, dewPointP50C: 12, dewPointP90C: 17,
      relativeHumidityP50Percent: 60, muggyDayPercent: 5, rainFreeDayPercent: 82, rainP90MM: 4.2,
      cloudCoverP50Percent: 32, clearSkyDayPercent: 74, windP50KPH: 9, windP90KPH: 21, days: 600,
    },
  };
}
