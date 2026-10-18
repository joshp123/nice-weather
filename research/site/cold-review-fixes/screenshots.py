# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import asyncio, base64, json, time
from pathlib import Path
from playwright.async_api import async_playwright
OUT=Path('/tmp/nwf-agents/round5/cold-fixes'); SHOTS=OUT/'shots'
BASE='http://127.0.0.1:5198'
async def ready(page):
 await page.locator('.wordmark').wait_for(state='visible',timeout=60000)
 await page.wait_for_function("!document.querySelector('.headline-note')?.textContent.startsWith('Loading weather') && document.querySelectorAll('.inline-place').length>0",timeout=120000)
 await page.evaluate('document.fonts.ready')
 await page.wait_for_timeout(1200)
async def main():
 report={'written_by':'AI, GPT-6.1 Sol, 2026.10.18','shots':[],'errors':[],'checks':[]}
 async with async_playwright() as p:
  browser=await p.webkit.launch(headless=True)
  for theme in ['light','dark']:
   for width,height in [(390,844),(1512,982),(3840,1080)]:
    context=await browser.new_context(viewport={'width':width,'height':height},color_scheme=theme,timezone_id='Europe/Amsterdam',locale='en-GB')
    page=await context.new_page();page.on('pageerror',lambda e:report['errors'].append(str(e)))
    async def shot(state):
     path=SHOTS/f'{state}-{width}x{height}-{theme}.png';await page.screenshot(path=str(path))
     bounds=await page.evaluate("""()=>['.slot-headline','.card','.time-picker-panel','.bar','.taste-panel','.poster-artwork'].flatMap(s=>[...document.querySelectorAll(s)].filter(e=>e.getClientRects().length).map(e=>{const r=e.getBoundingClientRect();return{selector:s,x:r.x,y:r.y,width:r.width,height:r.height,right:r.right,bottom:r.bottom}}))""")
     report['shots'].append({'path':str(path),'bounds':bounds});print(str(path),flush=True)
    try:
     await page.goto(BASE+'/');await ready(page);await shot('first')
     await page.goto(BASE+'/nov-mar');await ready(page);await shot('novmar')
     await page.locator('.list-toggle' if width==390 else '.list-trigger').click();await shot('list');await page.keyboard.press('Escape')
     await page.get_by_role('button',name='Choose when',exact=True).click();await shot('dial');await page.get_by_role('button',name='Close time picker',exact=True).click()
     await page.goto(BASE+'/greece/chania');await ready(page)
     await page.locator('.verdict-days').wait_for(state='visible',timeout=120000);await page.wait_for_timeout(1000);await shot('place')
     report['checks'].append({'size':width,'theme':theme,'forecastCard':await page.locator('.card').inner_text(),'phoneDockHidden':await page.locator('.slot-bar').is_hidden() if width==390 else None})
     await page.locator('.card').get_by_role('button', name='Close', exact=True).click()
     await page.get_by_role('button',name='Choose when',exact=True).click();await shot('calendar')
     report['checks'].append({'size':width,'theme':theme,'calendarDays':await page.locator('.forecast-calendar-day').count()})
     await page.get_by_role('button',name='Close time picker',exact=True).click()
     await page.get_by_role('button',name='Your taste',exact=True).click();await shot('taste');await page.keyboard.press('Escape')
     await page.get_by_role('button',name='Make a poster',exact=True).click();await page.locator('.poster-preview').wait_for(state='visible',timeout=120000)
     await page.wait_for_function("document.querySelector('.poster-preview').complete && document.querySelector('.poster-preview').naturalWidth>0",timeout=120000);await shot('poster')
     image=await page.locator('.poster-preview').evaluate("async e=>{const b=await fetch(e.src).then(r=>r.blob());return await new Promise(resolve=>{const r=new FileReader();r.onload=()=>resolve(r.result.split(',')[1]);r.readAsDataURL(b)})}")
     (SHOTS/f'poster-image-{width}x{height}-{theme}.png').write_bytes(base64.b64decode(image))
    except Exception as e:
     report['errors'].append(f'{width} {theme}: {e}');await shot('error');print(str(e),flush=True)
    finally:
     await context.close();(OUT/'screenshots.json').write_text(json.dumps(report,indent=2))
  await browser.close()
 print(json.dumps({'errors':report['errors'],'shots':len(report['shots'])}),flush=True)
asyncio.run(main())
