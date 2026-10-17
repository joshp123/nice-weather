// Written by AI on 2026.10.17 by model gpt-6.1-sol.
import type { CustomLayerInterface, CustomRenderMethodInput, Map as MapLibreMap } from 'maplibre-gl';
import { bands } from '../palette';
import type { EngineOptions, Period, Point, PointVerdict, RedrawTiming, WeatherEngine } from './index';
import { DEFAULT_TASTE, type Taste } from './taste';
import { WeatherData, type Cells, type PeriodData, type Records, type ScoreImage } from './data';
import { PointSampler, readFineScore, nearestLandFineIndex } from './sampling';
import { FinePlaces } from './finePlaces';
import { finePlaceCandidates } from '../../hosting/sites/fine-candidates.mjs';
import { CELL_TEXTURE_WIDTH, SHIFT_LEVELS, fullscreenVertex, scoringFragment, resolveFragment, mapVertex, mapFragment } from './shaders';

function periodKey(period: Period): string {
  return period.kind === 'months' ? `months:${period.startMonth}:${period.endMonth}` : period.kind === 'month' ? `month:${period.month}` : period.kind === 'season' ? `season:${period.season}` : period.kind === 'forecast-day' ? `forecast-day:${period.day}` : period.kind;
}

type PendingFrame = { started: number; resolve: (timing: RedrawTiming) => void; reject: (error: Error) => void };
type Programs = { score: WebGLProgram; resolve: WebGLProgram; map: WebGLProgram };

export class BrowserWeatherEngine implements WeatherEngine {
  readonly layer: CustomLayerInterface;
  private source: WeatherData;
  private finePlaces: FinePlaces;
  private reportedLoading = false;
  private hasPeriod = false;
  private loadState = false;
  private scoreImage?: ScoreImage;
  private imageTexture?: WebGLTexture;
  private uploadedImage?: ScoreImage;
  private gpuRevision = -1;
  private releaseImageWait?: () => void;
  private periodError?: Error;
  private cells?: Cells;
  private data?: PeriodData;
  private period: Period = { kind: 'month', month: 1 };
  private taste: Taste = DEFAULT_TASTE;
  private sampler?: PointSampler;
  private periodSamplers = new Map<string, PointSampler>();
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
    const dataUrl = (options.dataUrl ?? '/data/v6').replace(/\/$/, '');
    this.source = new WeatherData(dataUrl);
    this.finePlaces = new FinePlaces(dataUrl, () => {
      if (this.destroyed) return;
      this.notifyLoading();
      if (this.hasPeriod) this.options.onRedraw?.({ milliseconds: 0, period: this.period, revision: this.revision });
    });
    this.layer = {
      id: 'weather-v6', type: 'custom', renderingMode: '2d',
      onAdd: (_map, context) => this.onAdd(context),
      render: (_context, input) => this.render(input),
      onRemove: () => this.destroy(),
    };
  }

  private isClimate(period: Period = this.period): boolean { return period.kind !== 'forecast' && period.kind !== 'forecast-day'; }

  get loading(): boolean { return this.loadState || (this.isClimate() && this.finePlaces.loading); }

  private setLoading(loading: boolean): void {
    if (this.loadState === loading) return;
    this.loadState = loading;
    this.notifyLoading();
  }

  private notifyLoading(): void {
    const loading = this.loading;
    if (this.reportedLoading === loading) return;
    this.reportedLoading = loading;
    this.options.onLoadingChange?.(loading);
  }

  private isDefaultTaste(): boolean {
    return (Object.keys(DEFAULT_TASTE) as (keyof Taste)[]).every(key => this.taste[key] === DEFAULT_TASTE[key]);
  }

  async setPeriod(period: Period): Promise<void> {
    this.releaseImageWait?.();
    this.releaseImageWait = undefined;
    const request = ++this.periodRequest;
    this.periodError = undefined;
    this.hasPeriod = true;
    this.period = { ...period };
    this.data = undefined;
    this.sampler = undefined;
    this.scoreImage = undefined;
    this.revision++;
    this.map.triggerRepaint();
    this.setLoading(true);
    try {
      const cachedData = this.cells ? this.source.loaded(period) : null;
      if (!cachedData && this.isDefaultTaste()) {
        const image = await this.source.scoreImage(period);
        if (request !== this.periodRequest || this.destroyed) return;
        if (image) {
          this.scoreImage = image;
          // The frame fence and next animation frame let the small image paint before bulk requests start.
          if (this.isDefaultTaste()) {
            let release!: () => void;
            const interrupted = new Promise<void>(resolve => { release = resolve; });
            this.releaseImageWait = release;
            await Promise.race([this.requestFrame(), interrupted]);
            if (this.releaseImageWait === release) this.releaseImageWait = undefined;
          }
          if (request !== this.periodRequest || this.destroyed) return;
        }
      }
      const [cells, data] = cachedData ? [this.cells!, cachedData] : await Promise.all([this.source.cells, this.source.load(period)]);
      if (request !== this.periodRequest || this.destroyed) return;
      this.cells = cells;
      this.data = data;
      this.sampler = new PointSampler(cells, data, this.period, this.taste);
      this.periodSamplers.set(periodKey(this.period), this.sampler);
      this.setLoading(false);
      if (cachedData && this.isDefaultTaste()) this.loadScoreImage();
      await this.requestFrame();
    } catch (error) {
      if (request !== this.periodRequest || this.destroyed) return;
      this.periodError = error as Error;
      this.setLoading(false);
      for (const frame of this.pending.splice(0)) frame.reject(error as Error);
      throw error;
    }
  }

  setTaste(taste: Taste): Promise<RedrawTiming> {
    this.taste = { ...taste };
    if (!this.isDefaultTaste()) { this.releaseImageWait?.(); this.releaseImageWait = undefined; }
    this.periodSamplers.clear();
    if (this.cells && this.data) this.sampler = new PointSampler(this.cells, this.data, this.period, this.taste);
    if (!this.hasPeriod) return Promise.resolve({ milliseconds: 0, period: this.period, revision: this.revision });
    if (this.isDefaultTaste() && !this.scoreImage) this.loadScoreImage();
    if (this.periodError && !this.isDefaultTaste()) return Promise.reject(this.periodError);
    return this.requestFrame();
  }

  private loadScoreImage(): void {
    const request = this.periodRequest;
    void this.source.scoreImage(this.period).then(image => {
      if (image && request === this.periodRequest && !this.destroyed) {
        this.scoreImage = image;
        if (this.isDefaultTaste()) void this.requestFrame().catch(() => {});
      }
    }).catch(error => console.warn('Score image unavailable', error));
  }

  judgePoint(latitude: number, longitude: number, period?: Period): PointVerdict | null {
    if (this.loadState || this.destroyed) return null;
    const selectedPeriod = period ?? this.period;
    if (this.isClimate(selectedPeriod) && this.finePlaces.enabled) return this.finePlaces.judge(latitude, longitude, selectedPeriod, this.taste);
    if (!period) return this.sampler?.judge(latitude, longitude) ?? null;
    if (!this.cells) return null;
    const key = periodKey(period);
    let sampler = this.periodSamplers.get(key);
    if (!sampler) {
      const data = this.source.loaded(period);
      if (!data) return null;
      sampler = new PointSampler(this.cells, data, period, this.taste);
      this.periodSamplers.set(key, sampler);
    }
    return sampler.judge(latitude, longitude);
  }

  scorePoints(points: readonly Point[]): Float32Array {
    if (this.isClimate() && this.finePlaces.enabled) {
      const candidates = this.fineCandidates(points);
      return Float32Array.from(points, (point, index) => candidates.has(index)
        ? this.finePlaces.judge(point.latitude, point.longitude, this.period, this.taste, true)?.meanScore ?? NaN : NaN);
    }
    return this.overviewScores(points);
  }

  private fineCandidates(points: readonly Point[]): Set<number> {
    if (this.loadState || this.destroyed) return new Set();
    const bounds = this.map.getBounds();
    const labelSlugs = new Set(this.map.queryRenderedFeatures({ layers: ['city-labels'] }).map(feature => String(feature.properties?.slug)));
    return finePlaceCandidates(points, this.overviewScores(points), {
      west: bounds.getWest(), east: bounds.getEast(), south: bounds.getSouth(), north: bounds.getNorth(), labelSlugs,
    });
  }

  private overviewScores(points: readonly Point[]): Float32Array {
    if (this.isDefaultTaste() && this.scoreImage && !this.destroyed) {
      const bytes = this.scoreImage.bytes;
      return Float32Array.from(points, point => {
        const index = nearestLandFineIndex(point.latitude, point.longitude, [bytes]);
        return index === null ? NaN : bytes[index];
      });
    }
    const gl = this.gl;
    if (!this.data || !gl || !this.fineScores || this.gpuRevision !== this.revision || this.drawnRevision !== this.revision || this.destroyed) {
      return Float32Array.from(points, point => this.sampler?.judge(point.latitude, point.longitude)?.meanScore ?? NaN);
    }
    this.readScores();
    const offsets = [...this.data!.offsets.values()];
    return Float32Array.from(points, point => {
      const index = nearestLandFineIndex(point.latitude, point.longitude, offsets);
      return index === null ? NaN : this.fineScoreReadback![index * 4];
    });
  }

  private readScores(): void {
    if (this.readbackRevision === this.drawnRevision) return;
    const gl = this.gl!;
    this.fineScoreReadback ??= new Float32Array(1440 * 721 * 4);
    const previous = gl.getParameter(gl.READ_FRAMEBUFFER_BINDING) as WebGLFramebuffer | null;
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, this.framebuffer!);
    gl.readPixels(0, 0, 1440, 721, gl.RGBA, gl.FLOAT, this.fineScoreReadback);
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, previous);
    this.readbackRevision = this.drawnRevision;
  }

  countPoints(points: readonly Point[]): Float32Array {
    const counts = new Float32Array(points.length * 3).fill(NaN);
    if (this.isClimate() && this.finePlaces.enabled) {
      const candidates = this.fineCandidates(points);
      points.forEach((point, index) => {
        const verdict = candidates.has(index) ? this.finePlaces.judge(point.latitude, point.longitude, this.period, this.taste, true) : null;
        if (verdict) counts.set([verdict.niceDayCount, verdict.perfectDayCount, verdict.awfulDayCount], index * 3);
      });
      return counts;
    }
    if (!this.data || !this.gl || this.gpuRevision !== this.revision || this.drawnRevision !== this.revision || this.destroyed) return counts;
    this.readScores();
    const offsets = [...this.data.offsets.values()];
    points.forEach((point, ordinal) => {
      const index = nearestLandFineIndex(point.latitude, point.longitude, offsets);
      if (index === null) return;
      for (let kind = 0; kind < 3; kind++) counts[ordinal * 3 + kind] = Math.round(this.fineScoreReadback![index * 4 + kind + 1] * this.data!.totalDays * 8) / 8;
    });
    return counts;
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
      gl.texStorage3D(gl.TEXTURE_2D_ARRAY, 1, gl.RGBA32F, CELL_TEXTURE_WIDTH, rows * SHIFT_LEVELS, 48);
      this.fineScores = this.texture(gl.TEXTURE_2D);
      gl.texStorage2D(gl.TEXTURE_2D, 1, gl.RGBA32F, 1440, 721);
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
      for (let pack = 0; pack < 4; pack++) gl.framebufferTextureLayer(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0 + pack, this.scores!, 0, groupIndex * 4 + pack);
      gl.drawBuffers([gl.COLOR_ATTACHMENT0, gl.COLOR_ATTACHMENT1, gl.COLOR_ATTACHMENT2, gl.COLOR_ATTACHMENT3]);
      gl.drawArrays(gl.TRIANGLES, 0, 6);
    }
    for (let pack = 1; pack < 4; pack++) gl.framebufferTextureLayer(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0 + pack, null, 0, 0);
    gl.drawBuffers([gl.COLOR_ATTACHMENT0]);
    gl.viewport(0, 0, 1440, 721);
    gl.useProgram(programs.resolve);
    const resolveUniform = (name: string) => gl.getUniformLocation(programs.resolve, name);
    this.bindTexture(programs.resolve, 'u_scores', 0, gl.TEXTURE_2D_ARRAY, this.scores!);
    this.bindTexture(programs.resolve, 'u_offsets', 1, gl.TEXTURE_2D_ARRAY, this.offsets!);
    this.bindTexture(programs.resolve, 'u_cell_ordinals', 2, gl.TEXTURE_2D, this.ordinals!);
    gl.uniform1i(resolveUniform('u_cell_rows'), rows);
    gl.uniform1i(resolveUniform('u_month_count'), data.groups.length);
    gl.uniform1iv(resolveUniform('u_month_layers'), data.groups.map(group => group.month - 1));
    gl.uniform1iv(resolveUniform('u_day_counts'), data.groups.map(group => group.dayCount));
    gl.uniform1i(resolveUniform('u_single_day'), this.period.kind === 'forecast-day' ? 1 : 0);
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
    if (!this.gl || !this.programs || !this.quad || this.destroyed) return;
    const image = this.isDefaultTaste() ? this.scoreImage : undefined;
    if (!image && (!this.cells || !this.data)) {
      // Notify labels when a moved taste clears an already-painted image. Taste promises still wait for records.
      if (this.scoreImage && this.drawnRevision !== this.revision) {
        this.drawnRevision = this.revision;
        this.completeFrame([], this.revision, this.period, this.pending[this.pending.length - 1]?.started ?? performance.now());
      }
      return;
    }
    const gl = this.gl;
    const framebuffer = gl.getParameter(gl.FRAMEBUFFER_BINDING) as WebGLFramebuffer | null;
    const viewport = gl.getParameter(gl.VIEWPORT) as Int32Array;
    const states = [gl.BLEND, gl.DEPTH_TEST, gl.STENCIL_TEST, gl.SCISSOR_TEST].map(capability => [capability, gl.isEnabled(capability)] as const);
    gl.bindVertexArray(this.quad);
    for (const [capability] of states) gl.disable(capability);
    const needsRecount = this.drawnRevision !== this.revision;
    try {
      if (this.data && this.gpuRevision !== this.revision) {
        this.recount();
        this.gpuRevision = this.revision;
      }
      if (image && this.uploadedImage !== image) {
        gl.activeTexture(gl.TEXTURE0);
        this.imageTexture ??= this.texture(gl.TEXTURE_2D);
        gl.bindTexture(gl.TEXTURE_2D, this.imageTexture);
        gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1);
        gl.pixelStorei(gl.UNPACK_FLIP_Y_WEBGL, false);
        gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, false);
        gl.texImage2D(gl.TEXTURE_2D, 0, gl.R8, 1440, 721, 0, gl.RED, gl.UNSIGNED_BYTE, image.bytes);
        this.uploadedImage = image;
      }
      gl.bindFramebuffer(gl.FRAMEBUFFER, framebuffer);
      gl.viewport(viewport[0], viewport[1], viewport[2], viewport[3]);
      // MapLibre has already written opaque water depth; honour it when drawing the map.
      if (states.find(([capability]) => capability === gl.DEPTH_TEST)?.[1]) gl.enable(gl.DEPTH_TEST);
      const program = this.programs.map;
      gl.useProgram(program);
      this.bindTexture(program, 'u_grid', 0, gl.TEXTURE_2D, image ? this.imageTexture! : this.fineScores!);
      gl.uniform1f(gl.getUniformLocation(program, 'u_grid_scale'), image ? 255 : 1);
      gl.uniform1i(gl.getUniformLocation(program, 'u_round_scores'), this.isDefaultTaste() ? 1 : 0);
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

  private completeFrame(pending: PendingFrame[], revision: number, period: Period, clearedImageStarted?: number): void {
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
      const started = pending[pending.length - 1]?.started ?? clearedImageStarted;
      if (started !== undefined && revision === this.revision && !this.periodError) this.options.onRedraw?.({ milliseconds: finished - started, period, revision });
    };
    requestAnimationFrame(check);
  }

  destroy(): void {
    if (this.destroyed) return;
    this.destroyed = true;
    this.finePlaces.destroy();
    this.releaseImageWait?.();
    this.releaseImageWait = undefined;
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
