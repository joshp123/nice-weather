// Written by AI on 2026.10.17 by model gpt-6.1-sol.
import type { CustomLayerInterface, CustomRenderMethodInput, Map as MapLibreMap } from 'maplibre-gl';
import { bands } from '../palette';
import type { EngineOptions, Period, Point, PointVerdict, RedrawTiming, WeatherEngine } from './index';
import { DEFAULT_TASTE, type Taste } from './taste';
import { WeatherData, type Cells, type PeriodData, type Records } from './data';
import { PointSampler, readFineScore } from './sampling';
import { CELL_TEXTURE_WIDTH, SHIFT_LEVELS, fullscreenVertex, scoringFragment, resolveFragment, mapVertex, mapFragment } from './shaders';

type PendingFrame = { started: number; resolve: (timing: RedrawTiming) => void; reject: (error: Error) => void };
type Programs = { score: WebGLProgram; resolve: WebGLProgram; map: WebGLProgram };

export class BrowserWeatherEngine implements WeatherEngine {
  readonly layer: CustomLayerInterface;
  private source: WeatherData;
  private cells?: Cells;
  private data?: PeriodData;
  private period: Period = { kind: 'month', month: 1 };
  private taste: Taste = DEFAULT_TASTE;
  private sampler?: PointSampler;
  private gl?: WebGL2RenderingContext;
  private programs?: Programs;
  private quad?: WebGLVertexArrayObject;
  private framebuffer?: WebGLFramebuffer;
  private ordinals?: WebGLTexture;
  private offsets?: WebGLTexture;
  private scores?: WebGLTexture;
  private fineScores?: WebGLTexture;
  private recordTextures = new Map<Records, WebGLTexture>();
  private uploadedOffsets = new Set<number>();
  private resources: { textures: WebGLTexture[]; buffers: WebGLBuffer[] } = { textures: [], buffers: [] };
  private revision = 0;
  private drawnRevision = -1;
  private readbackRevision = -1;
  private fineScoreReadback?: Float32Array;
  private periodRequest = 0;
  private pending: PendingFrame[] = [];
  private destroyed = false;

  constructor(private map: MapLibreMap, private options: EngineOptions) {
    this.source = new WeatherData((options.dataUrl ?? '/data/v6').replace(/\/$/, ''));
    this.layer = {
      id: options.layerId ?? 'weather-v6', type: 'custom', renderingMode: '2d',
      onAdd: (_map, context) => this.onAdd(context),
      render: (_context, input) => this.render(input),
      onRemove: () => this.destroy(),
    };
  }

  async setPeriod(period: Period): Promise<void> {
    const request = ++this.periodRequest;
    const [cells, data] = await Promise.all([this.source.cells, this.source.load(period)]);
    if (request !== this.periodRequest || this.destroyed) return;
    this.cells = cells;
    this.data = data;
    this.period = { ...period };
    this.sampler = new PointSampler(cells, data, this.period, this.taste);
    await this.requestFrame();
  }

  setTaste(taste: Taste): Promise<RedrawTiming> {
    this.taste = { ...taste };
    if (this.cells && this.data) this.sampler = new PointSampler(this.cells, this.data, this.period, this.taste);
    return this.requestFrame();
  }

  judgePoint(latitude: number, longitude: number): PointVerdict | null {
    return this.sampler?.judge(latitude, longitude) ?? null;
  }

  scorePoints(points: readonly Point[]): Float32Array {
    const gl = this.gl;
    if (!gl || !this.fineScores || this.drawnRevision !== this.revision || this.destroyed) {
      return Float32Array.from(points, point => this.judgePoint(point.latitude, point.longitude)?.meanScore ?? NaN);
    }
    if (this.readbackRevision !== this.drawnRevision) {
      this.fineScoreReadback ??= new Float32Array(1440 * 721);
      const previous = gl.getParameter(gl.READ_FRAMEBUFFER_BINDING) as WebGLFramebuffer | null;
      gl.bindFramebuffer(gl.READ_FRAMEBUFFER, this.framebuffer!);
      gl.readPixels(0, 0, 1440, 721, gl.RED, gl.FLOAT, this.fineScoreReadback);
      gl.bindFramebuffer(gl.READ_FRAMEBUFFER, previous);
      this.readbackRevision = this.drawnRevision;
    }
    return Float32Array.from(points, point => readFineScore(this.fineScoreReadback!, point.latitude, point.longitude));
  }

  private requestFrame(): Promise<RedrawTiming> {
    if (this.destroyed) return Promise.reject(new Error('Weather engine has been destroyed'));
    this.revision++;
    const started = performance.now();
    const result = new Promise<RedrawTiming>((resolve, reject) => this.pending.push({ started, resolve, reject }));
    this.map.triggerRepaint();
    return result;
  }

  private onAdd(context: WebGLRenderingContext | WebGL2RenderingContext): void {
    if (!(context instanceof WebGL2RenderingContext)) throw new Error('Nice Weather v6 needs WebGL2');
    const gl = this.gl = context;
    if (!gl.getExtension('EXT_color_buffer_float')) throw new Error('Nice Weather v6 needs WebGL2 floating-point render targets');
    this.programs = {
      score: compileProgram(gl, fullscreenVertex, scoringFragment),
      resolve: compileProgram(gl, fullscreenVertex, resolveFragment),
      map: compileProgram(gl, mapVertex, mapFragment),
    };
    this.quad = gl.createVertexArray()!;
    gl.bindVertexArray(this.quad);
    const buffer = gl.createBuffer()!;
    this.resources.buffers.push(buffer);
    gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([0, 0, 1, 0, 0, 1, 0, 1, 1, 0, 1, 1]), gl.STATIC_DRAW);
    gl.enableVertexAttribArray(0);
    gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0);
    gl.bindVertexArray(null);
    this.framebuffer = gl.createFramebuffer()!;
    this.map.triggerRepaint();
  }

  private texture(target: number): WebGLTexture {
    const gl = this.gl!;
    const texture = gl.createTexture()!;
    this.resources.textures.push(texture);
    gl.bindTexture(target, texture);
    gl.texParameteri(target, gl.TEXTURE_MIN_FILTER, gl.NEAREST);
    gl.texParameteri(target, gl.TEXTURE_MAG_FILTER, gl.NEAREST);
    gl.texParameteri(target, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(target, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    return texture;
  }

  private uploadData(): void {
    const gl = this.gl!, cells = this.cells!, data = this.data!;
    const rows = Math.ceil(cells.count / CELL_TEXTURE_WIDTH);
    gl.activeTexture(gl.TEXTURE0);
    gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1);
    gl.pixelStorei(gl.UNPACK_FLIP_Y_WEBGL, false);
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, false);
    if (!this.ordinals) {
      this.ordinals = this.texture(gl.TEXTURE_2D);
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.R16UI, 360, 180, 0, gl.RED_INTEGER, gl.UNSIGNED_SHORT, cells.ordinals);
      this.offsets = this.texture(gl.TEXTURE_2D_ARRAY);
      gl.texStorage3D(gl.TEXTURE_2D_ARRAY, 1, gl.R8UI, 1440, 721, 12);
      this.scores = this.texture(gl.TEXTURE_2D_ARRAY);
      gl.texStorage3D(gl.TEXTURE_2D_ARRAY, 1, gl.RGBA32F, CELL_TEXTURE_WIDTH, rows * SHIFT_LEVELS, 12);
      this.fineScores = this.texture(gl.TEXTURE_2D);
      gl.texStorage2D(gl.TEXTURE_2D, 1, gl.R32F, 1440, 721);
    }
    for (const [month, bytes] of data.offsets) {
      if (this.uploadedOffsets.has(month)) continue;
      gl.bindTexture(gl.TEXTURE_2D_ARRAY, this.offsets!);
      gl.texSubImage3D(gl.TEXTURE_2D_ARRAY, 0, 0, 0, month - 1, 1440, 721, 1, gl.RED_INTEGER, gl.UNSIGNED_BYTE, bytes);
      this.uploadedOffsets.add(month);
    }
    for (const { records } of data.groups) {
      if (this.recordTextures.has(records)) continue;
      const padded = new Uint8Array(CELL_TEXTURE_WIDTH * rows * records.samples * 16);
      for (let cell = 0; cell < cells.count; cell++) {
        for (let day = 0; day < records.samples; day++) {
          const source = (cell * records.samples + day) * 15;
          const target = ((Math.floor(cell / CELL_TEXTURE_WIDTH) * records.samples + day) * CELL_TEXTURE_WIDTH + cell % CELL_TEXTURE_WIDTH) * 16;
          padded.set(records.bytes.subarray(source, source + 15), target);
        }
      }
      const texture = this.texture(gl.TEXTURE_2D);
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8UI, CELL_TEXTURE_WIDTH * 4, rows * records.samples, 0, gl.RGBA_INTEGER, gl.UNSIGNED_BYTE, padded);
      this.recordTextures.set(records, texture);
    }
  }

  private recount(): void {
    const gl = this.gl!, programs = this.programs!, cells = this.cells!, data = this.data!;
    this.uploadData();
    const rows = Math.ceil(cells.count / CELL_TEXTURE_WIDTH);
    gl.bindFramebuffer(gl.FRAMEBUFFER, this.framebuffer!);
    gl.viewport(0, 0, CELL_TEXTURE_WIDTH, rows * SHIFT_LEVELS);
    gl.useProgram(programs.score);
    const scoreUniform = (name: string) => gl.getUniformLocation(programs.score, name);
    gl.uniform1i(scoreUniform('u_records'), 0);
    gl.uniform1i(scoreUniform('u_cell_rows'), rows);
    gl.uniform1i(scoreUniform('u_cell_count'), cells.count);
    const taste = this.taste;
    gl.uniform4f(scoreUniform('u_thermal_taste'), taste.warmShift, taste.heatShift, taste.eveningShift, taste.humidityShift);
    gl.uniform2f(scoreUniform('u_weather_taste'), taste.windShift, taste.rainScale);
    for (let groupIndex = 0; groupIndex < data.groups.length; groupIndex++) {
      const group = data.groups[groupIndex];
      gl.activeTexture(gl.TEXTURE0);
      gl.bindTexture(gl.TEXTURE_2D, this.recordTextures.get(group.records)!);
      gl.uniform1i(scoreUniform('u_samples'), group.records.samples);
      gl.uniform1i(scoreUniform('u_first_day'), group.firstDay);
      gl.uniform1i(scoreUniform('u_day_count'), group.dayCount);
      gl.framebufferTextureLayer(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, this.scores!, 0, groupIndex);
      gl.drawArrays(gl.TRIANGLES, 0, 6);
    }
    gl.viewport(0, 0, 1440, 721);
    gl.useProgram(programs.resolve);
    const resolveUniform = (name: string) => gl.getUniformLocation(programs.resolve, name);
    this.bindTexture(programs.resolve, 'u_scores', 0, gl.TEXTURE_2D_ARRAY, this.scores!);
    this.bindTexture(programs.resolve, 'u_offsets', 1, gl.TEXTURE_2D_ARRAY, this.offsets!);
    this.bindTexture(programs.resolve, 'u_cell_ordinals', 2, gl.TEXTURE_2D, this.ordinals!);
    gl.uniform1i(resolveUniform('u_cell_rows'), rows);
    gl.uniform1i(resolveUniform('u_month_count'), data.groups.length);
    gl.uniform1iv(resolveUniform('u_month_layers'), data.groups.map(group => group.month - 1));
    gl.uniform1fv(resolveUniform('u_weights'), data.groups.map(group => group.weight));
    gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, this.fineScores!, 0);
    gl.drawArrays(gl.TRIANGLES, 0, 6);
  }

  private bindTexture(program: WebGLProgram, uniform: string, unit: number, target: number, texture: WebGLTexture): void {
    const gl = this.gl!;
    gl.activeTexture(gl.TEXTURE0 + unit);
    gl.bindTexture(target, texture);
    gl.uniform1i(gl.getUniformLocation(program, uniform), unit);
  }

  private render(input: CustomRenderMethodInput): void {
    if (!this.gl || !this.programs || !this.quad || !this.cells || !this.data || this.destroyed) return;
    const gl = this.gl;
    const framebuffer = gl.getParameter(gl.FRAMEBUFFER_BINDING) as WebGLFramebuffer | null;
    const viewport = gl.getParameter(gl.VIEWPORT) as Int32Array;
    const states = [gl.BLEND, gl.DEPTH_TEST, gl.STENCIL_TEST, gl.SCISSOR_TEST].map(capability => [capability, gl.isEnabled(capability)] as const);
    gl.bindVertexArray(this.quad);
    for (const [capability] of states) gl.disable(capability);
    const needsRecount = this.drawnRevision !== this.revision;
    try {
      if (needsRecount) this.recount();
      gl.bindFramebuffer(gl.FRAMEBUFFER, framebuffer);
      gl.viewport(viewport[0], viewport[1], viewport[2], viewport[3]);
      // MapLibre has already written opaque water depth; honour it when drawing the map.
      if (states.find(([capability]) => capability === gl.DEPTH_TEST)?.[1]) gl.enable(gl.DEPTH_TEST);
      const program = this.programs.map;
      gl.useProgram(program);
      this.bindTexture(program, 'u_grid', 0, gl.TEXTURE_2D, this.fineScores!);
      gl.uniformMatrix4fv(gl.getUniformLocation(program, 'u_matrix'), false, input.defaultProjectionData.mainMatrix);
      gl.uniform4fv(gl.getUniformLocation(program, 'u_band_colours'), bands.flatMap(band => premultipliedRgba(band.colour)));
      gl.enable(gl.BLEND);
      gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
      const centreWorld = Math.floor((this.map.getCenter().lng + 180) / 360);
      for (const worldOffset of [centreWorld - 1, centreWorld, centreWorld + 1]) {
        gl.uniform1f(gl.getUniformLocation(program, 'u_world_offset'), worldOffset);
        gl.drawArrays(gl.TRIANGLES, 0, 6);
      }
      if (needsRecount) {
        this.drawnRevision = this.revision;
        this.completeFrame(this.pending.splice(0), this.revision, this.period);
      }
    } catch (error) {
      for (const pending of this.pending.splice(0)) pending.reject(error as Error);
      throw error;
    } finally {
      gl.bindFramebuffer(gl.FRAMEBUFFER, framebuffer);
      gl.viewport(viewport[0], viewport[1], viewport[2], viewport[3]);
      for (const [capability, enabled] of states) if (enabled) gl.enable(capability); else gl.disable(capability);
      gl.bindVertexArray(null);
    }
  }

  private completeFrame(pending: PendingFrame[], revision: number, period: Period): void {
    const gl = this.gl!;
    const fence = gl.fenceSync(gl.SYNC_GPU_COMMANDS_COMPLETE, 0)!;
    gl.flush();
    const check = () => {
      if (this.destroyed) {
        for (const frame of pending) frame.reject(new Error('Weather engine has been destroyed'));
        return;
      }
      const status = gl.clientWaitSync(fence, 0, 0);
      if (status === gl.TIMEOUT_EXPIRED) { requestAnimationFrame(check); return; }
      gl.deleteSync(fence);
      if (status === gl.WAIT_FAILED) {
        for (const frame of pending) frame.reject(new Error('Weather GPU frame failed'));
        return;
      }
      const finished = performance.now();
      for (const frame of pending) frame.resolve({ milliseconds: finished - frame.started, period, revision });
      if (pending.length) this.options.onRedraw?.({ milliseconds: finished - pending[pending.length - 1].started, period, revision });
    };
    requestAnimationFrame(check);
  }

  destroy(): void {
    if (this.destroyed) return;
    this.destroyed = true;
    for (const frame of this.pending.splice(0)) frame.reject(new Error('Weather engine has been destroyed'));
    const gl = this.gl;
    if (!gl) return;
    for (const texture of this.resources.textures) gl.deleteTexture(texture);
    for (const buffer of this.resources.buffers) gl.deleteBuffer(buffer);
    if (this.programs) for (const program of Object.values(this.programs)) gl.deleteProgram(program);
    if (this.quad) gl.deleteVertexArray(this.quad);
    if (this.framebuffer) gl.deleteFramebuffer(this.framebuffer);
    this.recordTextures.clear();
    this.fineScoreReadback = undefined;
  }
}

function premultipliedRgba(colour: string): number[] {
  const channel = (index: number) => parseInt(colour.slice(1 + index * 2, 3 + index * 2), 16) / 255;
  const opacity = channel(3);
  return [channel(0) * opacity, channel(1) * opacity, channel(2) * opacity, opacity];
}

function compileProgram(gl: WebGL2RenderingContext, vertex: string, fragment: string): WebGLProgram {
  const program = gl.createProgram()!;
  for (const [type, source] of [[gl.VERTEX_SHADER, vertex], [gl.FRAGMENT_SHADER, fragment]] as const) {
    const shader = gl.createShader(type)!;
    gl.shaderSource(shader, source);
    gl.compileShader(shader);
    if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(shader) ?? 'Weather shader compilation failed');
    gl.attachShader(program, shader);
    gl.deleteShader(shader);
  }
  gl.bindAttribLocation(program, 0, 'a_position');
  gl.linkProgram(program);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(program) ?? 'Weather shader linking failed');
  return program;
}
