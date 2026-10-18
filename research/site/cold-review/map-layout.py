# Written by AI on 2026.10.18 by GPT-6.1 Sol.
import asyncio,json
from pathlib import Path
from playwright.async_api import async_playwright
ROOT=Path('/tmp/nwf-agents/round5/cold-fixes');ROOT.mkdir(exist_ok=True)
async def main():
 report=[]
 async with async_playwright() as p:
  browser=await p.webkit.launch(headless=True)
  for w,h in [(390,844),(1512,982),(3840,1080)]:
   c=await browser.new_context(viewport={'width':w,'height':h},color_scheme='light',locale='en-GB',timezone_id='Europe/Amsterdam');page=await c.new_page();errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
   await page.goto('http://127.0.0.1:5198/',wait_until='domcontentloaded');await page.wait_for_timeout(8000)
   async def shot(state):
    path=ROOT/f'{state}-{w}x{h}-light.png';await page.screenshot(path=str(path)); report.append({'state':state,'viewport':[w,h],'path':str(path),'errors':list(errors),'bounds':await page.evaluate('''() => [...document.querySelectorAll('.slot-headline,.slot-bar,.slot-card,.time-picker-panel,.maplibregl-ctrl-bottom-right,.legend-block')].filter(e=>e.getClientRects().length).map(e=>{let r=e.getBoundingClientRect();return{class:e.className,x:r.x,y:r.y,width:r.width,height:r.height}})''')})
   await shot('first')
   await page.get_by_role('button',name='Choose when',exact=True).click();await shot('picker'); await page.keyboard.press('Escape')
   await page.goto('http://127.0.0.1:5198/greece/chania',wait_until='domcontentloaded');await page.wait_for_timeout(6000);await shot('place')
   await page.goto('http://127.0.0.1:5198/year',wait_until='domcontentloaded');await page.wait_for_timeout(2000);await page.get_by_role('button',name='Your taste',exact=True).click();await page.locator('.slider-input').first.wait_for()
   await page.locator('.slider-input').first.evaluate('(e)=>{e.value=1;e.dispatchEvent(new Event("input",{bubbles:true}))}')
   for delay in [100,500,1000]:
    await page.wait_for_timeout(delay);await shot('slider-'+str(delay))
   await c.close()
  await browser.close()
 (ROOT/'layout-proof.json').write_text(json.dumps(report,indent=2));print(json.dumps(report))
asyncio.run(main())
