# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import asyncio,base64,json,re,pathlib,struct
from playwright.async_api import async_playwright
ROOT=pathlib.Path('/tmp/nwf-agents/round5/sharing');ROOT.mkdir(exist_ok=True)
async def main():
 report={'previews':[],'exports':[],'errors':[]}
 async with async_playwright() as p:
  browser=await p.chromium.launch(headless=True)
  context=await browser.new_context(viewport={'width':1512,'height':982},color_scheme='light',timezone_id='Europe/Amsterdam',permissions=['clipboard-read','clipboard-write'])
  for path in ['/','/greece/chania','/egypt/cairo','/portugal/lisbon','/spain/seville','/@38.7,-9.1','/year','/portugal/lisbon/nov-mar','/egypt/cairo/2026-10-20']:
   response=await context.request.get('http://127.0.0.1:5198'+path);html=await response.text()
   match=re.search(r'<meta property="og:image" content="([^"]+)"',html);assert match,path
   image=await context.request.get(match[1].replace('&amp;','&'));data=await image.body()
   assert image.status==200,(path,image.status,data[:100]);assert data.startswith(b'\x89PNG'),path
   name='preview-'+(path.strip('/').replace('/','-') or 'home')+'.png';(ROOT/name).write_bytes(data)
   offset=8;metadata=None
   while offset<len(data):
    length=struct.unpack_from('>I',data,offset)[0];kind=data[offset+4:offset+8];payload=data[offset+8:offset+8+length];offset+=length+12
    if kind==b'tEXt' and payload.startswith(b'NiceWeather\0'):metadata=json.loads(payload.split(b'\0',1)[1])
   assert metadata and metadata['template']=='nice-share-v1'
   if metadata.get('numbers'):
    assert all(isinstance(metadata['numbers'][key],int) for key in ['niceDayCount','perfectDayCount'])
   report['previews'].append({'path':path,'status':image.status,'bytes':len(data),'file':name,'metadata':metadata})
  for width,height in [(390,844),(1512,982),(3840,1080)]:
   await context.close();context=await browser.new_context(viewport={'width':width,'height':height},color_scheme='light',timezone_id='Europe/Amsterdam',permissions=['clipboard-read','clipboard-write'])
   page=await context.new_page();page.on('pageerror',lambda error:report['errors'].append(str(error)))
   await page.goto('http://127.0.0.1:5198/greece/chania')
   await page.wait_for_function("document.querySelector('.slot-card')?.textContent.includes('Perfect')||document.querySelector('.slot-card')?.textContent.includes('Nice on')",timeout=120000)
   await page.wait_for_timeout(1000)
   await page.locator('.slot-card').get_by_role('button',name='Make a poster',exact=True).click()
   await page.locator('.poster-preview').wait_for(state='visible',timeout=120000)
   url=await page.locator('.poster-preview').get_attribute('src')
   encoded=await page.evaluate("async url=>{const blob=await fetch(url).then(r=>r.blob());return await new Promise(resolve=>{const reader=new FileReader();reader.onload=()=>resolve(reader.result.split(',')[1]);reader.readAsDataURL(blob);});}",url)
   data=base64.b64decode(encoded);name=f'poster-{width}x{height}-light.png';(ROOT/name).write_bytes(data)
   await page.screenshot(path=str(ROOT/f'poster-open-{width}x{height}-light.png'))
   await page.get_by_role('button',name='Copy image',exact=True).click()
   await page.wait_for_function("document.querySelector('.share-status')?.textContent.includes('Image copied')",timeout=120000)
   copied=await page.evaluate("async()=>{const items=await navigator.clipboard.read();const blob=await items[0].getType('image/png');return Array.from(new Uint8Array(await blob.arrayBuffer()));}")
   same=await page.evaluate("""async ({original,copied})=>{const digest=async data=>{const bitmap=await createImageBitmap(await fetch(data).then(r=>r.blob()));const canvas=new OffscreenCanvas(bitmap.width,bitmap.height),draw=canvas.getContext('2d');draw.drawImage(bitmap,0,0);return [...new Uint8Array(await crypto.subtle.digest('SHA-256',draw.getImageData(0,0,bitmap.width,bitmap.height).data))].join(',');};return await digest(original)===await digest(copied);}""",{'original':url,'copied':'data:image/png;base64,'+base64.b64encode(bytes(copied)).decode()})
   assert same,'Poster and Copy image differ'
   report['exports'].append({'size':[width,height],'posterBytes':len(data),'clipboardMatches':True,'file':name})
  await browser.close()
 (ROOT/'report.json').write_text(json.dumps(report,indent=2));print(json.dumps(report,indent=2))
asyncio.run(main())
