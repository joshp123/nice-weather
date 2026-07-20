import maplibregl from 'maplibre-gl';
import { ScalarTileCanaryLayer, type ScalarPaletteColour } from './scalarTileLayer';
import './canary.css';

type PaletteStop = Readonly<{ score: number; color: string }>;
type CanaryProbe = Readonly<{ name: string; url: string; offset: number; score: number }>;
type CanaryTile = Readonly<{ z: number; x: number; y: number; url: string; sha256: string }>;
type CanaryIndex = Readonly<{
  contract: 'nice-weather-scalar-canary-v2';
  releaseId: string;
  dataStatus: 'live' | 'sample';
  periodId: string;
  periodLabel: string;
  sourceLabel: string;
  generatedAt: string;
  minZoom: number;
  maxZoom: number;
  tileCount: number;
  decodedBytes: number;
  estimatedGzipBytes: number;
  tileTemplate: string;
  palette: readonly PaletteStop[];
  tiles: readonly CanaryTile[];
  probes: readonly CanaryProbe[];
}>;

const root = document.querySelector<HTMLElement>('#canary');
if (!root) throw new Error('Canary root is missing.');

void start(root).catch((cause: unknown) => {
  const message = cause instanceof Error ? cause.message : String(cause);
  root.className = 'canaryError';
  root.innerHTML = `<p><strong>The scalar map canary could not start.</strong><br>${escapeHTML(message)}</p>`;
  console.error('[scalar-canary]', cause);
});

async function start(container: HTMLElement): Promise<void> {
  const response = await fetch('/scalar-canary-index.json', { cache: 'no-store', credentials: 'omit' });
  if (!response.ok) throw new Error(`Canary index returned HTTP ${response.status}. Build the disposable scalar assets first.`);
  const index = await response.json() as CanaryIndex;
  validateIndex(index);

  container.className = 'canaryMap';
  const mapElement = document.createElement('div');
  mapElement.style.width = '100%';
  mapElement.style.height = '100%';
  const status = statusElement(index);
  container.append(mapElement, status);

  const map = new maplibregl.Map({
    container: mapElement,
    style: 'https://tiles.openfreemap.org/styles/positron',
    center: [9.5, 48.5],
    zoom: matchMedia('(max-width: 640px)').matches ? 3.15 : 4,
    minZoom: 1,
    maxZoom: 8,
    attributionControl: false,
    dragRotate: false,
    pitchWithRotate: false,
    touchPitch: false,
    fadeDuration: matchMedia('(prefers-reduced-motion: reduce)').matches ? 0 : 100,
  });
  map.touchZoomRotate.disableRotation();
  map.addControl(new maplibregl.NavigationControl({ showCompass: false }), 'top-right');
  map.addControl(new maplibregl.AttributionControl({ compact: true }), 'bottom-right');

  const failures: string[] = [];
  const availableTiles = new Set(index.tiles.map((tile) => `${tile.z}/${tile.x}/${tile.y}`));
  const tileDigests = new Map(index.tiles.map((tile) => [`${tile.z}/${tile.x}/${tile.y}`, tile.sha256]));
  const layer = new ScalarTileCanaryLayer({
    id: 'evaluated-score-canary',
    minSourceZoom: index.minZoom,
    maxSourceZoom: index.maxZoom,
    maxTextures: 64,
    maxRequests: 6,
    palette: expandPalette(index.palette),
    tileAvailable: ({ z, x, y }) => availableTiles.has(`${z}/${x}/${y}`),
    sha256ForTile: ({ z, x, y }) => tileDigests.get(`${z}/${x}/${y}`) ?? 'missing',
    urlForTile: ({ z, x, y }) => index.tileTemplate.replace('{z}', String(z)).replace('{x}', String(x)).replace('{y}', String(y)),
    onError: (tile, error) => {
      failures.push(`${tile.z}/${tile.x}/${tile.y}: ${error.message}`);
      updateStatus(status, layer, index, failures);
    },
    onContractError: (tile, error) => {
      failures.push(`${tile.z}/${tile.x}/${tile.y}: ${error.message}`);
      updateStatus(status, layer, index, failures);
    },
    onRenderProof: (visible) => {
      if (!visible) {
        failures.push('renderer: uploaded textures produced no visible fragments');
        const parity = status.querySelector<HTMLElement>('[data-field="parity"]');
        if (parity) {
          parity.textContent = 'Not run · no rendered pixels';
          parity.dataset.state = 'bad';
        }
      } else {
        void verifyProbes(index.probes).then((parity) => {
          const parityElement = status.querySelector<HTMLElement>('[data-field="parity"]');
          if (parityElement) {
            parityElement.textContent = parity.ok ? `${parity.checked}/${parity.checked} exact` : parity.message;
            parityElement.dataset.state = parity.ok ? 'good' : 'bad';
          }
        }).catch((cause: unknown) => {
          const parityElement = status.querySelector<HTMLElement>('[data-field="parity"]');
          if (parityElement) {
            parityElement.textContent = cause instanceof Error ? cause.message : String(cause);
            parityElement.dataset.state = 'bad';
          }
        });
      }
      updateStatus(status, layer, index, failures);
    },
  });

  map.once('load', () => {
    try {
      const firstLabel = map.getStyle().layers.find((candidate) => candidate.type === 'symbol')?.id;
      map.addLayer(layer, firstLabel);
      const renderer = status.querySelector<HTMLElement>('[data-field="renderer"]');
      if (renderer) {
        renderer.textContent = 'WebGL 2 installed · checking pixels';
      }
      updateStatus(status, layer, index, failures);
    } catch (cause) {
      const error = cause instanceof Error ? cause : new Error(String(cause));
      failures.push(`renderer: ${error.message}`);
      const renderer = status.querySelector<HTMLElement>('[data-field="renderer"]');
      if (renderer) {
        renderer.textContent = error.message;
        renderer.dataset.state = 'bad';
      }
      const parity = status.querySelector<HTMLElement>('[data-field="parity"]');
      if (parity) {
        parity.textContent = 'Not run · renderer failed';
        parity.dataset.state = 'bad';
      }
      updateStatus(status, layer, index, failures);
    }
  });
  map.on('idle', () => updateStatus(status, layer, index, failures));
  map.on('moveend', () => updateStatus(status, layer, index, failures));
}

function statusElement(index: CanaryIndex): HTMLElement {
  const element = document.createElement('aside');
  element.className = 'canaryStatus';
  element.innerHTML = `
    <h1>Scalar map canary</h1>
    <p>Local rendering proof. The colours are evaluated by Go, interpolated as scores, then mapped to the product palette.</p>
    <dl>
      <dt>Evidence</dt><dd>${escapeHTML(index.dataStatus === 'live' ? 'Live release' : 'Sample release')} · ${escapeHTML(index.periodLabel)}</dd>
      <dt>Source</dt><dd>${escapeHTML(index.sourceLabel)}</dd>
      <dt>Renderer</dt><dd data-field="renderer">Starting</dd>
      <dt>Tiles</dt><dd data-field="tiles">Loading</dd>
      <dt>Transfer</dt><dd data-field="transfer">Loading</dd>
      <dt>Memory</dt><dd data-field="memory">Loading</dd>
      <dt>Parity</dt><dd data-field="parity">Checking</dd>
      <dt>Errors</dt><dd data-field="errors">None</dd>
    </dl>`;
  return element;
}

function updateStatus(status: HTMLElement, layer: ScalarTileCanaryLayer, index: CanaryIndex, failures: readonly string[]): void {
  const resources = performance.getEntriesByType('resource') as PerformanceResourceTiming[];
  const tileNamespace = `/assets/scalar-canary/${index.contract}/releases/`;
  const tiles = resources.filter((entry) => entry.name.includes(tileNamespace) && entry.name.endsWith('.nws'));
  const encodedBytes = tiles.reduce((sum, entry) => sum + entry.encodedBodySize, 0);
  const decodedBytes = tiles.reduce((sum, entry) => sum + entry.decodedBodySize, 0);
  const snapshot = layer.resourceSnapshot();
  setField(status, 'tiles', `${snapshot.textures} ready · ${snapshot.pending} loading · ${snapshot.unavailable} empty · ${tiles.length} requests`);
  setField(status, 'transfer', `${formatBytes(encodedBytes)} encoded · ${formatBytes(decodedBytes)} decoded`);
  setField(status, 'memory', `${formatBytes(snapshot.gpuBytes)} GPU`);
  const renderer = status.querySelector<HTMLElement>('[data-field="renderer"]');
  if (renderer && snapshot.renderedPixels) {
    renderer.textContent = 'Visible fragments confirmed';
    renderer.dataset.state = 'good';
  } else if (renderer && snapshot.renderProofFailed) {
    renderer.textContent = 'No visible fragments';
    renderer.dataset.state = 'bad';
  }
  const errors = status.querySelector<HTMLElement>('[data-field="errors"]');
  if (errors) {
    errors.textContent = failures.length === 0 ? 'None' : `${failures.length} unique · ${failures.at(-1)}`;
    errors.dataset.state = failures.length === 0 ? 'good' : 'bad';
    errors.title = failures.join('\n');
  }
  console.debug('[scalar-canary] resources', {
    releaseId: index.releaseId, periodId: index.periodId, ...snapshot,
    requests: tiles.length, encodedBytes, decodedBytes, failures: failures.length,
  });
}

async function verifyProbes(probes: readonly CanaryProbe[]): Promise<{ ok: boolean; checked: number; message: string }> {
  const bodies = new Map<string, Uint8Array>();
  for (const probe of probes) {
    let body = bodies.get(probe.url);
    if (!body) {
      const response = await fetch(probe.url, { cache: 'force-cache', credentials: 'omit' });
      if (!response.ok) return { ok: false, checked: bodies.size, message: `${probe.name}: HTTP ${response.status}` };
      if (response.headers.get('content-type') !== 'application/vnd.nice-weather.score-tile') return { ok: false, checked: bodies.size, message: `${probe.name}: wrong media type` };
      body = new Uint8Array(await response.arrayBuffer());
      bodies.set(probe.url, body);
    }
    if (body[probe.offset] !== probe.score) {
      return { ok: false, checked: bodies.size, message: `${probe.name}: tile ${body[probe.offset]} ≠ point ${probe.score}` };
    }
  }
  return { ok: true, checked: probes.length, message: '' };
}

function expandPalette(stops: readonly PaletteStop[]): readonly ScalarPaletteColour[] {
  return Array.from({ length: 101 }, (_, score): ScalarPaletteColour => {
    const upperIndex = stops.findIndex((stop) => stop.score >= score);
    const upper = stops[Math.max(0, upperIndex)];
    const lower = stops[Math.max(0, upperIndex - 1)] ?? upper;
    const fraction = upper.score === lower.score ? 0 : (score - lower.score) / (upper.score - lower.score);
    const from = parseHex(lower.color);
    const to = parseHex(upper.color);
    return [
      Math.round(from[0] + (to[0] - from[0]) * fraction),
      Math.round(from[1] + (to[1] - from[1]) * fraction),
      Math.round(from[2] + (to[2] - from[2]) * fraction),
      222,
    ];
  });
}

function parseHex(value: string): readonly [number, number, number] {
  if (!/^#[0-9a-f]{6}$/i.test(value)) throw new Error(`Invalid palette colour ${value}.`);
  return [Number.parseInt(value.slice(1, 3), 16), Number.parseInt(value.slice(3, 5), 16), Number.parseInt(value.slice(5, 7), 16)];
}

function validateIndex(index: CanaryIndex): void {
  if (index.contract !== 'nice-weather-scalar-canary-v2' || !index.releaseId || !index.periodId || !index.tileTemplate.startsWith('/assets/scalar-canary/nice-weather-scalar-canary-v2/') || !index.tileTemplate.includes('{z}') || !index.tileTemplate.includes('{x}') || !index.tileTemplate.includes('{y}')) throw new Error('Canary index contract is invalid.');
  if (!Number.isInteger(index.minZoom) || !Number.isInteger(index.maxZoom) || index.minZoom < 0 || index.maxZoom < index.minZoom || index.maxZoom > 22) throw new Error('Canary zoom range is invalid.');
  if (index.palette.length < 2 || index.palette[0]?.score !== 0 || index.palette.at(-1)?.score !== 100) throw new Error('Canary palette is incomplete.');
  const tileKeys = new Set<string>();
  for (const tile of index.tiles) {
    const key = `${tile.z}/${tile.x}/${tile.y}`;
    if (!Number.isInteger(tile.z) || !Number.isInteger(tile.x) || !Number.isInteger(tile.y) || !/^[0-9a-f]{64}$/.test(tile.sha256) || tileKeys.has(key)) throw new Error('Canary tile identity is invalid.');
    tileKeys.add(key);
  }
  if (tileKeys.size !== index.tileCount) throw new Error('Canary tile count does not match its index.');
}

function setField(status: HTMLElement, name: string, value: string): void {
  const field = status.querySelector<HTMLElement>(`[data-field="${name}"]`);
  if (field) field.textContent = value;
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
  return `${(value / 1024 / 1024).toFixed(2)} MiB`;
}

function escapeHTML(value: string): string {
  const element = document.createElement('span');
  element.textContent = value;
  return element.innerHTML;
}
