// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Exercise lean named-place, fallback, desktop and phone place loading against a read-only bundle.
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import {gunzipSync} from 'node:zlib';
import {pathToFileURL} from 'node:url';
import { spawn } from 'node:child_process';
import { localWorkerFixture } from './local_worker_fixture.mjs';
import { legacyBrowserNumbers } from './legacy_browser_numbers.mjs';
import {createNamedPlaceClient,namedPlaceNumbers} from '../../hosting/sites/named-place.mjs';
import ts from 'typescript';

const output = process.argv[2];
const namedState = process.argv[5] ?? 'active';
const timingOnly = process.env.NWF_NAMED_TIMING_ONLY === '1';
if (!['active', 'inactive'].includes(namedState)) throw Error('Named state must be active or inactive');
const packagedWorker = process.argv[4] ? path.resolve(process.argv[4], 'worker/index.js') : undefined;
const packagedFetch = packagedWorker ? (await import(pathToFileURL(packagedWorker).href)).default.fetch : undefined;
const fixture = process.argv[3] ? localWorkerFixture(path.resolve(process.argv[3]), undefined, packagedFetch) : undefined;
const localFetch = fixture ?? fetch;
const browserEvents = [];
let pageReads = [];
if (!output) throw Error('Usage: node research/fine/named_app_proof.mjs <output-folder> [read-only-data-overlay] [prepared-sites-package] [active|inactive]');
await fs.mkdir(output, { recursive: true });
await fs.writeFile(path.join(output,'results.json'),JSON.stringify({passes:false,status:'running'},null,2)+'\n');
const status = await (await localFetch('http://127.0.0.1:5198/api/named')).json();
if (Boolean(status.enabled) !== (namedState === 'active')) throw Error(`Expected ${namedState} named data, got ${JSON.stringify(status)}`);
if (namedState === 'inactive') {
  for (const root of [process.argv[3], 'artifacts']) {
    try { await fs.access(path.join(root, 'v6/named/current.json')); throw Error(`Inactive proof requires absent pointer in ${root}`); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
  }
}
const legacyNumbers = await legacyBrowserNumbers(localFetch);
const changedTaste={warmShift:1,heatShift:0,eveningShift:0,humidityShift:0,windShift:0,rainScale:1};
const changedTasteNumbers = await legacyBrowserNumbers(localFetch, 7, changedTaste);
const legacyForecastNumbers = await legacyBrowserNumbers(localFetch, {kind:'forecast'});
const changedForecastNumbers = await legacyBrowserNumbers(localFetch, {kind:'forecast'}, changedTaste);
const cardMatches=(text,numbers)=>text.split('\n').includes(String(numbers.score))&&new RegExp(`Nice on ${numbers.nice} of the \\d+ days .*Perfect on ${numbers.perfect}; awful on ${numbers.awful}\\.`).test(text);
const sourceUrl=source=>`data:text/javascript;base64,${Buffer.from(ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText).toString('base64')}`;
const countriesUrl=sourceUrl(await fs.readFile('src/ui/countries.ts','utf8'));
const ranking=await import(sourceUrl((await fs.readFile('src/ui/ranking.ts','utf8')).replace("'./countries'",JSON.stringify(countriesUrl))));
let namedClient;
let labelRows;
const nativeFetch=globalThis.fetch;
try {
  globalThis.fetch=(url,init)=>localFetch(new URL(url,'http://127.0.0.1:5198').href,init);
  namedClient=createNamedPlaceClient('http://127.0.0.1:5198/api/named');
  await namedClient.ready();
  labelRows=(await Promise.all([1,2].map(async level=>{const response=await localFetch(`http://127.0.0.1:5198/api/fine/labels-${level}.json`);const bytes=Buffer.from(await response.arrayBuffer());return JSON.parse((bytes[0]===31&&bytes[1]===139?gunzipSync(bytes):bytes).toString());}))).flat();
  await namedClient.ensure(labelRows.map(([, ,latitude,longitude])=>({latitude,longitude})),{kind:'month',month:7});
} finally {globalThis.fetch=nativeFetch;}
const worldNumbers=labelRows.map(([name,slug,latitude,longitude,population])=>{
  const row=namedClient.find(latitude,longitude,{kind:'month',month:7});
  const numbers=row&&namedPlaceNumbers(row,{kind:'month',month:7})||legacyNumbers(latitude,longitude);
  return numbers?{name,slug,latitude,longitude,population,score:numbers.meanScore,niceDayCount:numbers.niceDayCount,perfectDayCount:numbers.perfectDayCount}:null;
}).filter(row=>row&&row.population>=300000&&ranking.inOutlookRegion(row,'the world'));
const expectedWorldHeadline=ranking.rankPlaces(worldNumbers,300000,8).slice(0,3).map(row=>row.name);
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
  await command('Page.addScriptToEvaluateOnNewDocument',{source:`(()=>{
    window.__namedGeoJSON={};
    const post=Worker.prototype.postMessage;
    Worker.prototype.postMessage=function(message,...rest){
      if(message?.type==='LD'&&message.data?.source&&message.data?.data?.features){
        window.__namedGeoJSON[message.data.source]=message.data.data;
      }
      return post.call(this,message,...rest);
    };
  })()`});
  if (fixture) {
    eventHandler = value => {
      if (value.method !== 'Fetch.requestPaused') return;
      const {requestId,request} = value.params;
      void fixture(request.url,{method:request.method,headers:request.headers,...(request.postData?{body:request.postData}:{})}).then(async response => {
        const bytes = Buffer.from(await response.arrayBuffer());
        pageReads.push({url:request.url,status:response.status,bytes:bytes.length});
        const compressed=response.headers.get('Content-Encoding')==='gzip';
        let entity=compressed?gunzipSync(bytes):bytes;
        if (/\/assets\/main-[^/]+\.js$/.test(new URL(request.url).pathname)) {
          const source=entity.toString();
          const expression=/(\b(?:const|let)\s+[\w$]+=)(new [\w$]+\.Map\(\{container:)/;
          if(!expression.test(source))throw Error('Cannot observe the real rendered map labels');
          entity=Buffer.from(source.replace(expression,'$1window.__namedProofMap=$2'));
        }
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
    ['san-francisco-laptop','/united-states/san-francisco/july',1440,1080],
    ['san-diego-desktop','/united-states/san-diego/july',1440,1080],
    ['nearby-city-phone','/@37.7751,-122.4181,9z/july',390,844],
    ['remote-area-phone','/@-25,135,6z/july',390,844],
    ['san-francisco-forecast-phone','/united-states/san-francisco/next-14-days',390,844],
    ['san-francisco-forecast-laptop','/united-states/san-francisco/next-14-days',1440,1080],
  ].filter(([name]) => process.env.NWF_NAMED_VIEW ? name===process.env.NWF_NAMED_VIEW : !timingOnly || name === 'san-francisco-laptop')) {
    const forecastView=pathname.endsWith('next-14-days');
    const periodWord=forecastView?'next-14-days':'july';
    const overview=forecastView?legacyForecastNumbers:legacyNumbers;
    const changedOverview=forecastView?changedForecastNumbers:changedTasteNumbers;
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
      const data=await command('Runtime.evaluate',{expression:`JSON.stringify({text:document.body.innerText,card:document.querySelector('.slot-card')?.innerText??'',headline:document.querySelector('.headline')?.innerText??'',labels:document.querySelectorAll('.maplibregl-canvas').length,headlinePlaces:[...document.querySelectorAll('.headline-sentence .inline-place')].map(el=>el.textContent),url:location.href})`,returnByValue:true});
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
    const geo=await command('Runtime.evaluate',{expression:`JSON.stringify(window.__namedGeoJSON)`,returnByValue:true});
    state.geoJSON=JSON.parse(geo.result.value??'{}');
    const rendered=await command('Runtime.evaluate',{expression:`JSON.stringify(window.__namedProofMap.queryRenderedFeatures({layers:['city-labels']}).map(feature=>({properties:feature.properties,geometry:feature.geometry})))`,returnByValue:true});
    state.renderedCityLabels=JSON.parse(rendered.result.value??'[]');
    if(name==='world-desktop'){
      const actual=state.headlinePlaces.map(value=>value.replace(/^[^\p{L}]+/u,''));
      if(JSON.stringify(actual)!==JSON.stringify(expectedWorldHeadline))throw Error(`World headline ${JSON.stringify(actual)} differs from shared numbers ${JSON.stringify(expectedWorldHeadline)}`);
    }
    let cardApi, cardPoint;
    if(name!=='world-desktop') {
      const unnamed=name==='remote-area-phone';
      const coordinateView=pathname.startsWith('/@');
      const slug=pathname.split('/').filter(Boolean).slice(0,2).join('/');
      const place=coordinateView?(unnamed?{latitude:-25,longitude:135}:{latitude:37.7751,longitude:-122.4181}):await(await localFetch(`http://127.0.0.1:5198/api/slug/${slug}`)).json();
      const numbers=await(await localFetch(`http://127.0.0.1:5198/api/place?lat=${place.latitude}&lon=${place.longitude}&period=${periodWord}`)).json();
      cardPoint={latitude:numbers.latitude,longitude:numbers.longitude};
      const chosen=state.geoJSON?.['chosen-place']?.features?.[0];
      if(!chosen||chosen.geometry.coordinates[0]!==numbers.longitude||chosen.geometry.coordinates[1]!==numbers.latitude)throw Error(`${name}: selected marker differs from resolved place`);
      if(name==='nearby-city-phone'){
        const searched=await(await localFetch('http://127.0.0.1:5198/api/slug/united-states/san-francisco')).json();
        if(numbers.latitude!==searched.latitude||numbers.longitude!==searched.longitude||!state.url.includes('/united-states/san-francisco/july'))throw Error('Nearby city click did not use the searched city coordinates and link');
      }
      if(unnamed&&(numbers.latitude!==place.latitude||numbers.longitude!==place.longitude||!state.url.includes('/@-25,135,')||!state.card.includes('Area estimate')))throw Error('Remote area did not retain its point and overview model');
      const judgement=numbers.judgement;
      if (namedState === 'active'&&!unnamed) {
        cardApi={score:Math.floor(judgement.meanScore),nice:Math.round(judgement.niceDayCount),perfect:Math.round(judgement.perfectDayCount),awful:Math.round(judgement.awfulDayCount)};
      } else {
        const reference=overview(cardPoint.latitude,cardPoint.longitude);
        cardApi={score:Math.floor(reference.meanScore),nice:Math.round(reference.niceDayCount),perfect:Math.round(reference.perfectDayCount),awful:Math.round(reference.awfulDayCount),source:'Unchanged browser PointSampler over published 25 km files'};
      }
      if(!cardMatches(state.card,cardApi))throw Error(`${name}: displayed card and its weather reference disagree: ${JSON.stringify({card:state.card,cardApi,cardPoint})}`);
    }
    const labelled=state.renderedCityLabels.filter(feature=>Number.isFinite(feature.properties?.score));
    const labelProof=[];
    for(const feature of labelled.filter((feature,index)=>index<12||['San Francisco','San Diego','Funchal'].includes(feature.properties.name))) {
      const {latitude,longitude,score,countImage,name:placeName}=feature.properties;
      const response=await(await localFetch(`http://127.0.0.1:5198/api/place?lat=${latitude}&lon=${longitude}&period=${periodWord}`)).json();
      const reference=namedState==='active'?response.judgement:overview(latitude,longitude);
      if(!reference)throw Error(`${name}: label reference unavailable for ${placeName}`);
      if(score!==Math.floor(reference.meanScore)||!countImage.endsWith('-'+Math.round(reference.niceDayCount)))throw Error(`${name}: map label differs from shared place numbers for ${placeName}`);
      labelProof.push({name:placeName,score,nice:Math.round(reference.niceDayCount)});
    }
    if(!labelProof.length&&name!=='remote-area-phone')throw Error(`${name}: no scored rendered map labels observed`);
    const defaultShot=await command('Page.captureScreenshot',{format:'png'});
    await fs.writeFile(path.join(output,name+'-default.png'),Buffer.from(defaultShot.data,'base64'));
    const beforeTaste=pageReads.length;
    await command('Runtime.evaluate',{expression:`performance.clearMeasures('nice-weather-redraw')`});
    const dragStart=performance.now();
    const drag=await command('Runtime.evaluate',{expression:`(()=>{const e=document.querySelector('#slider-perfectFrom');e.value='24';e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));return true})()`,returnByValue:true});
    await new Promise(r=>setTimeout(r,2000));
    const after=await command('Runtime.evaluate',{expression:`JSON.stringify({card:document.querySelector('.slot-card')?.innerText??'',headline:document.querySelector('.headline')?.innerText??'',url:location.href,redraws:performance.getEntriesByName('nice-weather-redraw').map(e=>e.duration)})`,returnByValue:true});
    const shot=await command('Page.captureScreenshot',{format:'png'});
    await fs.writeFile(path.join(output,name+'.png'),Buffer.from(shot.data,'base64'));
    const afterTaste=JSON.parse(after.result.value);
    if(cardPoint){
      const reference=changedOverview(cardPoint.latitude,cardPoint.longitude);
      const expected={score:Math.floor(reference.meanScore),nice:Math.round(reference.niceDayCount),perfect:Math.round(reference.perfectDayCount),awful:Math.round(reference.awfulDayCount)};
      if(!cardMatches(afterTaste.card,expected))throw Error(`${name}: changed taste did not fall back to today's 25 km weather`);
    }
    let warmSlider;
    if(timingOnly) {
      const beforeWarm=pageReads.length;
      warmSlider=[];
      for(const value of [25,26,24,25,26,24,25,26,24,25,26,24]) {
        const measured=await command('Runtime.evaluate',{expression:`(async()=>{
          performance.clearMeasures('nice-weather-redraw');
          const start=performance.now();
          const answer=new Promise((resolve,reject)=>{
            const observer=new PerformanceObserver(list=>{
              const redraw=list.getEntries().find(entry=>entry.name==='nice-weather-redraw');
              if(redraw){observer.disconnect();clearTimeout(timer);resolve({duration:redraw.duration,wallMilliseconds:performance.now()-start})}
            });
            observer.observe({entryTypes:['measure']});
            const timer=setTimeout(()=>{observer.disconnect();reject(Error('No slider redraw'))},5000);
          });
          const slider=document.querySelector('#slider-perfectFrom');
          slider.value='${value}';
          slider.dispatchEvent(new Event('input',{bubbles:true}));
          slider.dispatchEvent(new Event('change',{bubbles:true}));
          return answer;
        })()`,awaitPromise:true,returnByValue:true});
        if(measured.exceptionDetails)throw Error(JSON.stringify(measured.exceptionDetails));
        warmSlider.push({value,...measured.result.value});
        await new Promise(resolve=>setTimeout(resolve,100));
      }
      if(pageReads.length!==beforeWarm)throw Error('Warm slider issued network requests');
    }
    const reads=pageReads.slice();
    const native=reads.filter(x=> {
      const url=new URL(x.url);
      return url.pathname==='/api/fine/replay'&&url.searchParams.has('lat');
    });
    const r={name,namedState,cardApi,labelProof,warmSlider,readyMilliseconds:ready,settledMilliseconds:performance.now()-began,requests:reads.length,bytes:reads.reduce((s,r)=>s+r.bytes,0),nativeRequests:native.length,nativeDistinct:new Set(native.map(x=>x.url)).size,correctionDistinct:new Set(reads.filter(x=>x.url.endsWith('.nwf.gz')).map(x=>x.url)).size,tasteExtraRequests:reads.length-beforeTaste,state,afterTaste,firstNativeRequests:native.slice(0,8),errors:browserEvents.filter(x=>x.method==='Runtime.exceptionThrown')};
    await fs.writeFile(path.join(output,name+'-reads.json'),JSON.stringify(reads,null,2));
    if(r.errors.length)throw Error(`${name}: runtime exceptions`);
    const missing=native.filter(read=>read.status!==200);
    if(missing.length)throw Error(`${name}: ${missing.length} required fine weather objects are unavailable`);
    const labelReads=reads.filter(read=>/\/api\/fine\/labels-[12]\.json$/.test(new URL(read.url).pathname));
    if(labelReads.length!==2||labelReads.some(read=>read.status!==200))throw Error(`${name}: canonical label files are unavailable`);
    if(reads.some(read=>read.url.includes('/native/')||read.url.endsWith('.nwf.gz')||read.url.endsWith('.nwz.gz')))throw Error(`${name}: browser downloaded full correction or native carrier tiles`);
    if(r.nativeRequests!==0)throw Error(`${name}: lean app requested full replay weather`);
    if(r.nativeDistinct>512)throw Error(`${name}: catalogue weather fanout returned`);
    results.push(r);console.log(JSON.stringify({...r,state:{headline:r.state.headline,card:r.state.card.slice(0,300)},afterTaste:{headline:r.afterTaste.headline,card:r.afterTaste.card.slice(0,300),url:r.afterTaste.url}}));
  }
  const sources={};
  for(const file of ['research/fine/named_app_proof.mjs','research/fine/legacy_browser_numbers.mjs','research/fine/local_worker_fixture.mjs','hosting/sites/worker.mjs','hosting/sites/fine-place.mjs','hosting/sites/fine-candidates.mjs','src/engine/finePlaces.ts','src/engine/namedPlaces.ts','hosting/sites/named-place.mjs','src/engine/weatherEngine.ts','src/engine/sampling.ts','src/engine/data.ts','src/engine/taste.ts','src/engine/shaders.ts','src/ui/map.ts','src/ui/home.ts','dist/index.html']) {
    sources[file]=crypto.createHash('sha256').update(await fs.readFile(file)).digest('hex');
  }
  if(packagedWorker) for(const name of await fs.readdir(path.dirname(packagedWorker))) {
    const file=path.join(path.dirname(packagedWorker),name);
    sources[file]=crypto.createHash('sha256').update(await fs.readFile(file)).digest('hex');
  }
  await fs.writeFile(path.join(output,'results.json'),JSON.stringify({written_by:'AI, GPT-6.1 Sol, 2026.10.18',passes:true,namedState,status,expectedWorldHeadline,sources,views:results},null,2));
  await send('Target.closeTarget', { targetId });
} catch(error) {
  await fs.writeFile(path.join(output,'results.json'),JSON.stringify({passes:false,error:error.message},null,2)+'\n');
  throw error;
} finally {
  await fs.writeFile(path.join(output,'browser-events.json'),JSON.stringify(browserEvents,null,2)+'\n');
  socket?.close();
  chrome.kill();
}
