# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import asyncio,json
from pathlib import Path
from datetime import datetime,timezone
from playwright.async_api import async_playwright
ROOT=Path('/tmp/nwf-agents/round5/ranking')
BASE='http://127.0.0.1:5198'
async def main():
 d=json.loads((ROOT/'outlook-candidates.json').read_text()); report={'date':d['date'],'parity':{},'headlines':{},'errors':[]}
 async with async_playwright() as p:
  browser=await p.webkit.launch(headless=True)
  context=await browser.new_context(viewport={'width':1512,'height':982},color_scheme='light',timezone_id='Europe/Amsterdam',locale='en-GB')
  page=await context.new_page();page.on('pageerror',lambda e:report['errors'].append(str(e)))
  await page.clock.set_fixed_time(datetime(2026,10,18,12,tzinfo=timezone.utc))
  async def instrument(route):
   response=await route.fetch();source=await response.text();source=source.replace('this.layer={id:"weather-v6"','window.__weatherEngine=this,this.layer={id:"weather-v6"');await route.fulfill(response=response,body=source)
  await page.route('**/assets/main-*.js',instrument)
  await page.route('**/ranking-proof.mjs',lambda route:route.fulfill(path=str(ROOT/'ranking.mjs'),content_type='text/javascript'))
  await page.goto(BASE+'/day-2');await page.wait_for_function('window.__weatherEngine?.data && !window.__weatherEngine.loading');await page.wait_for_timeout(1200)
  result=await page.evaluate('''async (d)=>{
   const {rankPlaces,inOutlookRegion}=await import('/ranking-proof.mjs');const e=window.__weatherEngine;
   const scores=e.scorePoints(d.candidates),counts=e.countPoints(d.candidates);
   const numbered=d.candidates.map((p,i)=>({...p,score:scores[i],niceDayCount:counts[i*3],perfectDayCount:counts[i*3+1]}));
   const result={};for(const[id,label,pop]of[['world','the world',300000],['europe','Europe',50000],['usa','the USA',50000]]){
    const go=d.ranked[id].map(p=>p.name);
    const app=rankPlaces(numbered.filter(p=>inOutlookRegion(p,label)),pop,8).slice(0,3).map(p=>p.name);
    const rule=rankPlaces(d.candidates.filter(p=>inOutlookRegion(p,label)).map(p=>({...p,score:p.floatScore})),pop,8).slice(0,3).map(p=>p.name);
    result[id]={app,go,rule,pass:JSON.stringify(app)===JSON.stringify(go)&&JSON.stringify(rule)===JSON.stringify(go)};
   }return result;
  }''',d);report['parity']=result
  for id,center,zoom in [('world',[10,25],1.6),('europe',[10,50],3.3),('usa',[-98,39],3.3)]:
   await page.evaluate('v=>window.__weatherEngine.map.jumpTo({center:v.center,zoom:v.zoom})',{'center':center,'zoom':zoom});await page.wait_for_timeout(800)
   report['parity'][id]['visibleHeadline']=await page.locator('.headline-sentence').text_content()
   assert all(name in report['parity'][id]['visibleHeadline'] for name in report['parity'][id]['go'])
  for key,path in [('winter','/nov-mar'),('fortnight','/next-14-days')]:
   await page.evaluate('localStorage.clear()');await page.goto(BASE+path);await page.wait_for_function('window.__weatherEngine?.data && !window.__weatherEngine.loading');await page.wait_for_timeout(1200)
   report['headlines'][key]=await page.locator('.headline-sentence').text_content()
  report['headlines']['europeTomorrow']=report['parity']['europe']['visibleHeadline']
  for size in [(1512,982),(390,844)]:
   await context.close()
   context=await browser.new_context(viewport={'width':size[0],'height':size[1]},color_scheme='light',timezone_id='Europe/Amsterdam',locale='en-GB');page=await context.new_page();await page.clock.set_fixed_time(datetime(2026,10,18,12,tzinfo=timezone.utc))
   await page.goto(BASE+'/');await page.wait_for_function("document.querySelectorAll('.inline-place').length>3 && !document.querySelector('.headline-note')?.textContent.includes('Loading')");await page.wait_for_timeout(1500)
   await page.screenshot(path=str(ROOT/f'first-{size[0]}x{size[1]}-light.png'))
  await context.close();await browser.close()
 (ROOT/'results.json').write_text(json.dumps(report,indent=2));print(json.dumps(report,indent=2));assert all(r['pass'] for r in report['parity'].values()) and not report['errors']
asyncio.run(main())
