# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import asyncio,json
from pathlib import Path
from playwright.async_api import async_playwright
OUT=Path('/tmp/nwf-agents/round5/view-spacing');OUT.mkdir(exist_ok=True)
BASE='http://127.0.0.1:5198'
async def main():
 report={'cases':[],'errors':[]}
 async with async_playwright() as p:
  browser=await p.chromium.launch(headless=True)
  for w,h in [(390,844),(1512,982)]:
   ctx=await browser.new_context(viewport={'width':w,'height':h},color_scheme='light',timezone_id='Europe/Amsterdam',locale='en-GB')
   async def instrument(route):
    response=await route.fetch();body=await response.text();needle='visibleCities(){return';assert needle in body
    await route.fulfill(response=response,body=body.replace(needle,'visibleCities(){globalThis.__nwfProofMap=this;return'))
   await ctx.route('**/assets/main-*.js',instrument)
   page=await ctx.new_page();page.on('pageerror',lambda e:report['errors'].append(str(e)))
   for case,path,camera in [('novmar','/nov-mar',None),('spain','/nov-mar',{'center':[-4,40],'zoom':6.2})]:
    await page.goto(BASE+path)
    await page.wait_for_function("globalThis.__nwfProofMap && (document.querySelector('.headline-sentence')?.textContent.includes('Nicest') || document.querySelector('.headline-sentence')?.textContent.startsWith('No nice'))",timeout=120000)
    if camera: await page.evaluate('(camera)=>globalThis.__nwfProofMap.map.jumpTo(camera)',camera)
    await page.wait_for_timeout(2500)
    await page.wait_for_function("!document.querySelector('.headline-note')?.textContent.startsWith('Loading') && !document.querySelector('.headline-sentence')?.textContent.startsWith('Finding')",timeout=120000)
    await page.wait_for_timeout(1000)
    spacing=await page.evaluate('''()=>{const b=globalThis.__nwfProofMap.map.getBounds();const width=6371*Math.PI/180*(b.getEast()-b.getWest())*Math.cos((b.getSouth()+b.getNorth())/2*Math.PI/180);return {widthKM:width,separationKM:Math.max(30,Math.min(250,width/8))}}''')
    headline=await page.locator('.headline-sentence').inner_text()
    names=await page.locator('.headline-sentence .inline-place').all_text_contents()
    features=await page.evaluate("globalThis.__nwfProofMap.map.queryRenderedFeatures({layers:['headline-labels']}).map(f=>f.properties)")
    labels={f['name']:f for f in features}
    checks=[]
    for name in names:
     label=next((label for key,label in labels.items() if key in name),None);assert label,(case,w,name,labels)
     assert label['displayedNumber']>0,(case,name,label)
     period='next-14-days' if case=='forecast' else 'nov-mar'
     response=await ctx.request.get(BASE+f"/api/place?lat={label['latitude']}&lon={label['longitude']}&period={period}")
     verdict=(await response.json())['judgement'];assert label['displayedNumber']==verdict['niceDayCount'],(name,label,verdict)
     checks.append({'name':label['name'],'label':label['displayedNumber'],'card':verdict['niceDayCount'],'perfect':verdict['perfectDayCount']})
    if case=='no-nice': assert not names and headline.startswith('No nice days on this map'),headline
    else: assert len(names)>0,(case,w,headline)
    await page.screenshot(path=str(OUT/f'{case}-{w}x{h}-light.png'))
    if case!='no-nice':
     await page.locator('.list-toggle' if w==390 else '.list-trigger').click();await page.wait_for_timeout(250)
     rows=await page.locator('.rank-name>span').all_text_contents();assert rows[:len(names)]==names,(case,rows,names)
     await page.screenshot(path=str(OUT/f'{case}-list-{w}x{h}-light.png'))
     await page.keyboard.press('Escape')
    report['cases'].append({'viewport':[w,h],'case':case,'headline':headline,'spacing':spacing,'checks':checks});print(report['cases'][-1],flush=True)
   await ctx.close()
  await browser.close()
 (OUT/'report.json').write_text(json.dumps(report,indent=2));print(json.dumps(report))
asyncio.run(main())
