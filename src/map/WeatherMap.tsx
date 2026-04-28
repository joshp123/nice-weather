import 'maplibre-gl/dist/maplibre-gl.css';
import maplibregl, { type Map } from 'maplibre-gl';
import { useEffect, useRef, useState } from 'react';
import type { LayerManifest } from '../data/types';
import { EUROPE_CENTER } from './EuropeBounds';
import { ScoreRasterLayer } from './ScoreRasterLayer';

type Props = {
  layer: LayerManifest | null;
};

export function WeatherMap({ layer }: Props) {
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
      zoom: 4,
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
    </div>
  );
}
