import { useEffect } from 'react';
import type { Map, RasterTileSource } from 'maplibre-gl';
import type { LayerManifest } from '../data/types';
import { telemetry } from '../telemetry';

const SOURCE_ID = 'score-raster-source';
const LAYER_ID = 'score-raster-layer';

type Props = {
  map: Map | null;
  layer: LayerManifest | null;
  maxZoom: number;
};

export function ScoreRasterLayer({ map, layer, maxZoom }: Props) {
  useEffect(() => {
    if (!map) return;

    if (!layer) {
      if (map.isStyleLoaded()) removeScoreLayer(map);
      return;
    }

    const installLayer = () => {
      const startedAt = performance.now();
      const tileUrl = `${window.location.origin}${layer.tileUrl}`;
      const existingSource = map.getSource(SOURCE_ID) as RasterTileSource | undefined;

      if (existingSource) {
        existingSource.setTiles([tileUrl]);
      } else {
        map.addSource(SOURCE_ID, {
          type: 'raster',
          tiles: [tileUrl],
          tileSize: 256,
          minzoom: 0,
          maxzoom: maxZoom,
          bounds: [-25, 34, 45, 72],
        });
      }

      if (!map.getLayer(LAYER_ID)) {
        map.addLayer({
          id: LAYER_ID,
          type: 'raster',
          source: SOURCE_ID,
          paint: {
            'raster-fade-duration': 0,
            'raster-opacity': 0.6,
            'raster-resampling': 'linear',
          },
        });
      }

      telemetry('score-layer-ready', {
        basis: layer.basis,
        profile: layer.profile,
        horizon: layer.horizon,
        maxZoom,
        mode: existingSource ? 'updated' : 'installed',
        tileUrl,
        elapsedMs: Math.round(performance.now() - startedAt),
      });
    };

    if (map.isStyleLoaded()) {
      installLayer();
    } else {
      map.once('load', installLayer);
    }

    return () => {
      map.off('load', installLayer);
    };
  }, [map, layer, maxZoom]);

  return null;
}

function removeScoreLayer(map: Map | undefined) {
  if (!map) return;
  try {
    if (map.getLayer(LAYER_ID)) map.removeLayer(LAYER_ID);
    if (map.getSource(SOURCE_ID)) map.removeSource(SOURCE_ID);
  } catch (error) {
    telemetry('score-layer-cleanup-skipped', { message: String(error) });
  }
}
