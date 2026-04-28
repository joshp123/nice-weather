import 'maplibre-gl/dist/maplibre-gl.css';
import maplibregl, { type Map } from 'maplibre-gl';
import { useEffect, useRef, useState } from 'react';
import type { Anchor, LayerManifest, Profile } from '../data/types';
import { EUROPE_CENTER } from './EuropeBounds';
import { ScoreRasterLayer } from './ScoreRasterLayer';

type Props = {
  layer: LayerManifest | null;
  anchors: Anchor[];
  profile: Profile;
  selectedAnchor: Anchor | null;
  onAnchorSelect: (anchor: Anchor) => void;
};

export function WeatherMap({ layer, anchors, profile, selectedAnchor, onAnchorSelect }: Props) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const mapRef = useRef<Map | null>(null);
  const [map, setMap] = useState<Map | null>(null);

  useEffect(() => {
    if (!containerRef.current || mapRef.current) return;
    const startedAt = performance.now();
    const nextMap = new maplibregl.Map({
      container: containerRef.current,
      style: {
        version: 8,
        sources: {
          osm: {
            type: 'raster',
            tiles: ['https://tile.openstreetmap.org/{z}/{x}/{y}.png'],
            tileSize: 256,
            attribution: 'OpenStreetMap',
          },
        },
        layers: [{ id: 'osm', type: 'raster', source: 'osm', paint: { 'raster-opacity': 0.9 } }],
      },
      center: EUROPE_CENTER,
      zoom: 4.65,
    });
    mapRef.current = nextMap;
    nextMap.addControl(new maplibregl.NavigationControl({ visualizePitch: false }), 'top-right');
    nextMap.on('load', () => {
      console.info('nice-weather-finder:map-loaded', JSON.stringify({
        elapsedMs: Math.round(performance.now() - startedAt),
      }));
      setMap(nextMap);
    });
    return () => {
      nextMap.remove();
      mapRef.current = null;
    };
  }, []);

  return (
    <div className="mapShell">
      <div ref={containerRef} className="mapCanvas" data-testid="weather-map" />
      <ScoreRasterLayer map={map} layer={layer} />
      <AnchorMarkers map={map} anchors={anchors} profile={profile} selectedAnchor={selectedAnchor} onAnchorSelect={onAnchorSelect} />
    </div>
  );
}

function AnchorMarkers({
  map,
  anchors,
  profile,
  selectedAnchor,
  onAnchorSelect,
}: {
  map: Map | null;
  anchors: Anchor[];
  profile: Profile;
  selectedAnchor: Anchor | null;
  onAnchorSelect: (anchor: Anchor) => void;
}) {
  const markersRef = useRef<maplibregl.Marker[]>([]);

  useEffect(() => {
    if (!map) return;
    for (const marker of markersRef.current) marker.remove();
    markersRef.current = anchors.map((anchor) => {
      const score = anchor.scores[profile];
      const element = document.createElement('button');
      element.className = anchor.name === selectedAnchor?.name ? 'anchorPin selected' : 'anchorPin';
      element.style.setProperty('--pin-color', score.color);
      element.type = 'button';
      const value = document.createElement('strong');
      value.textContent = String(score.score);
      const label = document.createElement('span');
      label.textContent = anchor.name;
      element.append(value, label);
      element.title = `${anchor.name}: ${score.score} ${score.category}. ${score.why}`;
      element.addEventListener('click', () => onAnchorSelect(anchor));
      return new maplibregl.Marker({ element, anchor: 'bottom' }).setLngLat([anchor.lon, anchor.lat]).addTo(map);
    });
    return () => {
      for (const marker of markersRef.current) marker.remove();
      markersRef.current = [];
    };
  }, [map, anchors, profile, selectedAnchor, onAnchorSelect]);

  return null;
}
