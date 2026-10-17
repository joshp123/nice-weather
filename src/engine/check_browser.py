# Written by AI on 2026.10.17 by model gpt-6.1-sol.
import json, pathlib, sys
from playwright.sync_api import sync_playwright
out = pathlib.Path('/tmp/nwf-agents/overnight/engine')
real = '--real' in sys.argv
source = '/data/v6' if real else '/data/v6-synthetic'
label = 'real' if real else 'synthetic'
city_file = pathlib.Path(__file__).resolve().parents[2] / 'artifacts/places.tsv'
cities = [dict(latitude=float(row[3]), longitude=float(row[4])) for row in [line.split('\t') for line in city_file.read_text().splitlines()[:100]]]
places=[dict(name='Malaga',latitude=36.72,longitude=-4.42,month=11),dict(name='Rotterdam',latitude=51.92,longitude=4.48,month=1),dict(name='Singapore',latitude=1.35,longitude=103.82,month=7)]
with sync_playwright() as p:
    for name in ['chromium','webkit']:
        browser=getattr(p,name).launch(headless=False, **({'args':['--use-angle=metal']} if name=='chromium' else {}))
        try:
            page=browser.new_page(viewport={'width':1512,'height':982},device_scale_factor=1)
            errors=[]; requests=[]
            page.on('pageerror',lambda e: errors.append(str(e)))
            page.on('request',lambda r: requests.append(r.url) if '/data/v6' in r.url else None)
            page.goto(f'http://127.0.0.1:5198/engine-debug/debug.html?data={source}',wait_until='domcontentloaded')
            page.wait_for_function('window.weatherDebugReady === true',timeout=90000)
            if page.locator('#status').inner_text(): raise RuntimeError(page.locator('#status').inner_text())
            initial_requests=list(requests)
            # Let the real basemap arrive before inspecting the rendered map.
            page.wait_for_function("weatherDebug.map.isSourceLoaded('openmaptiles') && weatherDebug.map.areTilesLoaded()",timeout=45000)
            page.evaluate("() => new Promise(resolve => { weatherDebug.map.once('idle', resolve); weatherDebug.map.triggerRepaint(); })")
            info=page.evaluate('''() => { const gl=weatherDebug.map.getCanvas().getContext('webgl2'), ext=gl.getExtension('WEBGL_debug_renderer_info');return {renderer:gl.getParameter(ext.UNMASKED_RENDERER_WEBGL),error:gl.getError(),size:[gl.drawingBufferWidth,gl.drawingBufferHeight]}; }''')
            month=page.evaluate('weatherDebug.measure(12)')
            page.screenshot(path=str(out/f'{name}-{label}-month.png'))
            page.evaluate("weatherDebug.setPeriod({kind:'year'})")
            year=page.evaluate('weatherDebug.measure(12)')
            page.screenshot(path=str(out/f'{name}-{label}-year.png'))
            yearBatch100=page.evaluate('''(points)=>{const start=performance.now();const scores=weatherDebug.engine.scorePoints(points);return {milliseconds:performance.now()-start,count:scores.length,finite:[...scores].filter(Number.isFinite).length};}''',cities)
            # Read the actual GPU-resolved fine grid and independently apply its displayed spline.
            comparisons=[]; reference=[]
            for place in places:
                period=dict(kind='month',month=place['month'])
                page.evaluate('(period)=>weatherDebug.setPeriod(period)',period)
                result=page.evaluate('''({latitude,longitude})=>{
                    const e=weatherDebug.engine, gl=e.gl, before=gl.getParameter(gl.FRAMEBUFFER_BINDING), fb=gl.createFramebuffer();
                    gl.bindFramebuffer(gl.FRAMEBUFFER,fb); gl.framebufferTexture2D(gl.FRAMEBUFFER,gl.COLOR_ATTACHMENT0,gl.TEXTURE_2D,e.fineScores,0);
                    const values=new Float32Array(1440*721);gl.readPixels(0,0,1440,721,gl.RED,gl.FLOAT,values);
                    gl.bindFramebuffer(gl.FRAMEBUFFER,before);gl.deleteFramebuffer(fb);
                    const x=((longitude+180)*4%1440+1440)%1440,y=(90-latitude)*4;
                    const weights=t=>[(1-t)**3/6,(3*t**3-6*t*t+4)/6,(-3*t**3+3*t*t+3*t+1)/6,t**3/6];
                    const wx=weights(x-Math.floor(x)),wy=weights(y-Math.floor(y));let score=0;
                    for(let r=0;r<4;r++)for(let c=0;c<4;c++)score+=values[Math.max(0,Math.min(720,Math.floor(y)+r-1))*1440+((Math.floor(x)+c-1)%1440+1440)%1440]*wx[c]*wy[r];
                    return {gpu:score,verdict:e.judgePoint(latitude,longitude),error:gl.getError()};
                }''',place)
                assert result['error'] == 0 and result['verdict'] is not None, result
                assert abs(result['gpu']-result['verdict']['meanScore']) < .001, result
                comparisons.append(dict(place=place['name'],**result))
                sample=page.evaluate('''async({latitude,longitude,month})=>({samples:await weatherDebug.referenceSample(latitude,longitude,{kind:'month',month}),taste:weatherDebug.getTaste(),verdict:weatherDebug.engine.judgePoint(latitude,longitude)})''',place)
                reference.append(dict(place=place['name'],**sample))
            # Exercise every forecast slot, both month boundaries and seasons with the synthetic fixture.
            if not real:
                for period in [dict(kind='season',season=s) for s in ['Dec-Feb','Mar-May','Jun-Aug','Sep-Nov']]+[dict(kind='forecast')]+[dict(kind='forecast-day',day=d) for d in range(15)]:
                    page.evaluate('(period)=>weatherDebug.setPeriod(period)',period)
            # Exercise each actual slider with keyboard input at both ends.
            for slider_id in ['perfectFrom','tooHotFrom','walkHome','humidity','wind','rain']:
                slider=page.locator('#'+slider_id)
                slider.focus()
                for key in ['Home','End']:
                    slider.press(key)
                    page.evaluate('weatherDebug.engine.setTaste(weatherDebug.getTaste())')
                    verdict=page.evaluate('weatherDebug.engine.judgePoint(36.72,-4.42)')
                    assert verdict is not None and 0 <= verdict['meanScore'] <= 100, (slider_id,key,verdict)
            batch=page.evaluate('''(points)=>{const start=performance.now();const scores=weatherDebug.engine.scorePoints(points);return {milliseconds:performance.now()-start,count:scores.length,finite:[...scores].filter(Number.isFinite).length};}''',cities)
            assert not errors, errors
            result=dict(info=info,month=month,year=year,initial_requests=initial_requests,errors=errors,gpu_comparisons=comparisons,batch100=batch,yearBatch100=yearBatch100)
            (out/f'{name}-{label}-proof.json').write_text(json.dumps(result,indent=2))
            (out/f'{name}-{label}-reference.json').write_text(json.dumps(reference))
            print(name,json.dumps(result),flush=True)
        finally: browser.close()
