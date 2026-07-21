import 'maplibre-gl/dist/maplibre-gl.css';
import maplibregl, { type GeoJSONSource, type Map, type MapSourceDataEvent } from 'maplibre-gl';
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { WindGrid } from '../data/wind';
import { telemetry } from '../telemetry';
import { recordFailure } from './failureWindow';
import { baseOverlayBefore, scoreOverlayBefore, windFeatures, windLayerLayout } from './windLayer';

const DEFAULT_BOUNDS: [number, number, number, number] = [-25, 30, 45, 72];

export type MapPoint = { lat: number; lon: number; label?: string };
export type OverlayState =
  | { status: 'idle' }
  | { status: 'loading' | 'ready'; assetUrl: string }
  | { status: 'error'; assetUrl: string; reason: 'stale-release' | 'load-failed' | 'timeout' };
type MapCommand = { type: 'select' | 'left' | 'right' | 'up' | 'down' | 'zoom-in' | 'zoom-out'; id: number };

type Props = {
  overlayUrl?: string;
  windGrid?: WindGrid;
  bounds?: [number, number, number, number];
  selectedPoint?: MapPoint;
  focusPoint?: MapPoint & { token: number };
  onSelect: (point: MapPoint, intent?: { focusDetails?: boolean }) => void;
  onViewportChange?: (viewport: { latitude: number; longitude: number }) => void;
  onReady?: (elapsedMs: number) => void;
  onOverlayReady?: (elapsedMs: number, renderer: 'maplibre', assetUrl: string) => void;
  onOverlayState?: (state: OverlayState) => void;
};

export function WeatherMap(props: Props) {
  const [unavailable, setUnavailable] = useState(false);
  const [command, setCommand] = useState<MapCommand | undefined>(undefined);
  const [verifiedOverlay, setVerifiedOverlay] = useState<string | undefined>();
  const overlayGeneration = useRef(0);
  const onOverlayStateRef = useRef(props.onOverlayState);
  onOverlayStateRef.current = props.onOverlayState;
  const handleFailure = useCallback(() => setUnavailable(true), []);
  useLayoutEffect(() => {
    const generation = ++overlayGeneration.current;
    setVerifiedOverlay(undefined);
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
  }, [props.overlayUrl]);
  const rendererProps = {
    ...props,
    overlayUrl: verifiedOverlay,
    targetOverlayUrl: props.overlayUrl,
    onRendererOverlayReady: (elapsedMs: number, renderer: 'maplibre', assetUrl: string) => {
      if (assetUrl !== props.overlayUrl) return;
      onOverlayStateRef.current?.({ status: 'ready', assetUrl });
      props.onOverlayReady?.(elapsedMs, renderer, assetUrl);
    },
    onRendererOverlayError: (assetUrl: string, reason: 'load-failed' | 'timeout') => {
      if (assetUrl !== props.overlayUrl) return;
      setVerifiedOverlay(undefined);
      onOverlayStateRef.current?.({ status: 'error', assetUrl, reason });
    },
  };
  const keyboard = (event: React.KeyboardEvent<HTMLDivElement>) => {
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
      {unavailable
        ? <MapUnavailable />
        : <MapLibreMap {...rendererProps} command={command} onFailure={handleFailure} />}
    </div>
  );
}

function MapUnavailable() {
  return <div className="mapUnavailable" role="alert"><div><strong>The map is not available</strong><p>Nice Weather needs WebGL. Open this page in a current version of Safari or Chrome.</p></div></div>;
}

type RendererProps = Props & {
  targetOverlayUrl?: string;
  onRendererOverlayReady: (elapsedMs: number, renderer: 'maplibre', assetUrl: string) => void;
  onRendererOverlayError: (assetUrl: string, reason: 'load-failed' | 'timeout') => void;
};

function MapLibreMap({ overlayUrl, targetOverlayUrl, windGrid, bounds = DEFAULT_BOUNDS, selectedPoint, focusPoint, onSelect, onViewportChange, onReady, onRendererOverlayReady, onRendererOverlayError, command, onFailure }: RendererProps & { command?: MapCommand; onFailure: () => void }) {
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
        maxBounds: [[-36, 20], [58, 77]],
        attributionControl: false,
        dragRotate: false,
        pitchWithRotate: false,
        touchPitch: false,
        keyboard: false,
        fadeDuration: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 0 : 120,
      });
    } catch (error) {
      console.warn('[nice-weather] WebGL map unavailable.', error);
      onFailure();
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
    let baseMapFailures: number[] = [];
    const deadline = window.setTimeout(() => {
      if (!loadedRef.current) {
        console.warn('[nice-weather] WebGL map did not render in time.');
        onFailure();
      }
    }, 8_000);
    map.once('style.load', () => {
      const currentBounds = boundsRef.current;
      const compact = window.matchMedia('(max-width: 680px)').matches;
      map.fitBounds([[currentBounds[0], currentBounds[1]], [currentBounds[2], currentBounds[3]]], {
        padding: { top: 72, right: compact ? 24 : 42, bottom: compact ? 250 : 190, left: compact ? 24 : 42 },
        animate: false,
      });
      setStyleLoaded(true);
    });
    map.on('load', () => {
      loadedRef.current = true;
      setMapLoaded(true);
      window.clearTimeout(deadline);
      window.requestAnimationFrame(collapseInitialAttribution);
      const center = map.getCenter();
      onViewportChangeRef.current?.({ latitude: center.lat, longitude: center.lng });
      onReadyRef.current?.(Math.round(performance.now() - startedAt));
    });
    map.on('moveend', () => {
      const center = map.getCenter();
      onViewportChangeRef.current?.({ latitude: center.lat, longitude: center.lng });
    });
    map.on('click', (event) => onSelectRef.current({ lat: event.lngLat.lat, lon: event.lngLat.lng }));
    map.on('error', (event) => {
      const message = event.error?.message ?? 'Map rendering error';
      console.warn('[nice-weather] map', message);
      if (/shader|webgl|context/i.test(message)) {
        onFailure();
        return;
      }
      const sourceID = (event as maplibregl.ErrorEvent & { sourceId?: string }).sourceId;
      if (sourceID === 'weather-score-image' || /\/releases\/[^/]+\/images\//.test(message)) return;
      const failureWindow = recordFailure(baseMapFailures, performance.now());
      baseMapFailures = failureWindow.failures;
      if (failureWindow.sustained) {
        console.warn('[nice-weather] Base map requests keep failing; the map is unavailable.');
        onFailure();
      }
    });
    map.getCanvas().addEventListener('webglcontextlost', onFailure, { once: true });
    return () => {
      resizeObserver.disconnect();
      window.cancelAnimationFrame(resizeFrame);
      window.clearTimeout(deadline);
      window.clearTimeout(attributionDeadline);
      markerRef.current?.remove();
      map.remove();
      mapRef.current = null;
      loadedRef.current = false;
    };
  }, [onFailure]);

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
      removeOverlay(activeMap);
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
    return () => { map.off('moveend', update); removeWindDirection(map); };
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
