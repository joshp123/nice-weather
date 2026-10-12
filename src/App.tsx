import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import { nextDetailsFocusIntent, restoreFocus, shouldFocusDetails, type DetailsFocusIntent } from './accessibility';
import { loadBestWeather, type BestWeatherPlace } from './data/bestWeather';
import { ClimateDataError, WorldwideClimateClient, type ClimateCatalog, type ClimateField, type ClimateIndex, type ClimatePointEvidence } from './data/climateClient';
import { ForecastDataError, WorldwideForecastClient, forecastDetailDeclared, forecastRunStatus, isStale, type DayForecastProduct, type RangeForecastCatalog, type ForecastField, type ForecastSnapshot } from './data/forecastClient';
import { WorldwidePlaceSearch, type PlaceSearchResult, type PlaceSearchViewport } from './data/placeSearch';
import type { DailyPoint, Lens, PointWeather, Profile, View, PaletteStop } from './data/types';
import type { MapPoint, OverlayState, ScalarOverlay } from './map/WeatherMap';
import { coveringTiles, type GeographicBounds, type ScalarPaletteColour, type TileDraw } from './map/scalarTileLayer';
import { pointSourceLabel } from './presentation';
import { reportResourceSummary, telemetry } from './telemetry';
import { WEATHER_PRESENTATION, type WeatherPresentation } from './weatherPresentation';
import './styles.css';

const WeatherMap = lazy(() => import('./map/WeatherMap').then((module) => ({ default: module.WeatherMap })));
const FORECAST_TEXTURE_BUDGET = 64;

type RememberedView = { view: View; period: string; profile: Profile; lens: Lens; camera?: { longitude: number; latitude: number; zoom: number } };
function rememberedView(): RememberedView {
  const defaults: RememberedView = { view: 'range', period: '', profile: 'nice', lens: 'overall' };
  try {
    const saved = JSON.parse(localStorage.getItem('nice-weather-view') ?? 'null');
    if (!saved) return defaults;
    const camera = saved.camera;
    return {
      view: ['range', 'daily', 'climate'].includes(saved.view) ? saved.view : defaults.view,
      period: typeof saved.period === 'string' ? saved.period : '',
      profile: saved.profile === 'cycling' ? 'cycling' : 'nice',
      lens: ['overall', 'comfort', 'dry', 'bright', 'wind'].includes(saved.lens) ? saved.lens : 'overall',
      ...(camera && Number.isFinite(camera.longitude) && Math.abs(camera.longitude) <= 180 && Number.isFinite(camera.latitude) && Math.abs(camera.latitude) <= 85 && Number.isFinite(camera.zoom) && camera.zoom >= 0 && camera.zoom <= 9 ? { camera } : {}),
    };
  } catch { return defaults; }
}
function rememberView(view: RememberedView) {
  try { localStorage.setItem('nice-weather-view', JSON.stringify(view)); } catch { /* Optional preference. */ }
}

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
  | { status: 'ready'; kind: 'forecast'; data: PointWeather; point: MapPoint; period: string; profile: Profile }
  | { status: 'ready'; kind: 'climate'; data: ClimatePointEvidence; point: MapPoint }
  | { status: 'error'; message: string };
type PeriodChoice = Readonly<{ id: string; shortLabel: string; date?: string }>;

export default function App() {
  const savedView = useMemo(rememberedView, []);
  const savedCamera = useRef(savedView.camera);
  const [climateState, setClimateState] = useState<ClimateState>({ status: 'loading' });
  const [climateCatalogState, setClimateCatalogState] = useState<ClimateCatalogState>({ status: 'idle' });
  const [forecastState, setForecastState] = useState<ForecastState>({ status: 'loading' });
  const [forecastDayState, setForecastDayState] = useState<ForecastDayState>({ status: 'idle' });
  const [climateRetry, setClimateRetry] = useState(0);
  const [forecastRetry, setForecastRetry] = useState(0);
  const [now, setNow] = useState(() => new Date());
  const [view, setView] = useState<View>(() => savedView.view === 'daily' ? 'daily' : 'range');
  const [profile, setProfile] = useState<Profile>(savedView.profile);
  const [lens, setLens] = useState<Lens>(savedView.lens);
  const [periodId, setPeriodId] = useState(savedView.period);
  const [rangeEvidence, setRangeEvidence] = useState<{ point: MapPoint; profile: Profile; indexId: string; days: readonly DailyPoint[] }>();
  const [rangeEvidenceUnavailable, setRangeEvidenceUnavailable] = useState(false);
  const annualClimateClient = useMemo(() => new WorldwideClimateClient(), []);
  const [annualEvidence, setAnnualEvidence] = useState<readonly ClimatePointEvidence[]>([]);
  const restoredTime = useRef(false);
  const [selectedPoint, setSelectedPoint] = useState<MapPoint>();
  const [focusPoint, setFocusPoint] = useState<(MapPoint & { token: number })>();
  const [viewport, setViewport] = useState<PlaceSearchViewport>({ latitude: 48.5, longitude: 9.5 });
  const [mapNotice, setMapNotice] = useState('');
  const [overlayState, setOverlayState] = useState<OverlayState>({ status: 'idle' });
  const [mapSelectionToken, setMapSelectionToken] = useState(0);
  const [pointState, setPointState] = useState<PointState>({ status: 'idle' });
  const [detailsFocusIntent, setDetailsFocusIntent] = useState<DetailsFocusIntent>({ token: 0, requested: false });
  const pointFocusOrigin = useRef<HTMLElement | null>(null);
  const staleReloadedClimateIndex = useRef<string | undefined>(undefined);
  const climateClient = useMemo(() => new WorldwideClimateClient(), []);
  const forecastClient = useMemo(() => new WorldwideForecastClient(), []);

  useEffect(() => {
    const controller = new AbortController();
    let pending = false;
    let lastAttempt = 0;
    const revalidate = async () => {
      setNow(new Date());
      if (pending || Date.now() - lastAttempt < 5_000) return;
      pending = true;
      lastAttempt = Date.now();
      const startedAt = performance.now();
      try {
        const snapshot = await forecastClient.loadSnapshot(controller.signal);
        if (controller.signal.aborted) return;
        setForecastState((previous) => previous.status === 'ready' && previous.snapshot.index.indexId === snapshot.index.indexId ? previous : { status: 'ready', snapshot });
        telemetry('forecast-index-ready', { elapsedMs: Math.round(performance.now() - startedAt), releaseId: snapshot.index.indexId, days: snapshot.index.days.length });
      } catch (error) {
        if (controller.signal.aborted) return;
        telemetry('forecast-index-error', { code: error instanceof ForecastDataError ? error.code : 'unexpected', name: error instanceof Error ? error.name : 'unexpected' });
        setForecastState((previous) => previous.status === 'ready' ? previous : { status: 'unavailable' });
      } finally { pending = false; }
    };
    const resume = () => { if (document.visibilityState === 'visible') void revalidate(); };
    const timer = window.setInterval(() => {
      if (document.visibilityState !== 'visible') return;
      setNow(new Date());
      if (Date.now() - lastAttempt >= 5 * 60_000) void revalidate();
    }, 60_000);
    window.addEventListener('focus', resume);
    document.addEventListener('visibilitychange', resume);
    void revalidate();
    return () => {
      controller.abort();
      window.clearInterval(timer);
      window.removeEventListener('focus', resume);
      document.removeEventListener('visibilitychange', resume);
    };
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
  const activeDayProduct = view === 'daily' && forecastDayState.status === 'ready' && forecastState.status === 'ready'
    && forecastDayState.product.date === forecastPeriod?.id
    && forecastDayState.product.indexId === forecastState.snapshot.index.indexId
    ? forecastDayState.product : undefined;
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
  const pointMatchesSelection = pointState.status !== 'ready' || (pointState.point === selectedPoint && (pointState.kind === 'forecast'
    ? view !== 'climate' && forecastState.status === 'ready' && pointState.data.releaseId === forecastState.snapshot.index.indexId && pointState.period === activePeriodId && pointState.profile === profile
    : view === 'climate' && pointState.data.period === climatePeriodId && pointState.data.releaseId === expectedClimateReleaseId));
  const visiblePointState: PointState = pointMatchesSelection ? pointState : { status: 'loading' };
  const rangeDailyEvidence = rangeEvidence && rangeEvidence.point === selectedPoint && rangeEvidence.profile === profile
    && forecastState.status === 'ready' && rangeEvidence.indexId === forecastState.snapshot.index.indexId ? rangeEvidence.days : [];

  useEffect(() => {
    if (climateState.status === 'loading') return;
    if (!restoredTime.current) {
      restoredTime.current = true;
      const saved = savedView;
      if (saved.view === 'climate' && climateModel.available) {
        setView('climate');
        setPeriodId(saved.period);
        return;
      }
    }
    rememberView({ view, period: periodId, profile, lens, camera: savedCamera.current });
  }, [view, periodId, profile, lens, savedView, climateState.status, climateModel.available]);

  useEffect(() => {
    setRangeEvidence(undefined);
    setRangeEvidenceUnavailable(false);
    if (view !== 'range' || !selectedPoint || !pointMatchesSelection || forecastState.status !== 'ready' || pointState.status !== 'ready' || pointState.kind !== 'forecast' || pointState.data.kind !== 'range') return;
    const controller = new AbortController();
    const snapshot = forecastState.snapshot;
    const point = pointState.data.sourceCell ? { lon: pointState.data.sourceCell.longitude, lat: pointState.data.sourceCell.latitude } : selectedPoint;
    const presentation = { legend: manifest.legend, generatedAt: snapshot.range.source.runUtc };
    async function loadPhysicalEvidence() {
      const days = await forecastClient.loadPeriodEvidence(snapshot, point.lon, point.lat, profile, presentation, controller.signal);
      if (!controller.signal.aborted) setRangeEvidence({ point: selectedPoint!, profile, indexId: snapshot.index.indexId, days });
    }
    loadPhysicalEvidence().catch((error: unknown) => {
      if (controller.signal.aborted) return;
      if (error instanceof ForecastDataError && error.code === 'stale') setForecastRetry((value) => value + 1);
      else { setRangeEvidenceUnavailable(true); telemetry('place-physical-evidence-unavailable', { name: error instanceof Error ? error.name : 'unexpected' }); }
    });
    return () => controller.abort();
  }, [view, selectedPoint, pointMatchesSelection, pointState, forecastState, profile, forecastClient, manifest]);

  useEffect(() => {
    setAnnualEvidence([]);
    if (view !== 'climate' || !selectedPoint || climateState.status !== 'ready') return;
    const controller = new AbortController();
    const index = climateState.index;
    const point = selectedPoint;
    async function loadYear() {
      const months: ClimatePointEvidence[] = [];
      for (const reference of index.months) {
        const evidence = await annualClimateClient.loadPoint(reference.period, point.lon, point.lat, controller.signal, point.allowNearbyClimate === true);
        if (!evidence) return;
        if (evidence.releaseId !== reference.releaseId || evidence.period !== reference.period) throw new ClimateDataError('stale');
        const first = months[0];
        if (first && (evidence.sourceCell.latitude !== first.sourceCell.latitude || evidence.sourceCell.longitude !== first.sourceCell.longitude)) return;
        months.push(evidence);
      }
      if (!controller.signal.aborted && months.length === 12) setAnnualEvidence(months);
    }
    loadYear().catch((error: unknown) => {
      if (controller.signal.aborted) return;
      telemetry('annual-place-evidence-unavailable', { name: error instanceof Error ? error.name : 'unexpected' });
      if (error instanceof ClimateDataError && error.code === 'stale') setClimateRetry((value) => value + 1);
    });
    return () => controller.abort();
  }, [view, selectedPoint, climateState, annualClimateClient]);

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
        if (controller.signal.aborted) return;
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
      climateClient.loadPoint(activePeriodId, selectedPoint.lon, selectedPoint.lat, controller.signal, selectedPoint.allowNearbyClimate === true)
        .then((data) => {
          if (controller.signal.aborted) return;
          if (!data) {
            setPointState({ status: 'error', message: 'No recent climate record is available for this point.' });
            return;
          }
          if (data.releaseId !== activeCatalog.releaseId || data.period !== activeCatalog.period) {
            if (climateIndexId && refreshStaleClimate(`${climateIndexId}->${data.releaseId}`)) return;
            setPointState({ status: 'error', message: 'Climate data changed. Select this place again.' });
            return;
          }
          setPointState({ status: 'ready', kind: 'climate', data, point: selectedPoint });
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
    const dayProduct = activeDayProduct;
    if (forecastState.status !== 'ready' || (view === 'daily' && !dayProduct)) {
      setPointState({ status: 'idle' });
      return () => controller.abort();
    }
    const presentation = { legend: manifest.legend, generatedAt: forecastState.snapshot.range.source.runUtc };
    let request: Promise<PointWeather>;
    if (view === 'range') {
      request = forecastClient.loadRangePoint(forecastState.snapshot, selectedPoint.lon, selectedPoint.lat, profile, presentation, controller.signal, selectedPoint.allowNearbyClimate);
    } else {
      if (!dayProduct) return () => controller.abort();
      request = selectedPoint.allowNearbyClimate
        ? forecastClient.loadRangePoint(forecastState.snapshot,selectedPoint.lon,selectedPoint.lat,profile,presentation,controller.signal,true).then(async (range) => {
          const location=range.sourceCell;
          const daily=await forecastClient.loadDailyPoint(dayProduct,location?.longitude ?? selectedPoint.lon,location?.latitude ?? selectedPoint.lat,profile,presentation,controller.signal);
          return { ...daily,lon:selectedPoint.lon,lat:selectedPoint.lat,sourceCell:location };
        })
        : forecastClient.loadDailyPoint(dayProduct, selectedPoint.lon, selectedPoint.lat, profile, presentation, controller.signal);
    }
    request
      .then((data) => {
        if (controller.signal.aborted) return;
        if (data.releaseId !== forecastState.snapshot.index.indexId) throw new ForecastDataError('stale');
        setPointState({ status: 'ready', kind: 'forecast', data, point: selectedPoint, period: activePeriodId, profile });
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
  }, [activeDayProduct, activePeriodId, climateCatalogState, climateClient, climateIndexId, forecastClient, forecastState, manifest, pointProfileKey, refreshForecast, refreshStaleClimate, selectedPoint, view]);

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
      : activeDayProduct;
    if (!product) return undefined;
    const field = forecastFieldFor(lens, profile);
    return {
      key: `forecast:${forecastState.snapshot.index.indexId}:${view}:${view === 'daily' ? activeDayProduct?.date : 'range'}:${field}`,
      minSourceZoom: 0,
      maxSourceZoom: 5,
      maxTextures: FORECAST_TEXTURE_BUDGET,
      palette: scalarPalette(manifest),
      urlForTile: () => '',
      sha256ForTile: () => '',
      tileAvailable: () => false,
      visibleTilesForMap: (bounds, mapZoom) => forecastMapTiles(product, bounds, mapZoom),
      sourceForTile: (tile) => forecastClient.scalarSourceForOutputTile(product, field, tile),
    };
  }, [activeDayProduct, climateCatalogState, climateClient, forecastClient, forecastState, lens, manifest, profile, view]);

  const handleView = (next: View) => {
    if (next === 'climate' && !climateModel.available) return;
    setMapNotice('');
    telemetry('view-changed', { view: next });
    setView(next);
    setPeriodId('');
  };

  const selectPoint = (point: MapPoint, focus = true, focusDetails = false) => {
    setSelectedPoint(point);
    setFocusPoint(focus ? { ...point, token: Date.now() } : undefined);
    setDetailsFocusIntent((current) => nextDetailsFocusIntent(current, focusDetails));
    setMapNotice('');
    telemetry('point-selected', { lat: Number(point.lat.toFixed(3)), lon: Number(point.lon.toFixed(3)), labelled: Boolean(point.label) });
  };

  return (
    <main className={`appShell view-${view}${(view !== 'climate' ? forecastPeriods.length > 0 : climateRail.length > 0) ? ' hasRail' : ''}${selectedPoint ? ' hasPoint' : ''}`}>
      <Suspense fallback={<div className="mapCanvas mapLoading" aria-label="Loading map" />}>
        <WeatherMap
          scalarOverlay={scalarOverlay}
          initialCamera={savedView.camera}
          selectedPoint={selectedPoint}
          focusPoint={focusPoint}
          onSelect={(point, intent) => {
            pointFocusOrigin.current = intent?.focusDetails && document.activeElement instanceof HTMLElement ? document.activeElement : null;
            setMapSelectionToken((value) => value + 1);
            selectPoint(point, false, shouldFocusDetails(intent?.focusDetails ? 'map-keyboard' : 'map-pointer'));
          }}
          onViewportChange={({ latitude, longitude, zoom }) => {
            setViewport({ latitude, longitude });
            savedCamera.current = { latitude, longitude, zoom };
            rememberView({ view, period: periodId, profile, lens, camera: savedCamera.current });
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
          <PeriodStatus
            view={view}
            now={now}
            climate={climateModel}
            forecast={forecastState.status === 'ready' ? forecastState.snapshot : undefined}
            day={activeDayProduct}
            selectedForecast={visiblePointState.status === 'ready' && visiblePointState.kind === 'forecast' ? visiblePointState.data : undefined}
          />
        </div>
        <PlaceSearch now={now} viewport={viewport} view={view} profile={profile} forecast={forecastState.status === 'ready' ? forecastState.snapshot : undefined} clearToken={mapSelectionToken} onLocate={() => locate((point) => {
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
          state={visiblePointState}
          point={selectedPoint}
          legend={manifest.legend}
          lens={lens}
          profile={profile}
          climateModel={climateModel}
          annualEvidence={annualEvidence}
          onMonthChange={setPeriodId}
          evidenceUnavailable={rangeEvidenceUnavailable}
          dailyEvidence={rangeDailyEvidence}
          onDayChange={(id) => { setView('daily'); setPeriodId(id); }}
          focusIntent={detailsFocusIntent}
          onLensChange={(next) => { setLens(next); telemetry('lens-changed', { lens: next, source: 'place-sheet' }); }}
          escapeEnabled
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
          <div className="segmentedControl timeControl" role="group" aria-label="Weather period">
            <button type="button" aria-pressed={view !== 'climate'} onClick={() => handleView('range')}>Forecast</button>
            {climateModel.available && <button type="button" aria-pressed={view === 'climate'} onClick={() => handleView('climate')}>Year</button>}
          </div>
          <div className="segmentedControl preferenceControl" role="group" aria-label="Weather preference">
            {(['nice', 'cycling'] as const).map((value) => <button key={value} type="button" aria-pressed={profile === value} onClick={() => { setProfile(value); telemetry('profile-changed', { profile: value }); }}>{value === 'nice' ? 'Nice' : 'Cycling'}</button>)}
          </div>
        </div>
        {view !== 'climate' && forecastPeriods.length > 0 && <PeriodRail periods={[{ id: 'next-14-days', shortLabel: '14 days' }, ...forecastPeriods]} selectedId={view === 'range' ? 'next-14-days' : forecastPeriod?.id ?? ''} label="Forecast date" onChange={(id) => { setView(id === 'next-14-days' ? 'range' : 'daily'); setPeriodId(id === 'next-14-days' ? '' : id); telemetry('period-changed', { period: id, view: 'forecast' }); }} />}
        {view === 'climate' && climatePeriodId && <PeriodRail periods={climateRail} selectedId={climatePeriodId} label="Month" onChange={setPeriodId} />}
        <div className="segmentedControl lensControl" role="group" aria-label="Map lens">
          {LENSES.map(({ id, label }) => <button key={id} type="button" aria-pressed={lens === id} onClick={() => { setLens(id); telemetry('lens-changed', { lens: id }); }}>{label}</button>)}
        </div>
        {overlayState.status !== 'error' && <ScoreRamp manifest={manifest} probability={view === 'climate'} profile={profile} />}
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

function PeriodRail({ periods, selectedId, label, onChange }: { periods: readonly PeriodChoice[]; selectedId: string; label: string; onChange: (id: string) => void }) {
  const rail = useRef<HTMLDivElement>(null);
  const selected = useRef(selectedId);
  const mounted = useRef(false);
  selected.current = selectedId;
  const [hasMore, setHasMore] = useState(false);
  const [hasBefore, setHasBefore] = useState(false);
  const updateCue = useCallback(() => {
    setHasMore(periodRailHasMore(rail.current));
    setHasBefore(Boolean(rail.current && rail.current.scrollLeft > 2));
  }, []);
  useEffect(() => {
    revealSelectedPeriod(rail.current, selectedId, mounted.current ? 'smooth' : 'auto');
    mounted.current = true;
    const frame = window.requestAnimationFrame(updateCue);
    return () => window.cancelAnimationFrame(frame);
  }, [periods, selectedId, updateCue]);
  useEffect(() => {
    const container = rail.current;
    if (!container) return;
    let frame = 0;
    const observer = new ResizeObserver(() => {
      revealSelectedPeriod(container, selected.current, 'auto');
      window.cancelAnimationFrame(frame);
      frame = window.requestAnimationFrame(updateCue);
    });
    observer.observe(container);
    return () => { observer.disconnect(); window.cancelAnimationFrame(frame); };
  }, [updateCue]);
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
  { id: 'overall', label: 'Overall' }, { id: 'comfort', label: 'Comfort' }, { id: 'dry', label: 'Dry' }, { id: 'bright', label: 'Clear sky' }, { id: 'wind', label: 'Calm' },
];

export function ScoreRamp({ manifest, probability = false, profile }: { manifest: WeatherPresentation; probability?: boolean; profile: Profile }) {
  const outcome = climateOutcomeCopy(profile);
  return <div className="scoreRamp" aria-label={probability ? outcome.rampAriaLabel : 'Weather suitability colours'}>
    {probability
      ? <><span>{outcome.rareLabel}</span><i className="probabilityRamp" style={{ background: `linear-gradient(90deg, ${manifest.legend.map((band) => band.color).join(', ')})` }} /><span>{outcome.usualLabel}</span></>
      : manifest.legend.map((band) => <span key={band.label}><i style={{ background: band.color }} />{band.label}</span>)}
    <span className="noDataLegend"><i />No data</span>
  </div>;
}

export type BestWeatherState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'unavailable' }
  | { status: 'ready'; profile: Profile; results: readonly BestWeatherPlace[] };

function PlaceSearch({ now, viewport, view, profile, forecast, clearToken, onLocate, onFound }: { now: Date; viewport: PlaceSearchViewport; view: View; profile: Profile; forecast?: ForecastSnapshot; clearToken: number; onLocate: () => void; onFound: (point: MapPoint, origin: HTMLElement | null) => void }) {
  const search = useMemo(() => new WorldwidePlaceSearch(), []);
  const viewportRef = useRef(viewport);
  viewportRef.current = viewport;
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<PlaceSearchResult[]>([]);
  const [status, setStatus] = useState('');
  const [focused, setFocused] = useState(false);
  const [bestWeather, setBestWeather] = useState<BestWeatherState>({ status: 'idle' });
  const [bestWeatherDismissed, setBestWeatherDismissed] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const requestSequence = useRef(0);
  const bestWeatherRequestSequence = useRef(0);
  const blurTimer = useRef<number | undefined>(undefined);
  const emptyQuery = query.trim() === '';
  const bestWeatherAllowed = view === 'range' && forecast !== undefined && !isStale(forecast.range.source.runUtc, forecast.range.source.staleAfterHours, now);
  const currentBestWeather = bestWeather.status !== 'ready' || bestWeather.profile === profile;
  const showBestWeather = bestWeatherAllowed && currentBestWeather && emptyQuery && !bestWeatherDismissed && bestWeather.status !== 'idle';
  const open = focused && (results.length > 0 || Boolean(status) || showBestWeather);
  const prompt = placeSearchPrompt(view);
  const engageInput = () => {
    window.clearTimeout(blurTimer.current);
    blurTimer.current = undefined;
    setFocused(true);
    setBestWeatherDismissed(false);
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
    const request = ++bestWeatherRequestSequence.current;
    if (!bestWeatherAllowed || !forecast) {
      setBestWeather({ status: 'idle' });
      return;
    }
    const controller = new AbortController();
    setBestWeather({ status: 'loading' });
    loadBestWeather({
      indexId: forecast.index.indexId,
      firstDate: forecast.index.firstDate,
      lastDate: forecast.index.lastDate,
      runUtc: forecast.range.source.runUtc,
    }, profile, controller.signal)
      .then(({ results: recommendations, profile: resultProfile }) => {
        if (controller.signal.aborted || request !== bestWeatherRequestSequence.current) return;
        setBestWeather({ status: 'ready', profile: resultProfile, results: recommendations });
        telemetry('best-weather-ready', { profile, count: recommendations.length });
      })
      .catch(() => {
        if (!controller.signal.aborted && request === bestWeatherRequestSequence.current) setBestWeather({ status: 'unavailable' });
      });
    return () => controller.abort();
  }, [bestWeatherAllowed, forecast, profile]);
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

  const choose = (result: PlaceSearchResult | BestWeatherPlace, source: 'search' | 'best-weather' = 'search') => {
      setQuery(result.name);
    setResults([]);
    setStatus('');
    setFocused(false);
    input.current?.blur();
    onFound({ lat: result.latitude, lon: result.longitude, label: result.name, secondaryLabel: [result.admin1, result.country].filter((item, index, all) => item && item !== result.name && all.indexOf(item) === index).join(', '), allowNearbyClimate: true }, input.current);
    telemetry(source === 'search' ? 'search-selected' : 'best-weather-selected', { geonameId: result.geonameId, country: result.countryCode, profile });
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
    <div className="placeSearchGroup">
    <form className="controlSurface placeSearch" role="search" onSubmit={submit} onKeyDown={(event) => {
      if (event.key !== 'Escape' || !open) return;
      event.preventDefault();
      event.stopPropagation();
      setResults([]);
      setStatus('');
      setBestWeatherDismissed(true);
      input.current?.focus();
    }}>
      <SearchIcon />
      <label className="srOnly" htmlFor="place-search">{prompt}</label>
      <input id="place-search" ref={input} value={query} placeholder={prompt} autoComplete="off" onPointerDown={engageInput} onFocus={() => {
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
        ? <button className="iconButton searchAction clearSearch" type="button" aria-label="Clear search" onClick={() => { setQuery(''); setResults([]); setStatus(''); setBestWeatherDismissed(false); input.current?.focus(); }}>×</button>
        : <button className="iconButton searchAction searchLocation" type="button" aria-label="Use my location" onClick={onLocate}><LocationIcon /></button>}
      {open && (
        <div id="place-results" className="controlSurface searchPopover">
          {results.length > 0
            ? <ul aria-label="Places">{results.map((result) => <li key={result.geonameId}><button type="button" onMouseDown={(event) => event.preventDefault()} onClick={() => choose(result)}><strong>{result.name}</strong><span>{[result.admin1, result.country].filter(Boolean).join(', ')}</span></button></li>)}</ul>
            : showBestWeather
              ? <BestWeatherResults state={bestWeather} onChoose={(result) => choose(result, 'best-weather')} />
              : <p role="status">{status}</p>}
          {(search.attribution || bestWeather.status === 'ready') && <small>Place names: <a href="https://www.geonames.org/" target="_blank" rel="noreferrer">GeoNames</a></small>}
        </div>
      )}
    </form>

    </div>
  );
}

export function BestWeatherResults({ state, onChoose }: { state: BestWeatherState; onChoose: (result: BestWeatherPlace) => void }) {
  if (state.status === 'idle') return null;
  if (state.status === 'loading') return <p role="status">Finding the best weather…</p>;
  if (state.status === 'unavailable') return <p role="status">Best weather is not available right now.</p>;
  return <>
    <div className="bestWeatherHeading"><strong>{state.profile === 'cycling' ? 'Best for cycling' : 'Best weather'}</strong><span>Next 14 days</span></div>
    <ul aria-label="Best weather worldwide">{state.results.map((result) => <li key={result.geonameId}>
      <button type="button" aria-label={`${result.name}, ${[result.admin1, result.country].filter(Boolean).join(', ')}, score ${result.score}, ${forecastOutcomeCopy(state.profile, result.niceDays).summaryLabel}`} onMouseDown={(event) => event.preventDefault()} onClick={() => onChoose(result)}>
        <span className="bestPlace"><strong>{result.name}</strong><small>{[result.admin1, result.country].filter(Boolean).join(', ')}</small></span>
        <span className="bestScore"><strong>{result.score}</strong><small>{result.niceDays}/14 {state.profile === 'cycling' ? 'cycling' : 'nice'}</small></span>
      </button>
    </li>)}</ul>
  </>;
}

function PointPanel({ state, point, legend, lens, profile, climateModel, dailyEvidence, evidenceUnavailable, annualEvidence, onMonthChange, onDayChange, focusIntent, onLensChange, escapeEnabled, onClose }: { state: PointState; point: MapPoint; legend: WeatherPresentation['legend']; lens: Lens; profile: Profile; climateModel: ClimateUiModel; dailyEvidence: readonly DailyPoint[]; evidenceUnavailable: boolean; annualEvidence: readonly ClimatePointEvidence[]; onMonthChange: (id: string) => void; onDayChange: (id: string) => void; focusIntent: DetailsFocusIntent; onLensChange: (lens: Lens) => void; escapeEnabled: boolean; onClose: () => void }) {
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
          : <PointHeadingFor data={state.data} point={point} profile={profile} />)}
      </div>
      <div className="pointPanelBody">
        {state.status === 'ready' && state.kind === 'forecast' && state.data.sourceCell && <p className="sourceLine">Nearest complete weather cell · {Math.max(1,Math.round(state.data.sourceCell.distanceKm))} km away</p>}
        {state.status === 'loading' && <PointSkeleton label={point.label} />}
        {state.status === 'error' && <div className="pointMessage"><h2>{point.label ?? 'Selected place'}</h2><strong>Weather unavailable</strong><p>{state.message}</p></div>}
        {state.status === 'ready' && (state.kind === 'climate'
          ? <ClimateDetails data={state.data} point={point} profile={profile} lens={lens} onLensChange={onLensChange} climateModel={climateModel} annualEvidence={annualEvidence} onMonthChange={onMonthChange} />
          : state.data.kind === 'range'
            ? <RangeDetails data={state.data} legend={legend} profile={profile} dailyEvidence={dailyEvidence} evidenceUnavailable={evidenceUnavailable} lens={lens} onLensChange={onLensChange} onDayChange={onDayChange} />
            : <DailyDetails data={state.data} legend={legend} lens={lens} onLensChange={onLensChange} />)}
        {state.status === 'idle' && <div className="pointMessage"><h2>{point.label ?? 'Selected place'}</h2><p>{point.lat.toFixed(2)}°, {point.lon.toFixed(2)}°</p></div>}
      </div>
      {state.status === 'ready' && <button className="sheetExpand" type="button" aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}>{expanded ? 'Show less' : 'Show all details'}</button>}
    </aside>
  );
}

export function ClimatePointHeading({ data, point, profile, climateModel }: { data: ClimatePointEvidence; point: MapPoint; profile: Profile; climateModel: ClimateUiModel }) {
  const evidence = data[profile];
  const month = monthName(data.period);
  const eyebrow = `Real ${month} days · ${climateModel.referencePeriod}`;
  const headline = profile === 'cycling' ? `Chance of a good cycling day in ${month}` : `Chance of a nice ${month} day`;
  return <div className="pointHeading climatePointHeading"><div><p>{eyebrow}</p><h2>{point.label ?? `${point.lat.toFixed(2)}°, ${point.lon.toFixed(2)}°`}</h2>{point.secondaryLabel && <small className="placeRegion">{point.secondaryLabel}</small>}</div><div className="score"><strong>{evidence.goodOrBetterPercent}%</strong><span>{headline}</span></div></div>;
}

export function ClimateDetails({ data, point, profile, lens, onLensChange, climateModel, annualEvidence = [], onMonthChange }: { data: ClimatePointEvidence; point: MapPoint; profile: Profile; lens: Lens; onLensChange: (lens: Lens) => void; climateModel: ClimateUiModel; annualEvidence?: readonly ClimatePointEvidence[]; onMonthChange?: (id: string) => void }) {
  return <>
    <p className="phonePointSummary climateReliability">{climateReliability(data, profile)}</p>
    <p className="rangeEvidence climateReliability">{climateReliability(data, profile)}</p>
    <ClimateComponentRows data={data} profile={profile} active={lens} onChange={onLensChange} />
    {annualEvidence.length === 12 && <AnnualWeatherChart months={annualEvidence} selected={data.period} profile={profile} onChange={onMonthChange} />}
    <p className="sourceLine">Real daily evidence · {climateModel.referencePeriod} · worldwide · about 31 km weather detail{nearbyClimateCellQualifier(data, point)}</p>
  </>;
}

function AnnualWeatherChart({ months, selected, profile, onChange }: { months: readonly ClimatePointEvidence[]; selected: string; profile: Profile; onChange?: (id: string) => void }) {
  const ranked = [...months].sort((left, right) => right[profile].goodOrBetterPermille - left[profile].goodOrBetterPermille);
  const best = ranked.slice(0, 3).sort((left, right) => monthNumber(left.period) - monthNumber(right.period));
  return <section className="annualChart" aria-label="Compare every month">
    <h3>{ranked[0][profile].goodOrBetterPermille === ranked[11][profile].goodOrBetterPermille ? 'Similar throughout the year' : `Best months: ${best.map((month) => shortMonthName(month.period)).join(', ')}`}</h3>
    <div>{months.map((month) => {
      const chance = month[profile].goodOrBetterPercent;
      const colour = WEATHER_PRESENTATION.legend.find((band) => chance >= band.min && chance <= band.max)?.color;
      return <button type="button" key={month.period} aria-pressed={selected === month.period} aria-label={`${monthName(month.period)}: ${chance}% ${profile === 'nice' ? 'nice days' : 'good cycling days'}`} onClick={() => onChange?.(month.period)}><span className="annualBar"><i style={{ height: `${chance}%`, background: colour }} /></span><span>{shortMonthName(month.period)}</span></button>;
    })}</div>
    <p>Chance of a {profile === 'nice' ? 'nice day' : 'good cycling day'} · January–December</p>
  </section>;
}

export function nearbyClimateCellQualifier(data: ClimatePointEvidence, point: Pick<MapPoint, 'lat' | 'lon'>): string {
  if (!data.usedAdjacentCell) return '';
  const kilometres = Math.max(1, Math.round(pointDistanceKm(point, data.sourceCell)));
  return ` · nearest climate cell ${kilometres} km away`;
}

function pointDistanceKm(point: Pick<MapPoint, 'lat' | 'lon'>, source: ClimatePointEvidence['sourceCell']): number {
  const radians = Math.PI / 180;
  const latitudeDelta = (source.latitude - point.lat) * radians;
  const longitudeDelta = ((((source.longitude - point.lon) + 180) % 360 + 360) % 360 - 180) * radians;
  const a = Math.sin(latitudeDelta / 2) ** 2 + Math.cos(point.lat * radians) * Math.cos(source.latitude * radians) * Math.sin(longitudeDelta / 2) ** 2;
  return 2 * 6371.0088 * Math.asin(Math.min(1, Math.sqrt(a)));
}

function ClimateComponentRows({ data, profile, active, onChange }: { data: ClimatePointEvidence; profile: Profile; active: Lens; onChange: (lens: Lens) => void }) {
  const physical = data.physical;
  const rows = [
    ['comfort', 'Comfort', data.comfortGoodPercent, `Daytime temperature was ${climateRange(physical.daytimeTemperatureP10C, physical.daytimeTemperatureP90C, '°C')} on 80% of days · dew point ${climateValue(physical.dewPointP50C)}°C typical · 90% at or below ${climateValue(physical.dewPointP90C)}°C · muggy ${physical.muggyDayPercent}%`],
    ['dry', 'Dry', data.dryGoodPercent, `Rain-free days ${physical.rainFreeDayPercent}% · 90% had ${climateValue(physical.rainP90MM)} mm or less`],
    ['bright', 'Clear sky', data.clearSkyGoodPercent, `Cloud cover ${physical.cloudCoverP50Percent}% typical · clear-sky days ${physical.clearSkyDayPercent}%`],
    ['wind', 'Calm', data[profile].calmGoodPercent, `Wind ${climateValue(physical.windP50KPH)} km/h typical · 90% at or below ${climateValue(physical.windP90KPH)} km/h`],
  ] as const;
  return <div className="componentRows climateComponents" aria-label="Chance of good weather by component">{rows.map(([id, label, value, evidence]) => (
    <button key={id} type="button" aria-pressed={active === id} aria-label={`${label}: ${value}% good or better. ${evidence}`} onClick={() => onChange(id)}>
      <span><strong>{label}</strong><small>Good or better</small></span><em>{value}%</em>
      <span className="componentEvidence">{evidence}</span>
    </button>
  ))}</div>;
}

export function PointHeadingFor({ data, point, profile = 'nice' }: { data: PointWeather; point: MapPoint; profile?: Profile }) {
  const eyebrow = data.kind === 'range'
    ? `${shortRange(data.window.startDate, data.window.endDate)} · ${dayCountLabel(data.window.dayCount)}`
    : data.periodLabel;
  if (data.kind === 'range') return <div className="pointHeading"><div><p>{eyebrow}</p><h2>{point.label ?? 'Selected place'}</h2>{point.secondaryLabel && <small className="placeRegion">{point.secondaryLabel}</small>}</div><div className="score"><strong>{data.window.niceDays}<small>/{data.window.dayCount}</small></strong><span>{profile === 'cycling' ? 'cycling days' : 'nice days'}</span></div></div>;
  return <PointHeading point={point} eyebrow={eyebrow} score={data.score} category={data.category} />;
}

export function RangeDetails({ data, legend, profile, dailyEvidence = [], evidenceUnavailable = false, lens = 'overall', onLensChange, onDayChange }: { data: Extract<PointWeather, { kind: 'range' }>; legend: WeatherPresentation['legend']; profile: Profile; dailyEvidence?: readonly DailyPoint[]; evidenceUnavailable?: boolean; lens?: Lens; onLensChange?: (lens: Lens) => void; onDayChange?: (id: string) => void }) {
  const physicalReady = dailyEvidence.length === data.window.dayCount;
  const reason = physicalReady ? rangeWeatherReason(dailyEvidence) : '';
  return <>
    {reason && <p className="pointSummary rangeReason">{reason}</p>}
    <p className="rangeRisk">{data.window.awfulDays} awful {data.window.awfulDays === 1 ? 'day' : 'days'} · longest poor stretch {data.window.longestPoorRun} {data.window.longestPoorRun === 1 ? 'day' : 'days'}</p>
    {physicalReady ? <PhysicalWeatherRows days={dailyEvidence} active={lens} onChange={onLensChange} /> : <p className="physicalLoading" role="status">{evidenceUnavailable ? 'Daily weather is unavailable for this place.' : 'Loading daily weather…'}</p>}
    <ForecastFingerprint days={data.days} legend={legend} onChange={onDayChange} />
    <details className="sourceDetails"><summary>Forecast details</summary><p>{pointSourceLine(data.source)}</p><p>Typical values are medians of the daily forecasts for this place. Complete European days use the finer detail.</p></details>
  </>;
}

function rangeWeatherReason(days: readonly DailyPoint[]): string {
  const limits = (['comfort', 'dryness', 'sun', 'wind'] as const).map((component) => ({ component, days: days.filter((day) => day.components[component].value < 55) })).sort((left, right) => right.days.length - left.days.length);
  const limited = limits[0];
  if (!limited?.days.length) return 'Warm, dry, bright and calm through the fortnight.';
  const count = limited.days.length;
  const highest = (field: keyof DailyPoint['metrics']) => Math.max(...limited.days.map((day) => day.metrics[field]).filter((value): value is number => typeof value === 'number'));
  if (limited.component === 'comfort') {
    const hot = limited.days.filter((day) => day.temperatureNotice === 'hot');
    const cold = limited.days.filter((day) => day.temperatureNotice === 'cold');
    const noticed = hot.length >= cold.length ? hot : cold;
    if (noticed.length === count) {
      const hotNotice = noticed === hot;
      const temperatures = noticed.map((day) => day.metrics.temperatureC);
      const extreme = hotNotice ? Math.max(...temperatures) : Math.min(...temperatures);
      return `${noticed.length === 1 ? '1 day is' : `${noticed.length} days are`} too ${hotNotice ? 'hot' : 'cool'} · daytime temperatures ${hotNotice ? 'reach' : 'fall to'} ${format(extreme, 0)}°C.`;
    }
    const temperatures = limited.days.map((day) => day.metrics.temperatureC);
    const dewPoint = highest('dewPointC');
    return `Comfort limits ${count} ${count === 1 ? 'day' : 'days'} · daytime ${temperatureRange(Math.min(...temperatures), Math.max(...temperatures))}C${Number.isFinite(dewPoint) ? ` · dew point up to ${format(dewPoint, 0)}°C` : ''}.`;
  }
  if (limited.component === 'dryness') return `Rain limits ${count} ${count === 1 ? 'day' : 'days'} · up to ${format(highest('rainMm'), 1)} mm during the daytime.`;
  if (limited.component === 'sun') return `Cloud limits ${count} ${count === 1 ? 'day' : 'days'} · up to ${format(highest('cloudPct'), 0)}% cloud cover.`;
  return `Wind limits ${count} ${count === 1 ? 'day' : 'days'} · up to ${format(highest('windKmh'), 0)} km/h${Number.isFinite(highest('gustKmh')) ? `, gusting ${format(highest('gustKmh'), 0)} km/h` : ''}.`;
}

function DailyDetails({ data, legend, lens, onLensChange }: { data: DailyPoint; legend: WeatherPresentation['legend']; lens: Lens; onLensChange: (lens: Lens) => void }) {
  return <>
    <p className="pointSummary">{data.summary}</p>
    <PhysicalWeatherRows days={[data]} active={lens} onChange={onLensChange} />
    <details className="sourceDetails"><summary>Forecast details</summary>
      <p>{pointSourceLine(data.source)}</p>
      <p>Daytime weather · 10:00–20:00 local time</p>
    </details>
  </>;
}

function PhysicalWeatherRows({ days, active, onChange }: { days: readonly DailyPoint[]; active: Lens; onChange?: (lens: Lens) => void }) {
  const typical = (field: keyof DailyPoint['metrics']) => {
    const values = days.map((day) => day.metrics[field]).filter((value): value is number => typeof value === 'number').sort((a, b) => a - b);
    if (!values.length) return undefined;
    const middle = Math.floor(values.length / 2);
    return values.length % 2 ? values[middle] : (values[middle - 1] + values[middle]) / 2;
  };
  const value = (field: keyof DailyPoint['metrics'], unit: string) => optional(typical(field), (number) => `${format(number, field === 'rainMm' ? 1 : 0)}${unit}`);
  const range = days.length > 1;
  const rows = [
    { id: 'comfort', label: 'Comfort', value: `${value('temperatureC', '°C')}`, evidence: `${range ? 'Typical range ' : 'Daytime range '}${value('minTempC', '°C')}–${value('maxTempC', '°C')} · dew point ${value('dewPointC', '°C')}` },
    { id: 'dry', label: 'Dry', value: value('rainMm', ' mm'), evidence: range ? `${days.filter((day) => day.metrics.rainMm < 0.1).length} rain-free days · typical daily rain` : 'Rain during the daytime' },
    { id: 'bright', label: 'Clear sky', value: value('cloudPct', '% cloud'), evidence: range ? 'Typical daytime cloud cover' : 'Daytime cloud cover' },
    { id: 'wind', label: 'Calm', value: value('windKmh', ' km/h'), evidence: `${range ? 'Typical daytime wind' : 'Daytime wind'}${typical('gustKmh') === undefined ? '' : ` · gusts ${value('gustKmh', ' km/h')}`}` },
  ] as const;
  return <div className="physicalRows" aria-label={range ? 'Typical daily forecast weather' : 'Daily forecast weather'}>{rows.map((row) => <button type="button" key={row.id} aria-pressed={active === row.id} onClick={() => onChange?.(row.id)}><span><strong>{row.label}</strong><small>{row.evidence}</small></span><b>{row.value}</b></button>)}</div>;
}

function PointHeading({ point, eyebrow, score, category }: { point: MapPoint; eyebrow: string; score: number; category: string }) {
  return <div className="pointHeading"><div><p>{eyebrow}</p><h2>{point.label ?? `${point.lat.toFixed(2)}°, ${point.lon.toFixed(2)}°`}</h2>{point.secondaryLabel && <small className="placeRegion">{point.secondaryLabel}</small>}</div><div className="score"><strong>{Math.round(score)}</strong><span>Overall · {category}</span></div></div>;
}

function PointSkeleton({ label }: { label?: string }) { return <div className="pointSkeleton"><strong>{label ?? 'Selected place'}</strong><span /><span /></div>; }

export function climateUiModel(index?: ClimateIndex): ClimateUiModel {
  if (!index) return { available: false, complete: false, label: 'Climate', compactLabel: 'Climate', referencePeriod: '', periods: [] };
  const periods = index.months.map(({ period }) => ({ id: period, shortLabel: shortMonthName(period) }));
  const referencePeriod = `${index.referencePeriod.firstYear}–${index.referencePeriod.lastYear}`;
  return { available: true, complete: true, label: 'Typical year', compactLabel: 'Climate', referencePeriod, periods };
}

export function climateRailPeriods(model: ClimateUiModel): readonly PeriodChoice[] {
  return model.periods.length > 1 ? model.periods : [];
}





export function climateFieldFor(lens: Lens, profile: Profile): ClimateField {
  if (lens === 'comfort') return 'comfort_good';
  if (lens === 'dry') return 'dry_good';
  if (lens === 'bright') return 'clear_sky_good';
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

export function forecastMapTiles(product: DayForecastProduct | RangeForecastCatalog, bounds: GeographicBounds, mapZoom: number): readonly TileDraw[] {
  return coveringTiles(bounds, forecastMapUsesDetail(product, bounds, mapZoom) ? 5 : 3);
}

export function forecastMapUsesDetail(product: DayForecastProduct | RangeForecastCatalog, bounds: GeographicBounds, mapZoom: number): boolean {
  if (mapZoom < 4 || ('tiers' in product ? !product.tiers.some(({ tier }) => tier === 'europe-detail') : !product.regional)) return false;
  const tiles = coveringTiles(bounds, 5);
  const uniqueTiles = new Set(tiles.map(({ id }) => `${id.z}/${id.x}/${id.y}`));
  return uniqueTiles.size <= FORECAST_TEXTURE_BUDGET && tiles.some(({ id }) => forecastDetailDeclared(product, id));
}

export function scalarPalette(manifest: Readonly<{ palette: readonly PaletteStop[] }>): readonly ScalarPaletteColour[] {
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

export function climateReliability(data: ClimatePointEvidence, profile: Profile): string {
  const evidence = data[profile];
  return `${evidence.awfulPercent}% awful · ${formatPermille(evidence.poorWeekPermille)} chance of 3 poor days in a 7-day window`;
}

function climateOutcomeCopy(profile: Profile): Readonly<{ daysLabel: string; rareLabel: string; usualLabel: string; rampAriaLabel: string }> {
  return profile === 'cycling'
    ? { daysLabel: 'Good cycling days', rareLabel: 'Rarely good', usualLabel: 'Usually good', rampAriaLabel: 'Climate colours run from Rarely good for cycling to Usually good for cycling' }
    : { daysLabel: 'Nice days', rareLabel: 'Rarely nice', usualLabel: 'Usually nice', rampAriaLabel: 'Climate colours run from Rarely nice to Usually nice' };
}

export function forecastOutcomeCopy(profile: Profile, goodDays: number): Readonly<{ daysLabel: string; summaryLabel: string }> {
  return profile === 'cycling'
    ? { daysLabel: 'Good cycling days', summaryLabel: `${goodDays} of 14 good for cycling` }
    : { daysLabel: 'Nice days', summaryLabel: `${goodDays} of 14 nice` };
}

function formatPermille(value: number): string {
  return `${new Intl.NumberFormat('en-GB', { maximumFractionDigits: 1 }).format(value / 10)}%`;
}

function climateRange(minimum: number, maximum: number, unit: string): string {
  return `${climateValue(minimum)}–${climateValue(maximum)}${unit}`;
}

function climateValue(value: number): string {
  return new Intl.NumberFormat('en-GB', { maximumFractionDigits: 1 }).format(value);
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

export function ForecastFingerprint({ days, legend, onChange }: { days: Extract<PointWeather, { kind: 'range' }>['days']; legend: WeatherPresentation['legend']; onChange?: (id: string) => void }) {
  return <section className="forecastFingerprint" aria-labelledby="forecast-fingerprint-title">
    <h3 id="forecast-fingerprint-title">Day by day</h3>
    <ol>{days.map((day) => {
      const colour = legend.find((band) => band.label === day.category)?.color;
      return <li key={day.date} style={{ '--day-colour': colour } as CSSProperties} aria-label={`${forecastDayLabel(day.date)}, ${day.score}, ${day.category}`}>
        <button type="button" onClick={() => onChange?.(day.date)} aria-label={`${forecastDayLabel(day.date)}: ${day.category}. Show day`}><time dateTime={day.date}>{forecastDayLabel(day.date)}</time></button>
      </li>;
    })}</ol>
  </section>;
}



function PeriodStatus({ view, now, climate, forecast, day, selectedForecast }: { view: View; now: Date; climate: ClimateUiModel; forecast?: ForecastSnapshot; day?: DayForecastProduct; selectedForecast?: PointWeather }) {
  const status = view === 'climate'
    ? { primary: 'Recent climate', meta: climate.referencePeriod ? `${climate.referencePeriod} · worldwide` : '', stale: false }
    : worldwideForecastStatus(forecast, day, selectedForecast, now);
  return <p className="metaLine mapStatus" role="status">
    <span className={status.stale ? 'statusPrimary isStale' : 'statusPrimary'}>{status.primary}</span>
    {status.meta && <><span className="statusSeparator" aria-hidden="true">·</span><span className="statusMeta">{status.meta}</span></>}
  </p>;
}

function worldwideForecastStatus(forecast?: ForecastSnapshot, day?: DayForecastProduct, selectedForecast?: PointWeather, now = new Date()): { primary: string; meta: string; stale: boolean } {
  if (!forecast) return { primary: 'Loading forecast', meta: '', stale: false };
  const run = day?.tiers.find(({ tier }) => tier === 'global')?.evidence.runUtc ?? forecast.range.source.runUtc;
  const status = forecastRunStatus(run, forecast.range.source.staleAfterHours, now);
  const detail = selectedForecast
    ? selectedForecastDetail(selectedForecast)
    : day?.tiers.some(({ tier }) => tier === 'europe-detail')
    ? 'Worldwide · European detail about 7 km'
    : 'Worldwide · about 28 km';
  return { primary: status.stale ? 'Forecast out of date' : status.label, meta: detail, stale: status.stale };
}

function selectedForecastDetail(data: PointWeather): string {
  if (data.source.basis === 'forecast-window') return data.source.detailLabel.startsWith('European') ? 'European detail 7 km · remaining days 28 km' : `Worldwide · about ${Math.round(data.source.resolutionKm)} km`;
  const scope = data.source.detailLabel === 'Detailed forecast' ? 'European detail' : 'Worldwide';
  return `${scope} · about ${Math.round(data.source.resolutionKm)} km`;
}

function pointSourceLine(source: PointWeather['source']): string {
  if (source.basis !== 'forecast' || source.detailLabel !== source.providerLabel) return pointSourceLabel(source);
  const detail = source.temporalResolutionHours === 1 ? 'hourly detail' : `up to ${source.temporalResolutionHours}-hour detail`;
  return `${source.detailLabel} · about ${Math.round(source.resolutionKm)} km · ${detail}`;
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
export function revealSelectedPeriod(container: Pick<HTMLElement, 'querySelectorAll'> | null, selectedId: string, behavior: ScrollBehavior = 'smooth'): void {
  if (!container) return;
  const selected = Array.from(container.querySelectorAll<HTMLElement>('[data-period-id]')).find((item) => item.dataset.periodId === selectedId);
  const reducedMotion = typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  selected?.scrollIntoView({ behavior: reducedMotion ? 'auto' : behavior, block: 'nearest', inline: 'center' });
}
export function revealFocusedPeriod(period: Pick<HTMLElement, 'scrollIntoView'>): void {
  period.scrollIntoView({ behavior: 'auto', block: 'nearest', inline: 'nearest' });
}
export function periodRailHasMore(container: Pick<HTMLElement, 'scrollWidth' | 'clientWidth' | 'scrollLeft'> | null): boolean {
  return Boolean(container && container.scrollWidth - container.clientWidth - container.scrollLeft > 2);
}


export function dayCountLabel(count: number): string { return `${count} ${count === 1 ? 'day' : 'days'}`; }
export function temperatureRange(minimum: number, maximum: number): string {
  const low = format(minimum, 0);
  const high = format(maximum, 0);
  return low === high ? `${low}°` : `${low}–${high}°`;
}
export function clearSearchAfterMapSelection(clearToken: number, clear: () => void): void { if (clearToken > 0) clear(); }
export function placeSearchPrompt(view: View): string { return view === 'range' ? 'Search places' : 'Search anywhere'; }
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
