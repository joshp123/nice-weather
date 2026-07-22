import { useCallback, useEffect, useLayoutEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type WheelEvent as ReactWheelEvent } from 'react';
import { readScalarResponse, sha256Hex, type GeographicBounds, type ScalarPaletteColour, type ScalarTileID, type ScalarTileSource, type TileDraw } from './scalarTileLayer';
import type { MapPoint, ScalarOverlay } from './WeatherMap';
import { WORLD_LAND_ASSET } from './worldLandAsset';
import styles from './CanvasFallback.module.css';

const MAP_TILE_SIZE = 512;
const MERCATOR_LIMIT = 85.0511287798066;
const SCORE_TILE_LIMIT = 64;

type MapCommand = { type: 'select' | 'left' | 'right' | 'up' | 'down' | 'zoom-in' | 'zoom-out'; id: number };

type Props = Readonly<{
  scalarOverlay?: ScalarOverlay;
  worldwide?: boolean;
  bounds: [number, number, number, number];
  selectedPoint?: MapPoint;
  focusPoint?: MapPoint & { token: number };
  command?: MapCommand;
  onSelect: (point: MapPoint, intent?: { focusDetails?: boolean }) => void;
  onViewportChange?: (viewport: { latitude: number; longitude: number; zoom: number; bounds: GeographicBounds }) => void;
  onReady?: (elapsedMs: number) => void;
  onOverlayReady: (elapsedMs: number, renderer: 'canvas', assetUrl: string) => void;
  onOverlayError: (assetUrl: string, reason: 'load-failed' | 'timeout') => void;
}>;

type ViewState = { longitude: number; latitude: number; zoom: number };
type LoadedScore = { canvas: HTMLCanvasElement; textureBounds: readonly [number, number, number, number] };

export function CanvasFallback({ scalarOverlay, worldwide = false, bounds, selectedPoint, focusPoint, command, onSelect, onViewportChange, onReady, onOverlayReady, onOverlayError }: Props) {
  const surfaceRef = useRef<HTMLDivElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const viewRef = useRef<ViewState>(worldwide ? { longitude: 0, latitude: 12, zoom: compactZoom() } : fittedView(bounds));
  const dragRef = useRef<{ pointer: number; x: number; y: number; moved: boolean } | undefined>(undefined);
  const frameRef = useRef(0);
  const renderGeneration = useRef(0);
  const drawController = useRef<AbortController | undefined>(undefined);
  const readyKey = useRef<string | undefined>(undefined);
  const failedKey = useRef<string | undefined>(undefined);
  const overlayClock = useRef<{ key: string; startedAt: number } | undefined>(undefined);
  const startedAt = useRef(performance.now());
  const landImage = useRef<Promise<ImageBitmap> | undefined>(undefined);
  const loadedLand = useRef<ImageBitmap | undefined>(undefined);
  const scoreCache = useRef(new Map<string, LoadedScore>());
  const readyReported = useRef(false);
  const onSelectRef = useRef(onSelect);
  const onViewportChangeRef = useRef(onViewportChange);
  const onReadyRef = useRef(onReady);
  const onOverlayReadyRef = useRef(onOverlayReady);
  const onOverlayErrorRef = useRef(onOverlayError);
  const [marker, setMarker] = useState<{ x: number; y: number; visible: boolean }>();
  onSelectRef.current = onSelect;
  onViewportChangeRef.current = onViewportChange;
  onReadyRef.current = onReady;
  onOverlayReadyRef.current = onOverlayReady;
  onOverlayErrorRef.current = onOverlayError;

  const reportViewport = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const view = viewRef.current;
    const northWest = screenToGeo(0, 0, canvas.clientWidth, canvas.clientHeight, view);
    const southEast = screenToGeo(canvas.clientWidth, canvas.clientHeight, canvas.clientWidth, canvas.clientHeight, view);
    onViewportChangeRef.current?.({
      latitude: view.latitude,
      longitude: view.longitude,
      zoom: view.zoom,
      bounds: { west: northWest.lon, east: southEast.lon, north: northWest.lat, south: southEast.lat },
    });
  }, []);

  const scheduleDraw = useCallback(() => {
    cancelAnimationFrame(frameRef.current);
    frameRef.current = requestAnimationFrame(() => {
      void draw();
    });

    async function draw() {
      const canvas = canvasRef.current;
      if (!canvas) return;
      drawController.current?.abort();
      const controller = new AbortController();
      drawController.current = controller;
      const context = canvas.getContext('2d');
      if (!context) return;
      const ratio = Math.min(2, window.devicePixelRatio || 1);
      const width = Math.max(1, Math.round(canvas.clientWidth * ratio));
      const height = Math.max(1, Math.round(canvas.clientHeight * ratio));
      if (canvas.width !== width || canvas.height !== height) { canvas.width = width; canvas.height = height; }
      context.setTransform(ratio, 0, 0, ratio, 0, 0);
      context.fillStyle = '#dce6e9';
      context.fillRect(0, 0, canvas.clientWidth, canvas.clientHeight);
      const generation = ++renderGeneration.current;
      const view = viewRef.current;
      const viewport = viewportBounds(canvas.clientWidth, canvas.clientHeight, view);
      if (!landImage.current) {
        landImage.current = loadLandImage();
      }
      const baseImage = await settled(landImage.current);
      if (controller.signal.aborted || generation !== renderGeneration.current) return;
      if (baseImage) {
        loadedLand.current = baseImage;
        drawLand(context, baseImage, view, canvas.clientWidth, canvas.clientHeight);
      }
      if (!readyReported.current) {
        readyReported.current = true;
        onReadyRef.current?.(Math.round(performance.now() - startedAt.current));
      }

      if (scalarOverlay) {
        if (overlayClock.current?.key !== scalarOverlay.key) {
          overlayClock.current = { key: scalarOverlay.key, startedAt: performance.now() };
          readyKey.current = undefined;
          failedKey.current = undefined;
        }
        const scoreTiles = scalarOverlay.visibleTilesForMap
          ? scalarOverlay.visibleTilesForMap(viewport, view.zoom)
          : scalarOverlay.sourceZoomForMap
            ? visibleTiles(viewport, scalarOverlay.sourceZoomForMap(view.zoom))
            : visibleTiles(viewport, Math.max(scalarOverlay.minSourceZoom, Math.min(scalarOverlay.maxSourceZoom, Math.floor(view.zoom))));
        const scoreRequests = scoreTiles.flatMap((tile) => {
          const source = scalarOverlay.sourceForTile?.(tile.id) ?? (scalarOverlay.tileAvailable?.(tile.id) === false ? undefined : {
            key: `${scalarOverlay.key}:${tile.id.z}/${tile.id.x}/${tile.id.y}`,
            url: scalarOverlay.urlForTile(tile.id),
            sha256: scalarOverlay.sha256ForTile(tile.id),
          });
          if (!source) return [];
          return [{ tile, source, cacheKey: scalarCacheKey(scalarOverlay.key, source) }];
        });
        if (scoreRequests.length === 0) {
          failOverlay(scalarOverlay.key);
          return;
        }
        const missing = [...new Map(scoreRequests.filter(({ cacheKey }) => !scoreCache.current.has(cacheKey)).map(({ cacheKey, source }) => [cacheKey, { cacheKey, source }])).values()];
        let loaded: { cacheKey: string; tile: LoadedScore }[];
        try {
          loaded = await runBounded(missing, 6, controller.signal, async ({ cacheKey, source }) => ({ cacheKey, tile: await loadScoreTile(source, scalarOverlay.palette, controller.signal) }));
        } catch (error) {
          if (controller.signal.aborted) return;
          controller.abort(error);
          failOverlay(scalarOverlay.key);
          return;
        }
        if (controller.signal.aborted || generation !== renderGeneration.current) return;
        const drawScores = mergeDrawCache(
          scoreCache.current,
          scoreRequests.map(({ cacheKey }) => cacheKey),
          loaded.map(({ cacheKey, tile }) => [cacheKey, tile] as const),
          SCORE_TILE_LIMIT,
        );
        let visibleScores = 0;
        for (const request of scoreRequests) {
          const score = drawScores.get(request.cacheKey);
          if (!score) {
            failOverlay(scalarOverlay.key);
            return;
          }
          drawScoreTile(context, score, request.tile, view, canvas.clientWidth, canvas.clientHeight);
          visibleScores += 1;
        }
        if (visibleScores > 0 && readyKey.current !== scalarOverlay.key) {
          readyKey.current = scalarOverlay.key;
          onOverlayReadyRef.current(Math.round(performance.now() - (overlayClock.current?.startedAt ?? performance.now())), 'canvas', scalarOverlay.key);
        }
      }
      updateMarker(selectedPoint, view, canvas.clientWidth, canvas.clientHeight, setMarker);

      function failOverlay(key: string) {
        if (failedKey.current === key) return;
        failedKey.current = key;
        onOverlayErrorRef.current(key, 'load-failed');
      }
    }
  }, [scalarOverlay, selectedPoint]);

  useLayoutEffect(() => {
    const surface = surfaceRef.current;
    if (!surface) return;
    const observer = new ResizeObserver(() => { scheduleDraw(); reportViewport(); });
    observer.observe(surface);
    scheduleDraw();
    reportViewport();
    return () => {
      observer.disconnect();
      cancelAnimationFrame(frameRef.current);
      drawController.current?.abort();
    };
  }, [reportViewport, scheduleDraw]);

  useEffect(() => () => {
    drawController.current?.abort();
    const loaded = loadedLand.current;
    if (loaded) loaded.close();
    else void landImage.current?.then((image) => image.close()).catch(() => undefined);
    loadedLand.current = undefined;
  }, []);

  useEffect(() => {
    readyKey.current = undefined;
    failedKey.current = undefined;
    overlayClock.current = scalarOverlay ? { key: scalarOverlay.key, startedAt: performance.now() } : undefined;
    scheduleDraw();
  }, [scalarOverlay, scheduleDraw]);

  useEffect(() => {
    if (!focusPoint) return;
    viewRef.current = { longitude: focusPoint.lon, latitude: focusPoint.lat, zoom: 7 };
    scheduleDraw();
    reportViewport();
  }, [focusPoint, reportViewport, scheduleDraw]);

  useEffect(() => {
    if (!command) return;
    const canvas = canvasRef.current;
    if (!canvas) return;
    const view = viewRef.current;
    if (command.type === 'select') onSelectRef.current({ lat: view.latitude, lon: view.longitude }, { focusDetails: true });
    else if (command.type === 'zoom-in') view.zoom = Math.min(9, view.zoom + 1);
    else if (command.type === 'zoom-out') view.zoom = Math.max(.5, view.zoom - 1);
    else panView(view, command.type === 'left' ? 80 : command.type === 'right' ? -80 : 0, command.type === 'up' ? 80 : command.type === 'down' ? -80 : 0);
    scheduleDraw(); reportViewport();
  }, [command, reportViewport, scheduleDraw]);

  const zoom = (delta: number) => {
    viewRef.current.zoom = Math.max(.5, Math.min(9, viewRef.current.zoom + delta));
    scheduleDraw(); reportViewport();
  };
  const pointerDown = (event: ReactPointerEvent) => {
    event.currentTarget.setPointerCapture(event.pointerId);
    dragRef.current = { pointer: event.pointerId, x: event.clientX, y: event.clientY, moved: false };
  };
  const pointerMove = (event: ReactPointerEvent) => {
    const drag = dragRef.current;
    if (!drag || drag.pointer !== event.pointerId) return;
    const dx = event.clientX - drag.x, dy = event.clientY - drag.y;
    if (Math.abs(dx) + Math.abs(dy) > 2) drag.moved = true;
    drag.x = event.clientX; drag.y = event.clientY;
    panView(viewRef.current, dx, dy);
    scheduleDraw();
  };
  const pointerUp = (event: ReactPointerEvent) => {
    const drag = dragRef.current;
    dragRef.current = undefined;
    if (!drag?.moved && canvasRef.current) {
      const rect = canvasRef.current.getBoundingClientRect();
      const point = screenToGeo(event.clientX - rect.left, event.clientY - rect.top, rect.width, rect.height, viewRef.current);
      onSelectRef.current({ lat: point.lat, lon: point.lon });
    }
    reportViewport();
  };
  const wheel = (event: ReactWheelEvent) => { event.preventDefault(); zoom(event.deltaY < 0 ? .5 : -.5); };

  return <div ref={surfaceRef} className={styles.surface} data-renderer="canvas" onPointerDown={pointerDown} onPointerMove={pointerMove} onPointerUp={pointerUp} onPointerCancel={() => { dragRef.current = undefined; }} onWheel={wheel}>
    <canvas ref={canvasRef} className={styles.canvas} aria-hidden="true" />
    {marker?.visible && <span className={styles.marker} style={{ left: marker.x, top: marker.y }} />}
    <div
      className={styles.controls}
      aria-label="Map zoom controls"
      onPointerDown={(event) => event.stopPropagation()}
      onPointerUp={(event) => event.stopPropagation()}
      onPointerCancel={(event) => event.stopPropagation()}
    >
      <button type="button" aria-label="Zoom in" onClick={() => zoom(1)}>+</button>
      <button type="button" aria-label="Zoom out" onClick={() => zoom(-1)}>−</button>
    </div>
    <p className={styles.note}>Simple map · Natural Earth</p>
  </div>;
}

async function loadLandImage(): Promise<ImageBitmap> {
  const response = await fetch(WORLD_LAND_ASSET.url, { cache: 'force-cache', credentials: 'omit' });
  if (!response.ok) throw new Error(`Land map returned HTTP ${response.status}`);
  return createImageBitmap(await response.blob());
}

async function loadScoreTile(source: ScalarTileSource, palette: readonly ScalarPaletteColour[], signal: AbortSignal): Promise<LoadedScore> {
  const response = await fetch(source.url, { cache: 'force-cache', credentials: 'omit', signal });
  const scores = await readScalarResponse(response);
  if (await sha256Hex(scores) !== source.sha256) throw new Error('Score tile SHA-256 does not match its index.');
  const canvas = document.createElement('canvas');
  canvas.width = 258; canvas.height = 258;
  const context = canvas.getContext('2d');
  if (!context) throw new Error('Canvas score rendering is unavailable.');
  const image = context.createImageData(258, 258);
  image.data.set(scorePixels(scores, palette));
  context.putImageData(image, 0, 0);
  return { canvas, textureBounds: source.textureBounds ?? [0, 0, 1, 1] };
}

export function scorePixels(scores: Uint8Array, palette: readonly ScalarPaletteColour[]): Uint8ClampedArray {
  const pixels = new Uint8ClampedArray(scores.length * 4);
  scores.forEach((score, index) => {
    if (score > 100) return;
    const colour = palette[score];
    pixels[index * 4] = colour[0]; pixels[index * 4 + 1] = colour[1]; pixels[index * 4 + 2] = colour[2]; pixels[index * 4 + 3] = Math.round(colour[3] * .82);
  });
  return pixels;
}

export function scalarCacheKey(overlayKey: string, source: ScalarTileSource): string {
  return `${overlayKey}:${source.key}:${source.url}:${source.sha256}`;
}

export function mergeDrawCache<K, V>(cache: Map<K, V>, requested: readonly K[], loaded: readonly (readonly [K, V])[], limit: number): Map<K, V> {
  const draw = new Map<K, V>();
  for (const key of requested) {
    if (cache.has(key)) draw.set(key, cache.get(key)!);
  }
  for (const [key, value] of loaded) {
    draw.set(key, value);
    insertBounded(cache, key, value, limit);
  }
  return draw;
}

export async function runBounded<T, R>(items: readonly T[], limit: number, signal: AbortSignal, worker: (item: T) => Promise<R>): Promise<R[]> {
  if (!Number.isInteger(limit) || limit < 1) throw new TypeError('Request limit must be positive.');
  const results = new Array<R>(items.length);
  let next = 0;
  const run = async () => {
    while (next < items.length) {
      if (signal.aborted) throw signal.reason;
      const index = next++;
      results[index] = await worker(items[index]);
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, run));
  return results;
}

function drawScoreTile(context: CanvasRenderingContext2D, loaded: LoadedScore, tile: TileDraw, view: ViewState, width: number, height: number) {
  const position = tileScreenPosition(tile, view, width, height);
  const [west, north, east, south] = loaded.textureBounds;
  context.imageSmoothingEnabled = true;
  context.drawImage(loaded.canvas, 1 + west * 256, 1 + north * 256, (east - west) * 256, (south - north) * 256, position.x, position.y, position.size, position.size);
}

function drawLand(context: CanvasRenderingContext2D, image: CanvasImageSource, view: ViewState, width: number, height: number) {
  const worldTiles = 2 ** view.zoom;
  const worldPixels = worldTiles * MAP_TILE_SIZE;
  const centre = geoToWorld(view.longitude, view.latitude, view.zoom);
  const firstWorld = Math.floor((centre.x * MAP_TILE_SIZE - width / 2) / worldPixels);
  const lastWorld = Math.ceil((centre.x * MAP_TILE_SIZE + width / 2) / worldPixels);
  const y = height / 2 - centre.y * MAP_TILE_SIZE;
  for (let world = firstWorld; world <= lastWorld; world += 1) {
    const x = width / 2 + (world * worldTiles - centre.x) * MAP_TILE_SIZE;
    context.drawImage(image, x, y, worldPixels, worldPixels);
  }
}

function tileScreenPosition(tile: TileDraw, view: ViewState, width: number, height: number) {
  const count = 2 ** tile.id.z;
  const scale = 2 ** (view.zoom - tile.id.z);
  const size = MAP_TILE_SIZE * scale;
  const centre = geoToWorld(view.longitude, view.latitude, tile.id.z);
  return { x: width / 2 + (tile.worldX - centre.x) * size, y: height / 2 + (tile.id.y - centre.y) * size, size };
}

function visibleTiles(bounds: GeographicBounds, zoom: number): TileDraw[] {
  const count = 2 ** zoom;
  const west = (bounds.west + 180) / 360 * count;
  let east = (bounds.east + 180) / 360 * count;
  while (east <= west) east += count;
  const yNorth = geoToWorld(0, bounds.north, zoom).y;
  const ySouth = geoToWorld(0, bounds.south, zoom).y;
  const result: TileDraw[] = [];
  for (let y = Math.max(0, Math.floor(yNorth)); y <= Math.min(count - 1, Math.ceil(ySouth) - 1); y += 1) {
    for (let worldX = Math.floor(west); worldX <= Math.ceil(east) - 1; worldX += 1) result.push({ id: { z: zoom, x: modulo(worldX, count), y }, worldX });
  }
  return result;
}

export function viewportBounds(width: number, height: number, view: ViewState): GeographicBounds {
  const northWest = screenToGeo(0, 0, width, height, view);
  const southEast = screenToGeo(width, height, width, height, view);
  return { west: northWest.lon, east: southEast.lon, north: northWest.lat, south: southEast.lat };
}

export function screenToGeo(x: number, y: number, width: number, height: number, view: ViewState) {
  const count = 2 ** view.zoom;
  const centre = geoToWorld(view.longitude, view.latitude, view.zoom);
  return worldToGeo(centre.x + (x - width / 2) / MAP_TILE_SIZE, centre.y + (y - height / 2) / MAP_TILE_SIZE, count);
}

function geoToWorld(lon: number, lat: number, zoom: number) {
  const count = 2 ** zoom;
  const safeLat = Math.max(-MERCATOR_LIMIT, Math.min(MERCATOR_LIMIT, lat));
  const radians = safeLat * Math.PI / 180;
  return { x: (lon + 180) / 360 * count, y: (0.5 - Math.asinh(Math.tan(radians)) / (2 * Math.PI)) * count };
}

function worldToGeo(x: number, y: number, count: number) {
  const lon = x / count * 360 - 180;
  const lat = Math.atan(Math.sinh(Math.PI * (1 - 2 * y / count))) * 180 / Math.PI;
  return { lon, lat: Math.max(-MERCATOR_LIMIT, Math.min(MERCATOR_LIMIT, lat)) };
}

function panView(view: ViewState, dx: number, dy: number) {
  const count = 2 ** view.zoom;
  const centre = geoToWorld(view.longitude, view.latitude, view.zoom);
  const next = worldToGeo(centre.x - dx / MAP_TILE_SIZE, centre.y - dy / MAP_TILE_SIZE, count);
  view.longitude = next.lon; view.latitude = next.lat;
}

function updateMarker(point: MapPoint | undefined, view: ViewState, width: number, height: number, update: (marker: { x: number; y: number; visible: boolean }) => void) {
  if (!point) { update({ x: 0, y: 0, visible: false }); return; }
  const count = 2 ** view.zoom;
  const centre = geoToWorld(view.longitude, view.latitude, view.zoom);
  const target = geoToWorld(point.lon, point.lat, view.zoom);
  let dx = target.x - centre.x;
  if (dx > count / 2) dx -= count;
  if (dx < -count / 2) dx += count;
  const x = width / 2 + dx * MAP_TILE_SIZE, y = height / 2 + (target.y - centre.y) * MAP_TILE_SIZE;
  update({ x, y, visible: x >= 0 && x <= width && y >= 0 && y <= height });
}

function fittedView(bounds: [number, number, number, number]): ViewState {
  return { longitude: (bounds[0] + bounds[2]) / 2, latitude: (bounds[1] + bounds[3]) / 2, zoom: 3.2 };
}
function compactZoom() { return window.matchMedia('(max-width: 680px)').matches ? .55 : 1.35; }
function modulo(value: number, modulus: number) { return ((value % modulus) + modulus) % modulus; }
function insertBounded<K, V>(cache: Map<K, V>, key: K, value: V, limit: number) { cache.delete(key); cache.set(key, value); while (cache.size > limit) cache.delete(cache.keys().next().value!); }
async function settled<T>(promise: Promise<T>): Promise<T | undefined> { try { return await promise; } catch { return undefined; } }
