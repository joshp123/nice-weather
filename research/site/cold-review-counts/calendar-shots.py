# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import asyncio,json
from pathlib import Path
from playwright.async_api import async_playwright
OUT=Path('/tmp/nwf-agents/round5/calendar-days');OUT.mkdir(exist_ok=True)
async def main():
 report=[]
 async with async_playwright() as p:
  browser=await p.webkit.launch(headless=True)
  for w,h in [(1512,982),(390,844)]:
   ctx=await browser.new_context(viewport={'width':w,'height':h},color_scheme='light',timezone_id='Europe/Amsterdam',locale='en-GB')
   page=await ctx.new_page();errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
   await page.goto('http://127.0.0.1:5198/nov-mar')
   await page.wait_for_function("document.querySelectorAll('.headline-sentence .inline-place').length && !document.querySelector('.headline-note')?.textContent.startsWith('Loading')",timeout=120000)
   await page.wait_for_timeout(1500)
   assert 'sampled' not in await page.locator('#app').inner_text()
   assert '151' in await page.locator('.legend-caption').inner_text()
   await page.screenshot(path=str(OUT/f'novmar-{w}x{h}-light.png'))
   await page.locator('.list-toggle' if w==390 else '.list-trigger').click();await page.wait_for_timeout(300)
   listtext=await page.locator('.place-list').inner_text();assert '151' in listtext and 'sampled' not in listtext
   await page.screenshot(path=str(OUT/f'novmar-list-{w}x{h}-light.png'))
   await page.keyboard.press('Escape')
   await page.goto('http://127.0.0.1:5198/portugal/lisbon/nov-mar')
   await page.locator('.verdict-days').wait_for(timeout=120000);await page.wait_for_timeout(1200)
   text=await page.locator('.card').inner_text();assert '151' in text and 'sampled' not in text
   place=await (await ctx.request.get('http://127.0.0.1:5198/api/slug/portugal/lisbon')).json()
   api=await (await ctx.request.get(f"http://127.0.0.1:5198/api/place?lat={place['latitude']}&lon={place['longitude']}&period=nov-mar")).json()
   assert f"Nice on {api['judgement']['niceDayCount']} of 151" in text
   description=await page.locator('meta[property="og:description"]').get_attribute('content')
   assert f"on {api['judgement']['niceDayCount']} of 151 days" in description
   image=await ctx.request.get('http://127.0.0.1:5198/preview/share.png?path=%2Fportugal%2Flisbon%2Fnov-mar')
   assert image.status==200
   (OUT/'novmar-preview.png').write_bytes(await image.body())
   await page.screenshot(path=str(OUT/f'novmar-card-{w}x{h}-light.png'))
   await page.locator('.card').get_by_role('button',name='Make a poster',exact=True).click();await page.locator('.poster-preview').wait_for(timeout=120000)
   await page.wait_for_function("document.querySelector('.poster-preview').complete && document.querySelector('.poster-preview').naturalWidth>0")
   await page.screenshot(path=str(OUT/f'novmar-poster-{w}x{h}-light.png'))
   report.append({'viewport':[w,h],'card':text,'list':listtext,'apiCounts':{k:api.get('judgement',{}).get(k) for k in ['niceDayCount','perfectDayCount','awfulDayCount','countedDays']},'errors':errors})
   await ctx.close()
  await browser.close()
 (OUT/'report.json').write_text(json.dumps(report,indent=2));print(json.dumps(report))
asyncio.run(main())
