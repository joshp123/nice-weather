// Written by AI on 2026.10.16 by model claude opus 5.5.
// Paints one weather grid (a greyscale PNG, value 0-100, 255 = no data) onto the
// map as smooth, rounded colour bands. The grid is sampled with a cubic B-spline,
// so band edges curve like contours instead of showing grid squares. Sea is
// clipped by the basemap's water layer drawn above this one.
import type { CustomLayerInterface, CustomRenderMethodInput, Map as MapLibreMap } from 'maplibre-gl';
import type { Band } from './palette';

export type Grid = Readonly<{ west: number; north: number; resolution: number; width: number; height: number }>;

const vertexShader = `#version 300 es
uniform mat4 u_matrix;
uniform float u_world_offset;
in vec2 a_mercator;
out vec2 v_mercator;
void main() {
  v_mercator = a_mercator;
  gl_Position = u_matrix * vec4(a_mercator.x + u_world_offset, a_mercator.y, 0.0, 1.0);
}`;

const fragmentShader = `#version 300 es
precision highp float;
uniform highp usampler2D u_grid;
uniform vec4 u_grid_geometry; // west, north, step, unused
uniform ivec2 u_grid_size;
uniform float u_value_to_score;
uniform vec4 u_band_colours[6]; // premultiplied
uniform float u_band_minimums[6];
in vec2 v_mercator;
out vec4 fragColour;

const float PI = 3.141592653589793;

vec4 cubicBSplineWeights(float t) {
  float u = 1.0 - t;
  return vec4(u * u * u, 3.0 * t * t * t - 6.0 * t * t + 4.0, -3.0 * t * t * t + 3.0 * t * t + 3.0 * t + 1.0, t * t * t) / 6.0;
}

void main() {
  float longitude = v_mercator.x * 360.0 - 180.0;
  float latitude = degrees(atan(sinh(PI * (1.0 - 2.0 * v_mercator.y))));
  vec2 cell = vec2((longitude - u_grid_geometry.x) / u_grid_geometry.z, (u_grid_geometry.y - latitude) / u_grid_geometry.z);
  vec2 base = floor(cell);
  vec4 weightsX = cubicBSplineWeights(cell.x - base.x);
  vec4 weightsY = cubicBSplineWeights(cell.y - base.y);

  float weightedValue = 0.0;
  float coverage = 0.0;
  for (int row = 0; row < 4; row++) {
    for (int column = 0; column < 4; column++) {
      ivec2 position = ivec2(base) + ivec2(column - 1, row - 1);
      position.x = (position.x % u_grid_size.x + u_grid_size.x) % u_grid_size.x;
      position.y = clamp(position.y, 0, u_grid_size.y - 1);
      float value = float(texelFetch(u_grid, position, 0).r);
      float weight = weightsX[column] * weightsY[row];
      if (value < 254.5) {
        weightedValue += value * weight;
        coverage += weight;
      }
    }
  }
  if (coverage < 0.5) discard;

  float score = weightedValue / coverage * u_value_to_score;
  float edgeWidth = max(fwidth(score) * 0.75, 0.02);
  vec4 colour = u_band_colours[0];
  float contour = 0.0;
  for (int band = 1; band < 6; band++) {
    float distanceToEdge = score - u_band_minimums[band];
    colour = mix(colour, u_band_colours[band], smoothstep(-edgeWidth, edgeWidth, distanceToEdge));
    contour = max(contour, 1.0 - smoothstep(0.0, edgeWidth * 2.0, abs(distanceToEdge)));
  }
  // A thin darker line where one band meets the next, as on Ryan Hall's map.
  colour.rgb *= 1.0 - 0.28 * contour;
  fragColour = colour;
}`;

export class WeatherLayer implements CustomLayerInterface {
  readonly id = 'weather';
  readonly type = 'custom' as const;
  readonly renderingMode = '2d' as const;

  private map?: MapLibreMap;
  private gl?: WebGL2RenderingContext;
  private program?: WebGLProgram;
  private quad?: WebGLVertexArrayObject;
  /** The grid at full detail, then halved three times. Zoomed out, a coarser level keeps bands smooth instead of speckled. */
  private levels: { texture: WebGLTexture; grid: Grid }[] = [];
  private valueToScore = 1;
  private requestedUrl?: string;

  constructor(private bands: readonly Band[]) {}

  /** Shows the grid at `url`. `valueToScore` maps stored values onto the 0-100 band scale. */
  async show(url: string, grid: Grid, valueToScore: number): Promise<void> {
    this.requestedUrl = url;
    const image = await loadGreyscaleImage(url);
    if (this.requestedUrl !== url || !this.gl) return;
    const gl = this.gl;
    const levels: { texture: WebGLTexture; grid: Grid }[] = [];
    let values = image;
    let levelGrid = grid;
    for (let level = 0; level < 4; level++) {
      if (level > 0) {
        const halved = halveGrid(values, levelGrid);
        values = halved.values;
        levelGrid = halved.grid;
      }
      const texture = gl.createTexture()!;
      gl.bindTexture(gl.TEXTURE_2D, texture);
      gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1);
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.R8UI, levelGrid.width, levelGrid.height, 0, gl.RED_INTEGER, gl.UNSIGNED_BYTE, values);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST);
      levels.push({ texture, grid: levelGrid });
    }
    for (const old of this.levels) gl.deleteTexture(old.texture);
    this.levels = levels;
    this.valueToScore = valueToScore;
    this.map?.triggerRepaint();
  }

  setBands(bands: readonly Band[]): void {
    this.bands = bands;
    this.map?.triggerRepaint();
  }

  onAdd(map: MapLibreMap, context: WebGLRenderingContext | WebGL2RenderingContext): void {
    const gl = context as WebGL2RenderingContext;
    this.map = map;
    this.gl = gl;
    this.program = compileProgram(gl, vertexShader, fragmentShader);
    this.quad = gl.createVertexArray()!;
    gl.bindVertexArray(this.quad);
    const buffer = gl.createBuffer();
    gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([0, 0, 1, 0, 0, 1, 0, 1, 1, 0, 1, 1]), gl.STATIC_DRAW);
    const location = gl.getAttribLocation(this.program, 'a_mercator');
    gl.enableVertexAttribArray(location);
    gl.vertexAttribPointer(location, 2, gl.FLOAT, false, 0, 0);
    gl.bindVertexArray(null);
  }

  render(_context: WebGLRenderingContext | WebGL2RenderingContext, input: CustomRenderMethodInput): void {
    const { gl, program, quad, map } = this;
    if (!gl || !program || !quad || !map || this.levels.length === 0) return;
    // Use the finest level whose cells are still at least ~5 screen pixels wide.
    const pixelsPerCell = (512 * 2 ** map.getZoom()) / this.levels[0].grid.width;
    const level = Math.min(this.levels.length - 1, Math.max(0, Math.ceil(Math.log2(5 / pixelsPerCell))));
    const { texture, grid } = this.levels[level];
    gl.useProgram(program);
    gl.bindVertexArray(quad);
    gl.activeTexture(gl.TEXTURE0);
    gl.bindTexture(gl.TEXTURE_2D, texture);
    const uniform = (name: string) => gl.getUniformLocation(program, name);
    gl.uniform1i(uniform('u_grid'), 0);
    gl.uniform4f(uniform('u_grid_geometry'), grid.west, grid.north, grid.resolution, 0);
    gl.uniform2i(uniform('u_grid_size'), grid.width, grid.height);
    gl.uniform1f(uniform('u_value_to_score'), this.valueToScore);
    gl.uniform4fv(uniform('u_band_colours'), this.bands.flatMap((band) => premultipliedRgba(band.colour)));
    gl.uniform1fv(uniform('u_band_minimums'), this.bands.map((band) => band.minimumScore));
    gl.uniformMatrix4fv(uniform('u_matrix'), false, input.defaultProjectionData.mainMatrix);
    gl.enable(gl.BLEND);
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
    for (const worldOffset of [-1, 0, 1]) {
      gl.uniform1f(uniform('u_world_offset'), worldOffset);
      gl.drawArrays(gl.TRIANGLES, 0, 6);
    }
    gl.bindVertexArray(null);
  }

  onRemove(): void {
    for (const level of this.levels) this.gl?.deleteTexture(level.texture);
    this.levels = [];
  }
}

/** Averages each 2 x 2 block of cells, ignoring sea. The coarser cell sits at the centre of its block. */
function halveGrid(values: Uint8Array, grid: Grid): { values: Uint8Array; grid: Grid } {
  const width = Math.ceil(grid.width / 2);
  const height = Math.ceil(grid.height / 2);
  const halved = new Uint8Array(width * height);
  for (let row = 0; row < height; row++) {
    for (let column = 0; column < width; column++) {
      let sum = 0;
      let count = 0;
      for (let dRow = 0; dRow < 2; dRow++) {
        for (let dColumn = 0; dColumn < 2; dColumn++) {
          const sourceRow = row * 2 + dRow;
          const sourceColumn = column * 2 + dColumn;
          if (sourceRow >= grid.height || sourceColumn >= grid.width) continue;
          const value = values[sourceRow * grid.width + sourceColumn];
          if (value !== 255) { sum += value; count++; }
        }
      }
      halved[row * width + column] = count ? Math.round(sum / count) : 255;
    }
  }
  const resolution = grid.resolution * 2;
  return {
    values: halved,
    grid: { width, height, resolution, west: grid.west + grid.resolution / 2, north: grid.north - grid.resolution / 2 },
  };
}

async function loadGreyscaleImage(url: string): Promise<Uint8Array> {
  const response = await fetch(url);
  if (!response.ok) throw new Error(`${url}: HTTP ${response.status}`);
  const bitmap = await createImageBitmap(await response.blob(), { colorSpaceConversion: 'none', premultiplyAlpha: 'none' });
  const canvas = new OffscreenCanvas(bitmap.width, bitmap.height);
  const context = canvas.getContext('2d', { willReadFrequently: true })!;
  context.drawImage(bitmap, 0, 0);
  const rgba = context.getImageData(0, 0, bitmap.width, bitmap.height).data;
  const values = new Uint8Array(bitmap.width * bitmap.height);
  for (let index = 0; index < values.length; index++) values[index] = rgba[index * 4];
  return values;
}

function premultipliedRgba(colour: string): number[] {
  if (colour === 'transparent') return [0, 0, 0, 0];
  const channel = (index: number) => parseInt(colour.slice(1 + index * 2, 3 + index * 2), 16) / 255;
  const opacity = channel(3);
  return [channel(0) * opacity, channel(1) * opacity, channel(2) * opacity, opacity];
}

function compileProgram(gl: WebGL2RenderingContext, vertexSource: string, fragmentSource: string): WebGLProgram {
  const program = gl.createProgram()!;
  for (const [type, source] of [[gl.VERTEX_SHADER, vertexSource], [gl.FRAGMENT_SHADER, fragmentSource]] as const) {
    const shader = gl.createShader(type)!;
    gl.shaderSource(shader, source);
    gl.compileShader(shader);
    if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(shader) ?? 'shader failed');
    gl.attachShader(program, shader);
  }
  gl.linkProgram(program);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(program) ?? 'program failed');
  return program;
}
