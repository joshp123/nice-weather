// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Capture the integrated local cards in an isolated headless browser.
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
if (!output) throw Error('Usage: node research/fine/b1_screenshots.mjs <output-folder> [read-only-data-overlay]');
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
  const screenshots = [];
  for (const [name, slug] of [
    ['funchal', 'portugal/funchal'],
    ['san-francisco', 'united-states/san-francisco'],
    ['san-diego', 'united-states/san-diego'],
  ]) {
    const url = `http://127.0.0.1:5198/${slug}/july`;
    pageReads = [];
    const started = performance.now();
    await command('Page.navigate', { url });
    let card;
    const deadline = Date.now() + 60000;
    do {
      await new Promise(resolve => setTimeout(resolve, 250));
      const result = await command('Runtime.evaluate', {
        expression: `JSON.stringify({text:document.querySelector('.slot-card')?.innerText ?? '', title:document.title, rect:document.querySelector('.slot-card')?.getBoundingClientRect().toJSON()})`,
        returnByValue: true,
      });
      card = JSON.parse(result.result.value ?? '{}');
      if (interceptionError) throw interceptionError;
      if (Date.now() > deadline) throw Error(`Card did not load: ${name}: ${card.text}`);
    } while (!card.text?.includes('Walk home') || /Loading|Checking/.test(card.text));
    const placeResponse = await localFetch(`http://127.0.0.1:5198/api/slug/${slug}`);
    if (!placeResponse.ok) throw Error(`Place lookup failed: ${slug}`);
    const place = await placeResponse.json();
    const apiUrl = new URL('http://127.0.0.1:5198/api/place');
    apiUrl.searchParams.set('lat', place.latitude);
    apiUrl.searchParams.set('lon', place.longitude);
    apiUrl.searchParams.set('period', 'july');
    const apiResponse = await localFetch(apiUrl);
    if (!apiResponse.ok) throw Error(`Place numbers failed: ${slug}`);
    const api = await apiResponse.json();
    if (!api.fineReplay || !api.judgement) throw Error(`Card has no exact-pixel numbers: ${slug}`);
    const expected = api.judgement;
    if (!card.text.includes(String(Math.floor(expected.meanScore))) ||
        !card.text.includes(`${Math.round(expected.typical.feelsInShadeAt21C)} °C`)) {
      throw Error(`Rendered card differs from the place API: ${slug}`);
    }
    await fs.writeFile(path.join(output, `${name}-july-api.json`), JSON.stringify(api, null, 2) + '\n');
    const preview = await localFetch(url).then(response => response.text());
    await fs.writeFile(path.join(output, `${name}-july-preview.html`), preview);
    const settled = await command('Runtime.evaluate', {
      expression: `document.fonts.ready.then(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve(JSON.stringify(document.querySelector('.slot-card').getBoundingClientRect().toJSON()))))))`,
      awaitPromise: true, returnByValue: true,
    });
    card.rect = JSON.parse(settled.result.value);
    const page = await command('Page.captureScreenshot', { format: 'png' });
    await fs.writeFile(path.join(output, `${name}-july-page.png`), Buffer.from(page.data, 'base64'));
    const image = await command('Page.captureScreenshot', {
      format: 'png', clip: { x: card.rect.x, y: card.rect.y, width: card.rect.width, height: card.rect.height, scale: 1 },
    });
    const file = `${name}-july.png`;
    await fs.writeFile(path.join(output, file), Buffer.from(image.data, 'base64'));
    screenshots.push({ name, url, file, ...card, milliseconds:performance.now()-started,
      network:{requests:pageReads.length,bytes:pageReads.reduce((sum,read)=>sum+read.bytes,0),
        nativePackets:new Set(pageReads.filter(read=>read.url.includes('/native/')).map(read=>read.url)).size,
        correctionTiles:new Set(pageReads.filter(read=>read.url.endsWith('.nwf.gz')).map(read=>read.url)).size},reads:pageReads });
    console.log(JSON.stringify(screenshots.at(-1)));
  }
  await fs.writeFile(path.join(output, 'screenshots.json'), JSON.stringify(screenshots, null, 2) + '\n');
  await send('Target.closeTarget', { targetId });
} finally {
  await fs.writeFile(path.join(output,'browser-events.json'),JSON.stringify(browserEvents,null,2)+'\n');
  socket?.close();
  chrome.kill();
}
