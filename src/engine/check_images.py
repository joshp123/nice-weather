# Written by AI on 2026.10.17 by model gpt-6.1-sol.
"""Compare real score-image and independently recounted GPU map pixels, in headless WebKit."""
import asyncio, io, json, pathlib, sys
import numpy as np
from PIL import Image
from playwright.async_api import async_playwright
ROOT=pathlib.Path(__file__).resolve().parents[2]
DEBUG_BUILD=pathlib.Path('/tmp/nwf-agents/v6b/engine/debug-build')
ASSETS={'engine-debug/'+str(p.relative_to(DEBUG_BUILD)):p.read_bytes() for p in DEBUG_BUILD.rglob('*') if p.is_file()}
assert 'engine-debug/debug.html' in ASSETS, 'Build the isolated proof bundle first'
OUT=pathlib.Path('/tmp/nwf-agents/v6b/engine');OUT.mkdir(parents=True,exist_ok=True)

IMAGE_FOLDER=pathlib.Path(sys.argv[1]) if len(sys.argv)>1 else None
SUFFIX='corrected' if IMAGE_FOLDER else 'published'

async def main():
    results=[]
    async with async_playwright() as p:
        for name in ['webkit']:
            browser=await p.webkit.launch()
            try:
                page=await browser.new_page(viewport={'width':1512,'height':982},device_scale_factor=2)
                async def asset(route):
                    path=route.request.url.split('5198/',1)[1].split('?',1)[0]
                    body=ASSETS.get(path)
                    if body is None: raise RuntimeError('Debug build missing: '+path)
                    kind='text/html' if path.endswith('.html') else 'text/javascript' if path.endswith('.js') else 'text/css' if path.endswith('.css') else 'application/octet-stream'
                    await route.fulfill(body=body,content_type=kind)
                await page.route('**/engine-debug/**',asset)
                if IMAGE_FOLDER:
                    async def corrected_image(route):
                        filename=route.request.url.rsplit('/',1)[1]
                        await route.fulfill(body=(IMAGE_FOLDER/filename).read_bytes(),content_type='image/png')
                    await page.route('**/climate/scores-*.png',corrected_image)
                page.on('pageerror',lambda error:print('pageerror',str(error),flush=True))
                await page.goto('http://127.0.0.1:5198/engine-debug/debug.html')
                await page.wait_for_function('window.weatherDebugReady===true',timeout=90000)
                await page.wait_for_function("weatherDebug.map.isSourceLoaded('openmaptiles') && weatherDebug.map.areTilesLoaded()",timeout=60000)
                await page.add_style_tag(content='aside { visibility: hidden }')
                for period in [dict(kind='month',month=11),dict(kind='year'),dict(kind='forecast'),*[dict(kind='forecast-day',day=day) for day in range(14)]]:
                    await page.evaluate('(period)=>weatherDebug.setPeriod(period)',period)
                    await page.wait_for_function('!!weatherDebug.engine.scoreImage')
                    await page.evaluate('weatherDebug.engine.setTaste(weatherDebug.getTaste())')
                    await page.evaluate("() => new Promise(resolve=>{weatherDebug.map.once('idle',resolve);weatherDebug.map.triggerRepaint();})")
                    image=await page.locator('canvas.maplibregl-canvas').screenshot()
                    stats=await page.evaluate('''async()=>{
                        const e=weatherDebug.engine, image=e.scoreImage;if(!image)throw new Error('Published score image missing');
                        const gl=e.gl,before=gl.getParameter(gl.FRAMEBUFFER_BINDING);gl.bindFramebuffer(gl.FRAMEBUFFER,e.framebuffer);
                        const values=new Float32Array(1440*721);gl.readPixels(0,0,1440,721,gl.RED,gl.FLOAT,values);gl.bindFramebuffer(gl.FRAMEBUFFER,before);
                        let mismatches=0,land=0,maximum=0;const examples=[];
                        for(let i=0;i<values.length;i++)if(image.bytes[i]!==255){land++;const error=Math.abs(Math.round(values[i])-image.bytes[i]);if(error){mismatches++;if(examples.length<5)examples.push({index:i,gpu:values[i],image:image.bytes[i],cpu:e.judgePoint(90-Math.floor(i/1440)/4,(i%1440)/4-180)?.meanScore});}maximum=Math.max(maximum,error);}
                        window.savedScoreImage=image;window.savedImageLoader=e.source.scoreImage.bind(e.source);
                        e.scoreImage=undefined;e.source.scoreImage=async()=>null;
                        await e.setTaste(weatherDebug.getTaste());
                        return {land,mismatches,maximum,examples};
                    }''')
                    await page.evaluate("() => new Promise(resolve=>{weatherDebug.map.once('idle',resolve);weatherDebug.map.triggerRepaint();})")
                    gpu=await page.locator('canvas.maplibregl-canvas').screenshot()
                    a=np.asarray(Image.open(io.BytesIO(image))).astype(int);b=np.asarray(Image.open(io.BytesIO(gpu))).astype(int)
                    delta=np.abs(a-b);different=np.any(delta[:,:,:3]>0,axis=2)
                    row=dict(browser=name,period=period,grid=stats,pixels=int(different.sum()),total=int(different.size),maximumChannelError=int(delta.max()),meanChannelError=float(delta.mean()))
                    results.append(row);print(json.dumps(row),flush=True)
                    word='november' if period['kind']=='month' else f'day-{period["day"]}' if period['kind']=='forecast-day' else period['kind']
                    if period['kind']!='forecast-day' or period['day'] in [0,13]:
                        (OUT/f'{name}-{word}-{SUFFIX}-image.png').write_bytes(image);(OUT/f'{name}-{word}-{SUFFIX}-gpu.png').write_bytes(gpu)
                    await page.evaluate('''async()=>{const e=weatherDebug.engine;e.scoreImage=window.savedScoreImage;e.source.scoreImage=window.savedImageLoader;await e.setTaste(weatherDebug.getTaste());}''')
                (OUT/f'image-parity-{SUFFIX}.json').write_text(json.dumps(results,indent=2))
            finally: await browser.close()

asyncio.run(main())
