import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { nextDetailsFocusIntent, restoreFocus, shouldFocusDetails, type DetailsFocusIntent } from './accessibility';
import { forecastRunStatus, freshnessLabel, imageAssetUrl, loadManifest, loadPoint, StaleReleaseError } from './data/api';
import { EuropePlaceSearch, type PlaceSearchResult, type PlaceSearchViewport } from './data/placeSearch';
import type { DailyPoint, Lens, Period, PointWeather, Profile, View, WeatherManifest } from './data/types';
import type { MapPoint, OverlayState } from './map/WeatherMap';
import { reportResourceSummary, telemetry } from './telemetry';
import './styles.css';

const WeatherMap = lazy(() => import('./map/WeatherMap').then((module) => ({ default: module.WeatherMap })));

type LoadState =
  | { status: 'loading' }
  | { status: 'ready'; manifest: WeatherManifest }
  | { status: 'error' };

type PointState = { status: 'idle' | 'loading' | 'ready' | 'error'; data?: PointWeather; message?: string };

export default function App() {
  const [loadState, setLoadState] = useState<LoadState>({ status: 'loading' });
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
  const [layerMenuOpen, setLayerMenuOpen] = useState(false);
  const [pointState, setPointState] = useState<PointState>({ status: 'idle' });
  const [detailsFocusIntent, setDetailsFocusIntent] = useState<DetailsFocusIntent>({ token: 0, requested: false });
  const pointFocusOrigin = useRef<HTMLElement | null>(null);
  const staleReloadedRelease = useRef<string | undefined>(undefined);

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

  const manifest = loadState.status === 'ready' ? loadState.manifest : undefined;
  const periods = useMemo(() => periodsFor(manifest, view), [manifest, view]);
  const period = periods.find((item) => item.id === periodId) ?? periods[0];
  const activePeriodId = view === 'range' ? manifest?.forecast.window.id : period?.id;

  useEffect(() => {
    if (view !== 'range' && period && period.id !== periodId) setPeriodId(period.id);
  }, [period, periodId, view]);

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
    loadPoint({ ...selectedPoint, releaseId: manifest.releaseId, period: activePeriodId, profile }, controller.signal, manifest.forecast.window)
      .then((data) => setPointState({ status: 'ready', data }))
      .catch((error: Error) => {
        if (controller.signal.aborted) return;
        if (error instanceof StaleReleaseError && refreshStaleRelease(manifest.releaseId)) return;
        setPointState({ status: 'error', message: error instanceof StaleReleaseError ? 'The forecast changed. Select this place again.' : error.message });
      });
    return () => controller.abort();
  }, [activePeriodId, manifest, profile, refreshStaleRelease, selectedPoint]);

  const overlayUrl = useMemo(() => {
    if (!manifest) return undefined;
    if (view === 'range') return imageAssetUrl(manifest.releaseId, manifest.forecast.window.profiles[profile].image);
    if (!period) return undefined;
    return imageAssetUrl(manifest.releaseId, lensAsset(period, profile, lens));
  }, [lens, manifest, period, profile, view]);

  const handleView = (next: View) => {
    if (next === 'climate' && !manifest?.climate) return;
    telemetry('view-changed', { view: next });
    setView(next);
    setLayerMenuOpen(false);
    if (next === 'range') setLens('overall');
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
    <main className={`appShell view-${view}${periods.length > 0 && view !== 'range' ? ' hasRail' : ''}${selectedPoint ? ' hasPoint' : ''}`}>
      <Suspense fallback={<div className="mapCanvas mapLoading" aria-label="Loading map" />}>
        <WeatherMap
          overlayUrl={overlayUrl}
          bounds={manifest?.bounds}
          selectedPoint={selectedPoint}
          focusPoint={focusPoint}
          onSelect={(point, intent) => {
            pointFocusOrigin.current = intent?.focusDetails && document.activeElement instanceof HTMLElement ? document.activeElement : null;
            selectPoint(point, false, shouldFocusDetails(intent?.focusDetails ? 'map-keyboard' : 'map-pointer'));
          }}
          onViewportChange={setViewport}
          onReady={(elapsedMs) => telemetry('map-ready', { elapsedMs })}
          onOverlayState={(state) => {
            setOverlayState(state);
            if (state.status === 'error' && state.reason === 'stale-release' && manifest) refreshStaleRelease(manifest.releaseId);
          }}
          onOverlayReady={(elapsedMs, renderer, assetUrl) => {
            telemetry('overlay-ready', { elapsedMs, renderer, assetUrl, period: activePeriodId, profile, lens: view === 'range' ? 'overall' : lens });
            reportResourceSummary();
          }}
        />
      </Suspense>

      <header className="masthead">
        <div className="wordmark" aria-label="Nice Weather"><SunMark /><span>Nice Weather</span></div>
        <PlaceSearch viewport={viewport} onFound={(point, origin) => {
          pointFocusOrigin.current = origin;
          selectPoint(point, true, shouldFocusDetails('search'));
        }} />
        {manifest?.dataStatus === 'sample' && <p className="sampleFlag" role="status"><strong>Sample</strong><span>Not a live forecast</span></p>}
      </header>

      <button className="mapAction locationButton" type="button" onClick={() => locate((point) => selectPoint({ ...point, label: 'Current location' }), setMapNotice)} aria-label="Use my location">
        <LocationIcon />
      </button>

      {(mapNotice || overlayState.status === 'error') && (
        <p className="mapNotice" role="status">{mapNotice || 'Weather colours could not be shown.'}</p>
      )}

      {manifest && (
        <LayerLegend
          manifest={manifest}
          view={view}
          lens={lens}
          open={layerMenuOpen}
          onOpenChange={setLayerMenuOpen}
          onChange={(next) => { setLens(next); setLayerMenuOpen(false); telemetry('lens-changed', { lens: next }); }}
        />
      )}

      {selectedPoint && (
        <PointPanel
          state={pointState}
          point={selectedPoint}
          lens={lens}
          focusIntent={detailsFocusIntent}
          onLensChange={(next) => { if (view !== 'range') setLens(next); }}
          escapeEnabled={!layerMenuOpen}
          onClose={() => {
            setSelectedPoint(undefined);
            setPointState({ status: 'idle' });
            restoreFocus(pointFocusOrigin.current);
            pointFocusOrigin.current = null;
          }}
        />
      )}

      <section className="cockpit" aria-label="Weather map controls">
        {loadState.status === 'loading' && <DockMessage title="Loading forecast" />}
        {loadState.status === 'error' && <DockMessage title="Forecast unavailable" action={<button type="button" onClick={() => setRetry((value) => value + 1)}>Try again</button>} />}
        {manifest && (
          <>
            <div className="cockpitMain">
              <ViewControl value={view} climateAvailable={Boolean(manifest.climate)} onChange={handleView} />
              <p className="periodStatus">{periodStatus(manifest, view, period)}</p>
              <ProfileControl value={profile} onChange={(next) => { setProfile(next); telemetry('profile-changed', { profile: next }); }} />
            </div>
            {view !== 'range' && period && <PeriodRail periods={periods} selectedId={period.id} view={view} onChange={(id) => { setPeriodId(id); telemetry('period-changed', { period: id, view }); }} />}
          </>
        )}
      </section>
    </main>
  );
}

function ViewControl({ value, climateAvailable, onChange }: { value: View; climateAvailable: boolean; onChange: (value: View) => void }) {
  return (
    <div className="segmented viewControl" role="group" aria-label="Weather period">
      <button type="button" aria-pressed={value === 'range'} onClick={() => onChange('range')}><span className="wideLabel">Next 14 days</span><span className="shortLabel">14 days</span></button>
      <button type="button" aria-pressed={value === 'daily'} onClick={() => onChange('daily')}>Day by day</button>
      <button type="button" aria-pressed={value === 'climate'} disabled={!climateAvailable} onClick={() => onChange('climate')}>Typical year</button>
    </div>
  );
}

function ProfileControl({ value, onChange }: { value: Profile; onChange: (value: Profile) => void }) {
  return (
    <div className="segmented profileControl" role="group" aria-label="Weather preference">
      <button type="button" aria-pressed={value === 'nice'} onClick={() => onChange('nice')}>Nice</button>
      <button type="button" aria-pressed={value === 'cycling'} onClick={() => onChange('cycling')}>Cycling</button>
    </div>
  );
}

function PeriodRail({ periods, selectedId, view, onChange }: { periods: Period[]; selectedId: string; view: View; onChange: (id: string) => void }) {
  return (
    <div className="periodRail" role="group" aria-label={view === 'daily' ? 'Forecast day' : 'Typical month'}>
      {periods.map((period) => (
        <button key={period.id} type="button" aria-pressed={selectedId === period.id} onClick={() => onChange(period.id)}>
          <span>{period.shortLabel}</span>{view === 'daily' && period.date && <small>{dateLabel(period.date)}</small>}
        </button>
      ))}
    </div>
  );
}

const LENSES: Array<{ id: Lens; label: string }> = [
  { id: 'overall', label: 'Overall' }, { id: 'comfort', label: 'Comfort' }, { id: 'dry', label: 'Dry' }, { id: 'bright', label: 'Bright' }, { id: 'wind', label: 'Wind' },
];

function LayerLegend({ manifest, view, lens, open, onOpenChange, onChange }: { manifest: WeatherManifest; view: View; lens: Lens; open: boolean; onOpenChange: (open: boolean) => void; onChange: (lens: Lens) => void }) {
  const trigger = useRef<HTMLButtonElement>(null);
  useEscape(open, () => { onOpenChange(false); restoreFocus(trigger.current); });
  const active = view === 'range' ? 'Overall' : LENSES.find((item) => item.id === lens)?.label ?? 'Overall';
  return (
    <div className="legendControl">
      <button ref={trigger} type="button" className="legendTrigger" disabled={view === 'range'} aria-haspopup={view === 'range' ? undefined : 'true'} aria-expanded={view === 'range' ? undefined : open} onClick={() => onOpenChange(!open)}>
        <span className="legendTop"><strong>{active}</strong>{view !== 'range' && <span aria-hidden="true">⌄</span>}</span>
        <span className="legendRamp" style={{ background: `linear-gradient(90deg, ${manifest.palette.map((stop) => `${stop.color} ${stop.score}%`).join(',')})` }} />
        <span className="legendEnds"><span>Awful</span><span>Excellent</span></span>
      </button>
      {open && view !== 'range' && (
        <div className="layerMenu" role="radiogroup" aria-label="Map layer">
          {LENSES.map((item) => <button key={item.id} type="button" role="radio" aria-checked={lens === item.id} onClick={() => { onChange(item.id); window.requestAnimationFrame(() => restoreFocus(trigger.current)); }}><span>{item.label}</span><b aria-hidden="true">{lens === item.id ? '✓' : ''}</b></button>)}
        </div>
      )}
    </div>
  );
}

function PlaceSearch({ viewport, onFound }: { viewport: PlaceSearchViewport; onFound: (point: MapPoint, origin: HTMLElement | null) => void }) {
  const search = useMemo(() => new EuropePlaceSearch(), []);
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

  useEffect(() => () => {
    search.clear();
    window.clearTimeout(blurTimer.current);
  }, [search]);
  useEffect(() => {
    const request = ++requestSequence.current;
    const coordinates = parseCoordinates(query);
    if (coordinates || query.trim().length < 3) {
      setResults([]);
      setStatus('');
      return;
    }
    const timer = window.setTimeout(() => {
      setStatus('Searching…');
      search.search(query, { viewport: viewportRef.current })
        .then((matches) => { if (request === requestSequence.current) { setResults(matches); setStatus(matches.length ? '' : 'No matching place'); } })
        .catch((error: Error) => { if (request === requestSequence.current) { console.error('[nice-weather] Place search failed.', error); setResults([]); setStatus('Place search is unavailable'); } });
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
    <form className="placeSearch" role="search" onSubmit={submit} onKeyDown={(event) => {
      if (event.key !== 'Escape' || !open) return;
      event.preventDefault(); event.stopPropagation(); setResults([]); setStatus(''); input.current?.focus();
    }}>
      <SearchIcon />
      <label className="srOnly" htmlFor="place-search">Find a place in Europe</label>
      <input id="place-search" ref={input} value={query} placeholder="Find a place" autoComplete="off" onFocus={() => { window.clearTimeout(blurTimer.current); blurTimer.current = undefined; setFocused(true); }} onBlur={() => { window.clearTimeout(blurTimer.current); blurTimer.current = window.setTimeout(() => { blurTimer.current = undefined; setFocused(false); }, 120); }} onChange={(event) => setQuery(event.target.value)} aria-expanded={open} aria-controls="place-results" />
      {query && <button className="clearSearch" type="button" aria-label="Clear search" onClick={() => { setQuery(''); setResults([]); setStatus(''); input.current?.focus(); }}>×</button>}
      {open && (
        <div id="place-results" className="searchPopover">
          {results.length > 0 ? <ul aria-label="Places">{results.map((result) => <li key={result.geonameId}><button type="button" onMouseDown={(event) => event.preventDefault()} onClick={() => choose(result)}><strong>{result.name}</strong><span>{[result.admin1, result.country].filter(Boolean).join(', ')}</span></button></li>)}</ul> : <p role="status">{status}</p>}
          {search.attribution && <small>Place names: <a href="https://www.geonames.org/" target="_blank" rel="noreferrer">GeoNames</a></small>}
        </div>
      )}
    </form>
  );
}

function PointPanel({ state, point, lens, focusIntent, onLensChange, escapeEnabled, onClose }: { state: PointState; point: MapPoint; lens: Lens; focusIntent: DetailsFocusIntent; onLensChange: (lens: Lens) => void; escapeEnabled: boolean; onClose: () => void }) {
  const [expanded, setExpanded] = useState(false);
  const panel = useRef<HTMLElement>(null);
  const close = useRef<HTMLButtonElement>(null);
  useEffect(() => setExpanded(false), [point.lat, point.lon, state.data?.periodLabel]);
  useEffect(() => { if (focusIntent.requested) restoreFocus(panel.current); }, [focusIntent]);
  useEscape(escapeEnabled, onClose);
  return (
    <aside ref={panel} tabIndex={-1} className={`pointPanel${expanded ? ' expanded' : ''}`} aria-live="polite" aria-label="Weather at selected place">
      <div className="sheetHandle" aria-hidden="true" />
      <button ref={close} className="closeButton" type="button" onClick={onClose} aria-label="Close weather details">×</button>
      {state.status === 'loading' && <PointSkeleton label={point.label} />}
      {state.status === 'error' && <div className="pointMessage"><h2>{point.label ?? 'Selected place'}</h2><strong>Weather unavailable</strong><p>{state.message}</p></div>}
      {state.status === 'ready' && state.data && (state.data.kind === 'range'
        ? <RangeDetails data={state.data} point={point} />
        : <DailyDetails data={state.data} point={point} lens={lens} onLensChange={onLensChange} />)}
      {state.status === 'idle' && <div className="pointMessage"><h2>{point.label ?? 'Selected place'}</h2><p>{point.lat.toFixed(2)}°, {point.lon.toFixed(2)}°</p></div>}
      {state.status === 'ready' && <button className="sheetExpand" type="button" aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}>{expanded ? 'Show less' : 'Show all details'}</button>}
    </aside>
  );
}

function RangeDetails({ data, point }: { data: Extract<PointWeather, { kind: 'range' }>; point: MapPoint }) {
  return (
    <>
      <PointHeading point={point} eyebrow={`${shortRange(data.window.startDate, data.window.endDate)} · ${data.window.dayCount} days`} score={data.score} category={data.category} />
      <dl className="rangeFacts">
        <div><dd>{data.window.niceDays}</dd><dt>Nice days</dt></div>
        <div className={data.window.awfulDays > 0 ? 'bad' : ''}><dd>{data.window.awfulDays}</dd><dt>Awful days</dt></div>
        <div><dd>{data.window.longestPoorRun}<small> days</small></dd><dt>Longest poor run</dt></div>
      </dl>
      <ComponentRows data={data} />
      <p className="sourceLine">{data.source.detailLabel} · about {Math.round(data.source.resolutionKm)} km · built {humanDate(data.source.artifactGeneratedAt)}</p>
    </>
  );
}

function DailyDetails({ data, point, lens, onLensChange }: { data: DailyPoint; point: MapPoint; lens: Lens; onLensChange: (lens: Lens) => void }) {
  return (
    <>
      <PointHeading point={point} eyebrow={data.periodLabel} score={data.score} category={data.category} />
      <p className="pointSummary">{data.summary}</p>
      {data.source.basis === 'climate' && <p className="climateNote">Monthly average from {data.source.referencePeriod}; not the chance of a nice day.</p>}
      <dl className="weatherFacts">
        <div className="comfortFact"><dt>Temperature</dt><dd>{format(data.metrics.minTempC, 0)}–{format(data.metrics.maxTempC, 0)}°{comfortContext(data)}</dd></div>
        <div><dt>Rain</dt><dd>{format(data.metrics.rainMm, 1)} mm</dd></div>
        <div><dt>{data.metrics.cloudPct !== undefined ? 'Cloud' : 'Sun energy'}</dt><dd>{data.metrics.cloudPct !== undefined ? `${format(data.metrics.cloudPct, 0)}%` : data.metrics.solarEnergyMJm2 !== undefined ? `${format(data.metrics.solarEnergyMJm2, 1)} MJ/m²` : '—'}</dd></div>
        <div><dt>Wind</dt><dd>{format(data.metrics.windKmh, 0)} km/h</dd></div>
      </dl>
      <ComponentRows data={data} active={lens} onChange={onLensChange} />
      <details className="sourceDetails">
        <summary>Weather and source</summary>
        <dl>
          <div><dt>Daytime</dt><dd>{format(data.metrics.temperatureC, 0)}°C</dd></div>
          <div><dt>Gusts</dt><dd>{optional(data.metrics.gustKmh, (value) => `${format(value, 0)} km/h`)}</dd></div>
          <div><dt>Dew point</dt><dd>{optional(data.metrics.dewPointC, (value) => `${format(value, 0)}°C`)}</dd></div>
          <div><dt>Humidity</dt><dd>{optional(data.metrics.humidityPct, (value) => `${format(value, 0)}%`)}</dd></div>
        </dl>
      </details>
      <p className="sourceLine">{sourceLabel(data)}</p>
    </>
  );
}

function PointHeading({ point, eyebrow, score, category }: { point: MapPoint; eyebrow: string; score: number; category: string }) {
  return <div className="pointHeading"><div><p>{eyebrow}</p><h2>{point.label ?? `${point.lat.toFixed(2)}°, ${point.lon.toFixed(2)}°`}</h2></div><div className="score"><strong>{Math.round(score)}</strong><span>{category}</span></div></div>;
}

function ComponentRows({ data, active, onChange }: { data: PointWeather; active?: Lens; onChange?: (lens: Exclude<Lens, 'overall'>) => void }) {
  const rows = [
    ['comfort', 'Comfort', data.components.comfort], ['dry', 'Dry', data.components.dryness], ['bright', 'Bright', data.components.sun], ['wind', 'Wind', data.components.wind],
  ] as const;
  return <div className="componentRows" aria-label="Weather score parts">{rows.map(([id, label, score]) => {
    const content = <><span><strong>{label}</strong><small>{score.category}</small></span><i><b style={{ width: `${score.value}%` }} /></i><em>{score.value}</em></>;
    return onChange ? <button key={id} type="button" aria-pressed={active === id} onClick={() => onChange(id)}>{content}</button> : <div key={id}>{content}</div>;
  })}</div>;
}

function DockMessage({ title, action }: { title: string; action?: React.ReactNode }) { return <div className="dockMessage"><strong>{title}</strong>{action}</div>; }
function PointSkeleton({ label }: { label?: string }) { return <div className="pointSkeleton"><strong>{label ?? 'Selected place'}</strong><span /><span /><span /></div>; }

function periodsFor(manifest: WeatherManifest | undefined, view: View): Period[] {
  if (!manifest || view === 'range') return [];
  return view === 'climate' ? manifest.climate?.months ?? [] : manifest.forecast.days;
}

function lensAsset(period: Period, profile: Profile, lens: Lens) {
  if (lens === 'overall') return period.images[profile];
  if (lens === 'wind') return period.lenses[`wind-${profile}`];
  return period.lenses[lens];
}

function periodStatus(manifest: WeatherManifest, view: View, period?: Period): string {
  if (view === 'climate') return `Monthly average · ${manifest.climate?.period ?? ''}`;
  const status = forecastRunStatus(view === 'daily' && period ? period.sourceRunAt : manifest.forecast.updatedAt, manifest.forecast.staleAfterHours);
  if (view === 'range') return `${status.label} · ${shortRange(manifest.forecast.window.startDate, manifest.forecast.window.endDate)}`;
  return `${status.label} · ${period?.kind === 'outlook' ? 'Outlook' : 'Detailed'} · about ${Math.round(period?.resolutionKm ?? 0)} km`;
}

function sourceLabel(data: DailyPoint): string {
  if (data.source.basis === 'climate') return `Typical month · ${data.source.referencePeriod} · ${data.source.providerLabel} · about ${Math.round(data.source.resolutionKm)} km`;
  const detail = data.source.temporalResolutionHours === 1 ? 'hourly detail' : `${data.source.temporalResolutionHours}-hour detail`;
  return `${data.source.detailLabel} · ${data.source.providerLabel} · about ${Math.round(data.source.resolutionKm)} km · ${detail}`;
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
function humanDate(value: string): string { return freshnessLabel(value).replace(/^Updated /, ''); }
function format(value: number, digits: number): string { return new Intl.NumberFormat('en-GB', { maximumFractionDigits: digits, minimumFractionDigits: digits }).format(value); }
function optional(value: number | undefined, present: (value: number) => string): string { return value === undefined ? 'Not available' : present(value); }

function SunMark() { return <svg viewBox="0 0 28 28" aria-hidden="true"><circle cx="14" cy="14" r="4.5" /><path d="M14 2v5M14 21v5M2 14h5M21 14h5M5.5 5.5 9 9M19 19l3.5 3.5M22.5 5.5 19 9M9 19l-3.5 3.5" /></svg>; }
function SearchIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6" /><path d="m15 15 5 5" /></svg>; }
function LocationIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m12 3 7 17-7-3-7 3 7-17Z" /></svg>; }
