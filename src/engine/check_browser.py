# Written by AI on 2026.10.17 by model gpt-6.1-sol.
import json, pathlib, sys
from playwright.sync_api import sync_playwright
out = pathlib.Path('/tmp/nwf-agents/overnight/engine')
real = '--real' in sys.argv
source = '/data/v6' if real else '/data/v6-synthetic'
label = 'real' if real else 'synthetic'
city_file = pathlib.Path(__file__).resolve().parents[2] / 'artifacts/places.tsv'
cities = [dict(latitude=float(row[3]), longitude=float(row[4])) for row in [line.split('\t') for line in city_file.read_text().splitlines()[:100]]]
score_cities=[dict(name='Malaga',latitude=36.72,longitude=-4.42),dict(name='Lisbon',latitude=38.72,longitude=-9.14),dict(name='Rotterdam',latitude=51.92,longitude=4.48),dict(name='Cape Town',latitude=-33.92,longitude=18.42)]
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
            assert page.evaluate("weatherDebug.engine.judgePoint.length === 3 && weatherDebug.engine.judgePoint(36.72,-4.42,{kind:'month',month:1}) === null && weatherDebug.engine.judgePoint(36.72,-4.42,{kind:'forecast-day',day:0}) === null")
            # Let the real basemap arrive before inspecting the rendered map.
            page.wait_for_function("weatherDebug.map.isSourceLoaded('openmaptiles') && weatherDebug.map.areTilesLoaded()",timeout=45000)
            page.evaluate("() => new Promise(resolve => { weatherDebug.map.once('idle', resolve); weatherDebug.map.triggerRepaint(); })")
            info=page.evaluate('''() => { const gl=weatherDebug.map.getCanvas().getContext('webgl2'), ext=gl.getExtension('WEBGL_debug_renderer_info');return {renderer:gl.getParameter(ext.UNMASKED_RENDERER_WEBGL),error:gl.getError(),size:[gl.drawingBufferWidth,gl.drawingBufferHeight]}; }''')
            month=page.evaluate('weatherDebug.measure(12)')
            page.screenshot(path=str(out/f'{name}-{label}-month.png'))
            page.evaluate("weatherDebug.setPeriod({kind:'year'})")
            year=page.evaluate('weatherDebug.measure(12)')
            page.screenshot(path=str(out/f'{name}-{label}-year.png'))
            # A year's loaded months can power the card without any map selection or network work.
            before_requests=len(requests)
            cached_months=page.evaluate("""() => {
                const e=weatherDebug.engine, revision=e.revision, current=e.judgePoint(36.72,-4.42);
                const months=Array.from({length:12},(_,i)=>e.judgePoint(36.72,-4.42,{kind:'month',month:i+1}));
                const seasons=['Dec-Feb','Mar-May','Jun-Aug','Sep-Nov'].map(season=>e.judgePoint(36.72,-4.42,{kind:'season',season}));
                return {months,seasons,unchanged:e.revision===revision && JSON.stringify(current)===JSON.stringify(e.judgePoint(36.72,-4.42)),year:current};
            }""")
            assert len(requests)==before_requests and cached_months['unchanged'], cached_months
            assert all(v and v['sampleDays']==8 for v in cached_months['months'])
            assert all(v and v['sampleDays']==24 for v in cached_months['seasons'])
            assert abs(sum(v['meanScore'] for v in cached_months['months'])/12-cached_months['year']['meanScore'])<1e-9
            for key,value in cached_months['year']['typical'].items():
                assert abs(sum(v['typical'][key] for v in cached_months['months'])/12-value)<1e-9
            yearBatch100=page.evaluate('''(points)=>{const start=performance.now();const scores=weatherDebug.engine.scorePoints(points);return {milliseconds:performance.now()-start,count:scores.length,finite:[...scores].filter(Number.isFinite).length};}''',cities)
            # Compare the published integer table to all twelve cached month verdicts at four places.
            default_scores=[]
            if real:
                table=(city_file.parent/'v6/climate/scores.bin').read_bytes()
                assert len(table)==1440*721*12
                for city in score_cities:
                    values=page.evaluate("""({latitude,longitude})=>{
                        const e=weatherDebug.engine;
                        return Array.from({length:12},(_,month)=>{
                            const data=e.source.loaded({kind:'month',month:month+1}), masks=[...data.offsets.values()];
                            const x=((longitude+180)*4%1440+1440)%1440,y=(90-latitude)*4;let index=null,distance=Infinity;
                            for(let r=Math.round(y)-2;r<=Math.round(y)+2;r++)for(let c=Math.round(x)-2;c<=Math.round(x)+2;c++){
                                if(r<0||r>600)continue;const i=r*1440+((c%1440+1440)%1440),d=(r-y)**2+(c-x)**2;
                                if(masks.every(m=>m[i]!==255)&&d<distance){distance=d;index=i;}
                            }
                            const verdict=e.judgePoint(latitude,longitude,{kind:'month',month:month+1});
                            const exact=e.judgePoint(90-Math.floor(index/1440)/4,index%1440/4-180,{kind:'month',month:month+1});
                            return {month:month+1,index,score:verdict.meanScore,rounded:Math.round(verdict.meanScore),exact:exact.meanScore};
                        });
                    }""",city)
                    for value in values:
                        published=table[value['index']*12+value['month']-1]
                        assert value['score']==value['exact'] and value['rounded']==published, (city,value,published)
                        value['published']=published
                    default_scores.append(dict(place=city['name'],months=values))
                assert page.evaluate("weatherDebug.engine.judgePoint(-30,-140)===null && Number.isNaN(weatherDebug.engine.scorePoints([{latitude:-30,longitude:-140}])[0])")
            # Read the actual GPU-resolved fine grid at the nearest land point, before the displayed spline.
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
                    const masks=[...e.data.offsets.values()];let index=null,distance=Infinity;
                    for(let r=Math.round(y)-2;r<=Math.round(y)+2;r++)for(let c=Math.round(x)-2;c<=Math.round(x)+2;c++){
                        if(r<0||r>600)continue;const i=r*1440+((c%1440+1440)%1440),d=(r-y)**2+(c-x)**2;
                        if(masks.every(m=>m[i]!==255)&&d<distance){distance=d;index=i;}
                    }
                    const score=values[index],label=e.scorePoints([{latitude,longitude}])[0];
                    if(Math.abs(label-score)>.001)throw new Error('City label differs from the fine-point score');
                    return {gpu:score,verdict:e.judgePoint(latitude,longitude),error:gl.getError()};
                }''',place)
                assert result['error'] == 0 and result['verdict'] is not None, result
                assert abs(result['gpu']-result['verdict']['meanScore']) < .001, result
                comparisons.append(dict(place=place['name'],**result))
                sample=page.evaluate('''async({latitude,longitude,month})=>({samples:await weatherDebug.referenceSample(latitude,longitude,{kind:'month',month}),taste:weatherDebug.getTaste(),verdict:weatherDebug.engine.judgePoint(latitude,longitude)})''',place)
                reference.append(dict(place=place['name'],**sample))
            # All fourteen forecast slots are immediately available after loading the forecast.
            page.evaluate("weatherDebug.setPeriod({kind:'forecast'})")
            before_requests=len(requests)
            cached_forecast=page.evaluate("""() => {
                const e=weatherDebug.engine, revision=e.revision, current=e.judgePoint(36.72,-4.42);
                const days=Array.from({length:14},(_,day)=>e.judgePoint(36.72,-4.42,{kind:'forecast-day',day}));
                return {days,forecast:current,unchanged:e.revision===revision && JSON.stringify(current)===JSON.stringify(e.judgePoint(36.72,-4.42)),invalid:e.judgePoint(36.72,-4.42,{kind:'forecast-day',day:14})};
            }""")
            assert len(requests)==before_requests and cached_forecast['unchanged'] and cached_forecast['invalid'] is None
            assert all(v and v['sampleDays']==1 for v in cached_forecast['days'])
            assert abs(sum(v['meanScore'] for v in cached_forecast['days'])/14-cached_forecast['forecast']['meanScore'])<1e-9
            for key,value in cached_forecast['forecast']['typical'].items():
                assert abs(sum(v['typical'][key] for v in cached_forecast['days'])/14-value)<1e-9
            for day,cached in enumerate(cached_forecast['days']):
                page.evaluate('(day)=>weatherDebug.setPeriod({kind:"forecast-day",day})',day)
                selected=page.evaluate('weatherDebug.engine.judgePoint(36.72,-4.42)')
                assert selected==cached, (day,selected,cached)
                gpu=page.evaluate('weatherDebug.engine.scorePoints([{latitude:36.72,longitude:-4.42}])[0]')
                assert abs(gpu-cached['meanScore'])<.001
            for period in [dict(kind='year'),dict(kind='forecast'),dict(kind='forecast-day',day=13)]:
                sample=page.evaluate("""async(period)=>({samples:await weatherDebug.referenceSample(36.72,-4.42,period),taste:weatherDebug.getTaste(),verdict:weatherDebug.engine.judgePoint(36.72,-4.42,period)})""",period)
                reference.append(dict(place='Malaga '+str(period),**sample))
            # Cached period samplers must follow taste changes while the map stays on another period.
            before=page.evaluate('weatherDebug.engine.judgePoint(36.72,-4.42,{kind:"month",month:11})')
            page.evaluate('weatherDebug.engine.setTaste({...weatherDebug.getTaste(),warmShift:5})')
            after=page.evaluate('weatherDebug.engine.judgePoint(36.72,-4.42,{kind:"month",month:11})')
            assert before['typical']==after['typical'] and before['meanScore']!=after['meanScore']
            page.evaluate('weatherDebug.setPeriod({kind:"month",month:11})')
            assert page.evaluate('weatherDebug.engine.judgePoint(36.72,-4.42)')==after
            page.evaluate('weatherDebug.engine.setTaste(weatherDebug.getTaste())')
            for period in [dict(kind='season',season=s) for s in ['Dec-Feb','Mar-May','Jun-Aug','Sep-Nov']]:
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
            result=dict(info=info,month=month,year=year,initial_requests=initial_requests,errors=errors,gpu_comparisons=comparisons,default_scores=default_scores,batch100=batch,yearBatch100=yearBatch100,cachedPeriods=dict(months=12,seasons=4,forecastDays=14,unchangedMap=True,noRequests=True,tasteUpdates=True))
            (out/f'{name}-{label}-proof.json').write_text(json.dumps(result,indent=2))
            (out/f'{name}-{label}-reference.json').write_text(json.dumps(reference))
            print(name,json.dumps(result),flush=True)
        finally: browser.close()
