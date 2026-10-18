# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Cold headless WebKit view transfers and rendered named-place answers.

Use fresh browser contexts for every view. request.sizes() measures encoded HTTP
response bodies, so gzip stays compressed in the transfer total. It excludes TLS,
HTTP framing and network packets. Timing is this Mac without network throttling,
not physical-phone performance. Mutation timestamps use the page's monotonic clock.
"""
import argparse
import asyncio
import base64
import gzip
import hashlib
import json
from pathlib import Path
import time

from playwright.async_api import async_playwright

INSTRUMENT = r"""(() => {
  window.__namedProof = {labels: [], headlines: [], cards: []};
  const signature = value => JSON.stringify(value);
  const record = (kind, value) => {
    const rows = window.__namedProof[kind];
    const key = signature(value);
    if (!rows.length || rows.at(-1).signature !== key)
      rows.push({ms: performance.now(), signature: key, value});
  };
  const post = Worker.prototype.postMessage;
  Worker.prototype.postMessage = function(message, ...rest) {
    if (message?.type === 'LD' && message.data?.source === 'city-scores' && message.data?.data?.features) {
      record('labels', message.data.data.features.map(f => ({...f.properties, coordinates:f.geometry.coordinates})));
    }
    return post.call(this, message, ...rest);
  };
  const read = () => {
    const headline = document.querySelector('.headline-sentence')?.innerText?.trim();
    const card = document.querySelector('.slot-card')?.innerText?.trim();
    if (headline) record('headlines', headline);
    if (card) record('cards', card);
  };
  new MutationObserver(read).observe(document, {subtree:true, childList:true, characterData:true});
})()"""

VIEWS = [
    ('nov-mar-laptop', '/nov-mar', 1512, 982),
    ('nov-mar-phone', '/nov-mar', 390, 844),
]
CORRECTNESS = [
    ('world-desktop', '/july', 1440, 1080),
    ('funchal-cold', '/portugal/funchal/july', 1440, 1080),
    ('san-francisco-phone', '/united-states/san-francisco/july', 390, 844),
    ('san-francisco-laptop', '/united-states/san-francisco/july', 1440, 1080),
    ('san-diego-desktop', '/united-states/san-diego/july', 1440, 1080),
    ('nearby-city-phone', '/@37.7751,-122.4181,9z/july', 390, 844),
    ('remote-area-phone', '/@-25,135,6z/july', 390, 844),
    ('san-francisco-forecast-phone', '/united-states/san-francisco/next-14-days', 390, 844),
    ('san-francisco-forecast-laptop', '/united-states/san-francisco/next-14-days', 1440, 1080),
]

class Fixture:
    async def start(self, overlay):
        self.process = await asyncio.create_subprocess_exec('node',str(Path(__file__).with_name('webkit_fixture_bridge.mjs')),str(overlay),cwd=Path(__file__).resolve().parents[2],stdin=asyncio.subprocess.PIPE,stdout=asyncio.subprocess.PIPE,limit=64*1024*1024)
        self.lock = asyncio.Lock()
        self.serial = 0
        self.reads = []
    async def route(self, route):
        request = route.request
        async with self.lock:
            self.serial += 1
            value = {'id':self.serial,'url':request.url,'method':request.method,'headers':request.headers,'body':base64.b64encode(request.post_data_buffer).decode() if request.post_data_buffer else None}
            self.process.stdin.write((json.dumps(value)+'\n').encode())
            await self.process.stdin.drain()
            result = json.loads(await self.process.stdout.readline())
        if result.get('error'): raise RuntimeError(result['error'])
        entity = base64.b64decode(result['body'])
        self.reads.append({'url':request.url,'status':result['status'],'encodedBytes':len(entity)})
        headers = result['headers']
        if headers.get('content-encoding')=='gzip':
            entity = gzip.decompress(entity)
            headers = {k:v for k,v in headers.items() if k.lower() not in ['content-encoding','content-length']}
        await route.fulfill(status=result['status'],headers=headers,body=entity)
    async def close(self):
        self.process.stdin.close()
        await self.process.wait()

async def measure(browser, base, view, output, settle_ms, fixture=None):
    name, pathname, width, height = view
    context = await browser.new_context(viewport={'width':width,'height':height}, color_scheme='light', locale='en-GB', timezone_id='Europe/Amsterdam')
    await context.add_init_script(INSTRUMENT)
    page = await context.new_page()
    if fixture:
        fixture.reads = []
        await page.route('http://127.0.0.1:5198/**',fixture.route)
    errors, requests, pending, tasks = [], [], set(), []
    page.on('pageerror', lambda error: errors.append(str(error)))
    page.on('request', lambda request: pending.add(request))
    async def finished(request):
        pending.discard(request)
        response = await request.response()
        try: sizes = await request.sizes()
        except Exception as error: sizes = {'error':str(error)}
        requests.append({'url':request.url,'method':request.method,'status':response.status if response else None,'sizes':sizes,'timing':request.timing})
    async def failed(request):
        pending.discard(request)
        errors.append(f'{request.url}: {request.failure}')
    page.on('requestfinished', lambda request: tasks.append(asyncio.create_task(finished(request))))
    page.on('requestfailed', lambda request: tasks.append(asyncio.create_task(failed(request))))
    began = time.monotonic()
    await page.goto(base.rstrip('/')+pathname, wait_until='domcontentloaded', timeout=120000)
    last_signature, stable_since, state = None, time.monotonic(), None
    deadline = time.monotonic()+120
    while time.monotonic()<deadline:
        await page.wait_for_timeout(100)
        state = await page.evaluate('''() => ({proof:window.__namedProof, headline:document.querySelector('.headline')?.innerText??'', card:document.querySelector('.slot-card')?.innerText??'', url:location.href, ms:performance.now()})''')
        signature = json.dumps([state['proof'],len(requests),len(pending)])
        if signature != last_signature:
            last_signature, stable_since = signature, time.monotonic()
        usable = state['proof']['headlines'] and state['proof']['labels']
        if usable and not pending and (time.monotonic()-stable_since)*1000>settle_ms:
            break
    await asyncio.gather(*tasks)
    state['resourceTiming'] = await page.evaluate('''() => performance.getEntriesByType('resource').map(r=>({name:r.name,start:r.startTime,end:r.responseEnd,transferSize:r.transferSize,encodedBodySize:r.encodedBodySize,decodedBodySize:r.decodedBodySize}))''')
    await page.screenshot(path=str(output/(name+'.png')))
    named_requests = [r for r in requests if '/api/named' in r['url'] or 'v6-named-' in r['url'] or r['url'].endswith('/named.bin.gz')]
    body = lambda rows: sum(r['sizes'].get('responseBodySize',0) for r in rows)
    proof = state['proof']
    ranked_headlines = [r for r in proof['headlines'] if r['value'].startswith('Nicest ')]
    scored_labels = [r for r in proof['labels'] if any(isinstance(f.get('score'),(int,float)) for f in r['value'])]
    result = {'name':name,'pathname':pathname,'viewport':[width,height], 'requests':requests,'requestCount':len(requests),'responseBodyBytes':body(requests),'namedRequests':named_requests,'namedResponseBodyBytes':body(named_requests),'errors':errors,'elapsedSeconds':time.monotonic()-began,'state':state,
      'headlineFirstMs':proof['headlines'][0]['ms'] if proof['headlines'] else None,'headlineSettledMs':proof['headlines'][-1]['ms'] if proof['headlines'] else None,
      'labelsFirstMs':proof['labels'][0]['ms'] if proof['labels'] else None,'labelsSettledMs':proof['labels'][-1]['ms'] if proof['labels'] else None,
      'rankedHeadlineFirstMs':ranked_headlines[0]['ms'] if ranked_headlines else None,'scoredLabelsFirstMs':scored_labels[0]['ms'] if scored_labels else None,
      'cardFirstMs':proof['cards'][0]['ms'] if proof['cards'] else None,'cardSettledMs':proof['cards'][-1]['ms'] if proof['cards'] else None,
      'passes':bool(ranked_headlines and scored_labels and not errors and not pending and (name.startswith(('world','nov-mar')) or 'Walk home' in state['card']))}
    if fixture: result['fixtureEncodedResponses'] = list(fixture.reads)
    (output/(name+'.json')).write_text(json.dumps(result,indent=2)+'\n')
    await context.close()
    print(json.dumps({k:result[k] for k in ['name','requestCount','responseBodyBytes','namedResponseBodyBytes','headlineFirstMs','headlineSettledMs','labelsFirstMs','labelsSettledMs','passes']}),flush=True)
    return result

async def main(args):
    args.out.mkdir(parents=True,exist_ok=True)
    fixture = Fixture() if args.fixture else None
    if fixture: await fixture.start(args.fixture)
    async with async_playwright() as playwright:
        browser = await playwright.webkit.launch(headless=True)
        views = VIEWS + (CORRECTNESS if args.correctness else [])
        if args.view: views = [v for v in views if v[0] in args.view]
        results = [await measure(browser,args.base,v,args.out,args.settle_ms,fixture) for v in views]
        await browser.close()
    if fixture: await fixture.close()
    summary = {'writtenBy':'AI, GPT-6.1 Sol, 2026-10-18','base':args.base,'browser':'headless WebKit','scriptSha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'measurement':'request.sizes responseBodySize; compressed entity bytes, excluding HTTP headers, protocol and TLS overhead','passes':all(r['passes'] for r in results),'views':[{k:v for k,v in r.items() if k not in ['requests','state','namedRequests']} for r in results]}
    (args.out/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
    if not summary['passes']: raise RuntimeError('Some views did not settle cleanly')

if __name__ == '__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base',default='https://nice.jjpcodes.com')
    parser.add_argument('--out',type=Path,required=True)
    parser.add_argument('--correctness',action='store_true')
    parser.add_argument('--view',action='append')
    parser.add_argument('--settle-ms',type=int,default=3000)
    parser.add_argument('--fixture',type=Path,help='Read-only local Worker overlay; compression is decoded by route fulfillment, so use live runs for wire bytes')
    asyncio.run(main(parser.parse_args()))
