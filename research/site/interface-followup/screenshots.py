# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import asyncio, base64, json, time, os
from pathlib import Path
from playwright.async_api import async_playwright

ROOT = Path('/tmp/nwf-agents/round5/build-followup')
SHOTS = ROOT / 'shots'
SHOTS.mkdir(parents=True, exist_ok=True)
SIZES = [(390,844),(1512,982),(1728,1117),(1920,1080),(3840,1080)]
if os.environ.get('NWF_SCREENSHOT_SIZES'):
    SIZES = [tuple(map(int, item.split('x'))) for item in os.environ['NWF_SCREENSHOT_SIZES'].split(',')]
BASE = 'http://127.0.0.1:5198'

async def ready(page):
    await page.locator('.period-trigger').wait_for(state='visible', timeout=60000)
    await page.wait_for_function("""() => !document.querySelector('.headline-note')?.textContent?.startsWith('Loading weather') && document.querySelectorAll('.inline-place').length > 0""", timeout=120000)
    await page.evaluate("document.fonts.ready")
    await page.wait_for_function("""() => document.querySelector('canvas')?.width > 0""", timeout=30000)
    await page.wait_for_timeout(500)

async def shot(page,state,size,theme,report):
    path=SHOTS / f'build-{state}-{size[0]}x{size[1]}-{theme}.png'
    await page.screenshot(path=str(path),full_page=False,animations='disabled')
    clipping=await page.evaluate("""() => {
      const selectors=['.time-picker-panel','.taste-panel','.card','.poster-toolbar','.poster-artwork','.bar','.wordmark','.search-input'];
      return selectors.flatMap(selector => [...document.querySelectorAll(selector)].filter(el=>el.getClientRects().length).map(el=>{
        const r=el.getBoundingClientRect();return {selector,x:r.x,y:r.y,width:r.width,height:r.height,overflow:r.left<-.5||r.top<-.5||r.right>innerWidth+.5||r.bottom>innerHeight+.5};
      }));
    }""")
    report['shots'].append({'state':state,'size':size,'theme':theme,'path':str(path),'bounds':clipping})
    print(json.dumps({'capture':str(path),'overflow':[x for x in clipping if x['overflow']]}),flush=True)

async def main():
    report={'shots':[],'errors':[],'times':[]}
    report_path=ROOT/'screenshots-report.json'
    if os.environ.get('NWF_SCREENSHOT_SIZES') and report_path.exists():
        report=json.loads(report_path.read_text())
        report['shots']=[shot for shot in report['shots'] if tuple(shot['size']) not in SIZES]
        report['times']=[timing for timing in report['times'] if tuple(timing['size']) not in SIZES]
    report['notes']=['Headless WebKit, real built app, default taste, Europe/Amsterdam home.']
    async with async_playwright() as p:
      browser=await p.webkit.launch(headless=True)
      for theme in ['light','dark']:
       for size in SIZES:
        context=await browser.new_context(viewport={'width':size[0],'height':size[1]},color_scheme=theme,locale='en-GB',timezone_id='Europe/Amsterdam',device_scale_factor=1)
        page=await context.new_page()
        page.on('pageerror',lambda error: report['errors'].append(str(error)))
        page.on('response',lambda response: report['errors'].append(f'HTTP {response.status}: {response.url}') if response.status>=400 else None)
        try:
          start=time.monotonic(); await page.goto(BASE+'/',wait_until='domcontentloaded'); await ready(page)
          report['times'].append({'state':'first','size':size,'theme':theme,'seconds':time.monotonic()-start})
          await shot(page,'first',size,theme,report)
          await page.goto(BASE+'/nov-mar',wait_until='domcontentloaded'); await ready(page)
          await shot(page,'novmar',size,theme,report)
          await page.get_by_role('button',name='Most nice days',exact=False).first.click()
          await page.locator('.place-list').wait_for(state='visible')
          await shot(page,'list',size,theme,report)
          await page.keyboard.press('Escape')
          await page.get_by_role('button',name='Choose when',exact=True).click()
          await page.locator('.year-dial-count').wait_for(state='visible',timeout=30000)
          await page.wait_for_timeout(350)
          await shot(page,'yearpicker',size,theme,report)
          await page.get_by_role('button',name='Close time picker',exact=True).click()
          await page.goto(BASE+'/greece/chania/nov-mar',wait_until='domcontentloaded'); await ready(page)
          await page.locator('.slot-card .card-header h2').wait_for(state='visible',timeout=60000)
          await page.wait_for_function("""() => !document.querySelector('.slot-card .card')?.textContent?.includes('Loading weather')""",timeout=60000)
          await shot(page,'place',size,theme,report)
          await page.evaluate('localStorage.clear()')
          await page.goto(BASE+'/next-14-days',wait_until='domcontentloaded'); await ready(page)
          await page.get_by_role('button',name='Choose when',exact=True).click()
          await page.locator('.forecast-calendar-day').first.wait_for(state='visible',timeout=60000)
          await shot(page,'days',size,theme,report)
          await page.get_by_role('button',name='Close time picker',exact=True).click()
          await page.get_by_role('button',name='Your taste',exact=True).click()
          await page.locator('.slider-input').first.wait_for(state='visible',timeout=30000)
          await shot(page,'taste',size,theme,report)
          await page.get_by_role('button',name='Your taste',exact=True).click()
          await page.get_by_role('button',name='Make a poster',exact=True).click()
          await page.locator('.poster-preview').wait_for(state='visible',timeout=60000)
          await page.wait_for_function("""() => document.querySelector('.poster-preview')?.complete && document.querySelector('.poster-preview')?.naturalWidth>0""",timeout=60000)
          await shot(page,'poster',size,theme,report)
          encoded=await page.evaluate("""async () => {
            const blob=await fetch(document.querySelector('.poster-preview').src).then(r=>r.blob());
            return await new Promise(resolve=>{const reader=new FileReader();reader.onload=()=>resolve(reader.result.split(',')[1]);reader.readAsDataURL(blob)});
          }""")
          actual=SHOTS / f'build-poster-image-{size[0]}x{size[1]}-{theme}.png'; actual.write_bytes(base64.b64decode(encoded))
        except Exception as e:
          report['errors'].append(f'{size} {theme}: {e}')
          await page.screenshot(path=str(SHOTS/f'build-error-{size[0]}x{size[1]}-{theme}.png'))
          print(json.dumps({'error':str(e),'size':size,'theme':theme}),flush=True)
        finally:
          await context.close()
          (ROOT/'screenshots-report.json').write_text(json.dumps(report,indent=2))
      await browser.close()
    print(json.dumps({'complete':len(report['shots']),'errors':report['errors'],'times':report['times']}),flush=True)

asyncio.run(main())
