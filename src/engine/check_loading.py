# Written by AI on 2026.10.17 by model gpt-6.1-sol.
"""Actual app routes, shared 500 KB/s download budget and 100 ms added response latency.
Usage: check_loading.py before|after. Run with uv --with playwright python -I.
"""
import asyncio, json, pathlib, re, sys, time
from urllib.parse import urlsplit
from playwright.async_api import async_playwright

ROOT = pathlib.Path(__file__).resolve().parents[2]
OUT = pathlib.Path('/tmp/nwf-agents/v6b/engine')
OUT.mkdir(parents=True, exist_ok=True)
PHASE = sys.argv[1]
PATHS = sys.argv[2:] or ['/','/november','/year']
# Hold the build fixed across all three visits while other owners work on the product.
BUILD=OUT/'before-build' if PHASE=='before' else ROOT/'dist'
ASSETS = {('/' + str(p.relative_to(BUILD))): p.read_bytes() for p in BUILD.rglob('*') if p.is_file()}
HOOK = """(() => {
  const metrics = window.weatherLoadingProof = {};
  addEventListener('DOMContentLoaded',()=>new MutationObserver(()=>{
    if (!metrics.cardVisible && document.querySelector('.card .score-badge')) {
      metrics.cardVisible=performance.now();window.recordWeatherMetric({kind:'cardVisible',milliseconds:metrics.cardVisible});
    }
  }).observe(document.body,{childList:true,subtree:true}));
  const draw = WebGL2RenderingContext.prototype.drawArrays;
  WebGL2RenderingContext.prototype.drawArrays = function(...args) {
    const program=this.getParameter(this.CURRENT_PROGRAM);
    if(program && this.getUniformLocation(program,'u_thermal_taste')!==null && !metrics.cardReady) {
      metrics.cardReady=performance.now();window.recordWeatherMetric({kind:'cardReady',milliseconds:metrics.cardReady});
    }
    const result=draw.apply(this,args);
    if(program && this.getUniformLocation(program,'u_band_colours')!==null && this.getUniformLocation(program,'u_world_offset')!==null && !metrics.colourQueued) {
      metrics.colourQueued=true;
      requestAnimationFrame(()=>requestAnimationFrame(()=>{metrics.firstColour=performance.now();window.recordWeatherMetric({kind:'firstColour',milliseconds:metrics.firstColour});}));
    }
    return result;
  };
})();"""

class Connection:
    def __init__(self):
        self.transfers=[]
        self.tasks=set()
        self.failures=[]
        self.sent=0
        self.finished=[]
    async def run(self):
        previous=time.monotonic()
        while True:
            await asyncio.sleep(.01)
            now=time.monotonic();budget=(now-previous)*500_000;previous=now
            active=[t for t in self.transfers if t['remaining']>0]
            while active and budget>=1:
                share=budget/len(active)
                for transfer in active:
                    sent=min(share,transfer['remaining']);transfer['remaining']-=sent;budget-=sent;self.sent+=sent
                    if transfer['remaining']<=0: transfer['done'].set()
                active=[t for t in active if t['remaining']>0]
    async def route(self, route):
        task=asyncio.current_task();self.tasks.add(task)
        url=route.request.url;parts=urlsplit(url)
        try:
            if parts.netloc=='127.0.0.1:5198' and (parts.path in ASSETS or parts.path in ['/','/november','/year']):
                path=parts.path if parts.path in ASSETS else '/index.html'
                body=ASSETS[path]
                extension=path.rsplit('.',1)[-1]
                kind={'html':'text/html','js':'text/javascript','css':'text/css','woff2':'font/woff2','woff':'font/woff','svg':'image/svg+xml'}.get(extension,'application/octet-stream')
                status=200;headers={'content-type':kind}
            else:
                response=await route.fetch(timeout=90000)
                body=await response.body();status=response.status;headers=response.headers
                headers.pop('content-encoding',None);headers.pop('content-length',None)
            await asyncio.sleep(.1)
            transfer={'url':url,'remaining':len(body),'done':asyncio.Event()}
            self.transfers.append(transfer)
            if body: await transfer['done'].wait()
            self.finished.append({'url':url,'bytes':len(body)})
            await route.fulfill(status=status,headers=headers,body=body)
        except Exception as error:
            self.failures.append({'url':url,'error':str(error)})
            if not route.request.is_navigation_request():
                try: await route.abort()
                except Exception: pass
            else: raise
        finally: self.tasks.discard(task)

async def main():
    previous=OUT/f'loading-{PHASE}.json'
    all_results=[row for row in json.loads(previous.read_text()) if row['path'] not in PATHS] if previous.exists() and len(sys.argv)>2 else []
    async with async_playwright() as p:
        for browser_name in ['webkit']:
            browser=await p.webkit.launch()
            try:
                for path in PATHS:
                    context=await browser.new_context(viewport={'width':1512,'height':982},device_scale_factor=2)
                    connection=Connection();pump=asyncio.create_task(connection.run())
                    page=await context.new_page();results={'browser':browser_name,'path':path,'phase':PHASE}
                    errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
                    async def record(source, metric):
                        results[metric['kind']]=dict(milliseconds=metric['milliseconds'],bytes=round(connection.sent),completed=list(connection.finished))
                    await page.expose_binding('recordWeatherMetric',record)
                    await page.add_init_script(HOOK)
                    await context.route(re.compile(r'^https?://'),connection.route)
                    async def progress():
                        while True:
                            await asyncio.sleep(10)
                            print('progress',browser_name,path,round(connection.sent),len(connection.finished),errors,await page.evaluate('JSON.stringify(window.weatherLoadingProof)'),flush=True)
                    progress_task=asyncio.create_task(progress())
                    try:
                        await page.goto('http://127.0.0.1:5198'+path,wait_until='domcontentloaded',timeout=120000)
                        await page.locator('.slot-bar > *').first.wait_for(state='visible',timeout=30000)
                        await page.locator('canvas.maplibregl-canvas').click(position={'x':756,'y':491})
                        await page.wait_for_function('weatherLoadingProof.firstColour && weatherLoadingProof.cardVisible',timeout=180000)
                        results['cardText']=await page.locator('.card').inner_text()
                        await page.screenshot(path=str(OUT/f'{PHASE}-{browser_name}-{path.strip("/") or "forecast"}.png'))
                        results['errors']=errors
                        results['networkErrors']=connection.failures
                        all_results.append(results)
                        (OUT/f'loading-{PHASE}.json').write_text(json.dumps(all_results,indent=2))
                        print(json.dumps({key:value for key,value in results.items() if key not in ['firstColour','cardReady','cardVisible','cardText']}|{key:{k:v for k,v in results[key].items() if k!='completed'} for key in ['firstColour','cardReady','cardVisible']}),flush=True)
                    finally:
                        progress_task.cancel()
                        await context.close()
                        pump.cancel()
                        for task in list(connection.tasks): task.cancel()
                        await asyncio.gather(progress_task,pump,*list(connection.tasks),return_exceptions=True)
            finally: await browser.close()

if __name__=='__main__': asyncio.run(main())
