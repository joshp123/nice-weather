import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import { nextDetailsFocusIntent, restoreFocus, shouldFocusDetails, type DetailsFocusIntent } from './accessibility';
import { forecastRunStatus, imageAssetUrl, isStale, loadManifest, loadPoint, StaleReleaseError } from './data/api';
import { loadBestWeather, type BestWeatherPlace } from './data/bestWeather';
import { ClimateDataError, WorldwideClimateClient, type ClimateCatalog, type ClimateField, type ClimateIndex, type ClimatePointEvidence } from './data/climateClient';
import { WorldwidePlaceSearch, type PlaceSearchBounds, type PlaceSearchResult, type PlaceSearchViewport } from './data/placeSearch';
import type { DailyPoint, Lens, Period, PointWeather, Profile, View, WeatherManifest } from './data/types';
import { StaleWindReleaseError, WindFrameLoader, type WindGrid } from './data/wind';
import type { MapPoint, OverlayState, ScalarOverlay } from './map/WeatherMap';
import type { ScalarPaletteColour } from './map/scalarTileLayer';
import { pointSourceLabel } from './presentation';
import { DRY_SCORE_NOTE, forecastDetailNote, PROFILE_WEIGHT_NOTES, ratingBandLabel } from './scoreGuide';
import { reportResourceSummary, telemetry } from './telemetry';
import './styles.css';

const WeatherMap = lazy(() => import('./map/WeatherMap').then((module) => ({ default: module.WeatherMap })));

type LoadState =
  | { status: 'loading' }
  | { status: 'ready'; manifest: WeatherManifest }
  | { status: 'error' };

type ClimateState =
  | { status: 'loading' | 'unavailable' }
  | { status: 'ready'; index: ClimateIndex };
type ClimateCatalogState =
  | { status: 'idle' | 'loading' | 'unavailable' }
  | { status: 'ready'; catalog: ClimateCatalog };
type PointState =
  | { status: 'idle' | 'loading' }
  | { status: 'ready'; kind: 'forecast'; data: PointWeather }
  | { status: 'ready'; kind: 'climate'; data: ClimatePointEvidence }
  | { status: 'error'; message: string };
type WindState = { status: 'idle' | 'loading' | 'ready' | 'error'; grid?: WindGrid };
type ControlPanel = 'when' | 'lenses' | 'profile' | null;
type PeriodChoice = Readonly<{ id: string; shortLabel: string; date?: string }>;

export default function App() {
  const [loadState, setLoadState] = useState<LoadState>({ status: 'loading' });
  const [climateState, setClimateState] = useState<ClimateState>({ status: 'loading' });
  const [climateCatalogState, setClimateCatalogState] = useState<ClimateCatalogState>({ status: 'idle' });
  const [climateRetry, setClimateRetry] = useState(0);
  const [retry, setRetry] = useState(0);
  const [view, setView] = useState<View>('range');
  const [profile, setProfile] = useState<Profile>('nice');
  const [lens, setLens] = useState<Lens>('overall');
  const [periodId, setPeriodId] = useState('');
  const [selectedPoint, setSelectedPoint] = useState<MapPoint>();
  const [focusPoint, setFocusPoint] = useState<(MapPoint & { token: number })>();
  const [viewport, setViewport] = useState<PlaceSearchViewport>({ latitude: 48.5, longitude: 9.5 });
  const [mapNotice, setMapNotice] = useState('');
  const [overlayState, setOverlayState] = useState<OverlayState>({ status: 'idle' });
  const [legendPanel, setLegendPanel] = useState<ControlPanel>(null);
  const [mapSelectionToken, setMapSelectionToken] = useState(0);
  const [windEnabled, setWindEnabled] = useState(false);
  const [windFrameSelection, setWindFrameSelection] = useState({ periodId: '', index: 0 });
  const [windState, setWindState] = useState<WindState>({ status: 'idle' });
  const [pointState, setPointState] = useState<PointState>({ status: 'idle' });
  const [detailsFocusIntent, setDetailsFocusIntent] = useState<DetailsFocusIntent>({ token: 0, requested: false });
  const pointFocusOrigin = useRef<HTMLElement | null>(null);
  const staleReloadedRelease = useRef<string | undefined>(undefined);
  const staleReloadedClimateIndex = useRef<string | undefined>(undefined);
  const windLoader = useRef(new WindFrameLoader());
  const activeSearchCoverage = useRef<{ view: View; bounds?: WeatherManifest['bounds'] }>({ view: 'range' });
  const climateClient = useMemo(() => new WorldwideClimateClient(), []);

  useEffect(() => {
    const controller = new AbortController();
    const startedAt = performance.now();
    setLoadState({ status: 'loading' });
    loadManifest(controller.signal)
      .then((manifest) => {
        if (staleReloadedRelease.current && staleReloadedRelease.current !== manifest.releaseId) staleReloadedRelease.current = undefined;
        setLoadState({ status: 'ready', manifest });
        telemetry('manifest-ready', { elapsedMs: Math.round(performance.now() - startedAt), version: manifest.version, dataStatus: manifest.dataStatus });
      })
      .catch((error: Error) => {
        if (controller.signal.aborted) return;
        console.error('[nice-weather] Manifest load failed.', error);
        setLoadState({ status: 'error' });
      });
    return () => controller.abort();
  }, [retry]);

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

  const manifest = loadState.status === 'ready' ? loadState.manifest : undefined;
  activeSearchCoverage.current = { view, bounds: manifest?.bounds };
  const periods = useMemo(() => periodsFor(manifest, view), [manifest, view]);
  const period = periods.find((item) => item.id === periodId) ?? periods[0];
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
  const windFrameIndex = windFrameSelection.periodId === period?.id
    ? Math.min(windFrameSelection.index, Math.max(0, (period?.windFrames?.length ?? 1) - 1))
    : defaultWindFrameIndex(period?.windFrames);
  const activePeriodId = view === 'range' ? manifest?.forecast.window.id : view === 'daily' ? period?.id : climatePeriodId;

  const refreshStaleClimate = useCallback((refreshKey: string) => {
    if (staleReloadedClimateIndex.current === refreshKey) return false;
    staleReloadedClimateIndex.current = refreshKey;
    setPointState({ status: 'idle' });
    setClimateCatalogState({ status: 'idle' });
    setClimateRetry((value) => value + 1);
    telemetry('climate-refresh', { refreshKey });
    return true;
  }, []);

  useEffect(() => {
    const next = view === 'climate' ? climatePeriodId : view === 'daily' ? period?.id : undefined;
    if (next && next !== periodId) setPeriodId(next);
  }, [climatePeriodId, period, periodId, view]);

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

  const refreshStaleRelease = useCallback((releaseId: string) => {
    if (staleReloadedRelease.current === releaseId) return false;
    staleReloadedRelease.current = releaseId;
    setPointState({ status: 'idle' });
    setPeriodId('');
    setRetry((value) => value + 1);
    telemetry('release-refresh', { releaseId });
    return true;
  }, []);

  useEffect(() => {
    if (!selectedPoint || !activePeriodId || !manifest) {
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
    loadPoint({ ...selectedPoint, releaseId: manifest.releaseId, period: activePeriodId, profile }, manifest, controller.signal)
      .then((data) => setPointState({ status: 'ready', kind: 'forecast', data }))
      .catch((error: Error) => {
        if (controller.signal.aborted) return;
        if (error instanceof StaleReleaseError && refreshStaleRelease(manifest.releaseId)) return;
        setPointState({ status: 'error', message: error instanceof StaleReleaseError ? 'The forecast changed. Select this place again.' : error.message });
      });
    return () => controller.abort();
  }, [activePeriodId, climateCatalogState, climateClient, climateIndexId, manifest, pointProfileKey, refreshStaleClimate, refreshStaleRelease, selectedPoint, view]);

  useEffect(() => {
    const frames = period?.windFrames;
    const asset = frames?.[windFrameIndex];
    if (!windEnabled || view !== 'daily' || !manifest || !asset) {
      windLoader.current.clear();
      setWindState({ status: 'idle' });
      return;
    }
    const startedAt = performance.now();
    setWindState({ status: 'loading' });
    windLoader.current.load(manifest.releaseId, asset, manifest.bounds)
      .then((grid) => {
        if (!grid) return;
        setWindState({ status: 'ready', grid });
        telemetry('wind-frame-ready', { elapsedMs: Math.round(performance.now() - startedAt), validAt: grid.validAt, rows: grid.rows, cols: grid.cols });
      })
      .catch((error: Error) => {
        if (error instanceof StaleWindReleaseError && refreshStaleRelease(manifest.releaseId)) return;
        console.error('[nice-weather] Wind direction load failed.', error);
        setWindState({ status: 'error' });
      });
    return () => windLoader.current.clear();
  }, [manifest, period, refreshStaleRelease, view, windEnabled, windFrameIndex]);

  const overlayUrl = useMemo(() => {
    if (!manifest || view === 'climate') return undefined;
    if (view === 'range') return imageAssetUrl(manifest.releaseId, rangeLensAsset(manifest.forecast.window, profile, lens));
    if (!period) return undefined;
    return imageAssetUrl(manifest.releaseId, lensAsset(period, profile, lens));
  }, [lens, manifest, period, profile, view]);

  const scalarOverlay = useMemo<ScalarOverlay | undefined>(() => {
    if (!manifest || view !== 'climate' || climateCatalogState.status !== 'ready') return undefined;
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
  }, [climateCatalogState, climateClient, lens, manifest, profile, view]);

  const handleView = (next: View) => {
    if (next === 'climate' && !climateModel.available) return;
    setMapNotice('');
    telemetry('view-changed', { view: next });
    if (view === 'climate' && next !== 'climate' && selectedPoint && manifest && !pointWithinBounds(selectedPoint, manifest.bounds)) {
      setSelectedPoint(undefined);
      setFocusPoint(undefined);
      setPointState({ status: 'idle' });
      telemetry('point-cleared', { reason: 'outside-forecast-coverage' });
    }
    setView(next);
    setLegendPanel(null);
    if (next !== 'daily') setWindEnabled(false);
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
    <main className={`appShell view-${view}${(view === 'daily' ? periods.length > 0 : climateRail.length > 0) ? ' hasRail' : ''}${selectedPoint ? ' hasPoint' : ''}`}>
      <Suspense fallback={<div className="mapCanvas mapLoading" aria-label="Loading map" />}>
        <WeatherMap
          overlayUrl={overlayUrl}
          scalarOverlay={scalarOverlay}
          worldwide={view === 'climate'}
          windGrid={windState.status === 'ready' ? windState.grid : undefined}
          bounds={manifest?.bounds}
          selectedPoint={selectedPoint}
          focusPoint={focusPoint}
          onSelect={(point, intent) => {
            pointFocusOrigin.current = intent?.focusDetails && document.activeElement instanceof HTMLElement ? document.activeElement : null;
            setMapSelectionToken((value) => value + 1);
            selectPoint(point, false, shouldFocusDetails(intent?.focusDetails ? 'map-keyboard' : 'map-pointer'));
          }}
          onViewportChange={setViewport}
          onReady={(elapsedMs) => telemetry('map-ready', { elapsedMs })}
          onOverlayState={(state) => {
            setOverlayState(state);
            if (state.status === 'error' && state.reason === 'stale-release' && manifest) refreshStaleRelease(manifest.releaseId);
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
          {manifest && <PeriodStatus manifest={manifest} view={view} period={period} climate={climateModel} />}
        </div>
        <PlaceSearch viewport={viewport} manifest={manifest} profile={profile} view={view} clearToken={mapSelectionToken} onEngage={() => setLegendPanel(null)} onLocate={() => locate((point) => {
          const coverage = activeSearchCoverage.current;
          if (!pointWithinActiveSearchCoverage(point, coverage.view, coverage.bounds)) {
            setMapNotice('Current location is outside the forecast area.');
            return;
          }
          setMapSelectionToken((value) => value + 1);
          selectPoint({ ...point, label: 'Current location' });
        }, setMapNotice)} onFound={(point, origin) => {
          pointFocusOrigin.current = origin;
          selectPoint(point, true, shouldFocusDetails('search'));
        }} />
      </header>

      {(mapNotice || overlayState.status === 'error' || (view === 'climate' && climateCatalogState.status === 'unavailable')) && (
        <p className="controlSurface mapNotice" role="status">{mapNotice || (view === 'climate' ? 'Climate colours are not available right now.' : 'Weather colours could not be shown.')}</p>
      )}

      {selectedPoint && (
        <PointPanel
          state={pointState}
          point={selectedPoint}
          legend={manifest?.legend ?? []}
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
        {loadState.status === 'loading' && <DockMessage title="Loading forecast" />}
        {loadState.status === 'error' && <DockMessage title="Forecast unavailable" action={<button type="button" onClick={() => setRetry((value) => value + 1)}>Try again</button>} />}
        {manifest && (
          <>
            <div className="cockpitToolbar">
              <ViewControl value={view} climate={climateModel} panel={legendPanel} onPanelChange={setLegendPanel} onChange={handleView} />
              <LayerLegend
                manifest={manifest}
                period={period}
                view={view}
                lens={lens}
                panel={legendPanel}
                windEnabled={windEnabled}
                windFrameIndex={windFrameIndex}
                windStatus={windState.status}
                onPanelChange={setLegendPanel}
                onChange={(next) => { setLens(next); telemetry('lens-changed', { lens: next }); }}
                onWindEnabledChange={(enabled) => { setWindEnabled(enabled); telemetry('wind-direction-changed', { enabled }); }}
                onWindFrameChange={(index) => setWindFrameSelection({ periodId: period?.id ?? '', index })}
              />
              <ProfileControl value={profile} panel={legendPanel} onPanelChange={setLegendPanel} onChange={(next) => { setProfile(next); telemetry('profile-changed', { profile: next }); }} />
            </div>
            {overlayState.status !== 'error' && <ScoreRamp manifest={manifest} probability={view === 'climate'} />}
            {view === 'daily' && period && <PeriodRail periods={periods} selectedId={period.id} label="Forecast day" onChange={(id) => { setPeriodId(id); telemetry('period-changed', { period: id, view }); }} />}
            {view === 'climate' && climatePeriodId && climateRail.length > 0 && <PeriodRail periods={climateRail} selectedId={climatePeriodId} label="Climate month" onChange={(id) => { setPeriodId(id); telemetry('period-changed', { period: id, view }); }} />}
          </>
        )}
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
  manifest: WeatherManifest;
  period?: Period;
  view: View;
  lens: Lens;
  panel: ControlPanel;
  windEnabled: boolean;
  windFrameIndex: number;
  windStatus: WindState['status'];
  onPanelChange: (panel: ControlPanel) => void;
  onChange: (lens: Lens) => void;
  onWindEnabledChange: (enabled: boolean) => void;
  onWindFrameChange: (index: number) => void;
};

export function LayerLegend({ manifest, period, view, lens, panel, windEnabled, windFrameIndex, windStatus, onPanelChange, onChange, onWindEnabledChange, onWindFrameChange }: LayerLegendProps) {
  const trigger = useRef<HTMLButtonElement>(null);
  const open = panel === 'lenses';
  useEscape(open, () => { onPanelChange(null); restoreFocus(trigger.current); });
  const active = lens === 'overall' && view === 'climate' ? 'Nice days' : LENSES.find((item) => item.id === lens)?.label ?? 'Overall score';
  const frames = view === 'daily' ? period?.windFrames ?? [] : [];
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
          {view === 'daily' && <button type="button" className="windToggle" role="switch" aria-checked={windEnabled} onClick={() => onWindEnabledChange(!windEnabled)}><span>Wind direction</span><b aria-hidden="true">{windEnabled ? 'On' : 'Off'}</b></button>}
          <ScoreGuide manifest={manifest} climate={view === 'climate'} />
        </div>
      )}
      {windEnabled && view === 'daily' && <WindTimeControl frames={frames} index={windFrameIndex} status={windStatus} onChange={onWindFrameChange} />}
    </div>
  );
}

function MenuChevron() {
  return <svg className="menuChevron" viewBox="0 0 12 8" aria-hidden="true"><path d="m1.5 1.5 4.5 4 4.5-4" /></svg>;
}

function MenuCheck({ selected }: { selected: boolean }) {
  return <span className="menuCheck" aria-hidden="true">{selected ? '✓' : ''}</span>;
}

function WindTimeControl({ frames, index, status, onChange }: { frames: NonNullable<Period['windFrames']>; index: number; status: WindState['status']; onChange: (index: number) => void }) {
  const frame = frames[index];
  return (
    <div className="controlSurface windTime" aria-label="Wind direction time">
      <button type="button" aria-label="Earlier wind direction" disabled={index <= 0} onClick={() => onChange(index - 1)}>‹</button>
      <span>{status === 'error' ? 'Direction unavailable' : frame ? timeLabel(frame.validAt) : 'No direction data'}{status === 'loading' && <i aria-label="Loading">…</i>}</span>
      <button type="button" aria-label="Later wind direction" disabled={index >= frames.length - 1} onClick={() => onChange(index + 1)}>›</button>
    </div>
  );
}

function ScoreGuide({ manifest, climate }: { manifest: WeatherManifest; climate: boolean }) {
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
        <p>{forecastDetailNote(manifest)}</p>
        </>}
      </div>
    </details>
  );
}

export function ScoreRamp({ manifest, probability = false }: { manifest: WeatherManifest; probability?: boolean }) {
  const gradient = `linear-gradient(90deg, ${manifest.palette.map((stop) => `${stop.color} ${stop.score}%`).join(', ')})`;
  return <div className="scoreRamp" aria-label={probability ? 'Climate colours run from Rarely nice to Usually nice' : 'Score colours run from Awful to Excellent'}>
    <b>{probability ? 'Rarely nice' : 'Awful'}</b><i style={{ background: gradient }} aria-hidden="true" /><b>{probability ? 'Usually nice' : 'Excellent'}</b>
  </div>;
}

function PlaceSearch({ viewport, manifest, profile, view, clearToken, onEngage, onLocate, onFound }: { viewport: PlaceSearchViewport; manifest?: WeatherManifest; profile: Profile; view: View; clearToken: number; onEngage: () => void; onLocate: () => void; onFound: (point: MapPoint, origin: HTMLElement | null) => void }) {
  const search = useMemo(() => new WorldwidePlaceSearch(), []);
  const viewportRef = useRef(viewport);
  viewportRef.current = viewport;
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<PlaceSearchResult[]>([]);
  const [status, setStatus] = useState('');
  const [focused, setFocused] = useState(false);
  const [bestWeather, setBestWeather] = useState<BestWeatherPlace[]>([]);
  const [bestStatus, setBestStatus] = useState('');
  const [bestDismissed, setBestDismissed] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const requestSequence = useRef(0);
  const blurTimer = useRef<number | undefined>(undefined);
  const restoringFocusAfterDismiss = useRef(false);
  const showBest = focused && !bestDismissed && view === 'range' && query.trim() === '';
  const bestAvailability = bestWeatherAvailability(manifest);
  const open = focused && (showBest || results.length > 0 || Boolean(status));
  const prompt = placeSearchPrompt(view);
  const searchBounds = useMemo<PlaceSearchBounds | undefined>(() => {
    if (view === 'climate' || !manifest) return undefined;
    return { west: manifest.bounds[0], south: manifest.bounds[1], east: manifest.bounds[2], north: manifest.bounds[3] };
  }, [manifest, view]);

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
    if (coordinates || query.trim().length < 3 || (view !== 'climate' && !searchBounds)) {
      setResults([]);
      setStatus('');
      return;
    }
    setResults([]);
    setStatus('');
    const timer = window.setTimeout(() => {
      setStatus('Searching…');
      search.search(query, { viewport: viewportRef.current, bounds: searchBounds })
        .then((matches) => { if (request === requestSequence.current) { setResults(matches); setStatus(matches.length ? '' : 'No matching place'); } })
        .catch((error: Error) => { if (request === requestSequence.current) { console.error('[nice-weather] Place search failed.', error); setResults([]); setStatus('Place search is unavailable'); } });
    }, 180);
    return () => window.clearTimeout(timer);
  }, [query, search, searchBounds, view]);

  useEffect(() => {
    if (!showBest) return;
    if (!manifest || !bestAvailability.allowed) {
      setBestWeather([]);
      setBestStatus(bestAvailability.message);
      return;
    }
    const controller = new AbortController();
    setBestWeather([]);
    setBestStatus('Finding the best weather…');
    loadBestWeather(manifest.releaseId, profile, controller.signal)
      .then((value) => { setBestWeather(value.results); setBestStatus(''); telemetry('best-weather-ready', { profile, count: value.results.length }); })
      .catch((error: Error) => {
        if (controller.signal.aborted) return;
        telemetry('best-weather-error', { profile, message: error.message });
        setBestWeather([]); setBestStatus('Best weather is unavailable');
      });
    return () => controller.abort();
  }, [bestAvailability.allowed, bestAvailability.message, manifest, profile, showBest]);

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

  const chooseBest = (result: BestWeatherPlace) => {
    const label = [result.name, result.admin1, result.country].filter((item, index, all) => item && all.indexOf(item) === index).join(', ');
    setQuery(result.name);
    setFocused(false);
    input.current?.blur();
    onFound({ lat: result.latitude, lon: result.longitude, label }, input.current);
    telemetry('best-weather-selected', { geonameId: result.geonameId, country: result.countryCode, profile, score: result.score });
  };

  const submit = (event: React.FormEvent) => {
    event.preventDefault();
    const coordinates = parseCoordinates(query);
    if (coordinates) {
      if (!pointWithinActiveSearchCoverage(coordinates, view, manifest?.bounds)) {
        setResults([]);
        setStatus('Outside the forecast area');
        return;
      }
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
      if (showBest) {
        setBestDismissed(true);
        restoringFocusAfterDismiss.current = document.activeElement !== input.current;
      }
      input.current?.focus();
    }}>
      <SearchIcon />
      <label className="srOnly" htmlFor="place-search">{prompt}</label>
      <input id="place-search" ref={input} value={query} placeholder={prompt} autoComplete="off" onFocus={() => {
        onEngage();
        window.clearTimeout(blurTimer.current);
        blurTimer.current = undefined;
        if (restoringFocusAfterDismiss.current) restoringFocusAfterDismiss.current = false;
        else setBestDismissed(false);
        setFocused(true);
      }} onBlur={() => {
        window.clearTimeout(blurTimer.current);
        blurTimer.current = window.setTimeout(() => {
          blurTimer.current = undefined;
          setFocused(false);
        }, 120);
      }} onChange={(event) => {
        setBestDismissed(false);
        setQuery(event.target.value);
      }} aria-expanded={open} aria-controls="place-results" />
      {query
        ? <button className="iconButton searchAction clearSearch" type="button" aria-label="Clear search" onClick={() => { setBestDismissed(false); setQuery(''); setResults([]); setStatus(''); input.current?.focus(); }}>×</button>
        : <button className="iconButton searchAction searchLocation" type="button" aria-label="Use my location" onClick={onLocate}><LocationIcon /></button>}
      {open && (
        <div id="place-results" className="controlSurface searchPopover">
          {showBest ? <>
            <div className="bestWeatherHeading"><strong>Best weather</strong><span>Next 14 days</span></div>
            {bestWeather.length > 0 ? <ul aria-label="Best weather over the next 14 days">{bestWeather.map((result) => <li key={result.geonameId}><button type="button" onMouseDown={(event) => event.preventDefault()} onClick={() => chooseBest(result)}><span className="bestPlace"><strong>{result.name}</strong><small>{[result.admin1, result.country].filter(Boolean).join(', ')}</small></span><span className="bestScore"><strong>{result.score}</strong><small>{result.niceDays} nice {result.niceDays === 1 ? 'day' : 'days'}</small></span></button></li>)}</ul> : <p role="status">{bestStatus}</p>}
          </> : results.length > 0 ? <ul aria-label="Places">{results.map((result) => <li key={result.geonameId}><button type="button" onMouseDown={(event) => event.preventDefault()} onClick={() => choose(result)}><strong>{result.name}</strong><span>{[result.admin1, result.country].filter(Boolean).join(', ')}</span></button></li>)}</ul> : <p role="status">{status}</p>}
          {(showBest || search.attribution) && <small>Place names: <a href="https://www.geonames.org/" target="_blank" rel="noreferrer">GeoNames</a></small>}
        </div>
      )}
    </form>
  );
}

function PointPanel({ state, point, legend, lens, profile, climateModel, focusIntent, onLensChange, escapeEnabled, onClose }: { state: PointState; point: MapPoint; legend: WeatherManifest['legend']; lens: Lens; profile: Profile; climateModel: ClimateUiModel; focusIntent: DetailsFocusIntent; onLensChange: (lens: Lens) => void; escapeEnabled: boolean; onClose: () => void }) {
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

function RangeDetails({ data, legend }: { data: Extract<PointWeather, { kind: 'range' }>; legend: WeatherManifest['legend'] }) {
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

function DailyDetails({ data, legend, lens, onLensChange }: { data: DailyPoint; legend: WeatherManifest['legend']; lens: Lens; onLensChange: (lens: Lens) => void }) {
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

function ComponentRows({ data, legend, active, onChange }: { data: PointWeather; legend: WeatherManifest['legend']; active?: Lens; onChange?: (lens: Exclude<Lens, 'overall'>) => void }) {
  const rows = [
    ['comfort', 'Comfort', data.components.comfort], ['dry', 'Dry', data.components.dryness], ['bright', 'Bright', data.components.sun], ['wind', 'Calm', data.components.wind],
  ] as const;
  return <div className="componentRows" aria-label="Weather score parts">{rows.map(([id, label, score]) => {
    const colour = legend.find((band) => band.label === score.category)?.color;
    const content = <><span><strong>{label}</strong><small>{score.category}</small></span><i><b style={{ width: `${score.value}%`, backgroundColor: colour }} /></i><em>{score.value}</em></>;
    return onChange ? <button key={id} type="button" aria-pressed={active === id} onClick={() => onChange(id)}>{content}</button> : <div key={id}>{content}</div>;
  })}</div>;
}

function DockMessage({ title, action }: { title: string; action?: React.ReactNode }) { return <div className="dockMessage"><strong>{title}</strong>{action}</div>; }
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
  return view === 'climate' || Boolean(bounds && pointWithinBounds(point, bounds));
}

export function climateFieldFor(lens: Lens, profile: Profile): ClimateField {
  if (lens === 'comfort') return 'comfort_good';
  if (lens === 'dry') return 'dry_good';
  if (lens === 'bright') return 'bright_good';
  if (lens === 'wind') return profile === 'nice' ? 'nice_calm_good' : 'cycling_calm_good';
  return profile === 'nice' ? 'nice_good' : 'cycling_good';
}

export function scalarPalette(manifest: Pick<WeatherManifest, 'palette'>): readonly ScalarPaletteColour[] {
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

export function ForecastFingerprint({ days, legend }: { days: Extract<PointWeather, { kind: 'range' }>['days']; legend: WeatherManifest['legend'] }) {
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

function PeriodStatus({ manifest, view, period, climate }: { manifest: WeatherManifest; view: View; period?: Period; climate: ClimateUiModel }) {
  const status = periodStatus(manifest, view, period, climate);
  return <p className="metaLine mapStatus" role="status">
    <span className={status.stale ? 'statusPrimary isStale' : 'statusPrimary'}>{status.primary}</span>
    {status.meta && <><span className="statusSeparator" aria-hidden="true">·</span><span className="statusMeta">{status.meta}</span></>}
  </p>;
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
export function forecastScoreEvidence(days: Extract<PointWeather, { kind: 'range' }>['days'], legend: WeatherManifest['legend']): string {
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
export function placeSearchPrompt(view: View): string { return view === 'climate' ? 'Search anywhere' : 'Search Europe'; }
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
function timeLabel(value: string): string { return new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZone: 'UTC' }).format(new Date(value)) + ' UTC'; }
function defaultWindFrameIndex(frames: Period['windFrames']): number {
  if (!frames?.length) return 0;
  let closest = 0;
  let distance = Number.POSITIVE_INFINITY;
  frames.forEach((frame, index) => {
    const date = new Date(frame.validAt);
    const fromNoon = Math.abs(date.getUTCHours() * 60 + date.getUTCMinutes() - 12 * 60);
    if (fromNoon < distance) { closest = index; distance = fromNoon; }
  });
  return closest;
}
function format(value: number, digits: number): string { return new Intl.NumberFormat('en-GB', { maximumFractionDigits: digits, minimumFractionDigits: digits }).format(value); }
function optional(value: number | undefined, present: (value: number) => string): string { return value === undefined ? 'Not available' : present(value); }

function SunMark() { return <svg viewBox="0 0 28 28" aria-hidden="true"><circle cx="14" cy="14" r="4.5" /><path d="M14 2v5M14 21v5M2 14h5M21 14h5M5.5 5.5 9 9M19 19l3.5 3.5M22.5 5.5 19 9M9 19l-3.5 3.5" /></svg>; }
function SearchIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6" /><path d="m15 15 5 5" /></svg>; }
function LocationIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="4" /><path d="M12 2v4M12 18v4M2 12h4M18 12h4" /></svg>; }
