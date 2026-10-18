# Written by AI on 2026.10.19 by model GPT-6.1 Sol.
import asyncio,json
from pathlib import Path
from playwright.async_api import async_playwright
OUT=Path('/tmp/nwf-agents/round5/morning-map-phone')
async def main():
 async with async_playwright() as p:
  browser=await p.chromium.launch(headless=True)
  page=await browser.new_page(viewport={'width':1512,'height':982},color_scheme='light')
  await page.add_init_script('''globalThis.__nwfSmooth=true;const names=new WeakMap();const get=WebGL2RenderingContext.prototype.getUniformLocation;WebGL2RenderingContext.prototype.getUniformLocation=function(program,name){const loc=get.call(this,program,name);if(loc)names.set(loc,name);return loc};const set=WebGL2RenderingContext.prototype.uniform1f;WebGL2RenderingContext.prototype.uniform1f=function(loc,value){if(names.get(loc)==='u_overview_smoothing' && !globalThis.__nwfSmooth)value=0;return set.call(this,loc,value)}''')
  async def instrument(route):
   response=await route.fetch();body=await response.text();needle='visibleCities(){return';assert needle in body
   await route.fulfill(response=response,body=body.replace(needle,'visibleCities(){globalThis.__nwfProofMap=this;return'))
  await page.route('**/assets/main-*.js',instrument)
  requests=[];page.on('request',lambda req:requests.append(req.url))
  await page.goto('http://127.0.0.1:5198/year')
  await page.wait_for_function("globalThis.__nwfProofMap && document.querySelector('.headline-sentence .inline-place')",timeout=120000)
  await page.wait_for_timeout(3000)
  firstpaint=list(requests)
  await page.screenshot(path=str(OUT/'year-world-smooth-firstpaint.png'))
  await page.evaluate('globalThis.__nwfSmooth=false;globalThis.__nwfProofMap.map.triggerRepaint()');await page.wait_for_timeout(300)
  await page.screenshot(path=str(OUT/'year-world-previous-interpolation.png'))
  await page.evaluate('globalThis.__nwfSmooth=true;globalThis.__nwfProofMap.map.triggerRepaint()')
  await page.get_by_role('button',name='Your taste',exact=True).first.click();await page.locator('.slider-input').first.fill('24');await page.locator('.slider-input').first.dispatch_event('input')
  await page.wait_for_function("document.querySelector('.headline-sentence .inline-place') && !document.querySelector('.headline-note')?.textContent.startsWith('Loading')",timeout=120000)
  await page.wait_for_timeout(15000)
  await page.keyboard.press('Escape');await page.wait_for_timeout(250)
  await page.screenshot(path=str(OUT/'year-world-smooth-drawn-taste.png'))
  (OUT/'smoothing-requests.json').write_text(json.dumps({'firstpaint':firstpaint,'aftertaste':requests[len(firstpaint):],'timings':await page.evaluate("performance.getEntriesByName('nice-weather-redraw').map(e=>e.duration)")},indent=2))
  await browser.close()
asyncio.run(main())
