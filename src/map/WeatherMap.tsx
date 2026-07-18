import 'maplibre-gl/dist/maplibre-gl.css';
import maplibregl, { type Map, type MapSourceDataEvent } from 'maplibre-gl';
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { telemetry } from '../telemetry';
import { recordFailure } from './failureWindow';
import { projectWebMercator, reprojectGeographicPath, unprojectWebMercator } from './projection';

const DEFAULT_BOUNDS: [number, number, number, number] = [-25, 30, 45, 72];
const MAP_WIDTH = 700;
const MAP_HEIGHT = 420;

// Natural Earth 1:50m land, cropped to Europe and simplified for this display.
// The map stays useful without WebGL, external tiles or a network connection.
const LAND_PATH_GEOGRAPHIC = 'M700,45.6L699,45.9L700,46.2L700,420L149.8,420L157.5,394.3L190.8,362.1L230.9,369.1L346.9,346.6L361.3,351.3L353.1,382.7L439.4,417.1L466.4,390.6L540.7,411.7L594.8,404.1L611.8,351.9L525.4,353.2L511.8,320.1L583.8,299.8L664.1,305.8L664.2,292.6L616.3,268.5L642,247.3L602.3,255.6L613.9,269.3L589.1,276.1L567.6,247.9L524.8,295.3L539.6,309.9L476.3,315L475.7,331.3L490.2,338.6L477.3,344.6L481.6,355.5L461.2,341.1L481.5,338.2L461.8,336.5L444,317.2L445.8,302.1L386.3,262.3L372.7,265.5L374,277.8L434.6,317.8L419.3,315.4L410.6,340.6L406.9,320.1L337.7,275.8L282.6,288.1L228.9,352.2L193.7,359.7L181.4,347.2L160,349.7L158.2,288.3L230.1,286.5L244.5,270L232.6,247.8L202.8,234.6L254.2,225.5L305.3,187.3L347.8,184.5L331.3,164L336.2,148.9L356.1,142.6L348.7,175.3L395.8,183.6L388.3,178.7L461.9,170.6L467.3,144.3L493.8,147.5L484.9,128L551.7,120.4L464.4,114L465.5,88L503.7,69.9L474,61.4L423.8,95.4L422.5,113L439.9,121.7L412.1,133.6L410,157.8L378.9,165.9L356,122.4L331.7,138.5L305.9,133.8L314.2,124.5L302.4,124.4L320,114.9L301.5,123.6L300.1,109.6L320.4,110.5L326,107.9L299.1,101.9L363.7,82L345.7,82.9L432.6,25.3L497.6,9.9L507.8,11.8L500.4,18.9L531.4,9.6L559.6,16.6L537.8,18.5L546.9,22.6L659.7,42.9L661.9,51.7L636.5,59.3L594.8,54.5L579.3,49.1L569,48.4L624.4,81.9L618.8,68.3L647.6,74.2L672.1,54.8L691,59.9L683.3,33.3L700,34.5Z M208.7,144.2L232.2,145.3L212.1,159L266.6,192.5L254.2,205.3L264,208.2L193.8,219.5L225.7,202.6L198.3,202.6L221.5,178.6L199.7,172.4L204.2,160.6L192.7,166.7L198.1,152.4L188.7,152.9L199.8,134.3L218.9,134.8Z M114.3,69.3L63.5,85.9L23.5,81.7L34.1,73.7L9.7,71.4L31.6,65.5L5.2,64.7L94.6,57.7Z M195.3,175L186.8,197.5L146.6,202L162.2,193.2L150.8,194.3L160.7,187.9L149.4,177.4Z M507.9,368.8L513.2,366.8L511.7,369.8L498,370.7L488.5,364.6Z M580.1,374.3L573.2,370.5L579.4,366.1L594.6,364.1Z M345.6,328.3L336.5,330.7L332.2,310.9L346.3,311.2Z';

const PLACES = [
  ['Lisbon', -9.14, 38.72], ['Madrid', -3.7, 40.42], ['Valencia', -0.38, 39.47],
  ['London', -0.13, 51.51], ['Paris', 2.35, 48.86], ['Amsterdam', 4.9, 52.37],
  ['Marseille', 5.37, 43.3], ['Berlin', 13.41, 52.52], ['Rome', 12.5, 41.9],
  ['Copenhagen', 12.57, 55.68], ['Warsaw', 21.01, 52.23], ['Athens', 23.73, 37.98],
] as const;

export type MapPoint = { lat: number; lon: number; label?: string };
export type OverlayState =
  | { status: 'idle' }
  | { status: 'loading' | 'ready'; assetUrl: string }
  | { status: 'error'; assetUrl: string; reason: 'stale-release' | 'load-failed' | 'timeout' };
type ViewBox = { x: number; y: number; width: number; height: number };
type MapCommand = { type: 'select' | 'left' | 'right' | 'up' | 'down' | 'zoom-in' | 'zoom-out'; id: number };

type Props = {
  overlayUrl?: string;
  bounds?: [number, number, number, number];
  selectedPoint?: MapPoint;
  focusPoint?: MapPoint & { token: number };
  onSelect: (point: MapPoint, intent?: { focusDetails?: boolean }) => void;
  onReady?: (elapsedMs: number) => void;
  onOverlayReady?: (elapsedMs: number, renderer: 'maplibre' | 'fallback', assetUrl: string) => void;
  onOverlayState?: (state: OverlayState) => void;
};

export function WeatherMap(props: Props) {
  const [fallback, setFallback] = useState(false);
  const [command, setCommand] = useState<MapCommand | undefined>(undefined);
  const [verifiedOverlay, setVerifiedOverlay] = useState<string | undefined>();
  const overlayGeneration = useRef(0);
  const onOverlayStateRef = useRef(props.onOverlayState);
  onOverlayStateRef.current = props.onOverlayState;
  const handleFailure = useCallback(() => setFallback(true), []);
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
    onRendererOverlayReady: (elapsedMs: number, renderer: 'maplibre' | 'fallback', assetUrl: string) => {
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
      <FallbackMap {...rendererProps} command={command} interactive={fallback} />
      {!fallback && <MapLibreMap {...rendererProps} command={command} onFailure={handleFailure} />}
    </div>
  );
}

type RendererProps = Props & {
  targetOverlayUrl?: string;
  onRendererOverlayReady: (elapsedMs: number, renderer: 'maplibre' | 'fallback', assetUrl: string) => void;
  onRendererOverlayError: (assetUrl: string, reason: 'load-failed' | 'timeout') => void;
};

function FallbackMap({ overlayUrl, targetOverlayUrl, bounds = DEFAULT_BOUNDS, selectedPoint, focusPoint, onSelect, onReady, onRendererOverlayReady, onRendererOverlayError, command, interactive }: RendererProps & { command?: MapCommand; interactive: boolean }) {
  const svgRef = useRef<SVGSVGElement | null>(null);
  const dragRef = useRef<{ x: number; y: number; view: ViewBox; moved: boolean } | undefined>(undefined);
  const [view, setView] = useState<ViewBox>({ x: 0, y: 0, width: MAP_WIDTH, height: MAP_HEIGHT });
  const [loadedOverlay, setLoadedOverlay] = useState<string | undefined>(undefined);
  const reportedOverlay = useRef<string | undefined>(undefined);
  const landPath = useMemo(
    () => reprojectGeographicPath(LAND_PATH_GEOGRAPHIC, DEFAULT_BOUNDS, bounds, MAP_WIDTH, MAP_HEIGHT),
    [bounds],
  );

  useEffect(() => {
    if (!focusPoint) return;
    const point = project(focusPoint.lon, focusPoint.lat, bounds);
    setView((current) => clampView({ x: point.x - current.width / 4, y: point.y - current.height / 4, width: current.width / 2, height: current.height / 2 }));
  }, [bounds, focusPoint]);

  useEffect(() => {
    if (interactive) onReady?.(0);
  }, [interactive, onReady]);

  useLayoutEffect(() => {
    setLoadedOverlay(undefined);
  }, [targetOverlayUrl]);

  useEffect(() => {
    if (!interactive || !overlayUrl || loadedOverlay !== overlayUrl || reportedOverlay.current === overlayUrl) return;
    reportedOverlay.current = overlayUrl;
    onRendererOverlayReady(0, 'fallback', overlayUrl);
  }, [interactive, loadedOverlay, onRendererOverlayReady, overlayUrl]);

  useEffect(() => {
    if (!interactive || !overlayUrl || loadedOverlay === overlayUrl) return;
    const deadline = window.setTimeout(() => onRendererOverlayError(overlayUrl, 'timeout'), 8_000);
    return () => window.clearTimeout(deadline);
  }, [interactive, loadedOverlay, onRendererOverlayError, overlayUrl]);

  useEffect(() => {
    if (!interactive || !command) return;
    if (command.type === 'zoom-in') return zoom(0.72);
    if (command.type === 'zoom-out') return zoom(1.38);
    if (command.type === 'select') {
      onSelect(unproject(view.x + view.width / 2, view.y + view.height / 2, bounds), { focusDetails: true });
      return;
    }
    const step = view.width * 0.08;
    setView((current) => clampView({ ...current, x: current.x + (command.type === 'left' ? -step : command.type === 'right' ? step : 0), y: current.y + (command.type === 'up' ? -step : command.type === 'down' ? step : 0) }));
  }, [command]);

  function zoom(factor: number) {
    setView((current) => {
      const width = Math.max(220, Math.min(MAP_WIDTH, current.width * factor));
      const height = width * MAP_HEIGHT / MAP_WIDTH;
      return clampView({ x: current.x + (current.width - width) / 2, y: current.y + (current.height - height) / 2, width, height });
    });
  }

  function pointer(event: React.PointerEvent<SVGSVGElement>): { x: number; y: number } {
    const svg = svgRef.current;
    if (!svg) return { x: 0, y: 0 };
    const point = svg.createSVGPoint();
    point.x = event.clientX;
    point.y = event.clientY;
    const transformed = point.matrixTransform(svg.getScreenCTM()?.inverse());
    return { x: transformed.x, y: transformed.y };
  }

  return (
    <div className={interactive ? 'fallbackMap interactive' : 'fallbackMap'} aria-hidden={!interactive}>
      <svg
        ref={svgRef}
        className="mapSvg"
        viewBox={`${view.x} ${view.y} ${view.width} ${view.height}`}
        preserveAspectRatio="xMidYMid slice"
        onPointerDown={(event) => {
          event.currentTarget.setPointerCapture(event.pointerId);
          dragRef.current = { x: event.clientX, y: event.clientY, view, moved: false };
        }}
        onPointerMove={(event) => {
          const drag = dragRef.current;
          if (!drag) return;
          const scale = drag.view.width / event.currentTarget.getBoundingClientRect().width;
          const dx = (event.clientX - drag.x) * scale;
          const dy = (event.clientY - drag.y) * scale;
          if (Math.abs(dx) + Math.abs(dy) > 3) drag.moved = true;
          setView(clampView({ ...drag.view, x: drag.view.x - dx, y: drag.view.y - dy }));
        }}
        onPointerUp={(event) => {
          const drag = dragRef.current;
          dragRef.current = undefined;
          if (drag?.moved) return;
          const point = pointer(event);
          onSelect(unproject(point.x, point.y, bounds));
        }}
        onPointerCancel={() => { dragRef.current = undefined; }}
        onLostPointerCapture={() => { dragRef.current = undefined; }}
        onWheel={(event) => { event.preventDefault(); zoom(event.deltaY > 0 ? 1.18 : 0.84); }}
      >
        <defs>
          <linearGradient id="sea" x1="0" y1="0" x2="0" y2="1"><stop stopColor="#c9dedd" /><stop offset="1" stopColor="#b9d3d2" /></linearGradient>
          <clipPath id="land"><path d={landPath} /></clipPath>
          <filter id="coastShadow"><feDropShadow dx="0" dy="1" stdDeviation="1.8" floodColor="#496267" floodOpacity=".18" /></filter>
        </defs>
        <rect width={MAP_WIDTH} height={MAP_HEIGHT} fill="url(#sea)" />
        <g className="graticule">
          {[-20, -10, 0, 10, 20, 30, 40].filter((lon) => lon > bounds[0] && lon < bounds[2]).map((lon) => {
            const x = project(lon, bounds[1], bounds).x;
            return <line key={`lon${lon}`} x1={x} x2={x} y1="0" y2={MAP_HEIGHT} />;
          })}
          {[30, 35, 40, 45, 50, 55, 60, 65, 70].filter((lat) => lat > bounds[1] && lat < bounds[3]).map((lat) => {
            const y = project(bounds[0], lat, bounds).y;
            return <line key={`lat${lat}`} x1="0" x2={MAP_WIDTH} y1={y} y2={y} />;
          })}
        </g>
        <path className="land" d={landPath} filter="url(#coastShadow)" />
        {interactive && overlayUrl && <image key={overlayUrl} className="weatherOverlay" href={overlayUrl} opacity={loadedOverlay === overlayUrl ? 1 : 0} x="0" y="0" width={MAP_WIDTH} height={MAP_HEIGHT} preserveAspectRatio="none" clipPath="url(#land)" onLoad={() => setLoadedOverlay(overlayUrl)} onError={() => onRendererOverlayError(overlayUrl, 'load-failed')} />}
        <path className="coastline" d={landPath} />
        <g className="mapLabels" aria-hidden="true">
          {PLACES.map(([name, lon, lat]) => {
            const point = project(lon, lat, bounds);
            return <g key={name} transform={`translate(${point.x} ${point.y})`}><circle r="1.6" /><text x="4" y="-3">{name}</text></g>;
          })}
        </g>
        {selectedPoint && (() => {
          const point = project(selectedPoint.lon, selectedPoint.lat, bounds);
          return <g className="svgMarker" transform={`translate(${point.x} ${point.y})`}><circle r="8" /><circle r="3" /></g>;
        })()}
      </svg>
      {interactive && <div className="mapZoomControls" role="group" aria-label="Map zoom controls">
        <button type="button" onClick={() => zoom(0.72)} aria-label="Zoom in">+</button>
        <button type="button" onClick={() => zoom(1.38)} aria-label="Zoom out">−</button>
      </div>}
      <p className="mapAttribution">Natural Earth</p>
    </div>
  );
}

function MapLibreMap({ overlayUrl, targetOverlayUrl, bounds = DEFAULT_BOUNDS, selectedPoint, focusPoint, onSelect, onReady, onRendererOverlayReady, onRendererOverlayError, command, onFailure }: RendererProps & { command?: MapCommand; onFailure: () => void }) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const mapRef = useRef<Map | null>(null);
  const markerRef = useRef<maplibregl.Marker | null>(null);
  const onSelectRef = useRef(onSelect);
  const loadedRef = useRef(false);
  const boundsRef = useRef(bounds);
  const onReadyRef = useRef(onReady);
  const onOverlayReadyRef = useRef(onRendererOverlayReady);
  const onOverlayErrorRef = useRef(onRendererOverlayError);
  const overlayGeneration = useRef(0);
  const [styleLoaded, setStyleLoaded] = useState(false);
  onSelectRef.current = onSelect;
  boundsRef.current = bounds;
  onReadyRef.current = onReady;
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
      console.warn('[nice-weather] WebGL map unavailable; using the compatibility map.', error);
      onFailure();
      return;
    }
    mapRef.current = map;
    map.touchZoomRotate.disableRotation();
    map.getCanvas().tabIndex = -1;
    map.addControl(new maplibregl.NavigationControl({ showCompass: false }), 'top-right');
    map.addControl(new maplibregl.AttributionControl({ compact: true }), 'top-left');
    let baseMapFailures: number[] = [];
    const deadline = window.setTimeout(() => {
      if (!loadedRef.current) {
        console.warn('[nice-weather] WebGL map did not render in time; using the compatibility map.');
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
      window.clearTimeout(deadline);
      onReadyRef.current?.(Math.round(performance.now() - startedAt));
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
        console.warn('[nice-weather] Base map requests keep failing; using the compatibility map.');
        onFailure();
      }
    });
    map.getCanvas().addEventListener('webglcontextlost', onFailure, { once: true });
    return () => {
      window.clearTimeout(deadline);
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

  return <div ref={containerRef} className="maplibreSurface" aria-hidden="true" />;
}

function installOverlay(map: Map, url: string, bounds: [number, number, number, number]) {
  const sourceId = 'weather-score-image';
  map.addSource(sourceId, {
    type: 'image',
    url,
    coordinates: [[bounds[0], bounds[3]], [bounds[2], bounds[3]], [bounds[2], bounds[1]], [bounds[0], bounds[1]]],
  });
  const beforeId = map.getStyle().layers?.find((layer) => layer.type === 'symbol')?.id;
  map.addLayer({ id: 'weather-score', type: 'raster', source: sourceId, paint: { 'raster-opacity': 1 } }, beforeId);
}

function removeOverlay(map: Map) {
  if (map.getLayer('weather-score')) map.removeLayer('weather-score');
  if (map.getSource('weather-score-image')) map.removeSource('weather-score-image');
}

function project(lon: number, lat: number, bounds: [number, number, number, number]) {
  return projectWebMercator(lon, lat, bounds, MAP_WIDTH, MAP_HEIGHT);
}

function unproject(x: number, y: number, bounds: [number, number, number, number]): MapPoint {
  return unprojectWebMercator(x, y, bounds, MAP_WIDTH, MAP_HEIGHT);
}

function clampView(view: ViewBox): ViewBox {
  return {
    ...view,
    x: Math.max(0, Math.min(MAP_WIDTH - view.width, view.x)),
    y: Math.max(0, Math.min(MAP_HEIGHT - view.height, view.y)),
  };
}
