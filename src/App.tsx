import { useEffect, useMemo, useState } from 'react';
import { BasisToggle } from './controls/BasisToggle';
import { HorizonPicker } from './controls/HorizonPicker';
import { Legend } from './controls/Legend';
import { ProfileToggle } from './controls/ProfileToggle';
import { findLayer, horizonsFor, loadManifest, validHorizon } from './data/manifest';
import type { Basis, Manifest, Profile } from './data/types';
import { WeatherMap } from './map/WeatherMap';
import './styles.css';

export default function App() {
  const [manifest, setManifest] = useState<Manifest | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [basis, setBasis] = useState<Basis>('forecast');
  const [profile, setProfile] = useState<Profile>('general');
  const [horizon, setHorizon] = useState('d1');

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
            <div className="anchorList">
              {manifest.anchors.map((anchor) => {
                const score = anchor.scores[profile];
                return (
                  <div className="anchorRow" key={anchor.name}>
                    <span className="anchorDot" style={{ background: score.color }} />
                    <span>{anchor.name}</span>
                    <strong>{score.score}</strong>
                  </div>
                );
              })}
            </div>
            <p className="source">
              {basis === 'forecast' || !manifest.sources.climatology
                ? `${manifest.sources.forecast.name} ${manifest.sources.forecast.run}`
                : `${manifest.sources.climatology.name} ${manifest.sources.climatology.period}`}
            </p>
          </>
        )}
      </section>
      <WeatherMap layer={layer} anchors={manifest?.anchors ?? []} profile={profile} />
    </main>
  );
}
