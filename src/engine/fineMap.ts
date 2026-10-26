// Written by AI on 2026.10.19 by model GPT-6.1 Sol.
import type {CustomRenderMethodInput,Map as MapLibreMap} from 'maplibre-gl';
import {decodeFineMapTile,fineMapNumbers,fineMapPointNumbers,fineMapTileAddress,FINE_MAP_TILE_SIZE,FINE_MAP_THRESHOLDS,type FineMapConfiguration,type FineMapTile} from '../../hosting/sites/fine-map.mjs';
import type {Period} from './index';
import {climateMonths} from './data';
import {BANDS} from './taste';
import {splineFunctions} from './shaders';

const MONTH_DAYS=[31,28,31,30,31,30,31,31,30,31,30,31];
type LoadedTile={tile:FineMapTile|null;texture?:WebGLTexture;scoreTexture?:WebGLTexture;scorePeriod?:string};

/** Only the 1 km pixels in a close view reach the browser; month changes reuse them. */
export class FineMap {
  private configuration?:FineMapConfiguration|null;
  private configurationRequest?:Promise<void>;
  private tiles=new Map<string,LoadedTile>();
  private requested=new Set<string>();
  private visible=new Set<string>();
  private selectedTile?:string;
  private queue:{row:number;column:number;key:string}[]=[];
  private active=0;
  private program?:WebGLProgram;
  private resolveProgram?:WebGLProgram;
  private framebuffer?:WebGLFramebuffer;
  private destroyed=false;
  private enabled=false;
  private period:Period={kind:'year'};

  constructor(private map:MapLibreMap,private endpoint:string,private changed:()=>void) {}

  setPeriod(period:Period,defaultTaste:boolean):void {
    this.period=period;
    this.enabled=defaultTaste&&period.kind!=='forecast'&&period.kind!=='forecast-day';
    this.prepare();
  }

  private get minimumZoom():number {
    const container=this.map.getContainer();
    return 8+Math.max(0,Math.log2(Math.max(container.clientWidth/1920,container.clientHeight/1080)));
  }

  get activeAtZoom():boolean {return this.enabled&&this.map.getZoom()>=this.minimumZoom&&!!this.configuration;}
  get loadingAtZoom():boolean {return this.enabled&&this.map.getZoom()>=this.minimumZoom&&this.configuration===undefined;}

  prepare():void {
    if(this.destroyed||!this.enabled||this.map.getZoom()<this.minimumZoom)return;
    if(!this.configurationRequest) {
      this.configurationRequest=fetch(this.endpoint).then(async response=> {
        if(!response.ok)throw Error(`Fine map configuration: HTTP ${response.status}`);
        this.configuration=(await response.json()).configuration??null;
      }).catch(error=>{this.configuration=null;console.warn('Fine map unavailable',error);}).finally(()=>{this.prepare();this.changed();});
      return;
    }
    if(!this.configuration)return;
    const bounds=this.map.getBounds(),west=bounds.getWest(),east=bounds.getEast();
    const north=Math.min(85.051129,bounds.getNorth()),south=Math.max(-85.051129,bounds.getSouth());
    const firstRow=Math.floor((90-north)*120/FINE_MAP_TILE_SIZE),lastRow=Math.min(84,Math.floor((90-south)*120/FINE_MAP_TILE_SIZE));
    this.visible.clear();
    for(let world=Math.floor((west+180)/360);world<=Math.floor((east+180)/360);world++) {
      const segmentWest=Math.max(-180,west-world*360),segmentEast=Math.min(180,east-world*360);
      const firstColumn=Math.max(0,Math.floor((segmentWest+180)*120/FINE_MAP_TILE_SIZE));
      const lastColumn=Math.min(168,Math.floor((segmentEast+180)*120/FINE_MAP_TILE_SIZE));
      for(let row=firstRow;row<=lastRow;row++)for(let column=firstColumn;column<=lastColumn;column++) {
        const key=`${row}-${column}`;
        this.visible.add(key);
        if(!this.requested.has(key)) {this.requested.add(key);this.queue.push({row,column,key});}
      }
    }
    this.drain();
  }

  private drain():void {
    if(!this.enabled||this.map.getZoom()<this.minimumZoom) {
      for(const entry of this.queue)this.requested.delete(entry.key);
      this.queue=[];
      return;
    }
    while(!this.destroyed&&this.active<4&&this.queue.length) {
      const entry=this.queue.shift()!;
      if(!this.visible.has(entry.key)&&entry.key!==this.selectedTile){this.requested.delete(entry.key);continue;}
      this.active++;
      void fetch(`${this.endpoint}/tile/${this.configuration!.generation}/${entry.row}/${entry.column}`).then(async response=> {
        if(response.status===204||response.status===404){this.tiles.set(entry.key,{tile:null});return;}
        if(!response.ok)throw Error(`Fine map tile: HTTP ${response.status}`);
        const bytes=new Uint8Array(await response.arrayBuffer());
        this.tiles.set(entry.key,{tile:decodeFineMapTile(bytes,this.configuration!)});
      }).catch(error=>{this.requested.delete(entry.key);console.warn('Fine map tile unavailable',error);}).finally(()=> {
        this.active--;this.drain();if(!this.destroyed){this.map.triggerRepaint();this.changed();}
      });
    }
  }

  numbers(latitude:number,longitude:number,period:Period=this.period):ReturnType<typeof fineMapNumbers>|undefined {
    if(!this.activeAtZoom||period.kind==='forecast'||period.kind==='forecast-day')return undefined;
    const address=fineMapTileAddress(latitude,longitude),entry=this.tiles.get(`${address.tileRow}-${address.tileColumn}`);
    return entry?.tile?fineMapPointNumbers(entry.tile,latitude,longitude,period):undefined;
  }

  preparePoint(latitude:number,longitude:number):void {
    if(!this.activeAtZoom)return;
    const address=fineMapTileAddress(latitude,longitude),key=`${address.tileRow}-${address.tileColumn}`;
    this.selectedTile=key;
    if(!this.requested.has(key)) {
      this.requested.add(key);
      this.queue.unshift({row:address.tileRow,column:address.tileColumn,key});
      this.drain();
    }
  }

  pointLoading(latitude:number,longitude:number):boolean {
    if(!this.activeAtZoom)return false;
    const address=fineMapTileAddress(latitude,longitude);
    return !this.tiles.has(`${address.tileRow}-${address.tileColumn}`);
  }

  render(gl:WebGL2RenderingContext,input:CustomRenderMethodInput,colours:readonly number[],landColour:readonly number[],edgePixels:number):void {
    this.prepare();
    if(!this.activeAtZoom)return;
    this.program??=compile(gl,vertex,fragment);
    this.resolveProgram??=compile(gl,resolveVertex,resolveFragment);
    this.framebuffer??=gl.createFramebuffer()!;
    const framebuffer=gl.getParameter(gl.FRAMEBUFFER_BINDING) as WebGLFramebuffer|null;
    const viewport=gl.getParameter(gl.VIEWPORT) as Int32Array;
    const depthTest=gl.isEnabled(gl.DEPTH_TEST);
    gl.useProgram(this.program);
    const uniform=(name:string)=>gl.getUniformLocation(this.program!,name);
    gl.uniformMatrix4fv(uniform('u_matrix'),false,input.defaultProjectionData.mainMatrix);
    gl.uniform4fv(uniform('u_band_colours'),colours);
    gl.uniform4fv(uniform('u_land_colour'),landColour);
    gl.uniform1f(uniform('u_edge_pixels'),edgePixels);
    const monthDays=Array(12).fill(0);
    if(this.period.kind==='forecast'||this.period.kind==='forecast-day')return;
    const months=climateMonths(this.period);
    for(const month of months)monthDays[month-1]=MONTH_DAYS[month-1];
    const scorePeriod=months.join(',');
    gl.uniform1i(uniform('u_scores'),0);
    gl.activeTexture(gl.TEXTURE0);
    const centreWorld=Math.floor((this.map.getCenter().lng+180)/360);
    for(const key of this.visible) {
      const entry=this.tiles.get(key);if(!entry?.tile)continue;
      const tile=entry.tile;
      if(!entry.texture) {
        entry.texture=gl.createTexture()!;
        gl.bindTexture(gl.TEXTURE_2D_ARRAY,entry.texture);
        gl.texParameteri(gl.TEXTURE_2D_ARRAY,gl.TEXTURE_MIN_FILTER,gl.NEAREST);
        gl.texParameteri(gl.TEXTURE_2D_ARRAY,gl.TEXTURE_MAG_FILTER,gl.NEAREST);
        gl.texParameteri(gl.TEXTURE_2D_ARRAY,gl.TEXTURE_WRAP_S,gl.CLAMP_TO_EDGE);
        gl.texParameteri(gl.TEXTURE_2D_ARRAY,gl.TEXTURE_WRAP_T,gl.CLAMP_TO_EDGE);
        gl.pixelStorei(gl.UNPACK_ALIGNMENT,1);
        gl.texImage3D(gl.TEXTURE_2D_ARRAY,0,gl.RGB8UI,tile.size,tile.size,12,0,gl.RGB_INTEGER,gl.UNSIGNED_BYTE,tile.counts);
      }
      if(entry.scorePeriod!==scorePeriod) {
        entry.scoreTexture??=gl.createTexture()!;
        gl.bindTexture(gl.TEXTURE_2D,entry.scoreTexture);
        gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_MIN_FILTER,gl.NEAREST);
        gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_MAG_FILTER,gl.NEAREST);
        gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_WRAP_S,gl.CLAMP_TO_EDGE);
        gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_WRAP_T,gl.CLAMP_TO_EDGE);
        gl.texImage2D(gl.TEXTURE_2D,0,gl.R32F,tile.size,tile.size,0,gl.RED,gl.FLOAT,null);
        gl.bindFramebuffer(gl.FRAMEBUFFER,this.framebuffer);
        gl.framebufferTexture2D(gl.FRAMEBUFFER,gl.COLOR_ATTACHMENT0,gl.TEXTURE_2D,entry.scoreTexture,0);
        gl.viewport(0,0,tile.size,tile.size);
        gl.disable(gl.BLEND);gl.disable(gl.DEPTH_TEST);
        gl.useProgram(this.resolveProgram);
        gl.uniform1iv(gl.getUniformLocation(this.resolveProgram,'u_month_days'),monthDays);
        gl.uniform1i(gl.getUniformLocation(this.resolveProgram,'u_counts'),0);
        gl.bindTexture(gl.TEXTURE_2D_ARRAY,entry.texture!);
        gl.drawArrays(gl.TRIANGLES,0,6);
        gl.bindFramebuffer(gl.FRAMEBUFFER,framebuffer);
        gl.viewport(viewport[0],viewport[1],viewport[2],viewport[3]);
        gl.enable(gl.BLEND);
        if(depthTest)gl.enable(gl.DEPTH_TEST);
        gl.useProgram(this.program);
        entry.scorePeriod=scorePeriod;
      }
      gl.bindTexture(gl.TEXTURE_2D,entry.scoreTexture!);
      const west=tile.column*FINE_MAP_TILE_SIZE/43200,east=Math.min(1,(tile.column+1)*FINE_MAP_TILE_SIZE/43200);
      const north=Math.min(85.051129,90-tile.row*FINE_MAP_TILE_SIZE/120),south=Math.max(-85.051129,90-(tile.row+1)*FINE_MAP_TILE_SIZE/120);
      gl.uniform4f(uniform('u_bounds'),west,mercatorY(north),east,mercatorY(south));
      gl.uniform2i(uniform('u_pixel_origin'),tile.column*FINE_MAP_TILE_SIZE-tile.halo,tile.row*FINE_MAP_TILE_SIZE-tile.halo);
      for(const worldOffset of [centreWorld-1,centreWorld,centreWorld+1]) {
        gl.uniform1f(uniform('u_world_offset'),worldOffset);
        gl.drawArrays(gl.TRIANGLES,0,6);
      }
    }
    for(const [key,entry]of this.tiles)if(this.tiles.size>32&&!this.visible.has(key)&&key!==this.selectedTile) {
      if(entry.texture)gl.deleteTexture(entry.texture);
      if(entry.scoreTexture)gl.deleteTexture(entry.scoreTexture);
      this.tiles.delete(key);this.requested.delete(key);
    }
  }

  destroy(gl?:WebGL2RenderingContext):void {
    this.destroyed=true;this.queue=[];
    if(gl){for(const entry of this.tiles.values()){if(entry.texture)gl.deleteTexture(entry.texture);if(entry.scoreTexture)gl.deleteTexture(entry.scoreTexture);}if(this.program)gl.deleteProgram(this.program);if(this.resolveProgram)gl.deleteProgram(this.resolveProgram);if(this.framebuffer)gl.deleteFramebuffer(this.framebuffer);}
    this.tiles.clear();
  }
}

const mercatorY=(latitude:number)=>(1-Math.log(Math.tan(Math.PI/4+latitude*Math.PI/360))/Math.PI)/2;
const vertex=`#version 300 es
in vec2 a_position;
uniform mat4 u_matrix;
uniform vec4 u_bounds;
uniform float u_world_offset;
out vec2 v_mercator;
void main(){v_mercator=mix(u_bounds.xy,u_bounds.zw,a_position);gl_Position=u_matrix*vec4(v_mercator.x+u_world_offset,v_mercator.y,0.,1.);}`;
const fragment=`#version 300 es
precision highp float;
precision highp int;
uniform highp sampler2D u_scores;
uniform ivec2 u_pixel_origin;
uniform vec4 u_band_colours[6];
uniform vec4 u_land_colour;
uniform float u_edge_pixels;
in vec2 v_mercator;
out vec4 fragColour;
${splineFunctions}
void main(){
 float latitude=degrees(atan(sinh(3.141592653589793*(1.-2.*v_mercator.y))));
 vec2 cell=vec2(v_mercator.x*43200.,(90.-latitude)*120.)-.5-vec2(u_pixel_origin);
 ivec2 base=ivec2(floor(cell));
 vec4 wx=cubicBSplineWeights(fract(cell.x)),wy=cubicBSplineWeights(fract(cell.y));
 vec2 weighted=vec2(0.);
 ivec2 maximum=textureSize(u_scores,0)-ivec2(1);
 for(int row=0;row<4;row++)for(int column=0;column<4;column++){
  float value=texelFetch(u_scores,clamp(base+ivec2(column-1,row-1),ivec2(0),maximum),0).r;
  if(value>100.5)continue;
  weighted+=vec2(value,1.)*wx[column]*wy[row];
 }
 if(weighted.y<.00001)discard;
 float score=floor(weighted.x/weighted.y*10000.+.5)/10000.;
 float edgeWidth=max(fwidth(score)*u_edge_pixels,.02);
 vec4 colour=u_band_colours[0];float contour=0.;
 float minimums[6]=float[6](${BANDS.map(band=>`${band.minimum}.`).join(',')});
 for(int band=1;band<6;band++){
  float distanceToEdge=score-minimums[band];
  colour=mix(colour,u_band_colours[band],smoothstep(-edgeWidth,edgeWidth,distanceToEdge));
  contour=max(contour,1.-smoothstep(0.,edgeWidth*2.,abs(distanceToEdge)));
 }
 colour.rgb*=1.-.14*contour;
 float landCoverage=smoothstep(0.,.02,weighted.y);
 vec3 composite=colour.rgb+u_land_colour.rgb*(1.-colour.a);
 fragColour=vec4(composite*landCoverage,landCoverage);
}`;
const resolveVertex=`#version 300 es
in vec2 a_position;
void main(){gl_Position=vec4(a_position*2.-1.,0.,1.);}`;
const resolveFragment=`#version 300 es
precision highp float;
precision highp int;
uniform highp usampler2DArray u_counts;
uniform int u_month_days[12];
out float resolvedScore;
void main(){
 ivec2 pixel=ivec2(gl_FragCoord.xy);
 int weightedCounts[7]=int[7](0,0,0,0,0,0,0);
 for(int month=0;month<12;month++){
  if(u_month_days[month]==0)continue;
  uvec3 counts=texelFetch(u_counts,ivec3(pixel,month),0).rgb;
  if(counts.r==255u){resolvedScore=255.;return;}
  weightedCounts[0]+=u_month_days[month]*8;
  for(int threshold=0;threshold<6;threshold++)weightedCounts[threshold+1]+=int((counts[threshold/2]>>uint(4*(threshold%2)))&15u)*u_month_days[month];
 }
 float thresholds[7]=float[7](0.,${FINE_MAP_THRESHOLDS.map(value=>`${value}.`).join(',')});
 float score=0.;
 for(int band=5;band>=0;band--){
  if(weightedCounts[band]*2<weightedCounts[0])continue;
  score=band==5?max(85.,85.+(float(weightedCounts[5])/float(weightedCounts[0])-.5)*30.):max(thresholds[band],mix(thresholds[band],thresholds[band+1],(float(weightedCounts[band])-.5*float(weightedCounts[0]))/float(weightedCounts[band]-weightedCounts[band+1])));break;
 }
 resolvedScore=floor(score*10000.+.5)/10000.;
}`;

function compile(gl:WebGL2RenderingContext,vertex:string,fragment:string):WebGLProgram {
 const program=gl.createProgram()!;
 for(const [type,source]of [[gl.VERTEX_SHADER,vertex],[gl.FRAGMENT_SHADER,fragment]]as const){
  const shader=gl.createShader(type)!;gl.shaderSource(shader,source);gl.compileShader(shader);
  if(!gl.getShaderParameter(shader,gl.COMPILE_STATUS))throw Error(gl.getShaderInfoLog(shader)??'Fine map shader failed');
  gl.attachShader(program,shader);gl.deleteShader(shader);
 }
 gl.bindAttribLocation(program,0,'a_position');gl.linkProgram(program);
 if(!gl.getProgramParameter(program,gl.LINK_STATUS))throw Error(gl.getProgramInfoLog(program)??'Fine map program failed');
 return program;
}
