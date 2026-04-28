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
        <div>
          <p className="eyebrow">Nice Weather Finder POC</p>
          <h1>Find the warm, dry, bike-friendly parts of Europe.</h1>
          <p className="summary">Built for escaping Dutch grey weather. Scores heavily prefer 22-27C, low rain, sun, and low wind.</p>
        </div>

        {error && <p className="error">{error}</p>}
        {!manifest && !error && <p className="loading">Loading real forecast artifacts...</p>}

        {manifest && (
          <>
            <div className="controls">
              <BasisToggle value={basis} enabled={enabledBasis} onChange={setBasis} />
              <ProfileToggle value={profile} onChange={setProfile} />
            </div>
            <HorizonPicker horizons={horizons} value={selectedHorizon} onChange={setHorizon} />
            <Legend categories={manifest.legend.categories} />
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
            <p className="source">
              {basis === 'forecast' || !manifest.sources.climatology
                ? forecastSourceText(manifest)
                : `${manifest.sources.climatology.name} ${manifest.sources.climatology.period}`}
            </p>
          </>
        )}
      </section>
      <WeatherMap layer={layer} anchors={manifest?.anchors ?? []} profile={profile} selectedAnchor={selectedAnchor} onAnchorSelect={(anchor: Anchor) => setSelectedAnchorName(anchor.name)} />
    </main>
  );
}

function AnchorDetail({ anchor, profile }: { anchor: Anchor; profile: Profile }) {
  const score = anchor.scores[profile];
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
        <div><dt>Temp</dt><dd>{anchor.input.tempC}C</dd><dd>{score.temp}</dd></div>
        <div><dt>Rain</dt><dd>{anchor.input.rainMm}mm</dd><dd>{score.rain}</dd></div>
        <div><dt>Cloud</dt><dd>{anchor.input.cloudPct}%</dd><dd>{score.cloud}</dd></div>
        <div><dt>Wind</dt><dd>{anchor.input.windKmh}km/h</dd><dd>{score.wind}</dd></div>
        <div><dt>Humidity</dt><dd>{anchor.input.humidity}%</dd><dd>{score.humidity}</dd></div>
      </dl>
    </section>
  );
}

function forecastSourceText(manifest: Manifest) {
  const run = manifest.sources.forecast.run.match(/^(\d{4})(\d{2})(\d{2})(\d{2})/)?.slice(1);
  const runText = run ? `${run[0]}-${run[1]}-${run[2]} ${run[3]}Z` : manifest.sources.forecast.run;
  return `${manifest.sources.forecast.name} run ${runText} • daily forecast sample`;
}
