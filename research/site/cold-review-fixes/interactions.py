# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import asyncio,json,re
from pathlib import Path
from playwright.async_api import async_playwright
OUT=Path('/tmp/nwf-agents/round5/cold-fixes');SHOTS=OUT/'shots';BASE='http://127.0.0.1:5198'
async def main():
 report={'written_by':'AI, GPT-6.1 Sol, 2026.10.18','checks':[],'errors':[]}
 async with async_playwright() as p:
  browser=await p.chromium.launch(headless=True)
  for width,height in [(1512,982),(390,844),(3840,1080)]:
   ctx=await browser.new_context(viewport={'width':width,'height':height},color_scheme='light',timezone_id='Europe/Amsterdam',locale='en-GB',permissions=['geolocation','clipboard-read','clipboard-write'],geolocation={'latitude':38.70123,'longitude':-9.40567})
   # Expose the real map for read-only assertions. This response-only hook does not alter a repository file.
   async def instrument(route):
    response=await route.fetch();body=await response.text();body=body.replace('labelledPlaces(){','labelledPlaces(){globalThis.__nwfProofMap=this;')
    await route.fulfill(response=response,body=body)
   await ctx.route('**/assets/main-*.js',instrument)
   page=await ctx.new_page();page.on('pageerror',lambda e:report['errors'].append(str(e)));requests=[]
   page.on('request',lambda r:requests.append({'url':r.url,'body':r.post_data}))
   async def ready():
    await page.wait_for_function("globalThis.__nwfProofMap && !document.querySelector('.headline-note')?.textContent.startsWith('Loading weather') && document.querySelectorAll('.headline-sentence .inline-place').length",timeout=120000)
    await page.wait_for_timeout(1200)
   async def ranking(case):
    labels=await page.evaluate('globalThis.__nwfProofMap.labelledPlaces()')
    labels=[l for l in labels if l.get('displayedNumber') is not None]
    names=await page.locator('.headline-sentence .inline-place').all_text_contents()
    picks=[next(l for l in labels if l['name'] in name) for name in names]
    assert picks[0]['displayedNumber']==max(l['displayedNumber'] for l in labels), (case,names,labels)
    await page.locator('.list-toggle' if width==390 else '.list-trigger').click()
    await page.wait_for_timeout(300)
    text=await page.locator('.place-list').inner_text();assert 'nice' in text and 'perfect' in text
    await page.screenshot(path=str(SHOTS/f'checked-{case}-{width}x{height}-light.png'))
    await page.keyboard.press('Escape')
    report['checks'].append({'width':width,'case':case,'headline':await page.locator('.headline-sentence').inner_text(),'visibleLabels':labels,'list':text})
   try:
    await page.goto(BASE+'/nov-mar');await ready();await ranking('ranking-winter')
    await page.goto(BASE+'/egypt/cairo/2026-10-20');await ready()
    assert '/2026-10-20' in page.url
    assert 'Tuesday 20 October' in await page.locator('.card-period').first.inner_text()
    await page.locator('.card').get_by_role('button', name='Close', exact=True).click();await page.wait_for_timeout(600);await ranking('ranking-day')
    await page.get_by_role('searchbox',name='Search a place').focus() if width!=390 else await page.get_by_role('button',name='Open search',exact=True).click()
    await page.screenshot(path=str(SHOTS/f'checked-search-focus-{width}x{height}-light.png'))
    await page.get_by_role('searchbox',name='Search a place').fill('Jerusalem');await page.locator('.search-results button').first.wait_for(state='visible',timeout=30000)
    assert 'Al-Quds' in await page.locator('.search-results').inner_text()
    await page.keyboard.press('Escape')
    requests.clear();await page.get_by_role('button',name='My location',exact=True).click();await page.wait_for_timeout(5000)
    location_requests=[r for r in requests if '/api/' in r['url']]
    assert any('lat=38.7&lon=-9.41' in r['url'] for r in location_requests),location_requests
    assert all('38.70123' not in r['url']+(r['body'] or '') and '-9.40567' not in r['url']+(r['body'] or '') for r in requests)
    await page.screenshot(path=str(SHOTS/f'checked-location-{width}x{height}-light.png'))
    report['checks'].append({'width':width,'case':'rounded-location','apiRequests':location_requests})
    await page.goto(BASE+'/method');await page.evaluate('document.fonts.ready');await page.wait_for_timeout(300)
    text=await page.locator('main').inner_text()
    for phrase in ['score regression','temperature pixel','25 km model','More years are being added','rounded to whole days'] : assert phrase not in text,phrase
    assert await page.locator('body').evaluate("e=>getComputedStyle(e).fontFamily")== 'Barlow, sans-serif'
    await page.screenshot(path=str(SHOTS/f'checked-method-{width}x{height}-light.png'))
    response=await page.goto(BASE+'/spain/tenerife');assert response.status==404
    assert 'Place not found' in await page.locator('body').inner_text()
    await page.screenshot(path=str(SHOTS/f'checked-not-found-{width}x{height}-light.png'))
    report['checks'].append({'width':width,'case':'method-and-not-found','passed':True})
   except Exception as e:
    report['errors'].append(f'{width}: {e}');print(str(e),flush=True)
    await page.screenshot(path=str(SHOTS/f'interactions-error-{width}.png'))
   await ctx.close();(OUT/'interactions.json').write_text(json.dumps(report,indent=2))
  await browser.close()
 print(json.dumps({'checks':len(report['checks']),'errors':report['errors']}),flush=True)
asyncio.run(main())
