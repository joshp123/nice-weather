# Written by AI on 2026.10.18 by GPT-6.1 Sol.
import asyncio,json,sys,time
from pathlib import Path
from playwright.async_api import async_playwright
async def main():
 async with async_playwright() as p:
  browser=await p.chromium.launch(headless=True,args=['--use-gl=angle','--use-angle=swiftshader','--enable-webgl'])
  context=await browser.new_context(viewport={'width':1728,'height':1117},color_scheme='light',locale='en-GB',timezone_id='Europe/Amsterdam',device_scale_factor=1)
  network=[]
  async def capture(response):
   try:
    body=await response.body(); headers=await response.all_headers(); network.append({'url':response.url,'bytes':int(headers.get('content-length',len(body))),'bodyBytes':len(body),'status':response.status})
   except Exception as e: network.append({'url':response.url,'error':str(e)})
  context.on('response',capture)
  if len(sys.argv)>2 and sys.argv[2]=='old-tiles':
   async def old_tiles(route):
    response=await route.fetch(); code=await response.text(); before='world-map\":{type:\"vector\",url:\"https://tiles.openfreemap.org/planet\",maxzoom:1}'
    assert before in code, 'World source baseline replacement not found'
    await route.fulfill(response=response,body=code.replace(before,before[:-2]+'2}'))
   await context.route('**/assets/main-*.js',old_tiles)
  page=await context.new_page(); client=await context.new_cdp_session(page); await client.send('Network.enable'); await client.send('Network.setCacheDisabled',{'cacheDisabled':True}); rows={}; errs=[]
  client.on('Network.responseReceived',lambda e: rows.update({e['requestId']:{'url':e['response']['url'],'status':e['response']['status'],'type':e['type']}}))
  client.on('Network.loadingFinished',lambda e: rows.setdefault(e['requestId'],{}).update({'bytes':e['encodedDataLength']}))
  page.on('pageerror',lambda e:errs.append(str(e)))
  started=time.monotonic();await page.goto('http://127.0.0.1:5198/',wait_until='domcontentloaded');await page.wait_for_timeout(30000)
  name=sys.argv[1];root=Path('/tmp/nwf-agents/round5/cold-fixes');await page.screenshot(path=str(root/(name+'.png')))
  result={'viewport':[1728,1117],'seconds':time.monotonic()-started,'totalBytes':sum(x.get('bytes',0) for x in network if x['url'].startswith(('http:','https:'))),'requests':network,'cdpRequests':list(rows.values()),'errors':errs};(root/(name+'.json')).write_text(json.dumps(result,indent=2));print(json.dumps(result));await browser.close()
asyncio.run(main())
