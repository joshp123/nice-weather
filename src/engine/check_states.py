# Written by AI on 2026.10.17 by model gpt-6.1-sol.
"""Exercise image-first loading, fast labels, taste changes and superseded selections in headless WebKit."""
import asyncio, json, pathlib
from playwright.async_api import async_playwright
ROOT=pathlib.Path(__file__).resolve().parents[2]
DEBUG_BUILD=pathlib.Path('/tmp/nwf-agents/v6b/engine/debug-build')
ASSETS={'engine-debug/'+str(p.relative_to(DEBUG_BUILD)):p.read_bytes() for p in DEBUG_BUILD.rglob('*') if p.is_file()}
assert 'engine-debug/debug.html' in ASSETS, 'Build the isolated proof bundle first'
OUT=pathlib.Path('/tmp/nwf-agents/v6b/engine')
CITIES=[dict(latitude=row[2],longitude=row[3]) for row in json.loads((ROOT/'artifacts/v6/labels-1.json').read_text())[:400]]

async def main():
    async with async_playwright() as p:
        browser=await p.webkit.launch()
        try:
            page=await browser.new_page(viewport={'width':1512,'height':982},device_scale_factor=2)
            errors=[];page.on('pageerror',lambda error:errors.append(str(error)))
            async def asset(route):
                path=route.request.url.split('5198/',1)[1].split('?',1)[0]
                kind='text/html' if path.endswith('.html') else 'text/javascript' if path.endswith('.js') else 'text/css' if path.endswith('.css') else 'application/octet-stream'
                await route.fulfill(body=ASSETS[path],content_type=kind)
            await page.route('**/engine-debug/**',asset)
            await page.goto('http://127.0.0.1:5198/engine-debug/debug.html')
            await page.wait_for_function('window.weatherDebugReady===true')
            await page.evaluate('''()=>{
                const old=weatherDebug.engine, Constructor=old.constructor;
                weatherDebug.map.removeLayer(old.layer.id);
                window.events=[];window.frames=[];window.readyScores=[];
                window.e=new Constructor(weatherDebug.map,{layerId:'proof-weather',onLoadingChange:loading=>{events.push(loading);if(!loading&&e.taste.warmShift===4){const score=e.scorePoints([{latitude:36.72,longitude:-4.42}])[0],mean=e.judgePoint(36.72,-4.42).meanScore;readyScores.push({score,mean});}},onRedraw:frame=>frames.push(frame)});
                weatherDebug.map.addLayer(e.layer,'sea');
            }''')
            # Initial taste is accepted before a period, without waiting for impossible data.
            assert await page.evaluate('async()=> (await e.setTaste(weatherDebug.getTaste())).milliseconds===0')
            await page.evaluate('e.setPeriod({kind:"month",month:11})')
            await page.evaluate('events.length=0;frames.length=0')
            gate=asyncio.Event();bulk=[]
            async def hold(route):
                bulk.append(route.request.url)
                await gate.wait();await route.continue_()
            await page.route('**/data/v6/**/*.bin',hold)
            await page.route('**/data/v6/cells.bin',hold)
            initial=await page.evaluate('''()=>{
                window.selection=e.setPeriod({kind:'year'});
                return {loading:e.loading,verdict:e.judgePoint(36.72,-4.42),events};
            }''')
            assert initial==dict(loading=True,verdict=None,events=[True]),initial
            await page.wait_for_function('frames.length>0 && !!e.scoreImage')
            await page.wait_for_function('e.loading && !e.data')
            for _ in range(100):
                if len(bulk)==11:break
                await asyncio.sleep(.02)
            assert len(bulk)==11,bulk
            parallel_record_requests=len(bulk)
            labels=await page.evaluate('''(points)=>{
                const gl=e.gl, original=gl.readPixels;let reads=0;gl.readPixels=function(...args){reads++;return original.apply(this,args)};
                const times=[];let score;
                for(let n=0;n<30;n++){const start=performance.now();score=e.scorePoints(points)[0];times.push(performance.now()-start);}
                gl.readPixels=original;
                return {count:points.length,score,reads,times,verdict:e.judgePoint(36.72,-4.42)};
            }''',CITIES)
            assert labels['reads']==0 and labels['score'] is not None and labels['verdict'] is None,labels
            # Move a slider while records are held. Default colours and labels must disappear.
            custom=await page.evaluate('''()=>{
                window.frameCountBeforeTaste=frames.length;window.changed=e.setTaste({...weatherDebug.getTaste(),warmShift:4});
                return {loading:e.loading,verdict:e.judgePoint(36.72,-4.42),missing:Number.isNaN(e.scorePoints([{latitude:36.72,longitude:-4.42}])[0])};
            }''')
            assert custom==dict(loading=True,verdict=None,missing=True),custom
            await page.wait_for_function('frames.length>frameCountBeforeTaste')
            assert await page.evaluate('e.loading && !e.data && Number.isNaN(e.scorePoints([{latitude:36.72,longitude:-4.42}])[0])')
            gate.set();await page.evaluate('async()=>{await selection;await changed;}')
            assert await page.evaluate('!e.loading && !!e.judgePoint(36.72,-4.42) && events.join() === "true,false"')
            ready_scores=await page.evaluate('readyScores')
            assert ready_scores and all(abs(row['score']-row['mean'])<.001 for row in ready_scores),ready_scores
            # Cached days/months draw immediately even if their image request is held.
            cached=await page.evaluate('''async()=>{
                await e.setTaste(weatherDebug.getTaste());
                const original=e.source.scoreImage;e.source.scoreImage=()=>new Promise(()=>{});
                const started=performance.now();await e.setPeriod({kind:'month',month:11});
                const result={milliseconds:performance.now()-started,loading:e.loading,score:e.judgePoint(36.72,-4.42).meanScore};
                e.source.scoreImage=original;return result;
            }''')
            assert not cached['loading'] and cached['score'] is not None,cached
            # Even cached-period queries return null during a new selection.
            await page.evaluate('''()=>{
                window.originalImage=e.source.scoreImage.bind(e.source);
                e.source.scoreImage=async()=>null;
                window.originalLoad=e.source.load.bind(e.source);
                e.source.load=period=>period.kind==='forecast'?new Promise(resolve=>window.releaseForecast=async()=>resolve(await originalLoad(period))):originalLoad(period);
                window.slow=e.setPeriod({kind:'forecast'});
            }''')
            assert await page.evaluate('e.loading && e.judgePoint(36.72,-4.42,{kind:"year"})===null')
            await page.wait_for_function('!!window.releaseForecast')
            await page.evaluate('e.setPeriod({kind:"month",month:11})')
            expected=await page.evaluate('e.judgePoint(36.72,-4.42)')
            await page.evaluate('async()=>{await releaseForecast();await slow;}')
            assert await page.evaluate('e.period.month===11 && !e.loading')
            assert await page.evaluate('e.judgePoint(36.72,-4.42)')==expected
            # Missing image uses records; failed records reject and expose no stale verdict.
            await page.evaluate('e.setTaste(weatherDebug.getTaste())')
            await page.evaluate('e.setPeriod({kind:"month",month:7})')
            assert await page.evaluate('!!e.judgePoint(1.35,103.82) && !e.scoreImage')
            failed=await page.evaluate('''async()=>{
                e.source.loaded=()=>null; e.source.load=async()=>{throw new Error('record failure for proof')};
                let rejected=false;try{await e.setPeriod({kind:'month',month:8})}catch(error){rejected=error.message==='record failure for proof'}
                return {rejected,loading:e.loading,verdict:e.judgePoint(36.72,-4.42)};
            }''')
            assert failed==dict(rejected=True,loading=False,verdict=None),failed
            assert not errors,errors
            result=dict(initial=initial,cachedPeriodWithoutImage=cached,parallelRecordRequests=parallel_record_requests,recordReadyScores=ready_scores,imageLabels=labels,sliderWhileLoading=custom,supersededSelection=True,missingImageFallback=True,failedRecords=failed,errors=errors)
            (OUT/'states.json').write_text(json.dumps(result,indent=2));print(json.dumps(result),flush=True)
        finally:await browser.close()

asyncio.run(main())
