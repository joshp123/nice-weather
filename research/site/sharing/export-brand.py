# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import asyncio, json, gzip, base64, pathlib
from playwright.async_api import async_playwright
async def main():
 async with async_playwright() as p:
  browser=await p.chromium.launch(headless=True)
  page=await browser.new_page()
  await page.goto('http://127.0.0.1:5198/')
  brand=await page.evaluate('''() => { const canvas=document.createElement('canvas'); canvas.width=620; canvas.height=200; const draw=canvas.getContext('2d'); draw.font='900 160px Superclarendon, Rockwell, Georgia, serif'; draw.fillStyle='#fff'; let x=8; for(const character of 'Nice!'){draw.fillText(character,x,158);x+=draw.measureText(character).width-10.4;} return {width:620,height:200,alpha:Array.from(draw.getImageData(0,0,620,200).data).filter((_,index)=>index%4===3)}; }''')
  pathlib.Path('/tmp/nwf-agents/round5/sharing/brand.json').write_text(json.dumps(brand))
  await browser.close()
asyncio.run(main())
