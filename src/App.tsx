import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import { nextDetailsFocusIntent, restoreFocus, shouldFocusDetails, type DetailsFocusIntent } from './accessibility';
import { forecastRunStatus, isStale } from './data/api';
import { ClimateDataError, WorldwideClimateClient, type ClimateCatalog, type ClimateField, type ClimateIndex, type ClimatePointEvidence } from './data/climateClient';
import { ForecastDataError, WorldwideForecastClient, forecastDetailDeclared, type DayForecastProduct, type ForecastField, type ForecastSnapshot } from './data/forecastClient';
import { WorldwidePlaceSearch, type PlaceSearchResult, type PlaceSearchViewport } from './data/placeSearch';
import type { DailyPoint, Lens, Period, PointWeather, Profile, View, WeatherManifest } from './data/types';
import type { MapPoint, OverlayState, ScalarOverlay } from './map/WeatherMap';
import { coveringTiles, type GeographicBounds, type ScalarPaletteColour, type TileDraw } from './map/scalarTileLayer';
import { pointSourceLabel } from './presentation';
import { DRY_SCORE_NOTE, PROFILE_WEIGHT_NOTES, ratingBandLabel } from './scoreGuide';
import { reportResourceSummary, telemetry } from './telemetry';
import { WEATHER_PRESENTATION, type WeatherPresentation } from './weatherPresentation';
import './styles.css';

const WeatherMap = lazy(() => import('./map/WeatherMap').then((module) => ({ default: module.WeatherMap })));
const FORECAST_TEXTURE_BUDGET = 64;

type ClimateState =
  | { status: 'loading' | 'unavailable' }
  | { status: 'ready'; index: ClimateIndex };
type ClimateCatalogState =
  | { status: 'idle' | 'loading' | 'unavailable' }
  | { status: 'ready'; catalog: ClimateCatalog };
type ForecastState =
  | { status: 'loading' | 'unavailable' }
  | { status: 'ready'; snapshot: ForecastSnapshot };
type ForecastDayState =
  | { status: 'idle' | 'loading' | 'unavailable' }
  | { status: 'ready'; product: DayForecastProduct };
type PointState =
  | { status: 'idle' | 'loading' }
  | { status: 'ready'; kind: 'forecast'; data: PointWeather }
  | { status: 'ready'; kind: 'climate'; data: ClimatePointEvidence }
  | { status: 'error'; message: string };
type ControlPanel = 'when' | 'lenses' | 'profile' | null;
type PeriodChoice = Readonly<{ id: string; shortLabel: string; date?: string }>;

export default function App() {
  const [climateState, setClimateState] = useState<ClimateState>({ status: 'loading' });
  const [climateCatalogState, setClimateCatalogState] = useState<ClimateCatalogState>({ status: 'idle' });
  const [forecastState, setForecastState] = useState<ForecastState>({ status: 'loading' });
  const [forecastDayState, setForecastDayState] = useState<ForecastDayState>({ status: 'idle' });
  const [climateRetry, setClimateRetry] = useState(0);
  const [forecastRetry, setForecastRetry] = useState(0);
  const [view, setView] = useState<View>('range');
  const [profile, setProfile] = useState<Profile>('nice');
  const [lens, setLens] = useState<Lens>('overall');
  const [periodId, setPeriodId] = useState('');
  const [selectedPoint, setSelectedPoint] = useState<MapPoint>();
  const [focusPoint, setFocusPoint] = useState<(MapPoint & { token: number })>();
  const [viewport, setViewport] = useState<PlaceSearchViewport>({ latitude: 48.5, longitude: 9.5 });
  const [mapWeatherViewport, setMapWeatherViewport] = useState<{ bounds: GeographicBounds; zoom: number }>({
    bounds: { west: -180, east: 180, south: -85, north: 85 }, zoom: 3,
  });
  const [mapNotice, setMapNotice] = useState('');
  const [overlayState, setOverlayState] = useState<OverlayState>({ status: 'idle' });
  const [legendPanel, setLegendPanel] = useState<ControlPanel>(null);
  const [mapSelectionToken, setMapSelectionToken] = useState(0);
  const [pointState, setPointState] = useState<PointState>({ status: 'idle' });
  const [detailsFocusIntent, setDetailsFocusIntent] = useState<DetailsFocusIntent>({ token: 0, requested: false });
  const pointFocusOrigin = useRef<HTMLElement | null>(null);
  const staleReloadedClimateIndex = useRef<string | undefined>(undefined);
  const climateClient = useMemo(() => new WorldwideClimateClient(), []);
  const forecastClient = useMemo(() => new WorldwideForecastClient(), []);

  useEffect(() => {
    const controller = new AbortController();
    const startedAt = performance.now();
    setForecastState({ status: 'loading' });
    forecastClient.loadSnapshot(controller.signal)
      .then((snapshot) => {
        setForecastState({ status: 'ready', snapshot });
        telemetry('forecast-index-ready', {
          elapsedMs: Math.round(performance.now() - startedAt),
          releaseId: snapshot.index.indexId,
          days: snapshot.index.days.length,
        });
      })
      .catch((error: Error) => {
        if (controller.signal.aborted) return;
        telemetry('forecast-index-error', { code: error instanceof ForecastDataError ? error.code : 'unexpected', name: error.name });
        if (!(error instanceof ForecastDataError && error.code === 'unavailable')) console.warn('[nice-weather] Worldwide forecast unavailable.', error);
        setForecastState({ status: 'unavailable' });
      });
    return () => controller.abort();
  }, [forecastClient, forecastRetry]);

  useEffect(() => {
    const controller = new AbortController();
    const startedAt = performance.now();
    setClimateState({ status: 'loading' });
    climateClient.loadIndex(controller.signal)
      .then((index) => {
        if (staleReloadedClimateIndex.current && staleReloadedClimateIndex.current !== index.indexId && !staleReloadedClimateIndex.current.startsWith(`${index.indexId}->`)) staleReloadedClimateIndex.current = undefined;
        setClimateState({ status: 'ready', index });
        telemetry('climate-index-ready', { elapsedMs: Math.round(performance.now() - startedAt), state: index.state, months: index.months.length });
      })
      .catch((error: Error) => {
        if (controller.signal.aborted) return;
        if (!(error instanceof ClimateDataError && error.code === 'unavailable')) console.warn('[nice-weather] Climate index unavailable.', error);
        setClimateState({ status: 'unavailable' });
      });
    return () => controller.abort();
  }, [climateClient, climateRetry]);

  const manifest = WEATHER_PRESENTATION;
  const forecastPeriods = useMemo<readonly PeriodChoice[]>(() => forecastState.status === 'ready'
    ? forecastState.snapshot.index.days.map(({ date }) => ({ id: date, date, shortLabel: forecastDayLabel(date) }))
    : [], [forecastState]);
  const forecastPeriod = forecastPeriods.find((item) => item.id === periodId) ?? forecastPeriods[0];
  const climateModel = climateUiModel(climateState.status === 'ready' ? climateState.index : undefined);
  const climateIndexId = climateState.status === 'ready' ? climateState.index.indexId : undefined;
  const climatePeriodId = view === 'climate'
    ? climateModel.periods.find((item) => item.id === periodId)?.id ?? climateModel.periods[0]?.id
    : undefined;
  const expectedClimateReleaseId = climateState.status === 'ready'
    ? climateState.index.months.find((month) => month.period === climatePeriodId)?.releaseId
    : undefined;
  const climateRail = climateRailPeriods(climateModel);
  const pointProfileKey = view === 'climate' ? 'climate' : profile;
  const activePeriodId = view === 'range' ? 'next-14-days' : view === 'daily' ? forecastPeriod?.id : climatePeriodId;
  const forecastDetailEnabled = view === 'daily' && forecastDayState.status === 'ready'
    ? forecastMapUsesDetail(forecastDayState.product, mapWeatherViewport.bounds, mapWeatherViewport.zoom)
    : false;

  const refreshStaleClimate = useCallback((refreshKey: string) => {
    if (staleReloadedClimateIndex.current === refreshKey) return false;
    staleReloadedClimateIndex.current = refreshKey;
    setPointState({ status: 'idle' });
    setClimateCatalogState({ status: 'idle' });
    setClimateRetry((value) => value + 1);
    telemetry('climate-refresh', { refreshKey });
    return true;
  }, []);

  const refreshForecast = useCallback(() => {
    setPointState({ status: 'idle' });
    setForecastDayState({ status: 'idle' });
    setPeriodId('');
    setForecastRetry((value) => value + 1);
    telemetry('forecast-refresh', {});
  }, []);

  useEffect(() => {
    const next = view === 'climate' ? climatePeriodId : view === 'daily' ? forecastPeriod?.id : undefined;
    if (next && next !== periodId) setPeriodId(next);
  }, [climatePeriodId, forecastPeriod, periodId, view]);

  useEffect(() => {
    if (view !== 'climate' || !climatePeriodId) {
      setClimateCatalogState({ status: 'idle' });
      return;
    }
    const controller = new AbortController();
    const startedAt = performance.now();
    setClimateCatalogState({ status: 'loading' });
    climateClient.loadCatalog(climatePeriodId, controller.signal)
      .then((catalog) => {
        if (!catalog) {
          setClimateCatalogState({ status: 'unavailable' });
          return;
        }
        if (expectedClimateReleaseId && catalog.releaseId !== expectedClimateReleaseId) {
          if (climateIndexId && refreshStaleClimate(`${climateIndexId}->${catalog.releaseId}`)) return;
          setClimateCatalogState({ status: 'unavailable' });
          return;
        }
        if (climateIndexId && staleReloadedClimateIndex.current === climateIndexId) staleReloadedClimateIndex.current = undefined;
        setClimateCatalogState({ status: 'ready', catalog });
        telemetry('climate-catalog-ready', { elapsedMs: Math.round(performance.now() - startedAt), period: catalog.period, tiles: catalog.scalarTiles.length });
      })
      .catch((error: Error) => {
        if (controller.signal.aborted) return;
        if (error instanceof ClimateDataError && error.code === 'stale' && climateIndexId && refreshStaleClimate(climateIndexId)) return;
        if (!(error instanceof ClimateDataError && error.code === 'unavailable')) console.warn('[nice-weather] Climate month unavailable.', error);
        setClimateCatalogState({ status: 'unavailable' });
      });
    return () => controller.abort();
  }, [climateClient, climateIndexId, climatePeriodId, expectedClimateReleaseId, refreshStaleClimate, view]);

  useEffect(() => {
    if (view !== 'daily' || !forecastPeriod || forecastState.status !== 'ready') {
      setForecastDayState({ status: 'idle' });
      return;
    }
    const controller = new AbortController();
    const startedAt = performance.now();
    setForecastDayState({ status: 'loading' });
    forecastClient.loadDayCatalog(forecastState.snapshot, forecastPeriod.id, controller.signal)
      .then((product) => {
        setForecastDayState({ status: 'ready', product });
        telemetry('forecast-day-ready', {
          elapsedMs: Math.round(performance.now() - startedAt),
          date: product.date,
          tiers: product.tiers.map(({ tier }) => tier).join(','),
        });
      })
      .catch((error: Error) => {
        if (controller.signal.aborted) return;
        if (error instanceof ForecastDataError && error.code === 'stale') {
          refreshForecast();
          return;
        }
        console.warn('[nice-weather] Forecast day unavailable.', error);
        setForecastDayState({ status: 'unavailable' });
      });
    return () => controller.abort();
  }, [forecastClient, forecastPeriod, forecastState, refreshForecast, view]);

  useEffect(() => {
    if (!selectedPoint || !activePeriodId) {
      setPointState({ status: 'idle' });
      return;
    }
    const controller = new AbortController();
    setPointState({ status: 'loading' });
    if (view === 'climate') {
      if (climateCatalogState.status !== 'ready') return () => controller.abort();
      const startedAt = performance.now();
      const activeCatalog = climateCatalogState.catalog;
      climateClient.loadPoint(activePeriodId, selectedPoint.lon, selectedPoint.lat, controller.signal)
        .then((data) => {
          if (!data) {
            setPointState({ status: 'error', message: 'No recent climate record is available for this point.' });
            return;
          }
          if (data.releaseId !== activeCatalog.releaseId || data.period !== activeCatalog.period) {
            if (climateIndexId && refreshStaleClimate(`${climateIndexId}->${data.releaseId}`)) return;
            setPointState({ status: 'error', message: 'Climate data changed. Select this place again.' });
            return;
          }
          setPointState({ status: 'ready', kind: 'climate', data });
          telemetry('climate-point-ready', { elapsedMs: Math.round(performance.now() - startedAt), period: data.period });
        })
        .catch((error: Error) => {
          if (controller.signal.aborted) return;
          const stale = error instanceof ClimateDataError && error.code === 'stale';
          setPointState({ status: 'error', message: stale ? 'Climate data changed. Select this place again.' : 'Climate history is not available for this point.' });
          if (stale && climateIndexId) refreshStaleClimate(climateIndexId);
        });
      return () => controller.abort();
    }
    const dayProduct = forecastDayState.status === 'ready' ? forecastDayState.product : undefined;
    if (forecastState.status !== 'ready' || (view === 'daily' && !dayProduct)) {
      setPointState({ status: 'idle' });
      return () => controller.abort();
    }
    const presentation = { legend: manifest.legend, generatedAt: forecastState.snapshot.range.source.runUtc };
    let request: Promise<PointWeather>;
    if (view === 'range') {
      request = forecastClient.loadRangePoint(forecastState.snapshot, selectedPoint.lon, selectedPoint.lat, profile, presentation, controller.signal);
    } else {
      if (!dayProduct) return () => controller.abort();
      request = forecastClient.loadDailyPoint(dayProduct, selectedPoint.lon, selectedPoint.lat, profile, presentation, forecastDetailEnabled, controller.signal);
    }
    request
      .then((data) => {
        if (data.releaseId !== forecastState.snapshot.index.indexId) throw new ForecastDataError('stale');
        setPointState({ status: 'ready', kind: 'forecast', data });
      })
      .catch((error: Error) => {
        if (controller.signal.aborted) return;
        if (error instanceof ForecastDataError && error.code === 'stale') {
          refreshForecast();
          return;
        }
        setPointState({ status: 'error', message: error instanceof ForecastDataError ? error.message : 'Weather is not available for this point.' });
      });
    return () => controller.abort();
  }, [activePeriodId, climateCatalogState, climateClient, climateIndexId, forecastClient, forecastDayState, forecastDetailEnabled, forecastState, manifest, pointProfileKey, refreshForecast, refreshStaleClimate, selectedPoint, view]);

  const scalarOverlay = useMemo<ScalarOverlay | undefined>(() => {
    if (view === 'climate') {
      if (climateCatalogState.status !== 'ready') return undefined;
      const catalog = climateCatalogState.catalog;
      const field = climateFieldFor(lens, profile);
      const tiles = new Map(catalog.scalarTiles
        .filter((tile) => tile.field === field)
        .map((tile) => [`${tile.zoom}/${tile.x}/${tile.y}`, tile]));
      const tileFor = (tile: { z: number; x: number; y: number }) => tiles.get(`${tile.z}/${tile.x}/${tile.y}`);
      return {
        key: `climate:${catalog.releaseId}:${field}`,
        minSourceZoom: 0,
        maxSourceZoom: catalog.maximumZoom,
        palette: scalarPalette(manifest),
        urlForTile: (tile) => {
          const asset = tileFor(tile);
          return asset ? climateClient.scalarTileUrl(catalog, asset) : '';
        },
        sha256ForTile: (tile) => tileFor(tile)?.sha256 ?? '',
        tileAvailable: (tile) => tiles.has(`${tile.z}/${tile.x}/${tile.y}`),
      };
    }
    if (forecastState.status !== 'ready') return undefined;
    const product = view === 'range'
      ? forecastState.snapshot.range
      : forecastDayState.status === 'ready' ? forecastDayState.product : undefined;
    if (!product) return undefined;
    const field = forecastFieldFor(lens, profile);
    return {
      key: `forecast:${forecastState.snapshot.index.indexId}:${view}:${view === 'daily' ? forecastDayState.status === 'ready' ? forecastDayState.product.date : '' : 'range'}:${field}`,
      minSourceZoom: 0,
      maxSourceZoom: view === 'daily' ? 5 : 3,
      maxTextures: FORECAST_TEXTURE_BUDGET,
      palette: scalarPalette(manifest),
      urlForTile: () => '',
      sha256ForTile: () => '',
      tileAvailable: () => false,
      sourceZoomForMap: 'tiers' in product ? undefined : (mapZoom) => forecastClient.sourceZoomFor(product, mapZoom),
      visibleTilesForMap: 'tiers' in product ? (bounds, mapZoom) => forecastMapTiles(product, bounds, mapZoom) : undefined,
      sourceForTile: (tile) => forecastClient.scalarSourceForOutputTile(product, field, tile),
    };
  }, [climateCatalogState, climateClient, forecastClient, forecastDayState, forecastState, lens, manifest, profile, view]);

  const handleView = (next: View) => {
    if (next === 'climate' && !climateModel.available) return;
    setMapNotice('');
    telemetry('view-changed', { view: next });
    setView(next);
    setLegendPanel(null);
    setPeriodId('');
  };

  const selectPoint = (point: MapPoint, focus = true, focusDetails = false) => {
    setLegendPanel(null);
    setSelectedPoint(point);
    setFocusPoint(focus ? { ...point, token: Date.now() } : undefined);
    setDetailsFocusIntent((current) => nextDetailsFocusIntent(current, focusDetails));
    setMapNotice('');
    telemetry('point-selected', { lat: Number(point.lat.toFixed(3)), lon: Number(point.lon.toFixed(3)), labelled: Boolean(point.label) });
  };

  return (
    <main className={`appShell view-${view}${(view === 'daily' ? forecastPeriods.length > 0 : climateRail.length > 0) ? ' hasRail' : ''}${selectedPoint ? ' hasPoint' : ''}`}>
      <Suspense fallback={<div className="mapCanvas mapLoading" aria-label="Loading map" />}>
        <WeatherMap
          scalarOverlay={scalarOverlay}
          worldwide
          selectedPoint={selectedPoint}
          focusPoint={focusPoint}
          onSelect={(point, intent) => {
            pointFocusOrigin.current = intent?.focusDetails && document.activeElement instanceof HTMLElement ? document.activeElement : null;
            setMapSelectionToken((value) => value + 1);
            selectPoint(point, false, shouldFocusDetails(intent?.focusDetails ? 'map-keyboard' : 'map-pointer'));
          }}
          onViewportChange={({ latitude, longitude, zoom, bounds }) => {
            setViewport({ latitude, longitude });
            setMapWeatherViewport({ bounds, zoom });
          }}
          onReady={(elapsedMs) => telemetry('map-ready', { elapsedMs })}
          onOverlayState={(state) => {
            setOverlayState(state);
          }}
          onOverlayReady={(elapsedMs, renderer, assetUrl) => {
            telemetry('overlay-ready', { elapsedMs, renderer, assetUrl, period: activePeriodId, profile, lens });
            reportResourceSummary();
          }}
        />
      </Suspense>

      <header className="masthead">
        <div className="mastheadIdentity">
          <div className="wordmark" aria-label="Nice Weather"><SunMark /><span>Nice Weather</span></div>
          <PeriodStatus view={view} climate={climateModel} forecast={forecastState.status === 'ready' ? forecastState.snapshot : undefined} day={forecastDayState.status === 'ready' ? forecastDayState.product : undefined} />
        </div>
        <PlaceSearch viewport={viewport} view={view} clearToken={mapSelectionToken} onEngage={() => setLegendPanel(null)} onLocate={() => locate((point) => {
          setMapSelectionToken((value) => value + 1);
          selectPoint({ ...point, label: 'Current location' });
        }, setMapNotice)} onFound={(point, origin) => {
          pointFocusOrigin.current = origin;
          selectPoint(point, true, shouldFocusDetails('search'));
        }} />
      </header>

      {(mapNotice || overlayState.status === 'error' || (view === 'climate' && climateCatalogState.status === 'unavailable') || (view !== 'climate' && forecastState.status === 'unavailable')) && (
        <p className="controlSurface mapNotice" role="status">{mapNotice || (view === 'climate' ? 'Climate colours are not available right now.' : forecastState.status === 'unavailable' ? 'Forecast unavailable. Try again shortly.' : 'Weather colours could not be shown.')}</p>
      )}

      {selectedPoint && (
        <PointPanel
          state={pointState}
          point={selectedPoint}
          legend={manifest.legend}
          lens={lens}
          profile={profile}
          climateModel={climateModel}
          focusIntent={detailsFocusIntent}
          onLensChange={(next) => { setLens(next); telemetry('lens-changed', { lens: next, source: 'place-sheet' }); }}
          escapeEnabled={legendPanel === null}
          onClose={() => {
            setSelectedPoint(undefined);
            setPointState({ status: 'idle' });
            restoreFocus(pointFocusOrigin.current);
            pointFocusOrigin.current = null;
          }}
        />
      )}

      <section className="controlSurface cockpit" aria-label="Weather map controls">
        <div className="cockpitToolbar">
          <ViewControl value={view} climate={climateModel} panel={legendPanel} onPanelChange={setLegendPanel} onChange={handleView} />
          <LayerLegend
            manifest={manifest}
            view={view}
            lens={lens}
            panel={legendPanel}
            onPanelChange={setLegendPanel}
            onChange={(next) => { setLens(next); telemetry('lens-changed', { lens: next }); }}
          />
          <ProfileControl value={profile} panel={legendPanel} onPanelChange={setLegendPanel} onChange={(next) => { setProfile(next); telemetry('profile-changed', { profile: next }); }} />
        </div>
        {overlayState.status !== 'error' && <ScoreRamp manifest={manifest} probability={view === 'climate'} />}
        {view === 'daily' && forecastPeriod && <PeriodRail periods={forecastPeriods} selectedId={forecastPeriod.id} label="Forecast day" onChange={(id) => { setPeriodId(id); telemetry('period-changed', { period: id, view }); }} />}
        {view === 'climate' && climatePeriodId && climateRail.length > 0 && <PeriodRail periods={climateRail} selectedId={climatePeriodId} label="Climate month" onChange={(id) => { setPeriodId(id); telemetry('period-changed', { period: id, view }); }} />}
      </section>
    </main>
  );
}

export type ClimateUiModel = Readonly<{
  available: boolean;
  complete: boolean;
  label: string;
  compactLabel: string;
  referencePeriod: string;
  periods: readonly PeriodChoice[];
}>;

function ViewControl({ value, climate, panel, onPanelChange, onChange }: { value: View; climate: ClimateUiModel; panel: ControlPanel; onPanelChange: (panel: ControlPanel) => void; onChange: (value: View) => void }) {
  const trigger = useRef<HTMLButtonElement>(null);
  const open = panel === 'when';
  useEscape(open, () => { onPanelChange(null); restoreFocus(trigger.current); });
  const label = value === 'range' ? 'Next 14 days' : value === 'daily' ? 'Day by day' : climate.label;
  const choose = (next: View) => {
    onChange(next);
    onPanelChange(null);
    window.requestAnimationFrame(() => restoreFocus(trigger.current));
  };
  return (
    <div className="toolbarMenu viewControl">
      <button ref={trigger} type="button" className="toolbarTrigger" aria-expanded={open} aria-controls="weather-period-menu" aria-label={`Choose time period. Active: ${label}`} onClick={() => onPanelChange(open ? null : 'when')}>
        <strong><span className="wideControlLabel">{label}</span><span className="compactControlLabel">{value === 'range' ? '14 days' : value === 'daily' ? 'By day' : climate.compactLabel}</span></strong><MenuChevron />
      </button>
      {open && <div id="weather-period-menu" className="controlSurface toolbarPopover" role="group" aria-label="Weather period">
        <button type="button" aria-pressed={value === 'range'} onClick={() => choose('range')}><span>Next 14 days</span><MenuCheck selected={value === 'range'} /></button>
        <button type="button" aria-pressed={value === 'daily'} onClick={() => choose('daily')}><span>Day by day</span><MenuCheck selected={value === 'daily'} /></button>
        {climate.available && <button type="button" aria-pressed={value === 'climate'} onClick={() => choose('climate')}><span>{climate.label}</span><MenuCheck selected={value === 'climate'} /></button>}
      </div>}
    </div>
  );
}

function ProfileControl({ value, panel, onPanelChange, onChange }: { value: Profile; panel: ControlPanel; onPanelChange: (panel: ControlPanel) => void; onChange: (value: Profile) => void }) {
  const trigger = useRef<HTMLButtonElement>(null);
  const open = panel === 'profile';
  useEscape(open, () => { onPanelChange(null); restoreFocus(trigger.current); });
  const choose = (next: Profile) => {
    onChange(next);
    onPanelChange(null);
    window.requestAnimationFrame(() => restoreFocus(trigger.current));
  };
  return (
    <div className="toolbarMenu profileControl">
      <button ref={trigger} type="button" className="toolbarTrigger" aria-expanded={open} aria-controls="weather-profile-menu" aria-label={`Choose weather preference. Active: ${value === 'nice' ? 'Warm & sunny' : 'Cycling'}`} onClick={() => onPanelChange(open ? null : 'profile')}>
        <strong>{value === 'nice' ? <><span className="wideControlLabel">Warm &amp; sunny</span><span className="compactControlLabel">Warm + sunny</span></> : 'Cycling'}</strong><MenuChevron />
      </button>
      {open && <div id="weather-profile-menu" className="controlSurface toolbarPopover alignRight" role="group" aria-label="Weather preference">
        <button type="button" aria-pressed={value === 'nice'} onClick={() => choose('nice')}><span>Warm &amp; sunny</span><MenuCheck selected={value === 'nice'} /></button>
        <button type="button" aria-pressed={value === 'cycling'} onClick={() => choose('cycling')}><span>Cycling</span><MenuCheck selected={value === 'cycling'} /></button>
      </div>}
    </div>
  );
}

function PeriodRail({ periods, selectedId, label, onChange }: { periods: readonly PeriodChoice[]; selectedId: string; label: string; onChange: (id: string) => void }) {
  const rail = useRef<HTMLDivElement>(null);
  const [hasMore, setHasMore] = useState(false);
  const [hasBefore, setHasBefore] = useState(false);
  const updateCue = useCallback(() => {
    setHasMore(periodRailHasMore(rail.current));
    setHasBefore(Boolean(rail.current && rail.current.scrollLeft > 2));
  }, []);
  useEffect(() => {
    revealSelectedPeriod(rail.current, selectedId);
    const frame = window.requestAnimationFrame(updateCue);
    window.addEventListener('resize', updateCue);
    return () => { window.cancelAnimationFrame(frame); window.removeEventListener('resize', updateCue); };
  }, [periods, selectedId, updateCue]);
  return (
    <div className="periodRailWrap" data-has-more={hasMore || undefined} data-has-before={hasBefore || undefined}>
      <div ref={rail} className="periodRail" role="group" aria-label={label} onScroll={updateCue}>
        {periods.map((period, index) => (
          <button key={period.id} type="button" data-period-id={period.id} aria-pressed={selectedId === period.id} onFocus={(event) => revealFocusedPeriod(event.currentTarget)} onClick={() => onChange(period.id)}>
            <span>{period.shortLabel}</span>{period.date && <small>{railMonthLabel(periods, index)}</small>}
          </button>
        ))}
      </div>
    </div>
  );
}

export const LENSES: ReadonlyArray<{ id: Lens; label: string }> = [
  { id: 'overall', label: 'Overall score' }, { id: 'comfort', label: 'Comfort' }, { id: 'dry', label: 'Dry' }, { id: 'bright', label: 'Bright' }, { id: 'wind', label: 'Calm' },
];

type LayerLegendProps = {
  manifest: WeatherPresentation;
  view: View;
  lens: Lens;
  panel: ControlPanel;
  onPanelChange: (panel: ControlPanel) => void;
  onChange: (lens: Lens) => void;
};

export function LayerLegend({ manifest, view, lens, panel, onPanelChange, onChange }: LayerLegendProps) {
  const trigger = useRef<HTMLButtonElement>(null);
  const open = panel === 'lenses';
  useEscape(open, () => { onPanelChange(null); restoreFocus(trigger.current); });
  const active = lens === 'overall' && view === 'climate' ? 'Nice days' : LENSES.find((item) => item.id === lens)?.label ?? 'Overall score';
  return (
    <div className="legendControl">
      <button ref={trigger} type="button" className="toolbarTrigger lensTrigger" aria-haspopup="dialog" aria-expanded={open} aria-label={`Choose map lens and explain scores. Active: ${active}`} onClick={() => onPanelChange(open ? null : 'lenses')}>
        <strong><span className="wideControlLabel">{active}</span><span className="compactControlLabel">{lens === 'overall' ? 'Overall' : active}</span></strong><MenuChevron />
      </button>
      {open && (
        <div className="controlSurface legendPopover" role="dialog" aria-label="Map lens and score guide">
          <div className="layerMenu" role="group" aria-label="Map lens">
            {LENSES.map((item) => <button key={item.id} type="button" aria-pressed={lens === item.id} onClick={() => { onChange(item.id); onPanelChange(null); window.requestAnimationFrame(() => restoreFocus(trigger.current)); }}><span>{item.id === 'overall' && view === 'climate' ? 'Nice days' : item.label}</span><MenuCheck selected={lens === item.id} /></button>)}
          </div>
          <ScoreGuide manifest={manifest} climate={view === 'climate'} />
        </div>
      )}
    </div>
  );
}

function MenuChevron() {
  return <svg className="menuChevron" viewBox="0 0 12 8" aria-hidden="true"><path d="m1.5 1.5 4.5 4 4.5-4" /></svg>;
}

function MenuCheck({ selected }: { selected: boolean }) {
  return <span className="menuCheck" aria-hidden="true">{selected ? '✓' : ''}</span>;
}

function ScoreGuide({ manifest, climate }: { manifest: WeatherPresentation; climate: boolean }) {
  return (
    <details className="scoreGuide">
      <summary>How scores work</summary>
      <div className="scoreGuideBody">
        {climate ? <>
          <p>The map shows the percentage of real days rated Good or better from 2006 to 2025.</p>
          <p>Comfort, Dry and Bright show the same chance for each part. Calm uses the selected weather preference.</p>
        </> : <>
        <p>Overall score starts with four scores. Severe heat, cold, rain or wind can cap it.</p>
        <p><strong>Warm &amp; sunny</strong> · {PROFILE_WEIGHT_NOTES.nice}</p>
        <p><strong>Cycling</strong> · {PROFILE_WEIGHT_NOTES.cycling}</p>
        <p>{DRY_SCORE_NOTE}</p>
        <dl>{manifest.legend.map((category) => <div key={category.label}><dt><i style={{ background: category.color }} />{category.label}</dt><dd>{ratingBandLabel(category)}</dd></div>)}</dl>
        <p>Forecasts cover the world. Europe uses finer detail where that complete source coverage is available.</p>
        </>}
      </div>
    </details>
  );
}

export function ScoreRamp({ manifest, probability = false }: { manifest: WeatherPresentation; probability?: boolean }) {
  const gradient = `linear-gradient(90deg, ${manifest.palette.map((stop) => `${stop.color} ${stop.score}%`).join(', ')})`;
  return <div className="scoreRamp" aria-label={probability ? 'Climate colours run from Rarely nice to Usually nice' : 'Score colours run from Awful to Excellent'}>
    <b>{probability ? 'Rarely nice' : 'Awful'}</b><i style={{ background: gradient }} aria-hidden="true" /><b>{probability ? 'Usually nice' : 'Excellent'}</b>
  </div>;
}

function PlaceSearch({ viewport, view, clearToken, onEngage, onLocate, onFound }: { viewport: PlaceSearchViewport; view: View; clearToken: number; onEngage: () => void; onLocate: () => void; onFound: (point: MapPoint, origin: HTMLElement | null) => void }) {
  const search = useMemo(() => new WorldwidePlaceSearch(), []);
  const viewportRef = useRef(viewport);
  viewportRef.current = viewport;
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<PlaceSearchResult[]>([]);
  const [status, setStatus] = useState('');
  const [focused, setFocused] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const requestSequence = useRef(0);
  const blurTimer = useRef<number | undefined>(undefined);
  const open = focused && (results.length > 0 || Boolean(status));
  const prompt = placeSearchPrompt(view);
  const engageInput = () => {
    window.clearTimeout(blurTimer.current);
    blurTimer.current = undefined;
    setFocused(true);
  };

  useEffect(() => () => {
    search.clear();
    window.clearTimeout(blurTimer.current);
  }, [search]);
  useEffect(() => {
    clearSearchAfterMapSelection(clearToken, () => {
      setQuery('');
      setResults([]);
      setStatus('');
      setFocused(false);
    });
  }, [clearToken]);
  useEffect(() => {
    const request = ++requestSequence.current;
    const coordinates = parseCoordinates(query);
    if (coordinates || query.trim().length < 3) {
      setResults([]);
      setStatus('');
      return;
    }
    setResults([]);
    setStatus('');
    const timer = window.setTimeout(() => {
      setStatus('Searching…');
      telemetry('search-requested', { characters: query.trim().length });
      search.search(query, { viewport: viewportRef.current })
        .then((matches) => {
          if (request !== requestSequence.current) return;
          if (document.activeElement === input.current) setFocused(true);
          setResults(matches);
          setStatus(matches.length ? '' : 'No matching place');
          telemetry('search-results', { count: matches.length, inputActive: document.activeElement === input.current });
        })
        .catch((error: Error) => {
          if (request !== requestSequence.current) return;
          console.error('[nice-weather] Place search failed.', error);
          setResults([]);
          setStatus('Place search is unavailable');
          telemetry('search-results', { count: 0, failed: true, inputActive: document.activeElement === input.current });
        });
    }, 180);
    return () => window.clearTimeout(timer);
  }, [query, search]);

  const choose = (result: PlaceSearchResult) => {
    const label = [result.name, result.admin1, result.country].filter((item, index, all) => item && all.indexOf(item) === index).join(', ');
    setQuery(result.name);
    setResults([]);
    setStatus('');
    setFocused(false);
    input.current?.blur();
    onFound({ lat: result.latitude, lon: result.longitude, label }, input.current);
    telemetry('search-selected', { geonameId: result.geonameId, country: result.countryCode });
  };

  const submit = (event: React.FormEvent) => {
    event.preventDefault();
    const coordinates = parseCoordinates(query);
    if (coordinates) {
      setFocused(false);
      input.current?.blur();
      onFound({ ...coordinates, label: `${coordinates.lat.toFixed(3)}°, ${coordinates.lon.toFixed(3)}°` }, input.current);
    } else if (results[0]) choose(results[0]);
  };

  return (
    <form className="controlSurface placeSearch" role="search" onSubmit={submit} onKeyDown={(event) => {
      if (event.key !== 'Escape' || !open) return;
      event.preventDefault();
      event.stopPropagation();
      setResults([]);
      setStatus('');
      input.current?.focus();
    }}>
      <SearchIcon />
      <label className="srOnly" htmlFor="place-search">{prompt}</label>
      <input id="place-search" ref={input} value={query} placeholder={prompt} autoComplete="off" onPointerDown={engageInput} onFocus={() => {
        onEngage();
        engageInput();
      }} onBlur={() => {
        window.clearTimeout(blurTimer.current);
        blurTimer.current = window.setTimeout(() => {
          blurTimer.current = undefined;
          setFocused(false);
        }, 120);
      }} onChange={(event) => {
        engageInput();
        setQuery(event.target.value);
      }} aria-expanded={open} aria-controls="place-results" />
      {query
        ? <button className="iconButton searchAction clearSearch" type="button" aria-label="Clear search" onClick={() => { setQuery(''); setResults([]); setStatus(''); input.current?.focus(); }}>×</button>
        : <button className="iconButton searchAction searchLocation" type="button" aria-label="Use my location" onClick={onLocate}><LocationIcon /></button>}
      {open && (
        <div id="place-results" className="controlSurface searchPopover">
          {results.length > 0 ? <ul aria-label="Places">{results.map((result) => <li key={result.geonameId}><button type="button" onMouseDown={(event) => event.preventDefault()} onClick={() => choose(result)}><strong>{result.name}</strong><span>{[result.admin1, result.country].filter(Boolean).join(', ')}</span></button></li>)}</ul> : <p role="status">{status}</p>}
          {search.attribution && <small>Place names: <a href="https://www.geonames.org/" target="_blank" rel="noreferrer">GeoNames</a></small>}
        </div>
      )}
    </form>
  );
}

function PointPanel({ state, point, legend, lens, profile, climateModel, focusIntent, onLensChange, escapeEnabled, onClose }: { state: PointState; point: MapPoint; legend: WeatherPresentation['legend']; lens: Lens; profile: Profile; climateModel: ClimateUiModel; focusIntent: DetailsFocusIntent; onLensChange: (lens: Lens) => void; escapeEnabled: boolean; onClose: () => void }) {
  const [expanded, setExpanded] = useState(false);
  const panel = useRef<HTMLElement>(null);
  const close = useRef<HTMLButtonElement>(null);
  useEffect(() => setExpanded(false), [point.lat, point.lon, state.status === 'ready' ? state.kind === 'climate' ? state.data.period : state.data.periodLabel : undefined]);
  useEffect(() => { if (focusIntent.requested) restoreFocus(panel.current); }, [focusIntent]);
  useEscape(escapeEnabled, onClose);
  return (
    <aside ref={panel} tabIndex={-1} className={`controlSurface pointPanel${expanded ? ' expanded' : ''}`} aria-live="polite" aria-label="Weather at selected place">
      <div className="pointPanelHeader">
        <div className="sheetHandle" aria-hidden="true" />
        <button ref={close} className="iconButton closeButton" type="button" onClick={onClose} aria-label="Close weather details">×</button>
        {state.status === 'ready' && (state.kind === 'climate'
          ? <ClimatePointHeading data={state.data} point={point} profile={profile} climateModel={climateModel} />
          : <PointHeadingFor data={state.data} point={point} />)}
      </div>
      <div className="pointPanelBody">
        {state.status === 'loading' && <PointSkeleton label={point.label} />}
        {state.status === 'error' && <div className="pointMessage"><h2>{point.label ?? 'Selected place'}</h2><strong>Weather unavailable</strong><p>{state.message}</p></div>}
        {state.status === 'ready' && (state.kind === 'climate'
          ? <ClimateDetails data={state.data} profile={profile} lens={lens} onLensChange={onLensChange} climateModel={climateModel} />
          : state.data.kind === 'range'
            ? <RangeDetails data={state.data} legend={legend} />
            : <DailyDetails data={state.data} legend={legend} lens={lens} onLensChange={onLensChange} />)}
        {state.status === 'idle' && <div className="pointMessage"><h2>{point.label ?? 'Selected place'}</h2><p>{point.lat.toFixed(2)}°, {point.lon.toFixed(2)}°</p></div>}
      </div>
      {state.status === 'ready' && <button className="sheetExpand" type="button" aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}>{expanded ? 'Show less' : 'Show all details'}</button>}
    </aside>
  );
}

function ClimatePointHeading({ data, point, profile, climateModel }: { data: ClimatePointEvidence; point: MapPoint; profile: Profile; climateModel: ClimateUiModel }) {
  const evidence = data[profile];
  const eyebrow = `${monthName(data.period)} climate · ${climateModel.referencePeriod}`;
  return <div className="pointHeading climatePointHeading"><div><p>{eyebrow}</p><h2>{point.label ?? `${point.lat.toFixed(2)}°, ${point.lon.toFixed(2)}°`}</h2></div><div className="score"><strong>{evidence.goodOrBetterPercent}%</strong><span>Nice days</span></div></div>;
}

export function ClimateDetails({ data, profile, lens, onLensChange, climateModel }: { data: ClimatePointEvidence; profile: Profile; lens: Lens; onLensChange: (lens: Lens) => void; climateModel: ClimateUiModel }) {
  const evidence = data[profile];
  return <>
    <p className="phonePointSummary">{climateSummary(data, profile)}</p>
    <p className="rangeEvidence">{climateSummary(data, profile)}</p>
    <dl className="climateFacts">
      <div><dd>{evidence.goodOrBetterPercent}%</dd><dt>Nice days</dt></div>
      <div className={evidence.awfulPercent > 0 ? 'bad' : ''}><dd>{evidence.awfulPercent}%</dd><dt>Awful days</dt></div>
      <div><dd>{formatPermille(evidence.poorWeekPermille)}</dd><dt>Chance of a poor week</dt></div>
      <div><dd>{evidence.medianScore}</dd><dt>Median score</dt></div>
    </dl>
    <ClimateComponentRows data={data} active={lens} onChange={onLensChange} />
    <p className="sourceLine">Recent daily climate · {climateModel.referencePeriod} · worldwide · about 30 km weather detail</p>
  </>;
}

function ClimateComponentRows({ data, active, onChange }: { data: ClimatePointEvidence; active: Lens; onChange: (lens: Lens) => void }) {
  const rows = [
    ['comfort', 'Comfort', data.comfortGoodPercent],
    ['dry', 'Dry', data.dryGoodPercent],
    ['bright', 'Bright', data.brightGoodPercent],
  ] as const;
  return <div className="componentRows climateComponents" aria-label="Chance of good weather by component">{rows.map(([id, label, value]) => (
    <button key={id} type="button" aria-pressed={active === id} onClick={() => onChange(id)}>
      <span><strong>{label}</strong><small>Good or better</small></span><i><b style={{ width: `${value}%` }} /></i><em>{value}%</em>
    </button>
  ))}</div>;
}

function PointHeadingFor({ data, point }: { data: PointWeather; point: MapPoint }) {
  const eyebrow = data.kind === 'range'
    ? `${shortRange(data.window.startDate, data.window.endDate)} · ${dayCountLabel(data.window.dayCount)}`
    : data.source.basis === 'climate' ? `${data.periodLabel} · monthly average` : data.periodLabel;
  return <PointHeading point={point} eyebrow={eyebrow} score={data.score} category={data.category} />;
}

function RangeDetails({ data, legend }: { data: Extract<PointWeather, { kind: 'range' }>; legend: WeatherPresentation['legend'] }) {
  const evidence = forecastScoreEvidence(data.days, legend);
  return (
    <>
      <p className="phonePointSummary">{evidence}</p>
      <p className="rangeEvidence">{evidence}</p>
      <dl className="rangeFacts">
        <div><dd>{data.window.niceDays}</dd><dt>Nice days</dt></div>
        <div className={data.window.awfulDays > 0 ? 'bad' : ''}><dd>{data.window.awfulDays}</dd><dt>Awful days</dt></div>
        <div><dd>{data.window.longestPoorRun}<small> {data.window.longestPoorRun === 1 ? 'day' : 'days'}</small></dd><dt>Longest poor run</dt></div>
      </dl>
      <ForecastFingerprint days={data.days} legend={legend} />
      <ComponentRows data={data} legend={legend} />
      <p className="sourceLine">{pointSourceLabel(data.source)}</p>
    </>
  );
}

function DailyDetails({ data, legend, lens, onLensChange }: { data: DailyPoint; legend: WeatherPresentation['legend']; lens: Lens; onLensChange: (lens: Lens) => void }) {
  const rain = rainDisplay(data);
  return (
    <>
      <p className="pointSummary">{data.summary}</p>
      {data.source.basis === 'climate' && <p className="climateNote">Monthly average from {data.source.referencePeriod}; not the chance of a nice day. Rain is the average per day.</p>}
      <dl className="weatherFacts">
        <div className="comfortFact"><dt>Temperature</dt><dd>{temperatureRange(data.metrics.minTempC, data.metrics.maxTempC)}{comfortContext(data)}</dd></div>
        <div><dt>{rain.label}</dt><dd>{rain.value}</dd></div>
        <div><dt>{data.metrics.cloudPct !== undefined ? 'Cloud' : 'Sun energy'}</dt><dd>{data.metrics.cloudPct !== undefined ? `${format(data.metrics.cloudPct, 0)}%` : data.metrics.solarEnergyMJm2 !== undefined ? `${format(data.metrics.solarEnergyMJm2, 1)} MJ/m²` : '—'}</dd></div>
        <div><dt>Wind</dt><dd>{format(data.metrics.windKmh, 0)} km/h</dd></div>
      </dl>
      <ComponentRows data={data} legend={legend} active={lens} onChange={onLensChange} />
      <details className="sourceDetails">
        <summary>Weather and source</summary>
        <dl>
          <div><dt>Daytime</dt><dd>{format(data.metrics.temperatureC, 0)}°C</dd></div>
          <div><dt>Gusts</dt><dd>{optional(data.metrics.gustKmh, (value) => `${format(value, 0)} km/h`)}</dd></div>
          <div><dt>Dew point</dt><dd>{optional(data.metrics.dewPointC, (value) => `${format(value, 0)}°C`)}</dd></div>
          <div><dt>Humidity</dt><dd>{optional(data.metrics.humidityPct, (value) => `${format(value, 0)}%`)}</dd></div>
        </dl>
      </details>
      <p className="sourceLine">{pointSourceLabel(data.source)}</p>
    </>
  );
}

function PointHeading({ point, eyebrow, score, category }: { point: MapPoint; eyebrow: string; score: number; category: string }) {
  return <div className="pointHeading"><div><p>{eyebrow}</p><h2>{point.label ?? `${point.lat.toFixed(2)}°, ${point.lon.toFixed(2)}°`}</h2></div><div className="score"><strong>{Math.round(score)}</strong><span>{category}</span></div></div>;
}

function ComponentRows({ data, legend, active, onChange }: { data: PointWeather; legend: WeatherPresentation['legend']; active?: Lens; onChange?: (lens: Exclude<Lens, 'overall'>) => void }) {
  const rows = [
    ['comfort', 'Comfort', data.components.comfort], ['dry', 'Dry', data.components.dryness], ['bright', 'Bright', data.components.sun], ['wind', 'Calm', data.components.wind],
  ] as const;
  return <div className="componentRows" aria-label="Weather score parts">{rows.map(([id, label, score]) => {
    const colour = legend.find((band) => band.label === score.category)?.color;
    const content = <><span><strong>{label}</strong><small>{score.category}</small></span><i><b style={{ width: `${score.value}%`, backgroundColor: colour }} /></i><em>{score.value}</em></>;
    return onChange ? <button key={id} type="button" aria-pressed={active === id} onClick={() => onChange(id)}>{content}</button> : <div key={id}>{content}</div>;
  })}</div>;
}

function PointSkeleton({ label }: { label?: string }) { return <div className="pointSkeleton"><strong>{label ?? 'Selected place'}</strong><span /><span /><span /></div>; }

function periodsFor(manifest: WeatherManifest | undefined, view: View): Period[] {
  if (!manifest || view !== 'daily') return [];
  return manifest.forecast.days;
}

export function climateUiModel(index?: ClimateIndex): ClimateUiModel {
  if (!index) return { available: false, complete: false, label: 'Climate', compactLabel: 'Climate', referencePeriod: '', periods: [] };
  const periods = index.months.map(({ period }) => ({ id: period, shortLabel: shortMonthName(period) }));
  const referencePeriod = `${index.referencePeriod.firstYear}–${index.referencePeriod.lastYear}`;
  if (index.state === 'complete') return { available: true, complete: true, label: 'Typical year', compactLabel: 'Climate', referencePeriod, periods };
  if (periods.length === 1) {
    const month = monthName(periods[0].id);
    return { available: true, complete: false, label: `${month} climate`, compactLabel: month, referencePeriod, periods };
  }
  return { available: true, complete: false, label: 'Climate preview', compactLabel: 'Climate', referencePeriod, periods };
}

export function climateRailPeriods(model: ClimateUiModel): readonly PeriodChoice[] {
  return model.periods.length > 1 ? model.periods : [];
}

export function pointWithinBounds(point: Pick<MapPoint, 'lat' | 'lon'>, bounds: readonly [number, number, number, number]): boolean {
  return point.lon >= bounds[0] && point.lon <= bounds[2] && point.lat >= bounds[1] && point.lat <= bounds[3];
}

export function pointWithinActiveSearchCoverage(point: Pick<MapPoint, 'lat' | 'lon'>, view: View, bounds?: readonly [number, number, number, number]): boolean {
  return view === 'climate' || !bounds || pointWithinBounds(point, bounds);
}

export function climateFieldFor(lens: Lens, profile: Profile): ClimateField {
  if (lens === 'comfort') return 'comfort_good';
  if (lens === 'dry') return 'dry_good';
  if (lens === 'bright') return 'bright_good';
  if (lens === 'wind') return profile === 'nice' ? 'nice_calm_good' : 'cycling_calm_good';
  return profile === 'nice' ? 'nice_good' : 'cycling_good';
}

export function forecastFieldFor(lens: Lens, profile: Profile): ForecastField {
  if (lens === 'comfort') return 'thermal';
  if (lens === 'dry') return 'dryness';
  if (lens === 'bright') return 'sun';
  if (lens === 'wind') return profile === 'nice' ? 'nice_wind' : 'cycling_wind';
  return profile === 'nice' ? 'nice_score' : 'cycling_score';
}

export function forecastMapTiles(product: DayForecastProduct, bounds: GeographicBounds, mapZoom: number): readonly TileDraw[] {
  return coveringTiles(bounds, forecastMapUsesDetail(product, bounds, mapZoom) ? 5 : 3);
}

export function forecastMapUsesDetail(product: DayForecastProduct, bounds: GeographicBounds, mapZoom: number): boolean {
  if (mapZoom < 4 || !product.tiers.some(({ tier }) => tier === 'europe-detail')) return false;
  const tiles = coveringTiles(bounds, 5);
  const uniqueTiles = new Set(tiles.map(({ id }) => `${id.z}/${id.x}/${id.y}`));
  return uniqueTiles.size <= FORECAST_TEXTURE_BUDGET && tiles.some(({ id }) => forecastDetailDeclared(product, id));
}

export function scalarPalette(manifest: Readonly<{ palette: readonly WeatherManifest['palette'][number][] }>): readonly ScalarPaletteColour[] {
  const stops = [...manifest.palette].sort((left, right) => left.score - right.score);
  if (!stops.length || stops[0].score !== 0 || stops[stops.length - 1].score !== 100) throw new Error('Climate palette must cover 0 to 100.');
  return Array.from({ length: 101 }, (_, score) => {
    const found = stops.findIndex((stop) => stop.score >= score);
    const upperIndex = found < 1 ? 1 : found;
    const lower = stops[upperIndex - 1];
    const upper = stops[upperIndex] ?? lower;
    const span = upper.score - lower.score;
    const ratio = span === 0 ? 0 : (score - lower.score) / span;
    const from = parseHexColour(lower.color);
    const to = parseHexColour(upper.color);
    return [
      Math.round(from[0] + (to[0] - from[0]) * ratio),
      Math.round(from[1] + (to[1] - from[1]) * ratio),
      Math.round(from[2] + (to[2] - from[2]) * ratio),
      255,
    ] as ScalarPaletteColour;
  });
}

function parseHexColour(value: string): readonly [number, number, number] {
  const match = value.match(/^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i);
  if (!match) throw new Error('Climate palette contains an invalid colour.');
  return [Number.parseInt(match[1], 16), Number.parseInt(match[2], 16), Number.parseInt(match[3], 16)];
}

export function climateSummary(data: ClimatePointEvidence, profile: Profile): string {
  const evidence = data[profile];
  return `${evidence.goodOrBetterPercent}% nice · ${evidence.awfulPercent}% awful · median ${evidence.medianScore}`;
}

function formatPermille(value: number): string {
  return `${new Intl.NumberFormat('en-GB', { maximumFractionDigits: 1 }).format(value / 10)}%`;
}

function monthNumber(period: string): number {
  const match = period.match(/^month-(0[1-9]|1[0-2])$/);
  return match ? Number(match[1]) : 1;
}

function monthName(period: string): string {
  return new Intl.DateTimeFormat('en-GB', { month: 'long', timeZone: 'UTC' }).format(new Date(Date.UTC(2024, monthNumber(period) - 1, 1)));
}

function shortMonthName(period: string): string {
  return new Intl.DateTimeFormat('en-GB', { month: 'short', timeZone: 'UTC' }).format(new Date(Date.UTC(2024, monthNumber(period) - 1, 1)));
}

function forecastDayLabel(value: string): string {
  return new Intl.DateTimeFormat('en-GB', { weekday: 'short', day: 'numeric', timeZone: 'UTC' }).format(new Date(`${value}T12:00:00Z`));
}

export function ForecastFingerprint({ days, legend }: { days: Extract<PointWeather, { kind: 'range' }>['days']; legend: WeatherPresentation['legend'] }) {
  return <section className="forecastFingerprint" aria-labelledby="forecast-fingerprint-title">
    <h3 id="forecast-fingerprint-title">Day by day</h3>
    <ol>{days.map((day) => {
      const colour = legend.find((band) => band.label === day.category)?.color;
      return <li key={day.date} style={{ '--day-colour': colour } as CSSProperties} aria-label={`${forecastDayLabel(day.date)}, ${day.score}, ${day.category}`}>
        <time dateTime={day.date}>{forecastDayLabel(day.date)}</time><strong>{day.score}</strong>
      </li>;
    })}</ol>
  </section>;
}

function lensAsset(period: Period, profile: Profile, lens: Lens) {
  if (lens === 'overall') return period.images[profile];
  if (lens === 'wind') return period.lenses[`wind-${profile}`];
  return period.lenses[lens];
}

export function rangeLensAsset(window: WeatherManifest['forecast']['window'], profile: Profile, lens: Lens) {
  if (lens === 'overall') return window.profiles[profile].image;
  if (lens === 'wind') return window.lenses[`wind-${profile}`];
  return window.lenses[lens];
}

function PeriodStatus({ view, climate, forecast, day }: { view: View; climate: ClimateUiModel; forecast?: ForecastSnapshot; day?: DayForecastProduct }) {
  const status = view === 'climate'
    ? { primary: climate.complete ? 'Recent climate' : 'Climate preview', meta: climate.referencePeriod ? `${climate.referencePeriod} · worldwide` : '', stale: false }
    : worldwideForecastStatus(forecast, day);
  return <p className="metaLine mapStatus" role="status">
    <span className={status.stale ? 'statusPrimary isStale' : 'statusPrimary'}>{status.primary}</span>
    {status.meta && <><span className="statusSeparator" aria-hidden="true">·</span><span className="statusMeta">{status.meta}</span></>}
  </p>;
}

function worldwideForecastStatus(forecast?: ForecastSnapshot, day?: DayForecastProduct): { primary: string; meta: string; stale: boolean } {
  if (!forecast) return { primary: 'Loading forecast', meta: '', stale: false };
  const run = day?.tiers.find(({ tier }) => tier === 'global')?.evidence.runUtc ?? forecast.range.source.runUtc;
  const status = forecastRunStatus(run, forecast.range.source.staleAfterHours);
  const detail = day?.tiers.some(({ tier }) => tier === 'europe-detail')
    ? 'Worldwide · European detail about 7 km'
    : 'Worldwide · about 28 km';
  return { primary: status.stale ? 'Forecast out of date' : status.label, meta: detail, stale: status.stale };
}

export function periodStatus(manifest: WeatherManifest, view: View, period?: Period, climate?: ClimateUiModel): { primary: string; meta: string; stale: boolean } {
  const prefix = manifest.dataStatus === 'sample' ? 'Sample data · ' : '';
  if (view === 'climate') return {
    primary: `${prefix}${climate?.complete ? 'Recent climate' : 'Climate preview'}`,
    meta: climate?.referencePeriod ? `${climate.referencePeriod} · worldwide` : '',
    stale: false,
  };
  const status = forecastRunStatus(view === 'daily' && period ? period.sourceRunAt : manifest.forecast.updatedAt, manifest.forecast.staleAfterHours);
  const primary = `${prefix}${status.stale ? 'Forecast out of date' : status.label}`;
  if (view === 'range') return { primary, meta: forecastRangeMeta(manifest), stale: status.stale };
  return { primary, meta: `${period?.kind === 'outlook' ? 'Outlook' : 'Detailed'} · about ${Math.round(period?.resolutionKm ?? 0)} km`, stale: status.stale };
}

export function forecastRangeMeta(manifest: WeatherManifest): string {
  const groups = new Map<string, { count: number; kind: NonNullable<Period['kind']>; resolutionKm: number }>();
  for (const day of manifest.forecast.days) {
    if (!day.kind) continue;
    const resolutionKm = Math.round(day.resolutionKm);
    const key = `${day.kind}:${resolutionKm}`;
    const group = groups.get(key);
    if (group) group.count += 1;
    else groups.set(key, { count: 1, kind: day.kind, resolutionKm });
  }
  if (!groups.size) return `Forecast detail · about ${Math.round(manifest.forecast.window.resolutionKm)} km`;
  return Array.from(groups.values()).map(({ count, kind, resolutionKm }) => `${count} ${count === 1 ? 'day' : 'days'} ${kind} at ${resolutionKm} km`).join(' · ');
}

function comfortContext(data: DailyPoint) {
  const parts = [
    data.metrics.dewPointC === undefined ? undefined : `Dew ${format(data.metrics.dewPointC, 0)}°`,
    data.metrics.humidityPct === undefined ? undefined : `${format(data.metrics.humidityPct, 0)}% humidity`,
  ].filter(Boolean);
  return parts.length ? <small>{parts.join(' · ')}</small> : null;
}

function useEscape(active: boolean, action: () => void) {
  const actionRef = useRef(action); actionRef.current = action;
  useEffect(() => {
    if (!active) return;
    const handler = (event: KeyboardEvent) => { if (event.key === 'Escape' && !event.defaultPrevented) { event.preventDefault(); actionRef.current(); } };
    document.addEventListener('keydown', handler);
    return () => document.removeEventListener('keydown', handler);
  }, [active]);
}

function parseCoordinates(query: string): MapPoint | undefined {
  const match = query.trim().match(/^(-?\d+(?:\.\d+)?)\s*[, ]\s*(-?\d+(?:\.\d+)?)$/);
  if (!match) return undefined;
  const lat = Number(match[1]); const lon = Number(match[2]);
  return lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 ? { lat, lon } : undefined;
}

function locate(success: (point: MapPoint) => void, error: (message: string) => void) {
  if (!navigator.geolocation) { error('Location is not available in this browser.'); return; }
  navigator.geolocation.getCurrentPosition((position) => success({ lat: position.coords.latitude, lon: position.coords.longitude }), () => error('Allow location access to use your position.'), { timeout: 8_000, maximumAge: 300_000 });
}

function shortRange(start: string, end: string): string { return `${dateLabel(start)}–${dateLabel(end)}`; }
function dateLabel(value: string): string { return new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', timeZone: 'UTC' }).format(new Date(`${value}T12:00:00Z`)); }
function monthLabel(value: string): string { return new Intl.DateTimeFormat('en-GB', { month: 'short', timeZone: 'UTC' }).format(new Date(`${value}T12:00:00Z`)); }
function railMonthLabel(periods: readonly PeriodChoice[], index: number): string {
  const current = periods[index]?.date;
  const previous = periods[index - 1]?.date;
  if (!current) return '\u00a0';
  const month = monthLabel(current);
  return index === 0 || !previous || monthLabel(previous) !== month ? month : '\u00a0';
}
export function revealSelectedPeriod(container: Pick<HTMLElement, 'querySelectorAll'> | null, selectedId: string): void {
  if (!container) return;
  const selected = Array.from(container.querySelectorAll<HTMLElement>('[data-period-id]')).find((item) => item.dataset.periodId === selectedId);
  const reducedMotion = typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  selected?.scrollIntoView({ behavior: reducedMotion ? 'auto' : 'smooth', block: 'nearest', inline: 'center' });
}
export function revealFocusedPeriod(period: Pick<HTMLElement, 'scrollIntoView'>): void {
  period.scrollIntoView({ behavior: 'auto', block: 'nearest', inline: 'nearest' });
}
export function periodRailHasMore(container: Pick<HTMLElement, 'scrollWidth' | 'clientWidth' | 'scrollLeft'> | null): boolean {
  return Boolean(container && container.scrollWidth - container.clientWidth - container.scrollLeft > 2);
}
export function bestWeatherAvailability(manifest: WeatherManifest | undefined, now = new Date()): { allowed: boolean; message: string } {
  if (!manifest) return { allowed: false, message: 'Loading the forecast…' };
  return isStale(manifest.forecast.updatedAt, manifest.forecast.staleAfterHours, now)
    ? { allowed: false, message: 'The forecast is out of date. Search for a place instead.' }
    : { allowed: true, message: '' };
}
export function forecastScoreEvidence(days: Extract<PointWeather, { kind: 'range' }>['days'], legend: WeatherPresentation['legend']): string {
  const good = new Set(legend.slice(3).map((band) => band.label));
  const poor = new Set(legend.slice(0, 2).map((band) => band.label));
  const goodDays = days.filter((day) => good.has(day.category)).length;
  const poorDays = days.filter((day) => poor.has(day.category)).length;
  const ordered = days.map((day) => day.score).sort((a, b) => a - b);
  const middle = Math.floor(ordered.length / 2);
  const median = ordered.length % 2 ? ordered[middle] : Math.round((ordered[middle - 1] + ordered[middle]) / 2);
  return `Good or better on ${goodDays} of ${days.length} days · Poor or Awful on ${poorDays} · median score ${median}`;
}
export function dayCountLabel(count: number): string { return `${count} ${count === 1 ? 'day' : 'days'}`; }
export function temperatureRange(minimum: number, maximum: number): string {
  const low = format(minimum, 0);
  const high = format(maximum, 0);
  return low === high ? `${low}°` : `${low}–${high}°`;
}
export function clearSearchAfterMapSelection(clearToken: number, clear: () => void): void { if (clearToken > 0) clear(); }
export function placeSearchPrompt(_view: View): string { return 'Search anywhere'; }
export function rainDisplay(data: DailyPoint): { label: string; value: string } {
  if (data.source.basis === 'climate') return { label: 'Rain · daily average', value: `${format(data.metrics.rainMm, 1)} mm/day` };
  const amount = `${format(data.metrics.rainMm, 1)} mm`;
  return !data.metrics.wetTimeFraction
    ? { label: 'Rain', value: amount }
    : { label: 'Rain', value: `${amount} · ${wetDaytimeDuration(data.metrics.wetTimeFraction)}` };
}
export function wetDaytimeDuration(fraction: number): string {
  const hours = Math.max(0, Math.min(1, fraction)) * 10;
  if (hours === 0) return 'no wet hours';
  if (hours < .25) return 'under ½ wet hour';
  const rounded = Math.round(hours * 2) / 2;
  if (rounded === .5) return 'about ½ wet hour';
  if (rounded === 1) return 'about 1 wet hour';
  return `about ${new Intl.NumberFormat('en-GB', { maximumFractionDigits: 1 }).format(rounded)} wet hours`;
}
function format(value: number, digits: number): string { return new Intl.NumberFormat('en-GB', { maximumFractionDigits: digits, minimumFractionDigits: digits }).format(value); }
function optional(value: number | undefined, present: (value: number) => string): string { return value === undefined ? 'Not available' : present(value); }

function SunMark() { return <svg viewBox="0 0 28 28" aria-hidden="true"><circle cx="14" cy="14" r="4.5" /><path d="M14 2v5M14 21v5M2 14h5M21 14h5M5.5 5.5 9 9M19 19l3.5 3.5M22.5 5.5 19 9M9 19l-3.5 3.5" /></svg>; }
function SearchIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6" /><path d="m15 15 5 5" /></svg>; }
function LocationIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="4" /><path d="M12 2v4M12 18v4M2 12h4M18 12h4" /></svg>; }
