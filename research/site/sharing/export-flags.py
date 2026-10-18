# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import asyncio,json,pathlib,gzip,base64,re
from playwright.async_api import async_playwright
root=pathlib.Path('/Users/josh/code/nice-weather-finder')
codes=re.search("const countryCodes = '([^']+)'",(root/'src/ui/countries.ts').read_text())[1].split()
async def main():
 async with async_playwright() as p:
  browser=await p.chromium.launch(headless=True); page=await browser.new_page()
  data=await page.evaluate('''codes=> {const c=document.createElement('canvas');c.width=32;c.height=32*codes.length; const d=c.getContext('2d');d.font='28px Apple Color Emoji, sans-serif';d.textBaseline='middle';codes.forEach((code,i)=>d.fillText([...code].map(letter=>String.fromCodePoint(127397+letter.charCodeAt(0))).join(''),0,i*32+16));return Array.from(d.getImageData(0,0,c.width,c.height).data);}''',codes)
  module=f"export const shareFlags={{size:32,codes:{json.dumps(codes)},rgba:'{base64.b64encode(gzip.compress(bytes(data))).decode()}'}};\n"
  path=root/'hosting/sites/share-assets.mjs';path.write_text(path.read_text()+module)
  await browser.close()
asyncio.run(main())
