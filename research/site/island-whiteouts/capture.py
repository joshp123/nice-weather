# Written by AI on 2026.10.26 by model GPT-6.
"""Capture the local island-colour regression and the actual anchor place cards."""
import argparse, json, pathlib
from playwright.sync_api import sync_playwright

ROOT=pathlib.Path(__file__).resolve().parent
PLACES=[('malaga','spain/malaga'),('chania','greece/chania'),('rotterdam','the-netherlands/rotterdam'),('singapore','singapore/singapore'),('cape-town','south-africa/cape-town'),('san-diego','united-states/san-diego'),('san-francisco','united-states/san-francisco')]
REGIONS=[('aegean',38.1,26.0),('lesbos',39.22,26.24),('samothrace',40.45,25.6),('dodecanese',36.45,27.35),('kastellorizo',36.15,29.5),('akamas',35.02,32.3),('canaries-west',28.2,-17.0),('canaries-east',28.7,-14.4)]

def settle(page,card=False):
    page.wait_for_function('window.niceWeatherMap && window.niceWeatherMap.loaded()',timeout=120000)
    if card:page.wait_for_selector('.card .score-badge',timeout=120000)
    page.wait_for_timeout(3500)

def main():
    parser=argparse.ArgumentParser();parser.add_argument('stage');parser.add_argument('--islands',action='store_true');parser.add_argument('--custom',action='store_true');parser.add_argument('--extra',action='store_true');parser.add_argument('--zooms',type=int,nargs='+',default=[4,6,8]);parser.add_argument('--regions',nargs='+');args=parser.parse_args()
    out=ROOT/args.stage;out.mkdir(parents=True,exist_ok=True)
    result={'written_by':'AI GPT-6, 2026-10-26','base':'http://127.0.0.1:5199','anchors':[],'islands':[]}
    if (out/'results.json').exists():result=json.loads((out/'results.json').read_text())
    result['written_by']='AI GPT-6, 2026-10-26'
    with sync_playwright() as p:
        browser=p.chromium.launch(args=['--enable-webgl','--use-angle=metal','--enable-unsafe-swiftshader'])
        context=browser.new_context(viewport={'width':1512,'height':982},color_scheme='light',device_scale_factor=1)
        page=context.new_page();errors=[];requests=[];page.on('pageerror',lambda error:errors.append(str(error)));page.on('request',lambda request:requests.append(request.url))
        for period,suffix in [('year','/year'),('forecast','')]:
            for name,slug in PLACES:
                if any(row['name']==name and row['period']==period and 'score' in row for row in result['anchors']):continue
                url=f'http://127.0.0.1:5199/{slug}{suffix}'
                try:
                    page.goto(url,wait_until='load');settle(page,True)
                    text=page.locator('.card').inner_text()
                    record={'name':name,'period':period,'url':url,'score':page.locator('.score-badge').inner_text(),'days':page.locator('.verdict-days').inner_text(),'card':text}
                    filename=f'{name}-{period}.png';page.screenshot(path=str(out/filename));record['screenshot']=filename
                    result['anchors'].append(record);print(name,period,record['score'],record['days'],flush=True)
                except Exception as error:
                    print('FAILED',url,str(error),flush=True);result['anchors'].append({'name':name,'period':period,'error':str(error)});page.screenshot(path=str(out/f'{name}-{period}-FAILED.png'))
                (out/'results.json').write_text(json.dumps(result,indent=2))
        if args.islands:
            for period,suffix in [('year','/year'),('forecast','')]:
                for name,lat,lon in REGIONS:
                    if args.regions and name not in args.regions:continue
                    for zoom in args.zooms:
                        filename=f'{name}-{period}-z{zoom}.png'
                        if any(row['screenshot']==filename for row in result['islands']):continue
                        first_request=len(requests)
                        url=f'http://127.0.0.1:5199/@{lat},{lon},{zoom}z{suffix}'
                        page.goto(url,wait_until='load');settle(page,True)
                        page.locator('.card button[aria-label="Close"]').click();page.wait_for_timeout(3000)
                        filename=f'{name}-{period}-z{zoom}.png';page.screenshot(path=str(out/filename))
                        result['islands'].append({'requestedAppScripts':[url for url in requests[first_request:] if '/assets/' in url and '.js' in url],'region':name,'period':period,'zoom':zoom,'url':url,'screenshot':filename,'actualZoom':page.evaluate('window.niceWeatherMap.getZoom()')})
                        (out/'results.json').write_text(json.dumps(result,indent=2));print(filename,flush=True)
        if args.custom:
            for period,suffix in [('year','/year'),('forecast','')]:
                for name,lat,lon in REGIONS:
                    if args.regions and name not in args.regions:continue
                    for zoom in args.zooms:
                        filename=f'{name}-{period}-custom-z{zoom}.png'
                        if any(row['screenshot']==filename for row in result['islands']):continue
                        url=f'http://127.0.0.1:5199/@{lat},{lon},{zoom}z{suffix}?perfectFrom=21'
                        first_request=len(requests)
                        page.goto(url,wait_until='load');settle(page,True)
                        page.wait_for_function("!document.querySelector('.card.updating')",timeout=120000)
                        page.wait_for_timeout(2000)
                        page.locator('.card button[aria-label="Close"]').click();page.wait_for_timeout(2000)
                        page.screenshot(path=str(out/filename))
                        page.locator('button[aria-label="Your taste"]').click()
                        slider_value=page.locator('#slider-perfectFrom').input_value()
                        result['islands'].append({'requestedAppScripts':[url for url in requests[first_request:] if '/assets/' in url and '.js' in url],'warmEnoughSlider':slider_value,'requestedWeatherFiles':[url for url in requests[first_request:] if '/data/v6/' in url and '.bin' in url],'region':name,'period':period,'taste':{'perfectFrom':21},'zoom':zoom,'url':url,'screenshot':filename,'actualZoom':page.evaluate('window.niceWeatherMap.getZoom()')})
                        (out/'results.json').write_text(json.dumps(result,indent=2));print(filename,flush=True)
        if args.extra:
            for period,suffix in [('year','/year'),('forecast','')]:
                for name,lat,lon in [('la-palma',28.68,-17.85),('el-hierro',27.74,-18.0),('karpathos-kasos',35.45,27.1),('patmos-leros',37.2,26.7)]:
                    url=f'http://127.0.0.1:5199/@{lat},{lon},8z{suffix}'
                    page.goto(url,wait_until='load');settle(page,True)
                    page.locator('.card button[aria-label="Close"]').click();page.wait_for_timeout(3000)
                    filename=f'{name}-{period}-z8.png';page.screenshot(path=str(out/filename))
                    result['islands'].append({'region':name,'period':period,'zoom':8,'url':url,'screenshot':filename,'actualZoom':page.evaluate('window.niceWeatherMap.getZoom()')})
                    (out/'results.json').write_text(json.dumps(result,indent=2));print(filename,flush=True)
        result['islands']=list({row['screenshot']:row for row in result['islands']}.values())
        result['errors']=errors;(out/'results.json').write_text(json.dumps(result,indent=2));browser.close()

if __name__=='__main__':main()
