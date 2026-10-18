# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
# Headless check of the text API against the actual rendered default-taste card.
import asyncio, json
from pathlib import Path
from playwright.async_api import async_playwright

ORIGIN = 'http://127.0.0.1:5198'
OUTPUT = Path('/tmp/nwf-agents/round5/text-api')
CASES = [('peru/chiclayo', -6.77, -79.84), ('greece/chania', 35.51124, 24.02921), ('spain/malaga', 36.72016, -4.42034), ('united-kingdom/london', 51.50853, -0.12574)]
PERIODS = ['year', 'nov-mar', 'next-14-days', '2026-10-19']

async def main():
    OUTPUT.mkdir(parents=True, exist_ok=True)
    results = []
    async with async_playwright() as p:
        browser = await p.chromium.launch(headless=True)
        context = await browser.new_context(viewport={'width':1512,'height':982}, color_scheme='light', timezone_id='Europe/Amsterdam', locale='en-GB')
        page = await context.new_page()
        errors=[]
        page.on('pageerror', lambda error: errors.append(str(error)))
        for invalid in ['', '?lat=-6.77', '?lon=-79.84', '?lat=&lon=', '?lat=hello&lon=1', '?lat=1&lon=181', '?lat=1&lon=2&period=made-up']:
            response = await context.request.get(f'{ORIGIN}/api/place{invalid}')
            assert response.status == 400, (invalid,response.status)
        response=await context.request.get(f'{ORIGIN}/api/place?lat=-6.77&lon=-79.84')
        default=await response.json()
        assert default['name']=='Chiclayo' and default['region']=='Lambayeque, Peru'
        assert default['period']['word']=='year' and default['card']['days']['total']==365
        for slug, latitude, longitude in CASES:
            for period in PERIODS:
                response=await context.request.get(f'{ORIGIN}/api/place?lat={latitude}&lon={longitude}&period={period}')
                assert response.status==200
                api=await response.json()
                card=api['card']; verdict=api['judgement']
                assert api['name'] and api['region'] and card
                assert api['requestedPoint']=={'latitude':latitude,'longitude':longitude}
                assert card['days']['nice']==verdict['niceDayCount']
                assert card['days']['perfect']==verdict['perfectDayCount']
                assert card['days']['awful']==verdict['awfulDayCount']
                assert card['days']['total']==verdict.get('countedDays',verdict['totalDays'])
                await page.goto(f'{ORIGIN}/{slug}/{period}', wait_until='domcontentloaded')
                await page.locator('.physical-card .why-row').first.wait_for(timeout=60000)
                await page.wait_for_timeout(300)
                rendered=await page.locator('.physical-card').evaluate('''card=>({name:card.querySelector('h2').textContent, region:card.querySelector('.card-header .muted').textContent, word:card.querySelector('.band-word').textContent,score:Number(card.querySelector('.score-badge').textContent),days:card.querySelector('.verdict-days')?.textContent,reasons:[...card.querySelectorAll('.why-row')].map(row=>({label:row.querySelector('dt').textContent,value:row.querySelector('.why-value').textContent,detail:row.querySelector('.why-detail').textContent}))})''')
                assert card['name'] in rendered['name'], (slug,api['name'],rendered['name'])
                assert card['region']==rendered['region']
                assert card['word']==rendered['word'], (slug,period,card,rendered)
                assert card['score']==rendered['score'], (slug,period,card,rendered)
                expected=[{k:reason[k] for k in ['label','value','detail']} for reason in card['reasons']]
                assert expected==rendered['reasons'], (slug,period,expected,rendered['reasons'])
                if card['days']['total']>1:
                    for expected_text in [f"Nice on {card['days']['nice']} of {card['days']['total']} days",f"Perfect on {card['days']['perfect']}; awful on {card['days']['awful']}."]:
                        assert expected_text in rendered['days'], (slug,period,rendered['days'])
                result={'slug':slug,'period':period,'point':[api['latitude'],api['longitude']],'word':card['word'],'score':card['score'],'days':card['days'],'reasonsMatched':len(expected)}
                results.append(result)
                print(json.dumps(result),flush=True)
                if slug=='peru/chiclayo' and period in ['year','nov-mar']:
                    await page.screenshot(path=str(OUTPUT/f'chiclayo-{period}-1512x982-light.png'))
        await browser.close()
    report={'written_by':'AI, GPT-6.1 Sol, 2026-10-18','defaultRequest':{key:default[key] for key in ['name','region','latitude','longitude','period','card']},'cases':results,'pageErrors':errors,'invalidRequestsRejected':7}
    assert not errors,errors
    (OUTPUT/'report.json').write_text(json.dumps(report,indent=2))

asyncio.run(main())
