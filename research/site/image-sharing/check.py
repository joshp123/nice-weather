# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import asyncio, json, os
from pathlib import Path
from playwright.async_api import async_playwright

OUTPUT = Path('/tmp/nwf-agents/round5/image-sharing')
OUTPUT.mkdir(parents=True, exist_ok=True)
INIT = '''
window.__shareProof = [];
Object.defineProperty(navigator, 'canShare', {value: data => data.files?.length === 1, configurable: true});
Object.defineProperty(navigator, 'share', {value: async data => {
  const activated = navigator.userActivation.isActive;
  const file = data.files[0];
  window.__shareProof.push({kind: 'native', active: activated, text: data.text, name: file.name, type: file.type,
    size: file.size, signature: Array.from(new Uint8Array(await file.slice(0, 8).arrayBuffer()))});
}, configurable: true});
'''

async def exercise(page, kind, native):
    if not native:
        await page.evaluate("""() => {
          Object.defineProperty(navigator, 'canShare', {value: () => false, configurable: true});
          const write = navigator.clipboard.write.bind(navigator.clipboard);
          navigator.clipboard.write = async items => {
            const item = items[0];
            window.__shareProof.push({kind: 'clipboard', types: item.types, text: await (await item.getType('text/plain')).text(), size: (await item.getType('image/png')).size});
            return write(items);
          };
        }""")
    await page.get_by_role('button', name='Share image', exact=True).click()
    await page.wait_for_function('window.__shareProof.length > 0')
    result = await page.evaluate('window.__shareProof.at(-1)')
    assert result['size'] > 1000, result
    assert '\nhttp' in result['text'], result
    if native:
        assert result['active'] and result['type'] == 'image/png', result
        assert result['signature'] == [137,80,78,71,13,10,26,10], result
    else:
        await page.wait_for_function("Array.from(document.querySelectorAll('[role=status]')).some(status => status.textContent.includes('copied'))")
        copied = await page.evaluate("""async () => {
          const items = await navigator.clipboard.read();
          return {types: items[0].types, text: await (await items[0].getType('text/plain')).text(), bytes: (await items[0].getType('image/png')).size};
        }""")
        assert copied['text'] == result['text'] and copied['bytes'] > 1000, copied
        result['readBack'] = copied
    async with page.expect_download() as pending:
        await page.get_by_role('link' if kind == 'outlook' else 'button', name='Download image', exact=True).click()
    download = await pending.value
    await download.save_as(str(OUTPUT / f'{kind}-{native}.png'))
    result['download'] = download.suggested_filename
    await page.screenshot(path=str(OUTPUT / f'{kind}-{"native" if native else "clipboard"}-390x844.png'))
    return result

async def main():
    results = []
    async with async_playwright() as p:
        browser = await p.chromium.launch(headless=True)
        for kind in os.environ.get('SHARE_PAGES', 'app,outlook').split(','):
            for native in [True, False]:
                context = await browser.new_context(viewport={'width':390,'height':844}, is_mobile=True, has_touch=True,
                  permissions=['clipboard-read','clipboard-write'], color_scheme='light')
                await context.add_init_script(INIT)
                page = await context.new_page()
                errors = []
                page.on('pageerror', lambda e: errors.append(str(e)))
                if kind == 'app':
                    await page.goto('http://127.0.0.1:5198/year')
                    await page.wait_for_function("document.querySelector('.headline')?.textContent.includes('Nicest')", timeout=180000)
                    await page.get_by_role('button', name='Make a poster', exact=True).click()
                else:
                    await page.goto('http://127.0.0.1:5198/outlook/world' + ('?date=' + os.environ['OUTLOOK_DATE'] if 'OUTLOOK_DATE' in os.environ else ''))
                await page.get_by_role('button', name='Share image', exact=True).wait_for(timeout=180000)
                await page.wait_for_function("Array.from(document.querySelectorAll('button')).some(button => button.textContent === 'Share image' && !button.disabled)", timeout=180000)
                result = await exercise(page, kind, native)
                result.update(page=kind, native=native, errors=errors)
                assert not errors, errors
                results.append(result)
                print(json.dumps(result), flush=True)
                await context.close()
        await browser.close()
    (OUTPUT / 'report.json').write_text(json.dumps(results, indent=2))

asyncio.run(main())
