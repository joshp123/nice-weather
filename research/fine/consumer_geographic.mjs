// Written by AI on 2026.10.17 by model GPT-6.1 Sol.
// Actual geographic SF fixture; no src edits, basemap, full-period facts or phone hardware claims.
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import http from 'node:http';
import {spawn} from 'node:child_process';
const directory=process.argv[2];
if(!directory)throw Error('Usage: devenv shell -- node research/fine/consumer_geographic.mjs <geographic-output>');
const data=JSON.parse(await fs.readFile(path.join(directory,'geographic-inputs.json'),'utf8'));
const payloads=new Map();
for(const entry of data.cases){
 for(const name of ['carrier','availability']){const url='/'+entry.prefix+'-'+name+'.gz';payloads.set(url,await fs.readFile(entry.files[name].path));entry.files[name].url=url;}
 for(const tile of entry.actual_tile_transfers){const url='/tile/'+tile.month+'/'+tile.tile_row+'/'+tile.tile_column;payloads.set(url,await fs.readFile(tile.path));tile.url=url;}
}
const vertex=`#version 300 es
in vec2 p;void main(){gl_Position=vec4(p,0,1);}`;
const displayFragment=`#version 300 es
precision highp float;uniform sampler2D image;uniform vec2 viewExtent;uniform vec2 mercatorCenter;uniform float worldPixels;uniform ivec2 fineOrigin;out vec4 color;
void main(){vec2 screen=vec2(gl_FragCoord.x,viewExtent.y-gl_FragCoord.y);vec2 merc=mercatorCenter+(screen-viewExtent*.5)/worldPixels;float latitude=degrees(atan(sinh(3.141592653589793*(1.-2.*merc.y))));ivec2 pixel=ivec2(floor(vec2(merc.x*43200.,(90.-latitude)*120.)))-fineOrigin;vec4 score=texelFetch(image,pixel,0);color=score.a==0.?vec4(.08,.12,.17,1.):vec4(vec3(score.r),1.);}`;
const fragment=`#version 300 es
precision highp float;precision highp int;
uniform highp usampler2DArray carrier;
uniform highp isampler2DArray offsets;uniform highp usampler2D availability;uniform ivec2 fineOrigin;uniform ivec2 quarterOrigin;
uniform int nx;uniform int ny;uniform int recordBytes;uniform int monthCount;uniform float tasteShift;
out vec4 colour;
float rawByte(int field,int day,int month,ivec2 point){return float(texelFetch(carrier,ivec3(point.x*recordBytes+field,point.y*8+day,month),0).r);}
float signedTenths(int offset,int day,int month,ivec2 point){int bits=int(rawByte(offset,day,month,point))+256*int(rawByte(offset+1,day,month,point));return float(bits>=32768?bits-65536:bits)/10.;}
float byteAt(int field,int day,int month,ivec2 point){if(recordBytes!=60)return rawByte(field,day,month,point);if(field<10)return (signedTenths(field*2,day,month,point)+30.)*3.;return rawByte(field+9,day,month,point);}
float aux(int field,int day,int month,ivec2 point){
 if(recordBytes==33)return byteAt(15+field,day,month,point)/3.-30.;
 return signedTenths((recordBytes==60?24:15)+field*2,day,month,point);}
float profile(int field,int hour,int day,int month,ivec2 point){
 int hours[8]=int[8](10,12,14,15,16,18,20,21);
 for(int j=0;j<7;j++)if(hour<=hours[j+1])return mix(aux(field+j,day,month,point),aux(field+j+1,day,month,point),float(hour-hours[j])/float(hours[j+1]-hours[j]));
 return aux(field+7,day,month,point);}
float vap(float t){return 6.105*exp(17.27*t/(237.7+t));}
vec2 correctionAt(int hour,int day,int month,ivec2 point,vec2 correction){
 float high=aux(16,day,month,point),low=aux(17,day,month,point),width=high-low;
 float air=profile(0,hour,day,month,point),dew=profile(8,hour,day,month,point);
 float delta=width==0.||width+correction.x-correction.y<=0.?(correction.x+correction.y)*.5:mix(correction.y,correction.x,width==0.?.5:(air-low)/width);
 return vec2(delta+.33*(vap(min(dew,air+delta))-vap(min(dew,air))),min(dew,air+delta));}
float curve(float x,int kind){
 if(kind==0){float p[8]=float[8](9.,11.,13.,15.,17.,19.,21.,23.);float v[8]=float[8](0.,3.,10.,22.,40.,62.,85.,100.);for(int j=1;j<8;j++)if(x<=p[j])return mix(v[j-1],v[j],clamp((x-p[j-1])/(p[j]-p[j-1]),0.,1.));return 100.;}
 if(kind==1){float p[6]=float[6](28.,31.,34.,37.,40.,43.);float v[6]=float[6](100.,85.,60.,30.,10.,0.);for(int j=1;j<6;j++)if(x<=p[j])return mix(v[j-1],v[j],clamp((x-p[j-1])/(p[j]-p[j-1]),0.,1.));return 0.;}
 float p[5]=float[5](4.,8.,12.,16.,19.);float v[5]=float[5](0.,30.,65.,90.,100.);for(int j=1;j<5;j++)if(x<=p[j])return mix(v[j-1],v[j],clamp((x-p[j-1])/(p[j]-p[j-1]),0.,1.));return 100.;}
float factor(float x,int kind){
 if(kind==0){float p[7]=float[7](0.,.5,1.,2.,4.,8.,15.);float v[7]=float[7](1.,.95,.88,.74,.5,.22,.05);for(int j=1;j<7;j++)if(x<=p[j])return mix(v[j-1],v[j],clamp((x-p[j-1])/(p[j]-p[j-1]),0.,1.));return .05;}
 if(kind==1){float p[7]=float[7](15.,20.,25.,30.,40.,50.,65.);float v[7]=float[7](1.,.97,.92,.85,.65,.4,.15);for(int j=1;j<7;j++)if(x<=p[j])return mix(v[j-1],v[j],clamp((x-p[j-1])/(p[j]-p[j-1]),0.,1.));return .15;}
 if(kind==2){float p[7]=float[7](16.,18.,20.,22.,24.,26.,28.);float v[7]=float[7](1.,.95,.85,.65,.4,.2,.1);for(int j=1;j<7;j++)if(x<=p[j])return mix(v[j-1],v[j],clamp((x-p[j-1])/(p[j]-p[j-1]),0.,1.));return .1;}
 return x<=.3?mix(.75,.85,clamp(x/.3,0.,1.)):x<=.6?mix(.85,.95,(x-.3)/.3):mix(.95,1.,clamp((x-.6)/.2,0.,1.));}
void main(){
 ivec2 pixel=ivec2(gl_FragCoord.xy),point=ivec2(floor((vec2(pixel+fineOrigin)+.5)/30.+.5))-quarterOrigin;float total=0.;if(texelFetch(availability,point,0).r==0u){colour=vec4(0.);return;}
 for(int month=0;month<12;month++){if(month>=monthCount)break;
 ivec2 encoded=texelFetch(offsets,ivec3(pixel,month),0).rg;if(any(equal(encoded,ivec2(-32768)))){colour=vec4(0.);return;}vec2 correction=vec2(encoded)/10.;
 for(int day=0;day<8;day++){
 float daylight=max(byteAt(14,day,month,point),1.),thermal=0.,dewSum=0.;
 for(int h=0;h<11;h++){vec2 corrected=correctionAt(h+10,day,month,point,correction);dewSum+=corrected.y;if(float(h)>=daylight)continue;
 float sun=mix(byteAt(h/2,day,month,point),byteAt(min(h/2+1,5),day,month,point),float(h%2)/2.)/3.-30.;
 float shadePosition=clamp(float(h-2)/3.,0.,3.);int si=min(int(shadePosition),2);
 float shade=mix(byteAt(6+si,day,month,point),byteAt(7+si,day,month,point),shadePosition-float(si))/3.-30.;
 float delta=corrected.x;
 thermal+=min(curve(sun+delta-tasteShift,0),curve(shade+delta,1));}
 vec2 at21=correctionAt(21,day,month,point,correction);dewSum+=at21.y;float evening=byteAt(9,day,month,point)/3.-30.+at21.x;
 thermal=(thermal+2.*min(curve(evening,2),curve(evening,1)))/(daylight+2.);
 total+=thermal*factor(pow(byteAt(11,day,month,point)/50.,2.),0)*factor(byteAt(12,day,month,point)/2.,1)*factor(dewSum/12.,2)*factor(byteAt(13,day,month,point)/daylight,3);
 }}
 float score=total/float(monthCount*8);
 colour=vec4(score/100.,score/100.,score/100.,1.);
}`;

const html=`<!doctype html><meta charset="utf-8"><style>body{margin:0}canvas{display:block}</style><canvas id="c"></canvas><script>
window.run=async function(entry){
 const benchmarkStart=performance.now();
 const {width,height,view_width:viewWidth,view_height:viewHeight,nx,ny}=entry;
 const c=document.querySelector('canvas');c.width=viewWidth;c.height=viewHeight;
 const gl=c.getContext('webgl2',{antialias:false,preserveDrawingBuffer:true});if(!gl)throw Error('WebGL2 unavailable');
 const debug=gl.getExtension('WEBGL_debug_renderer_info');const resources=[];
 const compile=(kind,source)=>{const shader=gl.createShader(kind);gl.shaderSource(shader,source);gl.compileShader(shader);if(!gl.getShaderParameter(shader,gl.COMPILE_STATUS))throw Error(gl.getShaderInfoLog(shader));return shader;};
 const compileStart=performance.now();const p=gl.createProgram();gl.attachShader(p,compile(gl.VERTEX_SHADER,${JSON.stringify(vertex)}));gl.attachShader(p,compile(gl.FRAGMENT_SHADER,${JSON.stringify(fragment)}));gl.linkProgram(p);if(!gl.getProgramParameter(p,gl.LINK_STATUS))throw Error(gl.getProgramInfoLog(p));gl.useProgram(p);const compileMs=performance.now()-compileStart;
 gl.bindBuffer(gl.ARRAY_BUFFER,gl.createBuffer());gl.bufferData(gl.ARRAY_BUFFER,new Float32Array([-1,-1,1,-1,-1,1,-1,1,1,-1,1,1]),gl.STATIC_DRAW);let loc=gl.getAttribLocation(p,'p');gl.enableVertexAttribArray(loc);gl.vertexAttribPointer(loc,2,gl.FLOAT,false,0,0);
 const transfers=[];
 async function load(url,category){const start=performance.now();const response=await fetch(url);const raw=await response.arrayBuffer();const fetched=performance.now();const decoded=await new Response(new Blob([raw]).stream().pipeThrough(new DecompressionStream('gzip'))).arrayBuffer();transfers.push({category,url,compressedBytes:raw.byteLength,decodedBytes:decoded.byteLength,fetchMs:fetched-start,decodeMs:performance.now()-fetched});return decoded;}
 const carrier=await load(entry.files.carrier.url,'carrier'),available=await load(entry.files.availability.url,'availability');
 const fine=new Int16Array(width*height*2*12);fine.fill(-32768);const support=new Uint8Array(width*height*12);
 let tileAssemblyMs=0;
 for(const tile of entry.actual_tile_transfers){const raw=await load(tile.url,'actual NWFT tile');const start=performance.now();const view=new DataView(raw);if(view.getUint32(0,true)!==0x5446574e||view.getUint16(4,true)!==1)throw Error('Invalid NWFT header');
 const tr=view.getUint16(8,true),tc=view.getUint16(10,true),month=view.getUint8(6);if(tr!==tile.tile_row||tc!==tile.tile_column||month!==tile.month)throw Error('NWFT addressing differs');
 const values=new Int16Array(raw,32,256*256*2),flags=new Uint8Array(raw,32+256*256*4,256*256);const x=tc*256-entry.fine_origin[1],y=tr*256-entry.fine_origin[0];
 for(let row=0;row<256;row++){const target=((month-1)*height+y+row)*width+x;fine.set(values.subarray(row*512,(row+1)*512),target*2);support.set(flags.subarray(row*256,(row+1)*256),target);}tileAssemblyMs+=performance.now()-start;}
 async function completed(){const fence=gl.fenceSync(gl.SYNC_GPU_COMMANDS_COMPLETE,0);gl.flush();let status;do{await new Promise(resolve=>setTimeout(resolve,0));status=gl.clientWaitSync(fence,0,0);if(status===gl.WAIT_FAILED)throw Error('GPU fence failed');}while(status===gl.TIMEOUT_EXPIRED);gl.deleteSync(fence);}
 const uploadStart=performance.now();
 function texture(unit,internal,w,h,depth,format,type,bytes){gl.activeTexture(gl.TEXTURE0+unit);const texture=gl.createTexture();resources.push(texture);gl.bindTexture(gl.TEXTURE_2D_ARRAY,texture);gl.texParameteri(gl.TEXTURE_2D_ARRAY,gl.TEXTURE_MIN_FILTER,gl.NEAREST);gl.texParameteri(gl.TEXTURE_2D_ARRAY,gl.TEXTURE_MAG_FILTER,gl.NEAREST);gl.pixelStorei(gl.UNPACK_ALIGNMENT,1);gl.texImage3D(gl.TEXTURE_2D_ARRAY,0,internal,w,h,depth,0,format,type,bytes);}
 texture(0,gl.R8UI,nx*60,ny*8,12,gl.RED_INTEGER,gl.UNSIGNED_BYTE,new Uint8Array(carrier));texture(1,gl.RG16I,width,height,12,gl.RG_INTEGER,gl.SHORT,fine);texture(3,gl.R8UI,width,height,12,gl.RED_INTEGER,gl.UNSIGNED_BYTE,support);
 gl.activeTexture(gl.TEXTURE2);const availability=gl.createTexture();resources.push(availability);gl.bindTexture(gl.TEXTURE_2D,availability);gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_MIN_FILTER,gl.NEAREST);gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_MAG_FILTER,gl.NEAREST);gl.texImage2D(gl.TEXTURE_2D,0,gl.R8UI,nx,ny,0,gl.RED_INTEGER,gl.UNSIGNED_BYTE,new Uint8Array(available));await completed();const uploadMs=performance.now()-uploadStart;
 gl.uniform1i(gl.getUniformLocation(p,'carrier'),0);gl.uniform1i(gl.getUniformLocation(p,'offsets'),1);gl.uniform1i(gl.getUniformLocation(p,'availability'),2);gl.uniform1i(gl.getUniformLocation(p,'recordBytes'),60);gl.uniform2i(gl.getUniformLocation(p,'fineOrigin'),entry.fine_origin[1],entry.fine_origin[0]);gl.uniform2i(gl.getUniformLocation(p,'quarterOrigin'),entry.quarter_origin[1],entry.quarter_origin[0]);gl.viewport(0,0,width,height);
 if(!gl.getExtension('EXT_color_buffer_float'))throw Error('Float score readback unavailable');
 const scoreTexture=gl.createTexture();resources.push(scoreTexture);gl.activeTexture(gl.TEXTURE4);gl.bindTexture(gl.TEXTURE_2D,scoreTexture);gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_MIN_FILTER,gl.NEAREST);gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_MAG_FILTER,gl.NEAREST);gl.texImage2D(gl.TEXTURE_2D,0,gl.RGBA32F,width,height,0,gl.RGBA,gl.FLOAT,null);
 const framebuffer=gl.createFramebuffer();gl.bindFramebuffer(gl.FRAMEBUFFER,framebuffer);gl.framebufferTexture2D(gl.FRAMEBUFFER,gl.COLOR_ATTACHMENT0,gl.TEXTURE_2D,scoreTexture,0);if(gl.checkFramebufferStatus(gl.FRAMEBUFFER)!==gl.FRAMEBUFFER_COMPLETE)throw Error('Float framebuffer incomplete');
 const dataReadyMs=performance.now()-benchmarkStart;
 const measurements=[];for(const months of [1,12]){gl.uniform1i(gl.getUniformLocation(p,'monthCount'),months);for(let repeat=0;repeat<4;repeat++){gl.uniform1f(gl.getUniformLocation(p,'tasteShift'),repeat*.25);const start=performance.now();gl.drawArrays(gl.TRIANGLES,0,6);await completed();measurements.push({months,repeat,completedMs:performance.now()-start});}}
 const readback=[];for(const item of entry.cpu_card_pixels){const pixel=new Float32Array(4);gl.readPixels(item.pixel[0],item.pixel[1],1,1,gl.RGBA,gl.FLOAT,pixel);readback.push({pixel:item.pixel,nativeFine:item.native_fine,cpu:item.score,gpu:pixel[0]*100,error:Math.abs(pixel[0]*100-item.score)});}
 const display=gl.createProgram();gl.attachShader(display,compile(gl.VERTEX_SHADER,${JSON.stringify(vertex)}));gl.attachShader(display,compile(gl.FRAGMENT_SHADER,${JSON.stringify(displayFragment)}));gl.linkProgram(display);if(!gl.getProgramParameter(display,gl.LINK_STATUS))throw Error(gl.getProgramInfoLog(display));gl.useProgram(display);loc=gl.getAttribLocation(display,'p');gl.enableVertexAttribArray(loc);gl.vertexAttribPointer(loc,2,gl.FLOAT,false,0,0);
 gl.uniform1i(gl.getUniformLocation(display,'image'),4);gl.uniform2f(gl.getUniformLocation(display,'viewExtent'),viewWidth,viewHeight);gl.uniform1f(gl.getUniformLocation(display,'worldPixels'),entry.world_css_pixels);const longitude=entry.center[0],latitude=entry.center[1]*Math.PI/180;gl.uniform2f(gl.getUniformLocation(display,'mercatorCenter'),(longitude+180)/360,(1-Math.asinh(Math.tan(latitude))/Math.PI)/2);gl.uniform2i(gl.getUniformLocation(display,'fineOrigin'),entry.fine_origin[1],entry.fine_origin[0]);
 gl.bindFramebuffer(gl.FRAMEBUFFER,null);gl.viewport(0,0,viewWidth,viewHeight);const presentMeasurements=[];for(let repeat=0;repeat<4;repeat++){const start=performance.now();gl.drawArrays(gl.TRIANGLES,0,6);await completed();presentMeasurements.push(performance.now()-start);}
 const result={renderer:debug?gl.getParameter(debug.UNMASKED_RENDERER_WEBGL):gl.getParameter(gl.RENDERER),compileMs,transfers,tileAssemblyMs,uploadMs,dataReadyMs,measurements,presentMeasurements,readback,maximumMapCardDifference:Math.max(...readback.map(v=>v.error)),error:gl.getError()};
 for(const texture of resources)gl.deleteTexture(texture);gl.deleteFramebuffer(framebuffer);gl.deleteProgram(p);gl.deleteProgram(display);return result;
};</script>`;
const server=http.createServer((request,response)=>{
 const bytes=payloads.get(request.url);if(bytes){response.setHeader('Content-Type','application/octet-stream');response.end(bytes);}else{response.setHeader('Content-Type','text/html');response.end(html);}
});await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const temporary=await fs.mkdtemp(path.join(os.tmpdir(),'nwf-fine-headless-'));
await fs.writeFile(path.join(directory,'consumer_geographic.html'),html);
const chrome=spawn('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',['--headless=new','--no-first-run','--no-default-browser-check','--remote-debugging-port=0',`--user-data-dir=${temporary}`,'--use-angle=metal','--window-size=3840,1080','about:blank'],{stdio:['ignore','ignore','pipe']});
let websocketUrl;const ready=new Promise((resolve,reject)=>{let log='';chrome.stderr.on('data',chunk=>{log+=chunk;const match=log.match(/DevTools listening on (ws:\/\/[^\s]+)/);if(match){websocketUrl=match[1];resolve();}});chrome.on('exit',code=>reject(Error('Chrome exited '+code+' '+log)));});
let socket;const pending=new Map();let serial=0;
try{
 await ready;socket=new WebSocket(websocketUrl);await new Promise(resolve=>socket.addEventListener('open',resolve));
 socket.addEventListener('message',event=>{const message=JSON.parse(event.data);if(message.id){const callback=pending.get(message.id);pending.delete(message.id);message.error?callback.reject(Error(JSON.stringify(message.error))):callback.resolve(message.result);}});
 const send=(method,params={},sessionId)=>new Promise((resolve,reject)=>{const id=++serial;pending.set(id,{resolve,reject});socket.send(JSON.stringify({id,method,params,sessionId}));});
 const {targetId}=await send('Target.createTarget',{url:`http://127.0.0.1:${server.address().port}/`});const {sessionId}=await send('Target.attachToTarget',{targetId,flatten:true});await send('Runtime.enable',{},sessionId);
 const report=[];
 for(const entry of data.cases){
  const width=entry.view_width,height=entry.view_height;
  await send('Emulation.setDeviceMetricsOverride',{width,height,deviceScaleFactor:1,mobile:false},sessionId);
  let readyPage=false;const deadline=Date.now()+10000;while(!readyPage){const r=await send('Runtime.evaluate',{expression:'typeof window.run==="function"',returnByValue:true},sessionId);readyPage=r.result.value;if(Date.now()>deadline)throw Error('Benchmark page did not initialize');if(!readyPage)await new Promise(resolve=>setTimeout(resolve,50));}
  const result=await send('Runtime.evaluate',{expression:'window.run('+JSON.stringify(entry)+')',awaitPromise:true,returnByValue:true},sessionId);if(result.exceptionDetails)throw Error(JSON.stringify(result.exceptionDetails));
  const value={...entry,...result.result.value};report.push(value);console.log(JSON.stringify(value));
  const screenshot=await send('Page.captureScreenshot',{format:'png'},sessionId);await fs.writeFile(path.join(directory,entry.prefix+'.png'),Buffer.from(screenshot.data,'base64'));
 }
 await fs.writeFile(path.join(directory,'geographic-results.json'),JSON.stringify({written_by:'AI, GPT-6.1 Sol, 2026.10.17',scope:data.scope,score_aggregation:data.score_aggregation,correction_tile_root:data.correction_tile_root,generation:data.generation,device:'This Mac, headless Chrome, CSS viewport at devicePixelRatio1; transfer byte counts and local gzip decoding measured; no phone hardware, basemap or internet latency claim.',results:report},null,2)+'\n');
 await send('Browser.close');
}finally{socket?.close();chrome.kill();server.close();}
