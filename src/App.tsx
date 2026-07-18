import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type RefObject } from 'react';
import { isEscapeKey, isKeyboardActivation, restoreFocus } from './accessibility';
import { forecastRunStatus, freshnessLabel, imageAssetUrl, loadManifest, loadPoint, StaleReleaseError } from './data/api';
import type { Lens, Mode, Period, PointWeather, Profile, WeatherManifest } from './data/types';
import type { MapPoint, OverlayState } from './map/WeatherMap';
import { reportResourceSummary, telemetry } from './telemetry';
import './styles.css';

const WeatherMap = lazy(() => import('./map/WeatherMap').then((module) => ({ default: module.WeatherMap })));

type LoadState =
  | { status: 'loading' }
  | { status: 'ready'; manifest: WeatherManifest }
  | { status: 'error'; message: string };

export default function App() {
  const [loadState, setLoadState] = useState<LoadState>({ status: 'loading' });
  const [retry, setRetry] = useState(0);
  const [mode, setMode] = useState<Mode>('forecast');
  const [profile, setProfile] = useState<Profile>('nice');
  const [lens, setLens] = useState<Lens>('overall');
  const [periodId, setPeriodId] = useState('');
  const [selectedPoint, setSelectedPoint] = useState<MapPoint>();
  const [focusPoint, setFocusPoint] = useState<(MapPoint & { token: number })>();
  const [mapNotice, setMapNotice] = useState('');
  const [overlayState, setOverlayState] = useState<OverlayState>({ status: 'idle' });
  const [showGuide, setShowGuide] = useState(false);
  const [transient, setTransient] = useState<'search' | 'lens' | undefined>();
  const [focusGuide, setFocusGuide] = useState(false);
  const [pointState, setPointState] = useState<{ status: 'idle' | 'loading' | 'ready' | 'error'; data?: PointWeather; message?: string }>({ status: 'idle' });
  const staleReloadedRelease = useRef<string | undefined>(undefined);
  const guideTriggerRef = useRef<HTMLButtonElement | null>(null);
  const pointFocusOrigin = useRef<HTMLElement | null>(null);
  const [focusPointPanel, setFocusPointPanel] = useState(false);

  const closeGuide = useCallback(() => {
    setShowGuide(false);
    setFocusGuide(false);
    restoreFocus(guideTriggerRef.current);
  }, []);

  const closePoint = useCallback(() => {
    setSelectedPoint(undefined);
    setFocusPointPanel(false);
    restoreFocus(pointFocusOrigin.current);
    pointFocusOrigin.current = null;
  }, []);

  useEscape(showGuide, closeGuide);
  useEscape(Boolean(selectedPoint) && !showGuide && transient === undefined, closePoint);

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
        if (!controller.signal.aborted) setLoadState({ status: 'error', message: error.message });
      });
    return () => controller.abort();
  }, [retry]);

  const manifest = loadState.status === 'ready' ? loadState.manifest : undefined;
  const periods = useMemo(() => periodsFor(manifest, mode), [manifest, mode]);
  const period = periods.find((item) => item.id === periodId) ?? periods[0];

  useEffect(() => {
    if (period && period.id !== periodId) setPeriodId(period.id);
  }, [period, periodId]);

  useEffect(() => {
    if (!selectedPoint || !period || !manifest) {
      setPointState({ status: 'idle' });
      return;
    }
    const controller = new AbortController();
    setPointState({ status: 'loading' });
    loadPoint({ ...selectedPoint, releaseId: manifest.releaseId, period: period.id, profile }, controller.signal)
      .then((data) => setPointState({ status: 'ready', data }))
      .catch((error: Error) => {
        if (controller.signal.aborted) return;
        if (error instanceof StaleReleaseError && staleReloadedRelease.current !== manifest.releaseId) {
          refreshStaleRelease(manifest.releaseId);
          return;
        }
        setPointState({ status: 'error', message: error instanceof StaleReleaseError ? 'The forecast changed again. Select this point once more.' : error.message });
      });
    return () => controller.abort();
  }, [manifest, mode, period, profile, refreshStaleRelease, selectedPoint]);

  const overlayUrl = period && manifest ? imageAssetUrl(manifest.releaseId, lensAsset(period, profile, lens)) : undefined;

  const handleOverlayState = useCallback((state: OverlayState) => {
    setOverlayState(state);
    if (state.status !== 'error' || state.reason !== 'stale-release' || !manifest) return;
    refreshStaleRelease(manifest.releaseId);
  }, [manifest, refreshStaleRelease]);

  return (
    <main className={selectedPoint && !showGuide && transient === undefined ? 'appShell detailOpen' : 'appShell'}>
      <Suspense fallback={<div className="mapCanvas mapLoading" aria-label="Loading map" />}>
        <WeatherMap
          overlayUrl={overlayUrl}
          bounds={manifest?.bounds}
          selectedPoint={selectedPoint}
          focusPoint={focusPoint}
          onSelect={(point, intent) => {
            telemetry('point-selected', { lat: Number(point.lat.toFixed(3)), lon: Number(point.lon.toFixed(3)) });
            const focusDetails = intent?.focusDetails === true;
            pointFocusOrigin.current = focusDetails && document.activeElement instanceof HTMLElement ? document.activeElement : null;
            setFocusPointPanel(focusDetails);
            setTransient(undefined);
            setShowGuide(false);
            setSelectedPoint(point);
          }}
          onReady={(elapsedMs) => telemetry('map-ready', { elapsedMs })}
          onOverlayState={handleOverlayState}
          onOverlayReady={(elapsedMs, renderer, assetUrl) => {
            telemetry('overlay-ready', { elapsedMs, renderer, assetUrl, period: period?.id, profile, lens });
            reportResourceSummary();
          }}
        />
      </Suspense>

      <header className="brandBar">
        <div className="wordmark" aria-label="Nice Weather">
          <SunMark />
          <span>Nice Weather</span>
        </div>
        {manifest?.dataStatus === 'sample' && <p className="sampleWarning"><strong>Sample data</strong><span>Not a live forecast</span></p>}
        <SearchControl open={transient === 'search'} onOpenChange={(open) => {
          setTransient(open ? 'search' : undefined);
          if (open) setShowGuide(false);
        }} onFound={(point) => {
          telemetry('search-selected', { lat: point.lat, lon: point.lon });
          setSelectedPoint(point);
          setFocusPoint({ ...point, token: Date.now() });
        }} />
      </header>

      <button className="locationButton mapButton" type="button" onClick={() => locate((point) => {
        telemetry('location-selected');
        const currentLocation = { ...point, label: 'Current location' };
        setSelectedPoint(currentLocation);
        setFocusPoint({ ...currentLocation, token: Date.now() });
        setMapNotice('');
      }, setMapNotice)} aria-label="Use my location">
        <LocationIcon />
      </button>

      {mapNotice && <p className="mapNotice" role="status">{mapNotice}</p>}
      {overlayState.status === 'error' && <p className="mapNotice overlayNotice" role="status">Weather colours could not be shown.</p>}

      <Legend manifest={manifest} lens={lens} open={transient === 'lens'} onOpenChange={(open) => {
        setTransient(open ? 'lens' : undefined);
        if (open) setShowGuide(false);
      }} onLensChange={(nextLens) => {
        telemetry('lens-changed', { lens: nextLens });
        setLens(nextLens);
      }} triggerRef={guideTriggerRef} onExplain={(focusGuide) => {
        setTransient(undefined);
        setShowGuide(true);
        setFocusGuide(focusGuide);
      }} />

      {showGuide && <ScoringGuide focusOnOpen={focusGuide} onClose={closeGuide} />}

      {selectedPoint && !showGuide && transient === undefined && (
        <PointPanel
          state={pointState}
          point={selectedPoint}
          period={period}
          lens={lens}
          onLensChange={setLens}
          focusOnOpen={focusPointPanel}
          onClose={closePoint}
        />
      )}

      <section className="controlDock" aria-label="Weather map controls">
        {loadState.status === 'loading' && <LoadingDock />}
        {loadState.status === 'error' && <ErrorDock message={loadState.message} onRetry={() => setRetry((value) => value + 1)} />}
        {manifest && period && (
          <>
            <div className="dockTopline">
              <ModeControl mode={mode} climateAvailable={Boolean(manifest.climate)} onChange={(nextMode) => {
                telemetry('mode-changed', { mode: nextMode });
                setMode(nextMode);
                setPeriodId('');
              }} />
              <ForecastStatus manifest={manifest} mode={mode} period={period} />
              <ProfileControl value={profile} onChange={(nextProfile) => {
                telemetry('profile-changed', { profile: nextProfile });
                setProfile(nextProfile);
              }} />
            </div>
            <PeriodRail periods={periods} selectedId={period.id} mode={mode} onChange={(nextPeriod) => {
              telemetry('period-changed', { period: nextPeriod, mode });
              setPeriodId(nextPeriod);
            }} />
          </>
        )}
      </section>
    </main>
  );
}

function ModeControl({ mode, climateAvailable, onChange }: { mode: Mode; climateAvailable: boolean; onChange: (mode: Mode) => void }) {
  return (
    <div className="segmented compact" role="group" aria-label="Weather view">
      <button type="button" aria-pressed={mode === 'forecast'} onClick={() => onChange('forecast')}>Forecast</button>
      <button type="button" aria-pressed={mode === 'climate'} disabled={!climateAvailable} title={climateAvailable ? undefined : 'Typical weather is not ready yet'} onClick={() => onChange('climate')}><span className="desktopLabel">Typical year</span><span className="mobileLabel">Typical</span></button>
    </div>
  );
}

function ProfileControl({ value, onChange }: { value: Profile; onChange: (profile: Profile) => void }) {
  return (
    <div className="segmented" role="group" aria-label="Weather preference">
      <button type="button" aria-pressed={value === 'nice'} onClick={() => onChange('nice')}>Nice</button>
      <button type="button" aria-pressed={value === 'cycling'} onClick={() => onChange('cycling')}><BikeIcon /> Cycling</button>
    </div>
  );
}

function ForecastStatus({ manifest, mode, period }: { manifest: WeatherManifest; mode: Mode; period: Period }) {
  if (mode === 'climate') return <p className="forecastStatus climate">Monthly average · {manifest.climate?.period}</p>;
  const status = forecastRunStatus(period.sourceRunAt, manifest.forecast.staleAfterHours);
  return (
    <p className={status.stale ? 'forecastStatus stale' : 'forecastStatus'}>
      <span className="statusDot" />
      {status.label}
      <span aria-hidden="true"> · </span>
      {period.kind === 'outlook' ? 'Outlook' : 'Detailed forecast'}
    </p>
  );
}

function PeriodRail({ periods, selectedId, mode, onChange }: { periods: Period[]; selectedId: string; mode: Mode; onChange: (id: string) => void }) {
  let detailedEnd = -1;
  periods.forEach((period, index) => { if (period.kind !== 'outlook') detailedEnd = index; });
  return (
    <div className="periodRailWrap">
      {mode === 'forecast' && (
        <div className="periodBands" aria-hidden="true">
          <span style={{ flex: Math.max(1, detailedEnd + 1) }}>Detailed</span>
          {detailedEnd < periods.length - 1 && <span style={{ flex: periods.length - detailedEnd - 1 }}>Outlook</span>}
        </div>
      )}
      <div className="periodRail" role="group" aria-label={mode === 'forecast' ? 'Forecast day' : 'Month'}>
        {periods.map((period) => (
          <button key={period.id} type="button" aria-pressed={selectedId === period.id} onClick={() => onChange(period.id)}>
            <span>{period.shortLabel}</span>
            {mode === 'forecast' && period.date && <small>{dateNumber(period.date)}</small>}
          </button>
        ))}
      </div>
    </div>
  );
}

const LENSES: Array<{ id: Lens; label: string; note: string }> = [
  { id: 'overall', label: 'Overall', note: 'The complete judgement' },
  { id: 'comfort', label: 'Comfort', note: 'Warmth and moisture' },
  { id: 'dry', label: 'Dry', note: 'Rain amount and wet time' },
  { id: 'bright', label: 'Bright', note: 'Sun and cloud' },
  { id: 'wind', label: 'Wind', note: 'Sustained wind and gusts' },
];

function Legend({ manifest, lens, open, onOpenChange, onLensChange, triggerRef, onExplain }: { manifest?: WeatherManifest; lens: Lens; open: boolean; onOpenChange: (open: boolean) => void; onLensChange: (lens: Lens) => void; triggerRef: RefObject<HTMLButtonElement | null>; onExplain: (focusGuide: boolean) => void }) {
  const lensButtonRef = useRef<HTMLButtonElement | null>(null);
  useEscape(open, () => { onOpenChange(false); restoreFocus(lensButtonRef.current); });
  if (!manifest) return null;
  const active = LENSES.find((item) => item.id === lens) ?? LENSES[0];
  return (
    <div className="legendCluster">
      <button ref={lensButtonRef} type="button" className="legend" aria-haspopup="true" aria-expanded={open} aria-controls="lens-menu" onClick={() => onOpenChange(!open)}>
        <strong>{active.label}<span aria-hidden="true">⌄</span></strong>
        <span className="legendScale" style={{ background: `linear-gradient(to right, ${manifest.palette.map((stop) => `${stop.color} ${stop.score}%`).join(', ')})` }} />
        <span className="legendLabels"><span>Awful</span><span>Excellent</span></span>
      </button>
      <button ref={triggerRef} type="button" className="legendHelp" aria-label="Explain the weather score" onClick={(event) => onExplain(isKeyboardActivation(event.detail))}>?</button>
      {open && <div id="lens-menu" className="lensMenu" role="radiogroup" aria-label="Map colours">
        {LENSES.map((item) => <button key={item.id} type="button" role="radio" aria-checked={item.id === lens} onClick={() => { onLensChange(item.id); onOpenChange(false); }}><span><strong>{item.label}</strong><small>{item.note}</small></span><b aria-hidden="true">{item.id === lens ? '✓' : ''}</b></button>)}
      </div>}
    </div>
  );
}

function ScoringGuide({ focusOnOpen, onClose }: { focusOnOpen: boolean; onClose: () => void }) {
  const closeRef = useRef<HTMLButtonElement | null>(null);
  useEffect(() => {
    if (focusOnOpen) closeRef.current?.focus({ preventScroll: true });
  }, [focusOnOpen]);
  return (
    <aside className="scoringGuide" aria-label="What counts as nice weather">
      <button ref={closeRef} className="closeButton" type="button" onClick={onClose} aria-label="Close score explanation">×</button>
      <p className="guideEyebrow">Weather score</p>
      <h2>Warm, dry and bright.</h2>
      <p>Overall combines comfort, dry weather, brightness and wind. Comfort asks whether it is warm enough, then accounts for moisture: hot and dry can feel good while hot and humid does not. Cold, damp weather is worse than cold, dry weather.</p>
      <small>Awful is reserved for the days that are genuinely miserable. Cycling is stricter about rain, wind and gusts.</small>
    </aside>
  );
}

function PointPanel({ state, point, period, lens, onLensChange, focusOnOpen, onClose }: {
  state: { status: 'idle' | 'loading' | 'ready' | 'error'; data?: PointWeather; message?: string };
  point: MapPoint;
  period?: Period;
  lens: Lens;
  onLensChange: (lens: Lens) => void;
  focusOnOpen: boolean;
  onClose: () => void;
}) {
  const closeRef = useRef<HTMLButtonElement | null>(null);
  useEffect(() => {
    if (focusOnOpen) closeRef.current?.focus({ preventScroll: true });
  }, [focusOnOpen]);
  return (
    <aside className="pointPanel" aria-live="polite" aria-label="Weather at selected point">
      <button ref={closeRef} className="closeButton" type="button" onClick={onClose} aria-label="Close weather details">×</button>
      {state.status === 'loading' && <PointSkeleton />}
      {state.status === 'error' && (
        <div className="pointMessage"><strong>Details are not available</strong><p>{state.message}</p></div>
      )}
      {state.status === 'ready' && state.data && <PointDetails data={state.data} point={point} period={period} lens={lens} onLensChange={onLensChange} />}
      {state.status === 'idle' && (
        <div className="pointMessage"><strong>{point.label ?? 'Selected point'}</strong><p>{point.lat.toFixed(2)}°, {point.lon.toFixed(2)}° · {period?.label}</p></div>
      )}
    </aside>
  );
}

function PointDetails({ data, point, lens, onLensChange }: { data: PointWeather; point: MapPoint; period?: Period; lens: Lens; onLensChange: (lens: Lens) => void }) {
  const sky = data.metrics.cloudPct !== undefined
    ? ['Cloud', `${format(data.metrics.cloudPct, 0)}%`]
    : data.metrics.solarEnergyMJm2 !== undefined
      ? ['Sun energy', `${format(data.metrics.solarEnergyMJm2, 1)} MJ/m²`]
      : ['Sun', 'Not available'];
  const metrics = [
    ['Temperature', `${format(data.metrics.temperatureC, 0)}°C · ${format(data.metrics.minTempC, 0)}–${format(data.metrics.maxTempC, 0)}°`],
    ['Rain', `${format(data.metrics.rainMm, 1)} mm${data.metrics.wetTimeFraction !== undefined ? ` · ${format(data.metrics.wetTimeFraction * 100, 0)}% wet` : ''}`],
    sky,
    ['Wind', `${format(data.metrics.windKmh, 0)} km/h${data.metrics.gustKmh !== undefined ? ` · gusts ${format(data.metrics.gustKmh, 0)}` : ''}`],
  ];
  const rows = componentRows(data);
  const selectedComponent = lens === 'overall' ? undefined : rows.find((item) => item.lens === lens);
  const activeScore = selectedComponent ?? { label: 'Overall', value: Math.round(data.score), category: data.category };
  return (
    <>
      <div className="pointHeading">
        <div><p>{data.periodLabel}</p><h2>{point.label ?? `${data.lat.toFixed(2)}°, ${data.lon.toFixed(2)}°`}</h2></div>
        <div className="score">
          <small>{activeScore.label}</small><strong>{activeScore.value}</strong><span>{activeScore.category}</span>
          {selectedComponent && <em>Overall {Math.round(data.score)} · {data.category}</em>}
        </div>
      </div>
      <p className="pointSummary">{data.summary}</p>
      {data.source.basis === 'climate' && <p className="climateCaveat">Monthly average from {data.source.referencePeriod}. This is not yet the chance of getting a nice day.</p>}
      <div className="componentRows" role="group" aria-label="Weather score parts">
        {rows.map((item) => <button key={item.lens} type="button" aria-pressed={lens === item.lens} aria-label={`${item.label}: ${item.value}, ${item.category}, ${item.evidence}. Show on map`} onClick={() => onLensChange(item.lens)}><span><strong>{item.label}</strong><small>{item.evidence}</small></span><i><b style={{ width: `${item.value}%` }} /></i><em><b>{item.value}</b><small>{item.category}</small></em></button>)}
      </div>
      <dl className="metricGrid">
        {metrics.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}
      </dl>
      <p className="dataNote">{sourceSummary(data)}</p>
      <details className="scoreWhy">
        <summary>Why this score</summary>
        <p className="evidenceTitle">Weather used</p>
        <dl className="scoreEvidence">
          {scoreEvidence(data).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}
        </dl>
        {data.source.basis === 'forecast' && (
          <>
            <p className="evidenceTitle">Forecast timing</p>
            <dl className="scoreEvidence sourceTiming">
              {sourceTiming(data).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}
            </dl>
          </>
        )}
      </details>
    </>
  );
}

function scoreEvidence(data: PointWeather): Array<[string, string]> {
  return [
    ['Temperature', `${format(data.metrics.temperatureC, 0)}°C`],
    ['Minimum', `${format(data.metrics.minTempC, 0)}°C`],
    ['Maximum', `${format(data.metrics.maxTempC, 0)}°C`],
    ['Rain', `${format(data.metrics.rainMm, 1)} mm`],
    ['Wet time', optionalMetric(data.metrics.wetTimeFraction, (value) => `${format(value * 100, 0)}%`)],
    ['Sun energy', optionalMetric(data.metrics.solarEnergyMJm2, (value) => `${format(value, 1)} MJ/m²`)],
    ['Cloud', optionalMetric(data.metrics.cloudPct, (value) => `${format(value, 0)}%`)],
    ['Wind', `${format(data.metrics.windKmh, 0)} km/h`],
    ['Gusts', optionalMetric(data.metrics.gustKmh, (value) => `${format(value, 0)} km/h`)],
    ['Humidity', optionalMetric(data.metrics.humidityPct, (value) => `${format(value, 0)}%`)],
    ['Dew point', optionalMetric(data.metrics.dewPointC, (value) => `${format(value, 0)}°C`)],
  ];
}

function optionalMetric(value: number | undefined, present: (value: number) => string): string {
  return value === undefined ? 'Not available' : present(value);
}

function sourceSummary(data: PointWeather): string {
  const source = data.source;
  const resolution = `${Math.round(source.resolutionKm)} km grid`;
  const detail = source.temporalResolutionHours > 24 ? 'Monthly normal' : source.temporalResolutionHours === 1 ? 'Hourly detail' : `${source.temporalResolutionHours}-hour detail`;
  if (source.basis === 'climate') return `Typical month · ${source.referencePeriod} · ${source.providerLabel} · ${resolution}`;
  return `${source.detailLabel} · ${source.providerLabel} · ${resolution} · ${detail}`;
}

function sourceTiming(data: PointWeather): Array<[string, string]> {
  return [
    ['Model run', formatMoment(data.source.runAt)],
    ['Valid from', formatMoment(data.source.validFrom)],
    ['Valid to', formatMoment(data.source.validTo)],
    ['Map built', formatMoment(data.source.artifactGeneratedAt)],
  ];
}

function formatMoment(value: string | undefined): string {
  if (!value) return 'Not available';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return 'Not available';
  return new Intl.DateTimeFormat('en-GB', { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }).format(date);
}

function SearchControl({ open, onOpenChange, onFound }: { open: boolean; onOpenChange: (open: boolean) => void; onFound: (point: MapPoint) => void }) {
  const [query, setQuery] = useState('');
  const [message, setMessage] = useState('');
  const [results, setResults] = useState<Array<{ name: string; country?: string; lat: number; lon: number }>>([]);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const restoreTriggerAfterClose = useRef(false);
  const searchController = useRef<AbortController | undefined>(undefined);

  useEffect(() => { if (open) inputRef.current?.focus(); }, [open]);
  useEffect(() => {
    if (open || !restoreTriggerAfterClose.current) return;
    restoreTriggerAfterClose.current = false;
    restoreFocus(triggerRef.current);
  }, [open]);
  useEffect(() => () => searchController.current?.abort(), []);
  useEscape(open, closeSearch);

  function cancelSearch() {
    searchController.current?.abort();
    searchController.current = undefined;
  }

  function closeSearch() {
    cancelSearch();
    onOpenChange(false);
    setMessage('');
    setResults([]);
    restoreTriggerAfterClose.current = true;
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    cancelSearch();
    const coordinates = parseCoordinates(query);
    if (coordinates) {
      onFound(coordinates);
      closeSearch();
      return;
    }
    const controller = new AbortController();
    searchController.current = controller;
    setMessage('Checking featured cities…');
    try {
      const response = await fetch(`/api/search?q=${encodeURIComponent(query)}`, { signal: controller.signal });
      if (!response.ok) throw new Error();
      const result = await response.json() as { results?: Array<{ name: string; country?: string; lat: number; lon: number }> };
      if (controller.signal.aborted || searchController.current !== controller) return;
      if (!result.results?.length) throw new Error();
      setResults(result.results.slice(0, 5));
      setMessage('');
    } catch {
      if (controller.signal.aborted || searchController.current !== controller) return;
      setMessage('Not in featured cities. Try latitude, longitude.');
    } finally {
      if (searchController.current === controller) searchController.current = undefined;
    }
  }

  return (
    <div className={open ? 'searchControl open' : 'searchControl'}>
      {open ? (
        <form onSubmit={submit}>
          <SearchIcon />
          <label className="srOnly" htmlFor="place-search">Featured cities or coordinates</label>
          <input id="place-search" ref={inputRef} value={query} onChange={(event) => { cancelSearch(); setQuery(event.target.value); setMessage(''); setResults([]); }} placeholder="City or coordinates" autoComplete="off" />
          <button type="button" onClick={closeSearch} aria-label="Close search">×</button>
          {message && <p role="status">{message}</p>}
          {results.length > 0 && (
            <ul className="searchResults" aria-label="Places">
              {results.map((result) => (
                <li key={`${result.name}-${result.lat}-${result.lon}`}><button type="button" onClick={() => {
                    onFound({ lat: result.lat, lon: result.lon, label: [result.name, result.country].filter(Boolean).join(', ') });
                    closeSearch();
                  }}>
                    <strong>{result.name}</strong>{result.country && <span>{result.country}</span>}
                  </button></li>
              ))}
            </ul>
          )}
        </form>
      ) : (
        <button ref={triggerRef} type="button" className="searchButton" aria-label="Search featured cities or coordinates" onClick={() => onOpenChange(true)}><SearchIcon /> <span>Featured cities or coordinates</span></button>
      )}
    </div>
  );
}

function useEscape(active: boolean, onEscape: () => void) {
  const onEscapeRef = useRef(onEscape);
  onEscapeRef.current = onEscape;
  useEffect(() => {
    if (!active) return;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (!isEscapeKey(event.key)) return;
      event.preventDefault();
      onEscapeRef.current();
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [active]);
}

function LoadingDock() {
  return <div className="dockMessage"><span className="spinner" /><div><strong>Loading the forecast</strong><span>The map will be ready shortly.</span></div></div>;
}

function ErrorDock({ message, onRetry }: { message: string; onRetry: () => void }) {
  return <div className="dockMessage error"><div><strong>The forecast could not be loaded</strong><span>{message}</span></div><button type="button" onClick={onRetry}>Try again</button></div>;
}

function PointSkeleton() {
  return <div className="pointSkeleton" aria-label="Loading weather details"><span /><span /><span /></div>;
}

function periodsFor(manifest: WeatherManifest | undefined, mode: Mode): Period[] {
  if (!manifest) return [];
  return mode === 'climate' ? manifest.climate.months : manifest.forecast.days;
}

function lensAsset(period: Period, profile: Profile, lens: Lens) {
  if (lens === 'overall') return period.images[profile];
  if (lens === 'wind') return period.lenses[`wind-${profile}`];
  return period.lenses[lens];
}

function componentRows(data: PointWeather): Array<{ lens: Exclude<Lens, 'overall'>; label: string; value: number; category: string; evidence: string }> {
  const comfortEvidence = [
    `${format(data.metrics.temperatureC, 0)}°C`,
    data.metrics.dewPointC !== undefined ? `${format(data.metrics.dewPointC, 0)}° dew point` : data.metrics.humidityPct !== undefined ? `${format(data.metrics.humidityPct, 0)}% humidity` : '',
  ].filter(Boolean).join(' · ');
  const dryEvidence = `${format(data.metrics.rainMm, 1)} mm${data.metrics.wetTimeFraction !== undefined ? ` · ${format(data.metrics.wetTimeFraction * 100, 0)}% wet` : ''}`;
  const brightEvidence = data.metrics.solarEnergyMJm2 !== undefined ? `${format(data.metrics.solarEnergyMJm2, 1)} MJ/m²` : data.metrics.cloudPct !== undefined ? `${format(data.metrics.cloudPct, 0)}% cloud` : 'No sky detail';
  const windEvidence = `${format(data.metrics.windKmh, 0)} km/h${data.metrics.gustKmh !== undefined ? ` · gusts ${format(data.metrics.gustKmh, 0)}` : ''}`;
  return [
    { lens: 'comfort', label: 'Comfort', value: Math.round(data.components.comfort.value), category: data.components.comfort.category, evidence: comfortEvidence },
    { lens: 'dry', label: 'Dry', value: Math.round(data.components.dryness.value), category: data.components.dryness.category, evidence: dryEvidence },
    { lens: 'bright', label: 'Bright', value: Math.round(data.components.sun.value), category: data.components.sun.category, evidence: brightEvidence },
    { lens: 'wind', label: 'Wind', value: Math.round(data.components.wind.value), category: data.components.wind.category, evidence: windEvidence },
  ];
}

function dateNumber(date: string): string {
  return new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', timeZone: 'UTC' }).format(new Date(`${date}T12:00:00Z`));
}

function format(value: number, digits: number): string {
  return new Intl.NumberFormat('en-GB', { maximumFractionDigits: digits, minimumFractionDigits: digits }).format(value);
}

function parseCoordinates(query: string): MapPoint | undefined {
  const match = query.trim().match(/^(-?\d+(?:\.\d+)?)\s*[, ]\s*(-?\d+(?:\.\d+)?)$/);
  if (!match) return undefined;
  const lat = Number(match[1]);
  const lon = Number(match[2]);
  if (lat < -90 || lat > 90 || lon < -180 || lon > 180) return undefined;
  return { lat, lon };
}

function locate(onSuccess: (point: MapPoint) => void, onError: (message: string) => void) {
  if (!navigator.geolocation) {
    onError('Location is not available in this browser.');
    return;
  }
  navigator.geolocation.getCurrentPosition(
    (position) => onSuccess({ lat: position.coords.latitude, lon: position.coords.longitude }),
    () => onError('Allow location access to show weather near you.'),
    { enableHighAccuracy: false, timeout: 8_000, maximumAge: 300_000 },
  );
}

function SunMark() { return <svg viewBox="0 0 30 30" aria-hidden="true"><circle cx="15" cy="15" r="5.5" /><path d="M15 1.5v5M15 23.5v5M1.5 15h5M23.5 15h5M5.5 5.5l3.6 3.6M20.9 20.9l3.6 3.6M24.5 5.5l-3.6 3.6M9.1 20.9l-3.6 3.6" /></svg>; }
function SearchIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5" /><path d="m15.5 15.5 5 5" /></svg>; }
function LocationIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m12 3 7.5 17.5L12 17l-7.5 3.5L12 3Z" /></svg>; }
function BikeIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="6" cy="17" r="3.5" /><circle cx="18" cy="17" r="3.5" /><path d="m6 17 4-8h4l4 8m-8-8 4 8H6m3.5-11H13" /></svg>; }
