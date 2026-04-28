import { useEffect } from 'react';
import type { Map } from 'maplibre-gl';
import type { LayerManifest } from '../data/types';

const SOURCE_ID = 'score-raster-source';
const LAYER_ID = 'score-raster-layer';

type Props = {
  map: Map | null;
  layer: LayerManifest | null;
};

export function ScoreRasterLayer({ map, layer }: Props) {
  useEffect(() => {
    if (!map || !layer) return;

    const installLayer = () => {
      const startedAt = performance.now();
      const tileUrl = `${window.location.origin}${layer.tileUrl}`;
      if (map.getLayer(LAYER_ID)) {
        map.removeLayer(LAYER_ID);
      }
      if (map.getSource(SOURCE_ID)) {
        map.removeSource(SOURCE_ID);
      }

      map.addSource(SOURCE_ID, {
        type: 'raster',
        tiles: [tileUrl],
        tileSize: 256,
        minzoom: 0,
        maxzoom: 6,
        bounds: [-25, 34, 45, 72],
      });
      map.addLayer({
        id: LAYER_ID,
        type: 'raster',
        source: SOURCE_ID,
        paint: { 'raster-opacity': 0.55 },
      });
      console.info('nice-weather-finder:score-layer-installed', JSON.stringify({
        basis: layer.basis,
        profile: layer.profile,
        horizon: layer.horizon,
        tileUrl,
        elapsedMs: Math.round(performance.now() - startedAt),
      }));
    };

    if (map.isStyleLoaded()) {
      installLayer();
    } else {
      map.once('load', installLayer);
    }

    return () => {
      map.off('load', installLayer);
      removeScoreLayer(map);
    };
  }, [map, layer]);

  return null;
}

function removeScoreLayer(map: Map | undefined) {
  if (!map) return;
  try {
    if (map.getLayer(LAYER_ID)) map.removeLayer(LAYER_ID);
    if (map.getSource(SOURCE_ID)) map.removeSource(SOURCE_ID);
  } catch (error) {
    console.info('nice-weather-finder:score-layer-cleanup-skipped', String(error));
  }
}
