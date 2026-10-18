# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import asyncio,json
from pathlib import Path
from playwright.async_api import async_playwright
OUT=Path('/tmp/nwf-agents/round5/morning-final/shots')
BASE='http://127.0.0.1:5198'
async def main():
 report={'views':[],'errors':[]}
 async with async_playwright() as p:
  browser=await p.chromium.launch(headless=True)
  for w,h in [(390,844),(1512,982),(1728,1117)]:
   ctx=await browser.new_context(viewport={'width':w,'height':h},color_scheme='light',timezone_id='Europe/Amsterdam',locale='en-GB')
   page=await ctx.new_page();page.on('pageerror',lambda e:report['errors'].append(str(e)))
   for case,path in [('year','/year'),('novmar','/nov-mar'),('card','/spain/arona/year'),('outlook','/outlook/world')]:
    await page.goto(BASE+path,wait_until='domcontentloaded')
    if case=='outlook':
     await page.locator('.poster').wait_for(timeout=120000)
     await page.locator('.poster').evaluate('(image)=>image.decode()')
     await page.get_by_role('button',name='Share image',exact=True).wait_for(state='visible')
     text=await page.locator('h1').inner_text()
    else:
     await page.wait_for_function("document.querySelector('.headline-sentence')?.textContent.includes('Nicest')",timeout=120000)
     await page.wait_for_timeout(2500)
     if case=='card':
      await page.locator('.physical-why').wait_for(timeout=120000)
      await page.wait_for_timeout(500)
     text=await page.locator('.headline-sentence').inner_text()
     assert 'Best by van' not in await page.locator('body').inner_text()
     if case!='card':
      names=await page.locator('.headline-sentence .inline-place').all_text_contents()
      assert len(names)==3,(case,w,names)
      await page.locator('.list-toggle' if w==390 else '.list-trigger').click();await page.wait_for_timeout(200)
      rows=await page.locator('.rank-name>span').all_text_contents();assert rows[:3]==names,(rows,names)
      details=await page.locator('.rank-name small').all_text_contents()
      totals=[]
      for detail in details:
       import re
       nums=re.findall(r'\d+',detail);assert len(nums)==2,detail;totals.append(sum(map(int,nums)))
      assert totals==sorted(totals,reverse=True),totals
      assert 'Best by van' not in await page.locator('body').inner_text()
      await page.keyboard.press('Escape')
      report['views'].append({'case':case,'viewport':[w,h],'headline':text,'ranking':details})
    await page.screenshot(path=str(OUT/f'{case}-{w}x{h}-light.png'))
    if case in ['card','outlook']:report['views'].append({'case':case,'viewport':[w,h],'text':text})
    print(case,w,text,flush=True)
   await ctx.close()
  await browser.close()
 Path('/tmp/nwf-agents/round5/morning-final/report.json').write_text(json.dumps(report,indent=2))
 assert not report['errors'],report['errors']
asyncio.run(main())
