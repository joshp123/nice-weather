# Written by AI on 2026.10.18 by GPT-6.1 Sol.
import asyncio,json,time
from pathlib import Path
from playwright.async_api import async_playwright
ROOT=Path('/tmp/nwf-agents/round5/cold-fixes')
async def main():
 result=[]
 async with async_playwright() as p:
  browser=await p.chromium.launch(headless=True,args=['--use-gl=angle','--use-angle=swiftshader','--enable-webgl'])
  for w,h in [(390,844),(1512,982),(3840,1080)]:
   context=await browser.new_context(viewport={'width':w,'height':h},color_scheme='light',locale='en-GB',timezone_id='Europe/Amsterdam');page=await context.new_page();errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
   async def delayed(route):
    await asyncio.sleep(3);await route.continue_()
   await context.route('**/data/v6/**/*.bin',delayed)
   await context.route('**/data/v6/cells.bin',delayed)
   await page.goto('http://127.0.0.1:5198/year',wait_until='domcontentloaded');await page.wait_for_timeout(7000)
   await page.get_by_role('button',name='Your taste',exact=True).click();await page.locator('.slider-input').first.wait_for()
   async def capture(stage):
    stats=await page.evaluate('''() => {const source=document.querySelector('.maplibregl-canvas');const canvas=document.createElement('canvas');canvas.width=source.width;canvas.height=source.height;const context=canvas.getContext('2d');context.drawImage(source,0,0);const data=context.getImageData(0,0,canvas.width,canvas.height).data;let coloured=0;for(let i=0;i<data.length;i+=4){if(Math.max(data[i],data[i+1],data[i+2])-Math.min(data[i],data[i+1],data[i+2])>30)coloured++}return{coloured,total:canvas.width*canvas.height}}''')
    path=ROOT/f'taste-{stage}-{w}x{h}-light.png';await page.screenshot(path=str(path));return {'stage':stage,'path':str(path),**stats}
   frames=[await capture('before')];start=time.monotonic();await page.locator('.slider-input').first.evaluate('(e)=>{e.value=1;e.dispatchEvent(new Event("input",{bubbles:true}))}')
   for target in [.1,.5,1.5,4.5]:
    await page.wait_for_timeout(max(0,target-(time.monotonic()-start))*1000);frame=await capture(str(target));frame['seconds']=time.monotonic()-start;frames.append(frame)
   result.append({'viewport':[w,h],'frames':frames,'errors':errors});await context.close()
  await browser.close()
 (ROOT/'taste-retention.json').write_text(json.dumps(result,indent=2));print(json.dumps(result))
asyncio.run(main())
