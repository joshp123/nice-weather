import { useEffect, useMemo, useState } from 'react';
import { BasisToggle } from './controls/BasisToggle';
import { HorizonPicker } from './controls/HorizonPicker';
import { Legend } from './controls/Legend';
import { ProfileToggle } from './controls/ProfileToggle';
import { findLayer, horizonsFor, loadManifest, validHorizon } from './data/manifest';
import type { Anchor, Basis, Manifest, Profile } from './data/types';
import { WeatherMap } from './map/WeatherMap';
import './styles.css';

export default function App() {
  const [manifest, setManifest] = useState<Manifest | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [basis, setBasis] = useState<Basis>('forecast');
  const [profile, setProfile] = useState<Profile>('general');
  const [horizon, setHorizon] = useState('d1');
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
  const selectedAnchor = useMemo(() => {
    if (!manifest) return null;
    return manifest.anchors.find((anchor) => anchor.name === selectedAnchorName) ?? manifest.anchors[0] ?? null;
  }, [manifest, selectedAnchorName]);

  return (
    <main className="app">
      <section className="panel">
        <div className="intro">
          <p className="eyebrow">Nice Weather Finder POC</p>
          <h1>Find the warm, dry, bike-friendly parts of Europe.</h1>
          <p className="summary">Built for escaping Dutch grey weather. Scores heavily prefer 22-27C, low rain, sun, and low wind.</p>
        </div>

        {error && <p className="error">{error}</p>}
        {!manifest && !error && <p className="loading">Loading real forecast artifacts...</p>}

        {manifest && (
          <>
            <div className="controlStrip">
              <div className="controls">
                <BasisToggle value={basis} enabled={enabledBasis} onChange={setBasis} />
                <ProfileToggle value={profile} onChange={setProfile} />
                <HorizonPicker horizons={horizons} value={selectedHorizon} onChange={setHorizon} />
              </div>
              <Legend categories={manifest.legend.categories} />
            </div>
            <div className="anchorList" aria-label="Anchor city scores">
              {manifest.anchors.map((anchor) => {
                const score = anchor.scores[profile];
                return (
                  <button className={anchor.name === selectedAnchor?.name ? 'anchorRow active' : 'anchorRow'} key={anchor.name} onClick={() => setSelectedAnchorName(anchor.name)}>
                    <span className="anchorDot" style={{ background: score.color }} />
                    <span>{anchor.name}</span>
                    <strong>{score.score}</strong>
                  </button>
                );
              })}
            </div>
            {selectedAnchor && <AnchorDetail anchor={selectedAnchor} profile={profile} />}
            <ScoringGuide basis={basis} />
          </>
        )}
      </section>
      <WeatherMap layer={layer} anchors={manifest?.anchors ?? []} profile={profile} selectedAnchor={selectedAnchor} onAnchorSelect={(anchor: Anchor) => setSelectedAnchorName(anchor.name)} />
    </main>
  );
}

function AnchorDetail({ anchor, profile }: { anchor: Anchor; profile: Profile }) {
  const score = anchor.scores[profile];
  const rows = componentRows(anchor, score);
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
      <h2>Good means</h2>
      <dl>
        <div><dt>Window</dt><dd>{basis === 'forecast' ? '10:00-20:00 local forecast' : 'daytime monthly normal'}</dd></div>
        <div><dt>Temp</dt><dd>avg 22-27C</dd></div>
        <div><dt>Rain</dt><dd>total near 0mm</dd></div>
        <div><dt>Cloud</dt><dd>avg 0-20%</dd></div>
        <div><dt>Wind</dt><dd>avg under 10km/h</dd></div>
        <div><dt>Humidity</dt><dd>avg 30-60%</dd></div>
      </dl>
      <p>Current artifact is a cached model sample, not live observed weather.</p>
    </section>
  );
}

function componentRows(anchor: Anchor, score: Anchor['scores'][Profile]) {
  return [
    { label: 'Temp', value: `${anchor.input.tempC}C`, verdict: componentVerdict(score.temp), score: score.temp },
    { label: 'Rain', value: `${anchor.input.rainMm}mm`, verdict: componentVerdict(score.rain), score: score.rain },
    { label: 'Cloud', value: `${anchor.input.cloudPct}%`, verdict: componentVerdict(score.cloud), score: score.cloud },
    { label: 'Wind', value: `${anchor.input.windKmh}km/h`, verdict: componentVerdict(score.wind), score: score.wind },
    { label: 'Humidity', value: `${anchor.input.humidity}%`, verdict: componentVerdict(score.humidity), score: score.humidity },
  ];
}

function componentVerdict(score: number) {
  if (score >= 92) return 'excellent';
  if (score >= 82) return 'great';
  if (score >= 72) return 'good';
  if (score >= 62) return 'okay';
  if (score >= 50) return 'weak';
  return 'bad';
}
