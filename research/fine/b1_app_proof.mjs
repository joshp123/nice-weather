// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Exercise whole-world, desktop and phone place loading against a read-only bundle.
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import {gunzipSync} from 'node:zlib';
import {pathToFileURL} from 'node:url';
import { spawn } from 'node:child_process';
import { localWorkerFixture } from './local_worker_fixture.mjs';

const output = process.argv[2];
const packagedWorker = process.argv[4] ? path.resolve(process.argv[4], 'worker/index.js') : undefined;
const packagedFetch = packagedWorker ? (await import(pathToFileURL(packagedWorker).href)).default.fetch : undefined;
const fixture = process.argv[3] ? localWorkerFixture(path.resolve(process.argv[3]), undefined, packagedFetch) : undefined;
const localFetch = fixture ?? fetch;
const browserEvents = [];
let pageReads = [];
if (!output) throw Error('Usage: node research/fine/b1_app_proof.mjs <output-folder> [read-only-data-overlay] [prepared-sites-package]');
await fs.mkdir(output, { recursive: true });
await fs.writeFile(path.join(output,'results.json'),JSON.stringify({passes:false,status:'running'},null,2)+'\n');
const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'nwf-b1-cards-'));
const chrome = spawn('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', [
  '--headless=new', '--no-first-run', '--no-default-browser-check',
  '--remote-debugging-port=0', `--user-data-dir=${profile}`, '--use-angle=metal', 'about:blank',
], { stdio: ['ignore', 'ignore', 'pipe'] });
let socket;
try {
  const endpoint = await new Promise((resolve, reject) => {
    let log = '';
    chrome.stderr.on('data', bytes => {
      log += bytes;
      const match = log.match(/DevTools listening on (ws:\/\/[^\s]+)/);
      if (match) resolve(match[1]);
    });
    chrome.on('exit', code => reject(Error(`Headless browser exited ${code}: ${log}`)));
  });
  socket = new WebSocket(endpoint);
  await new Promise(resolve => socket.addEventListener('open', resolve));
  const pending = new Map();
  let serial = 0;
  let eventHandler = () => {};
  let interceptionError;
  socket.addEventListener('message', event => {
    const value = JSON.parse(event.data);
    if (!value.id) {
      if (['Runtime.consoleAPICalled','Runtime.exceptionThrown','Network.loadingFailed'].includes(value.method)) browserEvents.push(value);
      eventHandler(value); return;
    }
    const callback = pending.get(value.id);
    pending.delete(value.id);
    value.error ? callback.reject(Error(JSON.stringify(value.error))) : callback.resolve(value.result);
  });
  const send = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
    const id = ++serial;
    pending.set(id, { resolve, reject });
    socket.send(JSON.stringify({ id, method, params, sessionId }));
  });
  const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
  const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
  const command = (method, params) => send(method, params, sessionId);
  await command('Page.enable');
  await command('Runtime.enable');
  await command('Network.enable');
  if (fixture) {
    eventHandler = value => {
      if (value.method !== 'Fetch.requestPaused') return;
      const {requestId,request} = value.params;
      void fixture(request.url).then(async response => {
        const bytes = Buffer.from(await response.arrayBuffer());
        pageReads.push({url:request.url,status:response.status,bytes:bytes.length});
        const compressed=response.headers.get('Content-Encoding')==='gzip';
        const entity=compressed?gunzipSync(bytes):bytes;
        await command('Fetch.fulfillRequest',{requestId,responseCode:response.status,
          responseHeaders:[...response.headers].filter(([name])=>!compressed||!['content-encoding','content-length'].includes(name.toLowerCase())).map(([name,value])=>({name,value})),
          body:entity.toString('base64')});
      }).catch(async error => {
        interceptionError=error;
        await command('Fetch.failRequest',{requestId,errorReason:'Failed'});
      });
    };
    await command('Fetch.enable',{patterns:[{urlPattern:'http://127.0.0.1:5198/*'}]});
  }
  await command('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1080, deviceScaleFactor: 1, mobile: false });
  const results = [];
  for (const [name, pathname, width, height] of [
    ['world-desktop','/july',1440,1080],
    ['funchal-cold','/portugal/funchal/july',1440,1080],
    ['san-francisco-phone','/united-states/san-francisco/july',390,844],
    ['san-diego-desktop','/united-states/san-diego/july',1440,1080],
  ]) {
    await command('Network.clearBrowserCache');
    await command('Emulation.setDeviceMetricsOverride',{width,height,deviceScaleFactor:1,mobile:width===390});
    await command('Page.navigate',{url:'about:blank'});
    pageReads=[]; browserEvents.length=0;
    const began=performance.now();
    await command('Page.navigate',{url:'http://127.0.0.1:5198'+pathname});
    let ready=null,state,lastCount=-1,lastChange=performance.now();
    const deadline=performance.now()+60000;
    while(performance.now()<deadline) {
      await new Promise(r=>setTimeout(r,250));
      const data=await command('Runtime.evaluate',{expression:`JSON.stringify({text:document.body.innerText,card:document.querySelector('.slot-card')?.innerText??'',headline:document.querySelector('.headline')?.innerText??'',labels:document.querySelectorAll('.maplibregl-canvas').length})`,returnByValue:true});
      state=JSON.parse(data.result.value??'{}');if(interceptionError)throw interceptionError;
      const usable=name==='world-desktop'? state.headline?.includes('Nicest'):state.card?.includes('Walk home');
      if(usable&&!ready)ready=performance.now()-began;
      if(pageReads.length!==lastCount){lastCount=pageReads.length;lastChange=performance.now();}
      if(usable&&!/Loading weather|Checking/.test(state.text)&&performance.now()-lastChange>3000)break;
    }
    if(!ready) {
      await fs.writeFile(path.join(output,name+'-failure.json'),JSON.stringify({state,reads:pageReads},null,2));
      const failedShot=await command('Page.captureScreenshot',{format:'png'});
      await fs.writeFile(path.join(output,name+'-failure.png'),Buffer.from(failedShot.data,'base64'));
      throw Error(`${name}: the real app never produced an answer`);
    }
    let cardApi;
    if(name!=='world-desktop') {
      const slug=pathname.split('/').filter(Boolean).slice(0,2).join('/');
      const place=await(await localFetch(`http://127.0.0.1:5198/api/slug/${slug}`)).json();
      const numbers=await(await localFetch(`http://127.0.0.1:5198/api/place?lat=${place.latitude}&lon=${place.longitude}&period=july`)).json();
      const judgement=numbers.judgement;
      cardApi={score:Math.floor(judgement.meanScore),nice:Math.round(judgement.niceDayCount),perfect:Math.round(judgement.perfectDayCount),awful:Math.round(judgement.awfulDayCount)};
      if(!state.card.includes(`\n\n${cardApi.score}\n`)||!state.card.includes(`Nice on ${cardApi.nice} of the 31 days of July. Perfect on ${cardApi.perfect}; awful on ${cardApi.awful}.`))throw Error(`${name}: displayed card and Worker API disagree`);
    }
    const defaultShot=await command('Page.captureScreenshot',{format:'png'});
    await fs.writeFile(path.join(output,name+'-default.png'),Buffer.from(defaultShot.data,'base64'));
    const beforeTaste=pageReads.length;
    const dragStart=performance.now();
    const drag=await command('Runtime.evaluate',{expression:`(()=>{const e=document.querySelector('#slider-perfectFrom');e.value='24';e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));return true})()`,returnByValue:true});
    await new Promise(r=>setTimeout(r,2000));
    const after=await command('Runtime.evaluate',{expression:`JSON.stringify({card:document.querySelector('.slot-card')?.innerText??'',headline:document.querySelector('.headline')?.innerText??'',url:location.href})`,returnByValue:true});
    const shot=await command('Page.captureScreenshot',{format:'png'});
    await fs.writeFile(path.join(output,name+'.png'),Buffer.from(shot.data,'base64'));
    const reads=pageReads.slice();
    const native=reads.filter(x=> {
      const url=new URL(x.url);
      return url.pathname==='/api/fine/replay'&&url.searchParams.has('lat');
    });
    const r={name,cardApi,readyMilliseconds:ready,settledMilliseconds:performance.now()-began,requests:reads.length,bytes:reads.reduce((s,r)=>s+r.bytes,0),nativeRequests:native.length,nativeDistinct:new Set(native.map(x=>x.url)).size,correctionDistinct:new Set(reads.filter(x=>x.url.endsWith('.nwf.gz')).map(x=>x.url)).size,tasteExtraRequests:reads.length-beforeTaste,state,afterTaste:JSON.parse(after.result.value),firstNativeRequests:native.slice(0,8),errors:browserEvents.filter(x=>x.method==='Runtime.exceptionThrown')};
    await fs.writeFile(path.join(output,name+'-reads.json'),JSON.stringify(reads,null,2));
    if(r.errors.length)throw Error(`${name}: runtime exceptions`);
    const missing=native.filter(read=>read.status!==200);
    if(missing.length)throw Error(`${name}: ${missing.length} required fine weather objects are unavailable`);
    const labelReads=reads.filter(read=>/\/api\/fine\/labels-[12]\.json$/.test(new URL(read.url).pathname));
    if(labelReads.length!==2||labelReads.some(read=>read.status!==200))throw Error(`${name}: canonical label files are unavailable`);
    if(reads.some(read=>read.url.includes('/native/')||read.url.endsWith('.nwf.gz')||read.url.endsWith('.nwz.gz')))throw Error(`${name}: browser downloaded full correction or native carrier tiles`);
    if(r.nativeDistinct>512)throw Error(`${name}: catalogue weather fanout returned`);
    results.push(r);console.log(JSON.stringify({...r,state:{headline:r.state.headline,card:r.state.card.slice(0,300)},afterTaste:{headline:r.afterTaste.headline,card:r.afterTaste.card.slice(0,300),url:r.afterTaste.url}}));
  }
  const sources={};
  for(const file of ['hosting/sites/worker.mjs','hosting/sites/fine-place.mjs','hosting/sites/fine-candidates.mjs','src/engine/finePlaces.ts','src/engine/weatherEngine.ts','src/ui/map.ts','src/ui/home.ts','dist/index.html']) {
    sources[file]=crypto.createHash('sha256').update(await fs.readFile(file)).digest('hex');
  }
  if(packagedWorker) for(const name of await fs.readdir(path.dirname(packagedWorker))) {
    const file=path.join(path.dirname(packagedWorker),name);
    sources[file]=crypto.createHash('sha256').update(await fs.readFile(file)).digest('hex');
  }
  await fs.writeFile(path.join(output,'results.json'),JSON.stringify({written_by:'AI, GPT-6.1 Sol, 2026.10.18',passes:true,sources,views:results},null,2));
  await send('Target.closeTarget', { targetId });
} catch(error) {
  await fs.writeFile(path.join(output,'results.json'),JSON.stringify({passes:false,error:error.message},null,2)+'\n');
  throw error;
} finally {
  await fs.writeFile(path.join(output,'browser-events.json'),JSON.stringify(browserEvents,null,2)+'\n');
  socket?.close();
  chrome.kill();
}
