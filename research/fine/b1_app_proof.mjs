// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Exercise whole-world, desktop and phone place loading against a read-only bundle.
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { spawn } from 'node:child_process';
import { localWorkerFixture } from './local_worker_fixture.mjs';

const output = process.argv[2];
const fixture = process.argv[3] ? localWorkerFixture(path.resolve(process.argv[3])) : undefined;
const localFetch = fixture ?? fetch;
const browserEvents = [];
let pageReads = [];
if (!output) throw Error('Usage: node research/fine/b1_app_proof.mjs <output-folder> [read-only-data-overlay]');
await fs.mkdir(output, { recursive: true });
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
        await command('Fetch.fulfillRequest',{requestId,responseCode:response.status,
          responseHeaders:[...response.headers].map(([name,value])=>({name,value})),
          body:bytes.toString('base64')});
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
    if(!ready)throw Error(`${name}: the real app never produced an answer`);
    const beforeTaste=pageReads.length;
    const dragStart=performance.now();
    const drag=await command('Runtime.evaluate',{expression:`(()=>{const e=document.querySelector('#slider-perfectFrom');e.value='24';e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));return true})()`,returnByValue:true});
    await new Promise(r=>setTimeout(r,2000));
    const after=await command('Runtime.evaluate',{expression:`JSON.stringify({card:document.querySelector('.slot-card')?.innerText??'',headline:document.querySelector('.headline')?.innerText??'',url:location.href})`,returnByValue:true});
    const shot=await command('Page.captureScreenshot',{format:'png'});
    await fs.writeFile(path.join(output,name+'.png'),Buffer.from(shot.data,'base64'));
    const reads=pageReads.slice();
    const native=reads.filter(x=>x.url.includes('/native/'));
    const r={name,readyMilliseconds:ready,settledMilliseconds:performance.now()-began,requests:reads.length,bytes:reads.reduce((s,r)=>s+r.bytes,0),nativeRequests:native.length,nativeDistinct:new Set(native.map(x=>x.url)).size,correctionDistinct:new Set(reads.filter(x=>x.url.endsWith('.nwf.gz')).map(x=>x.url)).size,tasteExtraRequests:reads.length-beforeTaste,state,afterTaste:JSON.parse(after.result.value),firstNativeRequests:native.slice(0,8),errors:browserEvents.filter(x=>x.method==='Runtime.exceptionThrown')};
    await fs.writeFile(path.join(output,name+'-reads.json'),JSON.stringify(reads,null,2));
    if(r.errors.length)throw Error(`${name}: runtime exceptions`);
    const missing=reads.filter(read=>(read.url.includes('/native/')||read.url.endsWith('.nwf.gz'))&&read.status!==200);
    if(missing.length)throw Error(`${name}: ${missing.length} required fine weather objects are unavailable`);
    if(r.nativeDistinct>512)throw Error(`${name}: catalogue weather fanout returned`);
    results.push(r);console.log(JSON.stringify({...r,state:{headline:r.state.headline,card:r.state.card.slice(0,300)},afterTaste:{headline:r.afterTaste.headline,card:r.afterTaste.card.slice(0,300),url:r.afterTaste.url}}));
  }
  await fs.writeFile(path.join(output,'results.json'),JSON.stringify(results,null,2));
  await send('Target.closeTarget', { targetId });
} finally {
  await fs.writeFile(path.join(output,'browser-events.json'),JSON.stringify(browserEvents,null,2)+'\n');
  socket?.close();
  chrome.kill();
}
