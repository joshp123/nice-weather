import { useEffect, useMemo, useState } from 'react';
import { BasisToggle } from './controls/BasisToggle';
import { HorizonPicker } from './controls/HorizonPicker';
import { Legend } from './controls/Legend';
import { ProfileToggle } from './controls/ProfileToggle';
import { findLayer, forecastFreshness, forecastHorizonDetail, horizonsFor, loadManifest, validHorizon } from './data/manifest';
import { formatUTC } from './data/time';
import type { Anchor, AnchorHorizon, Basis, ForecastHorizon, Manifest, Profile } from './data/types';
import { WeatherMap } from './map/WeatherMap';
import './styles.css';

export default function App() {
  if (window.location.hash === '#design-system') {
    return <DesignSystem />;
  }

  const [manifest, setManifest] = useState<Manifest | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [basis, setBasis] = useState<Basis>('forecast');
  const [profile, setProfile] = useState<Profile>('general');
  const [horizon, setHorizon] = useState('');
  const [selectedAnchorName, setSelectedAnchorName] = useState<string | null>(null);

  useEffect(() => {
    loadManifest().then(setManifest).catch((err: Error) => setError(err.message));
  }, []);

  const horizons = useMemo(() => (manifest ? horizonsFor(manifest, basis) : []), [manifest, basis]);
  const selectedHorizon = validHorizon(horizons, horizon);
  const enabledBasis = useMemo(() => {
    if (!manifest) return ['forecast'] as Basis[];
    return (['forecast', 'climatology'] as Basis[]).filter((candidate) => horizonsFor(manifest, candidate).length > 0);
  }, [manifest]);

  useEffect(() => {
    if (selectedHorizon && selectedHorizon !== horizon) {
      setHorizon(selectedHorizon);
    }
  }, [horizon, selectedHorizon]);

  const layer = useMemo(() => {
    if (!manifest || !selectedHorizon) return null;
    return findLayer(manifest, basis, profile, selectedHorizon);
  }, [basis, selectedHorizon, manifest, profile]);
  const horizonDetail = useMemo(() => {
    if (!manifest || !selectedHorizon || basis !== 'forecast') return undefined;
    return forecastHorizonDetail(manifest, selectedHorizon);
  }, [basis, manifest, selectedHorizon]);
  const selectedAnchor = useMemo(() => {
    if (!manifest || !selectedHorizon) return null;
    return anchorWithHorizon(manifest.anchors.find((anchor) => anchor.name === selectedAnchorName), selectedHorizon)
      ? manifest.anchors.find((anchor) => anchor.name === selectedAnchorName) ?? null
      : manifest.anchors.find((anchor) => anchorWithHorizon(anchor, selectedHorizon)) ?? null;
  }, [manifest, selectedAnchorName, selectedHorizon]);

  return (
    <NiceWeatherScreen
      manifest={manifest}
      error={error}
      basis={basis}
      profile={profile}
      horizon={selectedHorizon}
      horizonDetail={horizonDetail}
      layer={layer}
      selectedAnchor={selectedAnchor}
      enabledBasis={enabledBasis}
      horizons={horizons}
      onBasisChange={setBasis}
      onProfileChange={setProfile}
      onHorizonChange={setHorizon}
      onAnchorSelect={(anchor) => setSelectedAnchorName(anchor.name)}
    />
  );
}

function NiceWeatherScreen({
  manifest,
  error,
  basis,
  profile,
  horizon,
  horizonDetail,
  layer,
  selectedAnchor,
  enabledBasis,
  horizons,
  onBasisChange,
  onProfileChange,
  onHorizonChange,
  onAnchorSelect,
}: {
  manifest: Manifest | null;
  error: string | null;
  basis: Basis;
  profile: Profile;
  horizon: string | undefined;
  horizonDetail: ForecastHorizon | undefined;
  layer: ReturnType<typeof findLayer> | null;
  selectedAnchor: Anchor | null;
  enabledBasis: Basis[];
  horizons: string[];
  onBasisChange: (basis: Basis) => void;
  onProfileChange: (profile: Profile) => void;
  onHorizonChange: (horizon: string) => void;
  onAnchorSelect: (anchor: Anchor) => void;
}) {
  const visibleAnchors = useMemo(() => manifest?.anchors.filter((anchor) => !!horizon && anchorWithHorizon(anchor, horizon)) ?? [], [horizon, manifest]);
  return (
    <main className="app">
      <section className="panel">
        <div className="statusStack">
          <div className="intro">
            <p className="eyebrow">Nice Weather Finder</p>
            <h1>Warm, dry, bike-friendly Europe.</h1>
            <p className="summary">Scores prefer 22-27C, dry air, low cloud, and low wind.</p>
          </div>
          {error && <p className="error">{error}</p>}
          {!manifest && !error && <p className="loading">Loading real forecast artifacts...</p>}
          {manifest && <FreshnessStatus manifest={manifest} horizon={horizonDetail} />}
        </div>

        {manifest && (
          <>
            <div className="controlStrip">
              <div className="controls">
                <BasisToggle value={basis} enabled={enabledBasis} onChange={onBasisChange} />
                <ProfileToggle value={profile} onChange={onProfileChange} />
                <HorizonPicker horizons={horizons} details={manifest.sources.forecast.horizonDetails ?? []} value={horizon} onChange={onHorizonChange} />
              </div>
              <Legend categories={manifest.legend.categories} />
            </div>
            <div className="anchorStack">
              <SectionHeader label="Anchor Cities" meta={profile} />
              <div className="anchorList" aria-label="Anchor city scores">
                {visibleAnchors.map((anchor) => {
                  const anchorHorizon = anchorWithHorizon(anchor, horizon);
                  if (!anchorHorizon) return null;
                  const score = anchorHorizon.scores[profile];
                  return (
                    <button
                      aria-pressed={anchor.name === selectedAnchor?.name}
                      className={anchor.name === selectedAnchor?.name ? 'anchorRow active' : 'anchorRow'}
                      key={anchor.name}
                      onClick={() => onAnchorSelect(anchor)}
                    >
                      <span className="anchorDot" style={{ background: score.color }} />
                      <span>{anchor.name}</span>
                      <strong>{score.score}</strong>
                    </button>
                  );
                })}
              </div>
            </div>
            <div className="detailStack">
              {selectedAnchor && horizon && <AnchorDetail anchor={selectedAnchor} horizon={horizon} profile={profile} />}
              <ScoringGuide basis={basis} />
            </div>
          </>
        )}
      </section>
      <WeatherMap layer={layer} anchors={visibleAnchors} profile={profile} horizon={horizon} selectedAnchor={selectedAnchor} onAnchorSelect={onAnchorSelect} />
    </main>
  );
}

function FreshnessStatus({ manifest, horizon }: { manifest: Manifest; horizon: ForecastHorizon | undefined }) {
  const freshness = forecastFreshness(manifest);
  const isStale = freshness.state === 'stale';
  return (
    <section className={isStale ? 'freshness stale' : 'freshness'} aria-label="Forecast freshness">
      <strong>{isStale ? 'Stale forecast' : freshness.state === 'fresh' ? 'Forecast is current' : 'Forecast age unknown'}</strong>
      <span>DWD run {manifest.sources.forecast.runStartedAt ? formatUTC(manifest.sources.forecast.runStartedAt) : manifest.sources.forecast.run}</span>
      <span>Built {formatUTC(manifest.generatedAt)}</span>
      {freshness.ageHours !== undefined && <span>Age {Math.round(freshness.ageHours)}h; stale after {freshness.staleAfterHours}h</span>}
      {horizon && <span>{horizon.label}: {horizon.localDate}, {horizon.windowStart}-{horizon.windowEnd} local time</span>}
    </section>
  );
}

function AnchorDetail({ anchor, horizon, profile }: { anchor: Anchor; horizon: string; profile: Profile }) {
  const anchorHorizon = anchorWithHorizon(anchor, horizon);
  if (!anchorHorizon) return null;
  const score = anchorHorizon.scores[profile];
  const rows = componentRows(anchorHorizon, score);
  return (
    <section className="anchorDetail" aria-label={`${anchor.name} score details`}>
      <div className="anchorDetailHeader">
        <div>
          <h2>{anchor.name}</h2>
          <p>{score.category}</p>
        </div>
        <strong style={{ background: score.color }}>{score.score}</strong>
      </div>
      <dl className="weatherFacts">
        {rows.map((row) => (
          <div key={row.label}>
            <dt>{row.label}</dt>
            <dd>{row.value}</dd>
            <dd>{row.verdict}</dd>
            <dd>{row.score}/100</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function ScoringGuide({ basis }: { basis: Basis }) {
  return (
    <section className="scoringGuide" aria-label="Scoring guide">
      <SectionHeader label="Good Means" meta={basis === 'forecast' ? 'model forecast' : 'climate normal'} />
      <dl>
        <div><dt>Window</dt><dd>{basis === 'forecast' ? '10-20 local' : 'monthly daytime'}</dd></div>
        <div><dt>Temp</dt><dd>22-27C avg</dd></div>
        <div><dt>Rain</dt><dd>near 0mm</dd></div>
        <div><dt>Cloud</dt><dd>0-20% avg</dd></div>
        <div><dt>Wind</dt><dd>&lt;10km/h avg</dd></div>
        <div><dt>Humidity</dt><dd>30-60%</dd></div>
      </dl>
    </section>
  );
}

function SectionHeader({ label, meta }: { label: string; meta: string }) {
  return (
    <div className="sectionHeader">
      <span>{label}</span>
      <span>{meta}</span>
    </div>
  );
}

function componentRows(anchor: AnchorHorizon, score: AnchorHorizon['scores'][Profile]) {
  return [
    { label: 'Temp', value: `${anchor.input.tempC}C`, verdict: componentVerdict(score.temp), score: score.temp },
    { label: 'Rain', value: `${anchor.input.rainMm}mm`, verdict: componentVerdict(score.rain), score: score.rain },
    { label: 'Cloud', value: `${anchor.input.cloudPct}%`, verdict: componentVerdict(score.cloud), score: score.cloud },
    { label: 'Wind', value: `${anchor.input.windKmh}km/h`, verdict: componentVerdict(score.wind), score: score.wind },
    { label: 'Humidity', value: `${anchor.input.humidity}%`, verdict: componentVerdict(score.humidity), score: score.humidity },
  ];
}

function anchorWithHorizon(anchor: Anchor | undefined | null, horizon: string | undefined): AnchorHorizon | undefined {
  if (!anchor || !horizon) return undefined;
  return anchor.horizons[horizon];
}

function DesignSystem() {
  const [basis, setBasis] = useState<Basis>('forecast');
  const [profile, setProfile] = useState<Profile>('general');
  const [horizon, setHorizon] = useState('2026-05-05');
  const [selectedAnchor, setSelectedAnchor] = useState<Anchor | null>(designManifest.anchors[0]);
  const layer = findLayer(designManifest, basis, profile, horizon);
  return (
    <NiceWeatherScreen
      manifest={designManifest}
      error={null}
      basis={basis}
      profile={profile}
      horizon={horizon}
      horizonDetail={forecastHorizonDetail(designManifest, horizon)}
      layer={layer}
      selectedAnchor={selectedAnchor}
      enabledBasis={['forecast']}
      horizons={designManifest.sources.forecast.horizons}
      onBasisChange={setBasis}
      onProfileChange={setProfile}
      onHorizonChange={setHorizon}
      onAnchorSelect={setSelectedAnchor}
    />
  );
}

const designManifest: Manifest = {
  version: 1,
  generatedAt: '2026-05-05T14:00:00Z',
  sources: {
    forecast: {
      name: 'DWD ICON-EU',
      run: '2026050506',
      runStartedAt: '2026-05-05T06:00:00Z',
      staleAfterHours: 12,
      aggregation: '10:00-20:00 local time per grid cell',
      horizons: ['2026-05-05', '2026-05-06'],
      horizonDetails: [
        { id: '2026-05-05', label: 'Tue 5 May', localDate: '2026-05-05', windowStart: '10:00', windowEnd: '20:00' },
        { id: '2026-05-06', label: 'Wed 6 May', localDate: '2026-05-06', windowStart: '10:00', windowEnd: '20:00' },
      ],
    },
  },
  layers: [
    { basis: 'forecast', profile: 'general', horizon: '2026-05-05', tileUrl: '/artifacts/tiles/forecast/general/2026-05-05/{z}/{x}/{y}.png' },
    { basis: 'forecast', profile: 'cycling', horizon: '2026-05-05', tileUrl: '/artifacts/tiles/forecast/cycling/2026-05-05/{z}/{x}/{y}.png' },
    { basis: 'forecast', profile: 'general', horizon: '2026-05-06', tileUrl: '/artifacts/tiles/forecast/general/2026-05-06/{z}/{x}/{y}.png' },
    { basis: 'forecast', profile: 'cycling', horizon: '2026-05-06', tileUrl: '/artifacts/tiles/forecast/cycling/2026-05-06/{z}/{x}/{y}.png' },
  ],
  anchors: [
    {
      name: 'Rotterdam',
      lon: 4.4777,
      lat: 51.9244,
      horizons: {
        '2026-05-05': {
          input: { tempC: 18.4, rainMm: 0, cloudPct: 25, windKmh: 14, humidity: 64 },
          scores: {
            general: { score: 78, category: 'Beautiful', color: '#a3e635', temp: 75, rain: 100, cloud: 95, wind: 91, humidity: 92, why: 'clear and dry, slightly cool' },
            cycling: { score: 77, category: 'Beautiful', color: '#a3e635', temp: 75, rain: 100, cloud: 95, wind: 91, humidity: 92, why: 'dry with okay wind' },
          },
        },
        '2026-05-06': {
          input: { tempC: 14.8, rainMm: 3.2, cloudPct: 88, windKmh: 27, humidity: 82 },
          scores: {
            general: { score: 38, category: 'Not Nice', color: '#9ca3af', temp: 38, rain: 50, cloud: 20, wind: 58, humidity: 42, why: 'cold, cloudy, windy' },
            cycling: { score: 32, category: 'Not Nice', color: '#9ca3af', temp: 38, rain: 50, cloud: 20, wind: 58, humidity: 42, why: 'wet and windy' },
          },
        },
      },
    },
  ],
  legend: {
    min: 0,
    max: 100,
    categories: [
      { label: 'Not Nice', color: '#9ca3af', min: 0, max: 49 },
      { label: 'Fair', color: '#7dd3fc', min: 50, max: 61 },
      { label: 'Nice', color: '#38bdf8', min: 62, max: 71 },
      { label: 'Beautiful', color: '#a3e635', min: 72, max: 81 },
      { label: 'Perfect', color: '#16a34a', min: 82, max: 91 },
      { label: 'Pristine', color: '#f59e0b', min: 92, max: 100 },
    ],
  },
};

function componentVerdict(score: number) {
  if (score >= 92) return 'excellent';
  if (score >= 82) return 'great';
  if (score >= 72) return 'good';
  if (score >= 62) return 'okay';
  if (score >= 50) return 'weak';
  return 'bad';
}
