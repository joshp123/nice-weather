# Written by AI on 2026.10.19 by model GPT-6.1 Sol.
import asyncio,json
from pathlib import Path
from playwright.async_api import async_playwright
OUT=Path('/tmp/nwf-agents/round5/morning-map-phone');OUT.mkdir(exist_ok=True)
async def main():
 report=[]
 async with async_playwright() as p:
  browser=await p.chromium.launch(headless=True)
  for w,h in [(390,844),(1512,982),(1728,1117)]:
   context=await browser.new_context(viewport={'width':w,'height':h},color_scheme='light',timezone_id='Europe/Amsterdam')
   async def instrument(route):
    response=await route.fetch();body=await response.text();needle='visibleCities(){return';assert needle in body
    await route.fulfill(response=response,body=body.replace(needle,'visibleCities(){globalThis.__nwfProofMap=this;return'))
   await context.route('**/assets/main-*.js',instrument)
   page=await context.new_page()
   for period in ['spain/arona/year']:
    await page.goto('http://127.0.0.1:5198/'+period)
    await page.wait_for_function("globalThis.__nwfProofMap && document.querySelector('.headline-sentence .inline-place')",timeout=120000)
    await page.wait_for_timeout(3500)
    data=await page.evaluate('''async()=>{
     const map=globalThis.__nwfProofMap.map, style=getComputedStyle(document.documentElement), n=name=>parseFloat(style.getPropertyValue('--map-'+name));
     const fs=(n('label-small')+(n('label-large')-n('label-small'))*Math.max(0,Math.min(1,(map.getZoom()-1)/8)))*n('label-scale');
     const labels=map.queryRenderedFeatures({layers:['headline-labels']}).map(f=>{let p=f.properties;let pt=map.project([p.longitude,p.latitude]);let offset=JSON.parse(p.headlineOffset);let textWidth=p.name.length*fs*.65;return{name:p.name,count:p.displayedNumber,x:pt.x+offset[0]*fs,y:pt.y+offset[1]*fs,width:Math.min(textWidth,fs*n('label-width'))+n('label-padding')*2,height:fs*n('label-line-height')*Math.ceil(textWidth/(fs*n('label-width')))+n('count-height')*n('label-scale')+n('label-padding')*2}});
     const lines=(await map.getSource('headline-pointers').getData()).features.map(f=>f.geometry.coordinates.map(c=>map.project(c)));
     const chosenFeatures=(await map.getSource('chosen-place').getData()).features;const selected=chosenFeatures[0]?.properties;let chosenBox=null;if(selected && map.getLayoutProperty('chosen-place','text-field')!==''){const pt=map.project([selected.longitude,selected.latitude]),size=n('chosen-size')*n('label-scale'),textWidth=selected.name.length*size*.65,width=Math.min(textWidth,size*n('label-width'));chosenBox={name:selected.name,x:pt.x+size*n('chosen-offset')+width/2,y:pt.y,width:width+n('label-padding')*2,height:size*n('label-line-height')*Math.ceil(textWidth/(size*n('label-width')))+n('label-padding')*2}}
     const buttons=[...document.querySelectorAll('.dock-answer .inline-place')].map(b=>({text:b.textContent,x:b.getBoundingClientRect().x,y:b.getBoundingClientRect().y,width:b.getBoundingClientRect().width,height:b.getBoundingClientRect().height,rects:[...b.getClientRects()].map(r=>({x:r.x,y:r.y,width:r.width,height:r.height}))}));
     return{zoom:map.getZoom(),labels,lines,buttons,chosenBox,headline:document.querySelector('.headline-sentence').textContent,dock:document.querySelector('.dock-answer').textContent};
    }''')
    if data['chosenBox']:
     for label in data['labels']:
      chosen=data['chosenBox'];assert abs(label['x']-chosen['x'])>=(label['width']+chosen['width'])/2 or abs(label['y']-chosen['y'])>=(label['height']+chosen['height'])/2,('chosen overlaps',label,chosen)
    for i,a in enumerate(data['labels']):
     for b in data['labels'][i+1:]: assert abs(a['x']-b['x'])>=(a['width']+b['width'])/2 or abs(a['y']-b['y'])>=(a['height']+b['height'])/2,(period,w,'overlap',a,b)
    def cross(a,b,c):return (b['x']-a['x'])*(c['y']-a['y'])-(b['y']-a['y'])*(c['x']-a['x'])
    for i,(a,b) in enumerate(data['lines']):
     for c,d in data['lines'][i+1:]:assert not(cross(a,b,c)*cross(a,b,d)<0 and cross(c,d,a)*cross(c,d,b)<0),(period,w,'cross')
    assert len(data['labels'])==3,(period,w,data)
    await page.screenshot(path=str(OUT/f'card-{w}x{h}-light.png'))
    report.append({'viewport':[w,h],'period':period,**data});print(json.dumps(report[-1]),flush=True)
   await context.close()
  await browser.close()
 (OUT/'card-report.json').write_text(json.dumps(report,indent=2))
asyncio.run(main())
