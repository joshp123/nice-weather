import 'maplibre-gl/dist/maplibre-gl.css';
import maplibregl, { type Map } from 'maplibre-gl';
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { telemetry } from '../telemetry';
import { activateRendererAttempt, decideRendererFailure, detachRenderer, recordFailure, withCurrentRenderer, type RendererFailureReason } from './failureWindow';
import { initialWorldwideCamera, minimumWorldwideCoverZoom } from './initialCamera';
import { scoreOverlayBefore } from './overlayPlacement';
import { ScalarTileLayer, type GeographicBounds, type ScalarPaletteColour, type ScalarTileID, type ScalarTileSource, type TileDraw } from './scalarTileLayer';

const SCALAR_LAYER_ID = 'weather-score-scalar';

export type MapPoint = { lat: number; lon: number; label?: string; secondaryLabel?: string; allowNearbyClimate?: boolean };
export type OverlayState =
  | { status: 'idle' }
  | { status: 'loading' | 'ready'; assetUrl: string }
  | { status: 'error'; assetUrl: string; reason: 'stale-release' | 'load-failed' | 'timeout' };
type MapCommand = { type: 'select' | 'left' | 'right' | 'up' | 'down' | 'zoom-in' | 'zoom-out'; id: number };

export type ScalarOverlay = Readonly<{
  key: string;
  minSourceZoom: number;
  maxSourceZoom: number;
  palette: readonly ScalarPaletteColour[];
  urlForTile: (tile: ScalarTileID) => string;
  sha256ForTile: (tile: ScalarTileID) => string;
  tileAvailable: (tile: ScalarTileID) => boolean;
  maxTextures?: number;
  visibleTilesForMap?: (bounds: GeographicBounds, mapZoom: number) => readonly TileDraw[];
  sourceForTile?: (tile: ScalarTileID) => ScalarTileSource | undefined;
}>;

type Props = {
  scalarOverlay?: ScalarOverlay;
  initialCamera?: { longitude: number; latitude: number; zoom: number };
  selectedPoint?: MapPoint;
  focusPoint?: MapPoint & { token: number };
  onSelect: (point: MapPoint, intent?: { focusDetails?: boolean }) => void;
  onViewportChange?: (viewport: { latitude: number; longitude: number; zoom: number; bounds: GeographicBounds }) => void;
  onReady?: (elapsedMs: number) => void;
  onOverlayReady?: (elapsedMs: number, renderer: 'maplibre', assetUrl: string) => void;
  onOverlayState?: (state: OverlayState) => void;
};

export function WeatherMap(props: Props) {
  const [renderer, setRenderer] = useState<'maplibre' | 'unavailable'>('maplibre');
  const [rendererAttempt, setRendererAttempt] = useState(0);
  const recovery = useRef({ activeAttempt: 0, retryUsed: false, retryPending: false });
  const retryTimer = useRef<number | undefined>(undefined);
  recovery.current = activateRendererAttempt(recovery.current, rendererAttempt);
  const [command, setCommand] = useState<MapCommand | undefined>(undefined);
  const onOverlayStateRef = useRef(props.onOverlayState);
  onOverlayStateRef.current = props.onOverlayState;
  const handleFailure = useCallback((reason: MapFailureReason, failedAttempt: number) => {
    const decision = decideRendererFailure(recovery.current, failedAttempt, reason);
    recovery.current = decision.state;
    if (decision.action === 'ignore') return;
    if (decision.action === 'retry') {
      retryTimer.current = window.setTimeout(() => setRendererAttempt((value) => value + 1), 180);
      telemetry('map-renderer-retry', { reason });
      return;
    }
    window.clearTimeout(retryTimer.current);
    retryTimer.current = undefined;
    telemetry('map-unavailable', { reason });
    setRenderer('unavailable');
  }, []);
  const handleActiveFailure = useCallback((reason: MapFailureReason) => {
    handleFailure(reason, rendererAttempt);
  }, [handleFailure, rendererAttempt]);
  useEffect(() => () => window.clearTimeout(retryTimer.current), []);
  useLayoutEffect(() => {
    onOverlayStateRef.current?.(props.scalarOverlay ? { status: 'loading', assetUrl: props.scalarOverlay.key } : { status: 'idle' });
  }, [props.scalarOverlay]);
  const targetOverlay = props.scalarOverlay?.key;
  const rendererProps = {
    ...props,
    onRendererOverlayReady: (elapsedMs: number, renderer: 'maplibre', assetUrl: string) => {
      if (assetUrl !== targetOverlay) return;
      onOverlayStateRef.current?.({ status: 'ready', assetUrl });
      props.onOverlayReady?.(elapsedMs, renderer, assetUrl);
    },
    onRendererOverlayError: (assetUrl: string, reason: 'load-failed' | 'timeout') => {
      if (assetUrl !== targetOverlay) return;
      onOverlayStateRef.current?.({ status: 'error', assetUrl, reason });
    },
  };
  const keyboard = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.target !== event.currentTarget) return;
    const commands: Record<string, MapCommand['type']> = {
      Enter: 'select', ArrowLeft: 'left', ArrowRight: 'right', ArrowUp: 'up', ArrowDown: 'down', '+': 'zoom-in', '=': 'zoom-in', '-': 'zoom-out', '_': 'zoom-out',
    };
    const type = commands[event.key];
    if (!type) return;
    event.preventDefault();
    telemetry('map-keyboard', { command: type });
    setCommand({ type, id: performance.now() });
  };
  return (
    <div className="mapCanvas mapStack" data-testid="weather-map" role="region" tabIndex={0} aria-label="Weather score map" aria-describedby="map-instructions" aria-keyshortcuts="Enter ArrowLeft ArrowRight ArrowUp ArrowDown + -" onKeyDown={keyboard}>
      <p id="map-instructions" className="srOnly">Use arrow keys to pan the map. Use plus and minus to zoom. Press Enter to show weather at the centre.</p>
      {renderer === 'maplibre'
        ? <MapLibreMap key={rendererAttempt} {...rendererProps} command={command} onFailure={handleActiveFailure} />
        : <div className="mapUnavailable" role="status"><div><strong>Map unavailable</strong><p>This browser could not display the map. Reload to try again.</p></div></div>}
    </div>
  );
}

type MapFailureReason = RendererFailureReason;

type RendererProps = Props & {
  onRendererOverlayReady: (elapsedMs: number, renderer: 'maplibre', assetUrl: string) => void;
  onRendererOverlayError: (assetUrl: string, reason: 'load-failed' | 'timeout') => void;
};

function MapLibreMap({ scalarOverlay, initialCamera: restoredCamera, selectedPoint, focusPoint, onSelect, onViewportChange, onReady, onRendererOverlayReady, onRendererOverlayError, command, onFailure }: RendererProps & { command?: MapCommand; onFailure: (reason: MapFailureReason) => void }) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const mapRef = useRef<Map | null>(null);
  const markerRef = useRef<maplibregl.Marker | null>(null);
  const onSelectRef = useRef(onSelect);
  const loadedRef = useRef(false);
  const onReadyRef = useRef(onReady);
  const onViewportChangeRef = useRef(onViewportChange);
  const onOverlayReadyRef = useRef(onRendererOverlayReady);
  const onOverlayErrorRef = useRef(onRendererOverlayError);
  const cameraOwnedByUser = useRef(Boolean(restoredCamera));
  const [styleLoaded, setStyleLoaded] = useState(false);
  const [mapLoaded, setMapLoaded] = useState(false);
  onSelectRef.current = onSelect;
  onReadyRef.current = onReady;
  onViewportChangeRef.current = onViewportChange;
  onOverlayReadyRef.current = onRendererOverlayReady;
  onOverlayErrorRef.current = onRendererOverlayError;

  useEffect(() => {
    if (!containerRef.current || mapRef.current) return;
    const container = containerRef.current;
    const startedAt = performance.now();
    let map: Map;
    const initialWidth = container.clientWidth || window.innerWidth;
    const initialHeight = container.clientHeight || window.innerHeight;
    const initialCamera = restoredCamera ?? initialWorldwideCamera(initialWidth, initialHeight);
    try {
      map = new maplibregl.Map({
        container,
        style: 'https://tiles.openfreemap.org/styles/dark',
        center: [initialCamera.longitude, initialCamera.latitude],
        zoom: initialCamera.zoom,
        minZoom: minimumWorldwideCoverZoom(initialWidth, initialHeight),
        maxZoom: 9,
        attributionControl: false,
        dragRotate: false,
        pitchWithRotate: false,
        touchPitch: false,
        keyboard: false,
        renderWorldCopies: false,
        fadeDuration: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 0 : 120,
      });
    } catch (error) {
      console.warn('[nice-weather] WebGL map unavailable.', error);
      onFailure('init');
      return;
    }
    mapRef.current = map;
    let resizeFrame = 0;
    const applyInitialCamera = () => {
      if (cameraOwnedByUser.current || container.clientWidth <= 0 || container.clientHeight <= 0) return;
      const camera = initialWorldwideCamera(container.clientWidth, container.clientHeight);
      map.setMinZoom(minimumWorldwideCoverZoom(container.clientWidth, container.clientHeight));
      map.jumpTo({ center: [camera.longitude, camera.latitude], zoom: camera.zoom });
    };
    const resizeObserver = new ResizeObserver(() => {
      window.cancelAnimationFrame(resizeFrame);
      resizeFrame = window.requestAnimationFrame(() => {
        if (container.clientWidth > 0 && container.clientHeight > 0) map.setMinZoom(minimumWorldwideCoverZoom(container.clientWidth, container.clientHeight));
        map.resize();
        applyInitialCamera();
      });
    });
    resizeObserver.observe(container);
    const claimCamera = () => { cameraOwnedByUser.current = true; };
    container.addEventListener('pointerdown', claimCamera, { passive: true });
    container.addEventListener('wheel', claimCamera, { passive: true });
    map.touchZoomRotate.disableRotation();
    map.getCanvas().tabIndex = -1;
    map.addControl(new maplibregl.NavigationControl({ showCompass: false }), 'top-right');
    map.addControl(new maplibregl.AttributionControl({ compact: true }), 'bottom-right');
    // MapLibre opens compact attribution on creation. Close only that initial
    // disclosure so it cannot obscure search or sample status; the native
    // details control remains available and opens normally when selected.
    const collapseInitialAttribution = () => {
      const attribution = containerRef.current?.querySelector<HTMLDetailsElement>('details.maplibregl-ctrl-attrib');
      attribution?.removeAttribute('open');
      attribution?.classList.remove('maplibregl-compact-show');
    };
    const attributionDeadline = window.setTimeout(collapseInitialAttribution, 750);
    const reportViewport = () => {
      const center = map.getCenter();
      const visible = map.getBounds();
      onViewportChangeRef.current?.({
        latitude: center.lat,
        longitude: center.lng,
        zoom: map.getZoom(),
        bounds: { west: visible.getWest(), east: visible.getEast(), south: visible.getSouth(), north: visible.getNorth() },
      });
    };
    let baseMapFailures: number[] = [];
    const failMap = (reason: MapFailureReason) => {
      mapRef.current = detachRenderer(mapRef.current, map);
      onFailure(reason);
    };
    const deadline = window.setTimeout(() => {
      if (!loadedRef.current) {
        console.warn('[nice-weather] WebGL map did not render in time.');
        failMap('timeout');
      }
    }, 5_000);
    const markReady = () => {
      if (loadedRef.current) return;
      loadedRef.current = true;
      setMapLoaded(true);
      window.clearTimeout(deadline);
      window.requestAnimationFrame(collapseInitialAttribution);
      reportViewport();
      onReadyRef.current?.(Math.round(performance.now() - startedAt));
    };
    map.once('style.load', () => {
      applyInitialCamera();
      for (const layer of map.getStyle().layers ?? []) {
        if (layer.type === 'background') map.setPaintProperty(layer.id, 'background-color', '#242b33');
        if (layer.id === 'water') {
          map.setPaintProperty(layer.id, 'fill-color', '#101820');
          map.setPaintProperty(layer.id, 'fill-antialias', true);
          map.setPaintProperty(layer.id, 'fill-outline-color', '#627081');
        }
        if (layer.type === 'symbol' && /^place_/.test(layer.id)) {
          map.setLayoutProperty(layer.id, 'icon-image', '');
          if (/^place_country/.test(layer.id)) map.setLayerZoomRange(layer.id, 0, layer.maxzoom ?? 24);
          else map.setLayoutProperty(layer.id, 'text-transform', 'none');
          map.setLayoutProperty(layer.id, 'text-field', ['coalesce', ['get', 'name:en'], ['get', 'name']]);
          map.setPaintProperty(layer.id, 'text-color', '#f2f4f6');
          map.setPaintProperty(layer.id, 'text-halo-color', '#18212d');
          map.setPaintProperty(layer.id, 'text-halo-width', 1.4);
          map.setPaintProperty(layer.id, 'text-halo-blur', 0);
        }
        if (layer.type === 'line' && /^boundary_/.test(layer.id)) {
          map.setFilter(layer.id, ['all', layer.filter ?? ['literal', true], ['!=', ['get', 'maritime'], 1]] as maplibregl.FilterSpecification);
          map.setPaintProperty(layer.id, 'line-color', '#f2f4f6');
          map.setPaintProperty(layer.id, 'line-opacity', .45);
          map.setPaintProperty(layer.id, 'line-blur', 0);
        }
      }
      setStyleLoaded(true);
      map.once('render', markReady);
      map.triggerRepaint();
    });
    map.on('load', markReady);
    map.on('moveend', () => {
      reportViewport();
    });
    map.on('click', (event) => onSelectRef.current({ lat: event.lngLat.lat, lon: event.lngLat.lng }));
    map.on('error', (event) => {
      const message = event.error?.message ?? 'Map rendering error';
      console.warn('[nice-weather] map', message);
      if (/shader|webgl|context/i.test(message)) {
        failMap('context');
        return;
      }
      const failureWindow = recordFailure(baseMapFailures, performance.now());
      baseMapFailures = failureWindow.failures;
      if (failureWindow.sustained) {
        console.warn('[nice-weather] Base map requests keep failing; the map is unavailable.');
        failMap('base-map');
      }
    });
    const onContextLost = () => failMap('context');
    map.getCanvas().addEventListener('webglcontextlost', onContextLost, { once: true });
    return () => {
      resizeObserver.disconnect();
      container.removeEventListener('pointerdown', claimCamera);
      container.removeEventListener('wheel', claimCamera);
      window.cancelAnimationFrame(resizeFrame);
      window.clearTimeout(deadline);
      window.clearTimeout(attributionDeadline);
      markerRef.current?.remove();
      map.getCanvas().removeEventListener('webglcontextlost', onContextLost);
      if (mapRef.current === map) mapRef.current = null;
      map.remove();
      loadedRef.current = false;
    };
  }, [onFailure]);

  useLayoutEffect(() => {
    const map = mapRef.current;
    if (!map || !styleLoaded) return;
    const activeMap = map;
    removeScalarOverlay(activeMap);
    if (!scalarOverlay) return;
    const startedAt = performance.now();
    const key = scalarOverlay.key;
    let readyReported = false;
    let failed = false;
    const deadline = window.setTimeout(() => fail('timeout'), 8_000);
    const ready = () => {
      if (readyReported || failed) return;
      readyReported = true;
      window.clearTimeout(deadline);
      onOverlayReadyRef.current?.(Math.round(performance.now() - startedAt), 'maplibre', key);
    };
    const layer = new ScalarTileLayer({
      id: SCALAR_LAYER_ID,
      minSourceZoom: scalarOverlay.minSourceZoom,
      maxSourceZoom: scalarOverlay.maxSourceZoom,
      maxTextures: scalarOverlay.maxTextures ?? 64,
      maxRequests: 6,
      palette: scalarOverlay.palette,
      urlForTile: scalarOverlay.urlForTile,
      sha256ForTile: scalarOverlay.sha256ForTile,
      tileAvailable: scalarOverlay.tileAvailable,
      visibleTilesForMap: scalarOverlay.visibleTilesForMap,
      sourceForTile: scalarOverlay.sourceForTile,
      onError: () => fail('load-failed'),
      onContractError: () => fail('load-failed'),
      onReady: ready,
    });
    function fail(reason: 'load-failed' | 'timeout') {
      if (failed) return;
      failed = true;
      window.clearTimeout(deadline);
      withCurrentRenderer(mapRef.current, activeMap, removeScalarOverlay);
      onOverlayErrorRef.current(key, reason);
    }
    try {
      const layers = activeMap.getStyle().layers ?? [];
      const beforeId = scoreOverlayBefore(layers);
      activeMap.addLayer(layer, beforeId);
      if (activeMap.getLayer('water')) activeMap.moveLayer('water', beforeId);
    } catch {
      fail('load-failed');
    }
    return () => {
      window.clearTimeout(deadline);
      withCurrentRenderer(mapRef.current, activeMap, removeScalarOverlay);
    };
  }, [scalarOverlay, styleLoaded]);

  useEffect(() => {
    const map = mapRef.current;
    if (!map || !styleLoaded) return;
    markerRef.current?.remove();
    markerRef.current = null;
    if (!selectedPoint) return;
    const element = document.createElement('div');
    element.className = 'pointMarker';
    markerRef.current = new maplibregl.Marker({ element, anchor: 'center' }).setLngLat([selectedPoint.lon, selectedPoint.lat]).addTo(map);
  }, [selectedPoint, styleLoaded]);

  useEffect(() => {
    if (!focusPoint || !mapRef.current || !styleLoaded) return;
    cameraOwnedByUser.current = true;
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) mapRef.current.jumpTo({ center: [focusPoint.lon, focusPoint.lat], zoom: 7 });
    else mapRef.current.flyTo({ center: [focusPoint.lon, focusPoint.lat], zoom: 7, duration: 650, essential: false });
  }, [focusPoint, styleLoaded]);

  useEffect(() => {
    const map = mapRef.current;
    if (!map || !styleLoaded || !command) return;
    cameraOwnedByUser.current = true;
    if (command.type === 'select') {
      const center = map.getCenter();
      onSelectRef.current({ lat: center.lat, lon: center.lng }, { focusDetails: true });
    } else if (command.type === 'zoom-in') map.zoomIn({ duration: 0 });
    else if (command.type === 'zoom-out') map.zoomOut({ duration: 0 });
    else map.panBy([command.type === 'left' ? -80 : command.type === 'right' ? 80 : 0, command.type === 'up' ? -80 : command.type === 'down' ? 80 : 0], { duration: 0 });
  }, [command, styleLoaded]);

  return <><div ref={containerRef} className="maplibreSurface" aria-hidden="true" />{!mapLoaded && <div className="mapPending" aria-hidden="true"><span>Loading map</span></div>}</>;
}

function removeScalarOverlay(map: Map) {
  if (map.getLayer(SCALAR_LAYER_ID)) map.removeLayer(SCALAR_LAYER_ID);
}
