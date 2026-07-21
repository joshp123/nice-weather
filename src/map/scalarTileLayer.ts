import type { CustomLayerInterface, CustomRenderMethodInput, Map as MapLibreMap } from 'maplibre-gl';

export const SCALAR_TILE_SIZE = 256;
export const SCALAR_TILE_GUTTER = 1;
export const SCALAR_TEXTURE_SIZE = SCALAR_TILE_SIZE + SCALAR_TILE_GUTTER * 2;
export const SCALAR_NO_DATA = 255;
export const SCALAR_MEDIA_TYPE = 'application/vnd.nice-weather.score-tile';

export type ScalarTileID = Readonly<{ z: number; x: number; y: number }>;
export type ScalarPaletteColour = Readonly<[number, number, number, number]>;

export type ScalarTileLayerOptions = Readonly<{
  id: string;
  minSourceZoom: number;
  maxSourceZoom: number;
  maxTextures: number;
  maxRequests: number;
  palette: readonly ScalarPaletteColour[];
  urlForTile: (tile: ScalarTileID) => string;
  sha256ForTile?: (tile: ScalarTileID) => string;
  tileAvailable?: (tile: ScalarTileID) => boolean;
  onError?: (tile: ScalarTileID, error: Error) => void;
  onContractError?: (tile: ScalarTileID, error: Error) => void;
  onRenderProof?: (visibleFragments: boolean) => void;
}>;

export type GeographicBounds = Readonly<{
  west: number;
  east: number;
  south: number;
  north: number;
}>;

export type TileDraw = Readonly<{
  id: ScalarTileID;
  worldX: number;
}>;

type TileTexture = {
  texture: WebGLTexture;
  lastUsed: number;
};

type QueuedTile = {
  id: ScalarTileID;
  key: string;
};

const VERTEX_SOURCE = `#version 300 es
precision highp float;
uniform mat4 u_matrix;
uniform vec4 u_tile_bounds;
in vec2 a_pos;
out vec2 v_uv;

void main() {
  vec2 world = mix(u_tile_bounds.xy, u_tile_bounds.zw, a_pos);
  gl_Position = u_matrix * vec4(world, 0.0, 1.0);
  v_uv = a_pos;
}`;

// Each score tile has a one-texel gutter sourced from its real neighbours.
// Manual four-sample interpolation lets no-data contribute coverage without
// dragging the scalar towards zero. The palette is applied only afterwards.
export const FRAGMENT_SOURCE = `#version 300 es
precision highp float;
uniform sampler2D u_scores;
uniform sampler2D u_palette;
in vec2 v_uv;
out vec4 fragColor;

float scoreAt(ivec2 position) {
  return floor(texelFetch(u_scores, position, 0).r * 255.0 + 0.5);
}

void accumulate(inout float weightedScore, inout float coverage, float score, float weight) {
  if (score <= 100.0) {
    weightedScore += score * weight;
    coverage += weight;
  }
}

void main() {
  vec2 samplePosition = v_uv * ${SCALAR_TILE_SIZE}.0 + vec2(0.5);
  ivec2 base = ivec2(floor(samplePosition));
  vec2 fraction = fract(samplePosition);
  float weightedScore = 0.0;
  float coverage = 0.0;
  accumulate(weightedScore, coverage, scoreAt(base), (1.0 - fraction.x) * (1.0 - fraction.y));
  accumulate(weightedScore, coverage, scoreAt(base + ivec2(1, 0)), fraction.x * (1.0 - fraction.y));
  accumulate(weightedScore, coverage, scoreAt(base + ivec2(0, 1)), (1.0 - fraction.x) * fraction.y);
  accumulate(weightedScore, coverage, scoreAt(base + ivec2(1, 1)), fraction.x * fraction.y);
  if (coverage <= 0.0) discard;
  int paletteIndex = int(floor(weightedScore / coverage + 0.5));
  vec4 colour = texelFetch(u_palette, ivec2(paletteIndex, 0), 0);
  float alpha = colour.a * coverage;
  fragColor = vec4(colour.rgb * alpha, alpha);
}`;

export class ScalarTileLayer implements CustomLayerInterface {
  readonly id: string;
  readonly type = 'custom' as const;
  readonly renderingMode = '2d' as const;

  private map?: MapLibreMap;
  private program?: WebGLProgram;
  private vertices?: WebGLBuffer;
  private vertexArray?: WebGLVertexArrayObject;
  private paletteTexture?: WebGLTexture;
  private matrixLocation?: WebGLUniformLocation;
  private boundsLocation?: WebGLUniformLocation;
  private scoresLocation?: WebGLUniformLocation;
  private paletteLocation?: WebGLUniformLocation;
  private renderQuery?: WebGLQuery;
  private renderQueryPending = false;
  private renderedPixels = false;
  private renderProofFailed = false;
  private readonly textures = new Map<string, TileTexture>();
  private readonly uploads = new Map<string, Uint8Array>();
  private readonly requests = new Map<string, AbortController>();
  private readonly failed = new Set<string>();
  private readonly unavailable = new Set<string>();
  private queue: QueuedTile[] = [];
  private visible: TileDraw[] = [];
  private clock = 0;
  private removed = false;

  constructor(private readonly options: ScalarTileLayerOptions) {
    validateOptions(options);
    this.id = options.id;
  }

  onAdd(map: MapLibreMap, rawContext: WebGLRenderingContext | WebGL2RenderingContext): void {
    if (!('texStorage2D' in rawContext)) {
      throw new Error('Scalar score tiles require WebGL 2.');
    }
    if (!isMercatorProjection(map.getProjection() as { type?: string } | undefined)) {
      throw new Error('Scalar score tiles currently require the Mercator projection.');
    }
    const gl = rawContext as WebGL2RenderingContext;
    this.map = map;
    this.removed = false;
    this.program = createProgram(gl, VERTEX_SOURCE, FRAGMENT_SOURCE);
    this.vertices = requireResource(gl.createBuffer(), 'vertex buffer');
    this.vertexArray = requireResource(gl.createVertexArray(), 'vertex array');

    gl.bindVertexArray(this.vertexArray);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.vertices);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([
      0, 0, 1, 0, 0, 1,
      0, 1, 1, 0, 1, 1,
    ]), gl.STATIC_DRAW);
    const position = gl.getAttribLocation(this.program, 'a_pos');
    gl.enableVertexAttribArray(position);
    gl.vertexAttribPointer(position, 2, gl.FLOAT, false, 0, 0);

    this.matrixLocation = requiredUniform(gl, this.program, 'u_matrix');
    this.boundsLocation = requiredUniform(gl, this.program, 'u_tile_bounds');
    this.scoresLocation = requiredUniform(gl, this.program, 'u_scores');
    this.paletteLocation = requiredUniform(gl, this.program, 'u_palette');
    this.paletteTexture = uploadPalette(gl, this.options.palette);
    gl.bindVertexArray(null);
    map.triggerRepaint();
  }

  render(gl: WebGLRenderingContext | WebGL2RenderingContext, input: CustomRenderMethodInput): void {
    if (this.removed || !this.map || !this.program || !this.vertexArray || !this.paletteTexture || !this.matrixLocation || !this.boundsLocation || !this.scoresLocation || !this.paletteLocation) return;
    const gl2 = gl as WebGL2RenderingContext;
    this.pollRenderProof(gl2);
    this.refreshVisible();
    this.evictTextures(gl2, this.uploads.size);
    this.flushUploads(gl2);
    this.evictTextures(gl2);

    gl2.useProgram(this.program);
    gl2.bindVertexArray(this.vertexArray);
    // MapLibre's default projection matrix explicitly accepts normalized
    // Web Mercator coordinates in [0, 1]. modelViewProjectionMatrix is not
    // the stable custom-layer coordinate contract in MapLibre 5.24.
    gl2.uniformMatrix4fv(this.matrixLocation, false, input.defaultProjectionData.mainMatrix);
    gl2.uniform1i(this.scoresLocation, 0);
    gl2.uniform1i(this.paletteLocation, 1);
    gl2.activeTexture(gl2.TEXTURE1);
    gl2.bindTexture(gl2.TEXTURE_2D, this.paletteTexture);
    gl2.enable(gl2.BLEND);
    gl2.blendFunc(gl2.ONE, gl2.ONE_MINUS_SRC_ALPHA);

    const proveThisFrame = !this.renderedPixels && !this.renderProofFailed && !this.renderQueryPending && this.visible.some((draw) => this.textures.has(tileKey(draw.id)));
    if (proveThisFrame) {
      this.renderQuery = requireResource(gl2.createQuery(), 'render proof query');
      gl2.beginQuery(gl2.ANY_SAMPLES_PASSED_CONSERVATIVE, this.renderQuery);
    }
    for (const draw of this.visible) {
      const entry = this.textures.get(tileKey(draw.id));
      if (!entry) continue;
      entry.lastUsed = ++this.clock;
      const scale = 2 ** draw.id.z;
      gl2.uniform4f(this.boundsLocation, draw.worldX / scale, draw.id.y / scale, (draw.worldX + 1) / scale, (draw.id.y + 1) / scale);
      gl2.activeTexture(gl2.TEXTURE0);
      gl2.bindTexture(gl2.TEXTURE_2D, entry.texture);
      gl2.drawArrays(gl2.TRIANGLES, 0, 6);
    }
    if (proveThisFrame) {
      gl2.endQuery(gl2.ANY_SAMPLES_PASSED_CONSERVATIVE);
      this.renderQueryPending = true;
      this.map.triggerRepaint();
    }
    gl2.bindVertexArray(null);
  }

  onRemove(_map: MapLibreMap, rawContext: WebGLRenderingContext | WebGL2RenderingContext): void {
    const gl = rawContext as WebGL2RenderingContext;
    this.removed = true;
    for (const request of this.requests.values()) request.abort();
    this.requests.clear();
    this.queue = [];
    this.uploads.clear();
    this.failed.clear();
    this.unavailable.clear();
    this.visible = [];
    for (const entry of this.textures.values()) gl.deleteTexture(entry.texture);
    this.textures.clear();
    if (this.paletteTexture) gl.deleteTexture(this.paletteTexture);
    if (this.vertices) gl.deleteBuffer(this.vertices);
    if (this.vertexArray) gl.deleteVertexArray(this.vertexArray);
    if (this.program) gl.deleteProgram(this.program);
    if (this.renderQuery) gl.deleteQuery(this.renderQuery);
    this.paletteTexture = undefined;
    this.vertices = undefined;
    this.vertexArray = undefined;
    this.program = undefined;
    this.matrixLocation = undefined;
    this.boundsLocation = undefined;
    this.scoresLocation = undefined;
    this.paletteLocation = undefined;
    this.renderQuery = undefined;
    this.renderQueryPending = false;
    this.renderedPixels = false;
    this.renderProofFailed = false;
    this.map = undefined;
  }

  resourceSnapshot(): Readonly<{ textures: number; pending: number; queued: number; unavailable: number; failed: number; renderedPixels: boolean; renderProofPending: boolean; renderProofFailed: boolean; gpuBytes: number }> {
    return {
      textures: this.textures.size,
      pending: this.requests.size,
      queued: this.queue.length,
      unavailable: this.unavailable.size,
      failed: this.failed.size,
      renderedPixels: this.renderedPixels,
      renderProofPending: this.renderQueryPending,
      renderProofFailed: this.renderProofFailed,
      gpuBytes: this.textures.size * scalarTextureBytes() + (this.paletteTexture ? this.options.palette.length * 4 : 0),
    };
  }

  private refreshVisible(): void {
    if (!this.map) return;
    const bounds = this.map.getBounds();
    this.visible = boundedCoveringTiles({
      west: bounds.getWest(), east: bounds.getEast(),
      south: bounds.getSouth(), north: bounds.getNorth(),
    }, Math.floor(this.map.getZoom()), this.options.minSourceZoom, this.options.maxSourceZoom, this.options.maxTextures);
    const wanted = new Map<string, ScalarTileID>();
    for (const draw of this.visible) wanted.set(tileKey(draw.id), draw.id);

    for (const [key, request] of this.requests) {
      if (!wanted.has(key)) {
        request.abort();
        this.requests.delete(key);
      }
    }
    for (const key of this.uploads.keys()) {
      if (!wanted.has(key)) this.uploads.delete(key);
    }
    for (const key of this.failed) {
      if (!wanted.has(key)) this.failed.delete(key);
    }
    for (const key of this.unavailable) {
      if (!wanted.has(key)) this.unavailable.delete(key);
    }
    this.queue = this.queue.filter(({ key }) => wanted.has(key));
    const queued = new Set(this.queue.map(({ key }) => key));
    for (const [key, id] of wanted) {
      if (this.options.tileAvailable && !this.options.tileAvailable(id)) {
        this.unavailable.add(key);
        continue;
      }
      if (!this.textures.has(key) && !this.uploads.has(key) && !this.requests.has(key) && !this.failed.has(key) && !queued.has(key)) this.queue.push({ id, key });
    }
    this.pumpRequests();
  }

  private pollRenderProof(gl: WebGL2RenderingContext): void {
    if (!this.renderQueryPending || !this.renderQuery) return;
    if (!gl.getQueryParameter(this.renderQuery, gl.QUERY_RESULT_AVAILABLE)) {
      this.map?.triggerRepaint();
      return;
    }
    const visible = Boolean(gl.getQueryParameter(this.renderQuery, gl.QUERY_RESULT));
    gl.deleteQuery(this.renderQuery);
    this.renderQuery = undefined;
    this.renderQueryPending = false;
    this.renderedPixels = visible;
    this.renderProofFailed = !visible;
    this.options.onRenderProof?.(visible);
  }

  private pumpRequests(): void {
    while (!this.removed && this.requests.size < this.options.maxRequests && this.queue.length > 0) {
      const next = this.queue.shift()!;
      const controller = new AbortController();
      this.requests.set(next.key, controller);
      void this.fetchTile(next, controller);
    }
  }

  private async fetchTile(tile: QueuedTile, controller: AbortController): Promise<void> {
    try {
      const response = await fetch(this.options.urlForTile(tile.id), { signal: controller.signal, cache: 'force-cache', credentials: 'omit' });
      if (!response.ok) throw new Error(`score tile returned HTTP ${response.status}`);
      if (response.headers.get('content-type') !== SCALAR_MEDIA_TYPE) {
        this.options.onContractError?.(tile.id, new Error(`score tile returned ${response.headers.get('content-type') ?? 'no content type'}; want ${SCALAR_MEDIA_TYPE}`));
      }
      const scores = new Uint8Array(await response.arrayBuffer());
      validateScores(scores);
      const expectedSHA256 = this.options.sha256ForTile?.(tile.id);
      if (this.options.sha256ForTile && !validSHA256(expectedSHA256)) throw new Error('score tile index has no valid SHA-256');
      if (expectedSHA256 && await sha256Hex(scores) !== expectedSHA256) throw new Error('score tile SHA-256 does not match its index');
      if (!controller.signal.aborted && !this.removed) this.uploads.set(tile.key, scores);
    } catch (cause) {
      if (!controller.signal.aborted) {
        this.failed.add(tile.key);
        this.options.onError?.(tile.id, asError(cause));
      }
    } finally {
      if (this.requests.get(tile.key) === controller) this.requests.delete(tile.key);
      if (!this.removed) {
        this.pumpRequests();
        this.map?.triggerRepaint();
      }
    }
  }

  private flushUploads(gl: WebGL2RenderingContext): void {
    for (const [key, scores] of this.uploads) {
      const texture = requireResource(gl.createTexture(), 'score texture');
      gl.bindTexture(gl.TEXTURE_2D, texture);
      gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1);
      gl.texStorage2D(gl.TEXTURE_2D, 1, gl.R8, SCALAR_TEXTURE_SIZE, SCALAR_TEXTURE_SIZE);
      gl.texSubImage2D(gl.TEXTURE_2D, 0, 0, 0, SCALAR_TEXTURE_SIZE, SCALAR_TEXTURE_SIZE, gl.RED, gl.UNSIGNED_BYTE, scores);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      this.textures.set(key, { texture, lastUsed: ++this.clock });
      this.uploads.delete(key);
    }
  }

  private evictTextures(gl: WebGL2RenderingContext, incomingTextures = 0): void {
    const retainedLimit = Math.max(0, this.options.maxTextures - incomingTextures);
    if (this.textures.size <= retainedLimit) return;
    const visible = new Set(this.visible.map(({ id }) => tileKey(id)));
    const candidates = [...this.textures.entries()].filter(([key]) => !visible.has(key)).sort((a, b) => a[1].lastUsed - b[1].lastUsed);
    while (this.textures.size > retainedLimit && candidates.length > 0) {
      const [key, entry] = candidates.shift()!;
      gl.deleteTexture(entry.texture);
      this.textures.delete(key);
    }
  }
}

export function scalarTextureBytes(): number {
  return SCALAR_TEXTURE_SIZE * SCALAR_TEXTURE_SIZE;
}

// MapLibre 5.24 returns undefined for its default projection. An explicit
// non-Mercator projection remains a hard failure because the tile bounds are
// normalized Web Mercator coordinates.
export function isMercatorProjection(projection: { type?: string } | undefined): boolean {
  return projection?.type === undefined || projection.type === 'mercator';
}

export function boundedCoveringTiles(bounds: GeographicBounds, mapZoom: number, minZoom: number, maxZoom: number, maxTiles: number): TileDraw[] {
  let zoom = Math.min(maxZoom, Math.max(minZoom, mapZoom));
  while (zoom > minZoom) {
    const covered = coveringTiles(bounds, zoom);
    if (uniqueTileCount(covered) <= maxTiles) return covered;
    zoom -= 1;
  }
  const covered = coveringTiles(bounds, zoom);
  if (uniqueTileCount(covered) > maxTiles) throw new Error('Texture budget cannot cover the viewport at the minimum source zoom.');
  return covered;
}

export function coveringTiles(bounds: GeographicBounds, zoom: number): TileDraw[] {
  if (!Number.isInteger(zoom) || zoom < 0 || zoom > 22) throw new Error('Tile zoom is invalid.');
  for (const value of [bounds.west, bounds.east, bounds.south, bounds.north]) if (!Number.isFinite(value)) throw new Error('Tile bounds are invalid.');
  if (bounds.south >= bounds.north) throw new Error('Tile latitude bounds are invalid.');
  const limit = 85.0511287798066;
  const north = clamp(bounds.north, -limit, limit);
  const south = clamp(bounds.south, -limit, limit);
  if (south >= north) return [];
  let east = bounds.east;
  if (east <= bounds.west) east += (Math.floor((bounds.west - east) / 360) + 1) * 360;
  const count = 2 ** zoom;
  const xStart = Math.floor((bounds.west + 180) / 360 * count);
  const xEnd = Math.ceil((east + 180) / 360 * count) - 1;
  const yStart = Math.max(0, Math.floor(mercatorY(north) * count));
  const yEnd = Math.min(count - 1, Math.ceil(mercatorY(south) * count) - 1);
  const result: TileDraw[] = [];
  for (let y = yStart; y <= yEnd; y += 1) {
    for (let worldX = xStart; worldX <= xEnd; worldX += 1) {
      result.push({ id: { z: zoom, x: modulo(worldX, count), y }, worldX });
    }
  }
  return result;
}

function uniqueTileCount(tiles: readonly TileDraw[]): number {
  return new Set(tiles.map(({ id }) => tileKey(id))).size;
}

function tileKey(tile: ScalarTileID): string {
  return `${tile.z}/${tile.x}/${tile.y}`;
}

function mercatorY(latitude: number): number {
  const radians = latitude * Math.PI / 180;
  return 0.5 - Math.asinh(Math.tan(radians)) / (2 * Math.PI);
}

function modulo(value: number, modulus: number): number {
  return ((value % modulus) + modulus) % modulus;
}

function clamp(value: number, minimum: number, maximum: number): number {
  return Math.min(maximum, Math.max(minimum, value));
}

function validateOptions(options: ScalarTileLayerOptions): void {
  if (!options.id || !Number.isInteger(options.minSourceZoom) || !Number.isInteger(options.maxSourceZoom) || options.minSourceZoom < 0 || options.maxSourceZoom > 22 || options.minSourceZoom > options.maxSourceZoom) throw new Error('Scalar tile zoom limits are invalid.');
  if (!Number.isInteger(options.maxTextures) || options.maxTextures < 1 || !Number.isInteger(options.maxRequests) || options.maxRequests < 1) throw new Error('Scalar tile resource limits are invalid.');
  if (options.palette.length !== 101 || options.palette.some((colour) => colour.length !== 4 || colour.some((channel) => !Number.isInteger(channel) || channel < 0 || channel > 255))) throw new Error('Scalar tile palette must contain 101 RGBA colours.');
}

export function validateScores(scores: Uint8Array): void {
  if (scores.byteLength !== scalarTextureBytes()) throw new Error(`Scalar tile has ${scores.byteLength} bytes; want ${scalarTextureBytes()}.`);
  for (const score of scores) if (score > 100 && score !== SCALAR_NO_DATA) throw new Error(`Scalar tile contains invalid score ${score}.`);
}

export async function sha256Hex(bytes: Uint8Array): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', bytes as Uint8Array<ArrayBuffer>);
  return [...new Uint8Array(digest)].map((value) => value.toString(16).padStart(2, '0')).join('');
}

export function validSHA256(value: string | undefined): value is string {
  return typeof value === 'string' && /^[0-9a-f]{64}$/.test(value);
}

function uploadPalette(gl: WebGL2RenderingContext, palette: readonly ScalarPaletteColour[]): WebGLTexture {
  const texture = requireResource(gl.createTexture(), 'palette texture');
  gl.bindTexture(gl.TEXTURE_2D, texture);
  gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1);
  gl.texStorage2D(gl.TEXTURE_2D, 1, gl.RGBA8, palette.length, 1);
  gl.texSubImage2D(gl.TEXTURE_2D, 0, 0, 0, palette.length, 1, gl.RGBA, gl.UNSIGNED_BYTE, new Uint8Array(palette.flatMap((colour) => [...colour])));
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
  return texture;
}

function createProgram(gl: WebGL2RenderingContext, vertexSource: string, fragmentSource: string): WebGLProgram {
  const vertex = createShader(gl, gl.VERTEX_SHADER, vertexSource);
  const fragment = createShader(gl, gl.FRAGMENT_SHADER, fragmentSource);
  const program = requireResource(gl.createProgram(), 'shader program');
  gl.attachShader(program, vertex);
  gl.attachShader(program, fragment);
  gl.linkProgram(program);
  gl.deleteShader(vertex);
  gl.deleteShader(fragment);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
    const message = gl.getProgramInfoLog(program) || 'unknown link error';
    gl.deleteProgram(program);
    throw new Error(`Scalar tile shader link failed: ${message}`);
  }
  return program;
}

function createShader(gl: WebGL2RenderingContext, kind: number, source: string): WebGLShader {
  const shader = requireResource(gl.createShader(kind), 'shader');
  gl.shaderSource(shader, source);
  gl.compileShader(shader);
  if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
    const message = gl.getShaderInfoLog(shader) || 'unknown compile error';
    gl.deleteShader(shader);
    throw new Error(`Scalar tile shader compile failed: ${message}`);
  }
  return shader;
}

function requiredUniform(gl: WebGL2RenderingContext, program: WebGLProgram, name: string): WebGLUniformLocation {
  return requireResource(gl.getUniformLocation(program, name), `uniform ${name}`);
}

function requireResource<T>(resource: T | null, name: string): T {
  if (resource === null) throw new Error(`Could not create scalar tile ${name}.`);
  return resource;
}

function asError(cause: unknown): Error {
  return cause instanceof Error ? cause : new Error(String(cause));
}
