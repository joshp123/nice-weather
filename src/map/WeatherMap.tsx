import 'maplibre-gl/dist/maplibre-gl.css';
import maplibregl, { type GeoJSONSource, type Map, type MapSourceDataEvent } from 'maplibre-gl';
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { WindGrid } from '../data/wind';
import { telemetry } from '../telemetry';
import { activateRendererAttempt, decideRendererFailure, detachRenderer, recordFailure, withCurrentRenderer, type RendererFailureReason } from './failureWindow';
import { CanvasFallback } from './canvasFallback';
import { ScalarTileLayer, type GeographicBounds, type ScalarPaletteColour, type ScalarTileID, type ScalarTileSource, type TileDraw } from './scalarTileLayer';
import { baseOverlayBefore, scoreOverlayBefore, windFeatures, windLayerLayout } from './windLayer';

const DEFAULT_BOUNDS: [number, number, number, number] = [-25, 30, 45, 72];
const EUROPE_MAX_BOUNDS: [[number, number], [number, number]] = [[-36, 20], [58, 77]];
const SCALAR_LAYER_ID = 'weather-score-scalar';

export type MapPoint = { lat: number; lon: number; label?: string };
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
  sourceZoomForMap?: (mapZoom: number) => number;
  visibleTilesForMap?: (bounds: GeographicBounds, mapZoom: number) => readonly TileDraw[];
  sourceForTile?: (tile: ScalarTileID) => ScalarTileSource | undefined;
}>;

type Props = {
  overlayUrl?: string;
  scalarOverlay?: ScalarOverlay;
  worldwide?: boolean;
  windGrid?: WindGrid;
  bounds?: [number, number, number, number];
  selectedPoint?: MapPoint;
  focusPoint?: MapPoint & { token: number };
  onSelect: (point: MapPoint, intent?: { focusDetails?: boolean }) => void;
  onViewportChange?: (viewport: { latitude: number; longitude: number; zoom: number; bounds: GeographicBounds }) => void;
  onReady?: (elapsedMs: number) => void;
  onOverlayReady?: (elapsedMs: number, renderer: 'maplibre' | 'canvas', assetUrl: string) => void;
  onOverlayState?: (state: OverlayState) => void;
};

export function WeatherMap(props: Props) {
  const [renderer, setRenderer] = useState<'maplibre' | 'canvas'>('maplibre');
  const [rendererAttempt, setRendererAttempt] = useState(0);
  const recovery = useRef({ activeAttempt: 0, retryUsed: false, retryPending: false });
  const retryTimer = useRef<number | undefined>(undefined);
  recovery.current = activateRendererAttempt(recovery.current, rendererAttempt);
  const [command, setCommand] = useState<MapCommand | undefined>(undefined);
  const [verifiedOverlay, setVerifiedOverlay] = useState<string | undefined>();
  const overlayGeneration = useRef(0);
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
    telemetry('map-renderer-fallback', { reason });
    setRenderer('canvas');
  }, []);
  const handleActiveFailure = useCallback((reason: MapFailureReason) => {
    handleFailure(reason, rendererAttempt);
  }, [handleFailure, rendererAttempt]);
  useEffect(() => () => window.clearTimeout(retryTimer.current), []);
  useLayoutEffect(() => {
    const generation = ++overlayGeneration.current;
    setVerifiedOverlay(undefined);
    if (props.scalarOverlay) {
      onOverlayStateRef.current?.({ status: 'loading', assetUrl: props.scalarOverlay.key });
      return;
    }
    if (!props.overlayUrl) {
      onOverlayStateRef.current?.({ status: 'idle' });
      return;
    }
    const assetUrl = props.overlayUrl;
    onOverlayStateRef.current?.({ status: 'loading', assetUrl });
    const controller = new AbortController();
    let active = true;
    const deadline = window.setTimeout(() => controller.abort('timeout'), 8_000);
    fetch(assetUrl, { method: 'HEAD', cache: 'force-cache', signal: controller.signal })
      .then((response) => {
        if (!active || generation !== overlayGeneration.current) return;
        if (response.status === 409) {
          onOverlayStateRef.current?.({ status: 'error', assetUrl, reason: 'stale-release' });
          return;
        }
        if (!response.ok) {
          onOverlayStateRef.current?.({ status: 'error', assetUrl, reason: 'load-failed' });
          return;
        }
        setVerifiedOverlay(assetUrl);
      })
      .catch((error: unknown) => {
        if (!active || generation !== overlayGeneration.current) return;
        const reason = controller.signal.aborted && controller.signal.reason === 'timeout' ? 'timeout' : 'load-failed';
        onOverlayStateRef.current?.({ status: 'error', assetUrl, reason });
      })
      .finally(() => window.clearTimeout(deadline));
    return () => {
      active = false;
      window.clearTimeout(deadline);
      controller.abort();
    };
  }, [props.overlayUrl, props.scalarOverlay]);
  const targetOverlay = props.scalarOverlay?.key ?? props.overlayUrl;
  const rendererProps = {
    ...props,
    overlayUrl: verifiedOverlay,
    targetOverlayUrl: props.overlayUrl,
    onRendererOverlayReady: (elapsedMs: number, renderer: 'maplibre' | 'canvas', assetUrl: string) => {
      if (assetUrl !== targetOverlay) return;
      onOverlayStateRef.current?.({ status: 'ready', assetUrl });
      props.onOverlayReady?.(elapsedMs, renderer, assetUrl);
    },
    onRendererOverlayError: (assetUrl: string, reason: 'load-failed' | 'timeout') => {
      if (assetUrl !== targetOverlay) return;
      setVerifiedOverlay(undefined);
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
        : <CanvasFallback
            scalarOverlay={props.scalarOverlay}
            worldwide={props.worldwide}
            bounds={props.bounds ?? DEFAULT_BOUNDS}
            selectedPoint={props.selectedPoint}
            focusPoint={props.focusPoint}
            command={command}
            onSelect={props.onSelect}
            onViewportChange={props.onViewportChange}
            onReady={props.onReady}
            onOverlayReady={(elapsedMs, nextRenderer, assetUrl) => rendererProps.onRendererOverlayReady(elapsedMs, nextRenderer, assetUrl)}
            onOverlayError={rendererProps.onRendererOverlayError}
          />}
    </div>
  );
}

type MapFailureReason = RendererFailureReason;

type RendererProps = Props & {
  targetOverlayUrl?: string;
  onRendererOverlayReady: (elapsedMs: number, renderer: 'maplibre' | 'canvas', assetUrl: string) => void;
  onRendererOverlayError: (assetUrl: string, reason: 'load-failed' | 'timeout') => void;
};

function MapLibreMap({ overlayUrl, targetOverlayUrl, scalarOverlay, worldwide = false, windGrid, bounds = DEFAULT_BOUNDS, selectedPoint, focusPoint, onSelect, onViewportChange, onReady, onRendererOverlayReady, onRendererOverlayError, command, onFailure }: RendererProps & { command?: MapCommand; onFailure: (reason: MapFailureReason) => void }) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const mapRef = useRef<Map | null>(null);
  const markerRef = useRef<maplibregl.Marker | null>(null);
  const onSelectRef = useRef(onSelect);
  const loadedRef = useRef(false);
  const boundsRef = useRef(bounds);
  const onReadyRef = useRef(onReady);
  const onViewportChangeRef = useRef(onViewportChange);
  const onOverlayReadyRef = useRef(onRendererOverlayReady);
  const onOverlayErrorRef = useRef(onRendererOverlayError);
  const overlayGeneration = useRef(0);
  const [styleLoaded, setStyleLoaded] = useState(false);
  const [mapLoaded, setMapLoaded] = useState(false);
  onSelectRef.current = onSelect;
  boundsRef.current = bounds;
  onReadyRef.current = onReady;
  onViewportChangeRef.current = onViewportChange;
  onOverlayReadyRef.current = onRendererOverlayReady;
  onOverlayErrorRef.current = onRendererOverlayError;

  useEffect(() => {
    if (!containerRef.current || mapRef.current) return;
    const startedAt = performance.now();
    let map: Map;
    try {
      map = new maplibregl.Map({
        container: containerRef.current,
        style: 'https://tiles.openfreemap.org/styles/positron',
        center: [9.5, 48.5],
        zoom: window.matchMedia('(max-width: 680px)').matches ? 3.15 : 4,
        minZoom: 2.5,
        maxZoom: 9,
        maxBounds: worldwide ? undefined : EUROPE_MAX_BOUNDS,
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
    const resizeObserver = new ResizeObserver(() => {
      window.cancelAnimationFrame(resizeFrame);
      resizeFrame = window.requestAnimationFrame(() => map.resize());
    });
    resizeObserver.observe(containerRef.current);
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
      const currentBounds = boundsRef.current;
      const compact = window.matchMedia('(max-width: 680px)').matches;
      map.fitBounds([[currentBounds[0], currentBounds[1]], [currentBounds[2], currentBounds[3]]], {
        padding: { top: 72, right: compact ? 24 : 42, bottom: compact ? 250 : 190, left: compact ? 24 : 42 },
        animate: false,
      });
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
      const sourceID = (event as maplibregl.ErrorEvent & { sourceId?: string }).sourceId;
      if (sourceID === 'weather-score-image' || /\/releases\/[^/]+\/images\//.test(message)) return;
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

  useEffect(() => {
    const map = mapRef.current;
    if (!map || !styleLoaded) return;
    if (worldwide) {
      map.setMaxBounds(null);
      map.setMinZoom(0.5);
      map.jumpTo({ center: [0, 12], zoom: window.matchMedia('(max-width: 680px)').matches ? 0.55 : 1.35 });
      telemetry('map-coverage-changed', { coverage: 'worldwide' });
      return;
    }
    map.setMinZoom(2.5);
    map.setMaxBounds(EUROPE_MAX_BOUNDS);
    const compact = window.matchMedia('(max-width: 680px)').matches;
    map.fitBounds([[bounds[0], bounds[1]], [bounds[2], bounds[3]]], {
      padding: { top: 72, right: compact ? 24 : 42, bottom: compact ? 250 : 190, left: compact ? 24 : 42 },
      animate: false,
    });
    telemetry('map-coverage-changed', { coverage: 'europe' });
  }, [bounds, styleLoaded, worldwide]);

  useLayoutEffect(() => {
    const map = mapRef.current;
    if (!map || !styleLoaded) return;
    const generation = ++overlayGeneration.current;
    removeOverlay(map);
    if (!overlayUrl || overlayUrl !== targetOverlayUrl) return;
    const activeMap = map;
    const assetUrl = overlayUrl;
    const startedAt = performance.now();
    let reported = false;
    let sourceLoaded = false;
    const deadline = window.setTimeout(() => fail('timeout'), 8_000);
    const readyAfterRender = () => {
      if (reported || !sourceLoaded || generation !== overlayGeneration.current || !activeMap.isSourceLoaded('weather-score-image')) return;
      reported = true;
      cleanup();
      onOverlayReadyRef.current?.(Math.round(performance.now() - startedAt), 'maplibre', assetUrl);
    };
    const onSourceData = (event: MapSourceDataEvent) => {
      if (event.sourceId !== 'weather-score-image' || !activeMap.isSourceLoaded('weather-score-image') || sourceLoaded) return;
      sourceLoaded = true;
      activeMap.once('render', readyAfterRender);
      activeMap.triggerRepaint();
    };
    const onSourceError = (event: maplibregl.ErrorEvent) => {
      const sourceID = (event as maplibregl.ErrorEvent & { sourceId?: string }).sourceId;
      const message = event.error?.message ?? '';
      if (sourceID === 'weather-score-image' || message.includes(assetUrl)) fail('load-failed');
    };
    function fail(reason: 'load-failed' | 'timeout') {
      if (reported || generation !== overlayGeneration.current) return;
      reported = true;
      cleanup();
      withCurrentRenderer(mapRef.current, activeMap, removeOverlay);
      onOverlayErrorRef.current(assetUrl, reason);
    }
    const cleanup = () => {
      window.clearTimeout(deadline);
      activeMap.off('sourcedata', onSourceData);
      activeMap.off('render', readyAfterRender);
      activeMap.off('error', onSourceError);
    };
    activeMap.on('sourcedata', onSourceData);
    activeMap.on('error', onSourceError);
    try {
      installOverlay(activeMap, assetUrl, bounds);
    } catch {
      fail('load-failed');
    }
    return cleanup;
  }, [bounds, overlayUrl, styleLoaded, targetOverlayUrl]);

  useLayoutEffect(() => {
    const map = mapRef.current;
    if (!map || !styleLoaded) return;
    const activeMap = map;
    removeScalarOverlay(activeMap);
    if (!scalarOverlay) return;
    const startedAt = performance.now();
    const key = scalarOverlay.key;
    let reported = false;
    const deadline = window.setTimeout(() => fail('timeout'), 8_000);
    const ready = (visible: boolean) => {
      if (!visible) {
        fail('load-failed');
        return;
      }
      if (reported) return;
      reported = true;
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
      sourceZoomForMap: scalarOverlay.sourceZoomForMap,
      visibleTilesForMap: scalarOverlay.visibleTilesForMap,
      sourceForTile: scalarOverlay.sourceForTile,
      onError: () => fail('load-failed'),
      onContractError: () => fail('load-failed'),
      onRenderProof: ready,
    });
    function fail(reason: 'load-failed' | 'timeout') {
      if (reported) return;
      reported = true;
      window.clearTimeout(deadline);
      withCurrentRenderer(mapRef.current, activeMap, removeScalarOverlay);
      onOverlayErrorRef.current(key, reason);
    }
    try {
      const layers = activeMap.getStyle().layers ?? [];
      activeMap.addLayer(layer, scoreOverlayBefore(layers));
    } catch {
      fail('load-failed');
    }
    return () => {
      window.clearTimeout(deadline);
      withCurrentRenderer(mapRef.current, activeMap, removeScalarOverlay);
    };
  }, [scalarOverlay, styleLoaded]);

  useLayoutEffect(() => {
    const map = mapRef.current;
    if (!map || !styleLoaded) return;
    removeWindDirection(map);
    if (!windGrid) return;
    if (!map.hasImage('weather-wind-arrow')) map.addImage('weather-wind-arrow', windArrowImage(), { pixelRatio: 2 });
    map.addSource('weather-wind-direction', { type: 'geojson', data: { type: 'FeatureCollection', features: [] } });
    const layers = map.getStyle().layers ?? [];
    map.addLayer({
      id: 'weather-wind-direction', type: 'symbol', source: 'weather-wind-direction',
      layout: windLayerLayout(),
      paint: { 'icon-opacity': 0.78 },
    }, baseOverlayBefore(layers));
    const update = () => {
      const visible = map.getBounds();
      const data = windFeatures(windGrid, { west: visible.getWest(), east: visible.getEast(), south: visible.getSouth(), north: visible.getNorth() }, (coordinate) => map.project(coordinate));
      (map.getSource('weather-wind-direction') as GeoJSONSource | undefined)?.setData(data);
    };
    update();
    map.on('moveend', update);
    telemetry('wind-frame-rendered', { validAt: windGrid.validAt, rows: windGrid.rows, cols: windGrid.cols });
    return () => {
      map.off('moveend', update);
      withCurrentRenderer(mapRef.current, map, removeWindDirection);
    };
  }, [styleLoaded, windGrid]);

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
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) mapRef.current.jumpTo({ center: [focusPoint.lon, focusPoint.lat], zoom: 7 });
    else mapRef.current.flyTo({ center: [focusPoint.lon, focusPoint.lat], zoom: 7, duration: 650, essential: false });
  }, [focusPoint, styleLoaded]);

  useEffect(() => {
    const map = mapRef.current;
    if (!map || !styleLoaded || !command) return;
    if (command.type === 'select') {
      const center = map.getCenter();
      onSelectRef.current({ lat: center.lat, lon: center.lng }, { focusDetails: true });
    } else if (command.type === 'zoom-in') map.zoomIn({ duration: 0 });
    else if (command.type === 'zoom-out') map.zoomOut({ duration: 0 });
    else map.panBy([command.type === 'left' ? -80 : command.type === 'right' ? 80 : 0, command.type === 'up' ? -80 : command.type === 'down' ? 80 : 0], { duration: 0 });
  }, [command, styleLoaded]);

  return <><div ref={containerRef} className="maplibreSurface" aria-hidden="true" />{!mapLoaded && <div className="mapPending" aria-hidden="true"><span>Loading map</span></div>}</>;
}

function installOverlay(map: Map, url: string, bounds: [number, number, number, number]) {
  const sourceId = 'weather-score-image';
  map.addSource(sourceId, {
    type: 'image',
    url,
    coordinates: [[bounds[0], bounds[3]], [bounds[2], bounds[3]], [bounds[2], bounds[1]], [bounds[0], bounds[1]]],
  });
  // Keep administrative boundaries and labels above the score field without
  // pulling the basemap's complete road network above it at larger screens.
  const layers = map.getStyle().layers ?? [];
  const beforeId = scoreOverlayBefore(layers);
  map.addLayer({ id: 'weather-score', type: 'raster', source: sourceId, paint: { 'raster-opacity': 1, 'raster-resampling': 'linear' } }, beforeId);
}

function removeWindDirection(map: Map) {
  if (map.getLayer('weather-wind-direction')) map.removeLayer('weather-wind-direction');
  if (map.getSource('weather-wind-direction')) map.removeSource('weather-wind-direction');
}

function removeScalarOverlay(map: Map) {
  if (map.getLayer(SCALAR_LAYER_ID)) map.removeLayer(SCALAR_LAYER_ID);
}

function windArrowImage(): ImageData {
  const canvas = document.createElement('canvas');
  canvas.width = 36; canvas.height = 36;
  const context = canvas.getContext('2d');
  if (!context) throw new Error('Wind direction marker could not be drawn.');
  context.lineCap = 'round'; context.lineJoin = 'round';
  const path = () => { context.beginPath(); context.moveTo(18, 29); context.lineTo(18, 8); context.moveTo(10, 16); context.lineTo(18, 8); context.lineTo(26, 16); };
  path(); context.strokeStyle = 'rgba(255,255,255,.9)'; context.lineWidth = 6; context.stroke();
  path(); context.strokeStyle = 'rgba(24,31,39,.88)'; context.lineWidth = 2.5; context.stroke();
  return context.getImageData(0, 0, 36, 36);
}

function removeOverlay(map: Map) {
  if (map.getLayer('weather-score')) map.removeLayer('weather-score');
  if (map.getSource('weather-score-image')) map.removeSource('weather-score-image');
}
