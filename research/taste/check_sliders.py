# Written by AI on 2026.10.17 by model claude opus 5.5. Checks the page's live recount against Python, in Chromium and WebKit, and times it.
import json
from playwright.sync_api import sync_playwright
checks = json.load(open('/tmp/nwf-agents/round3/opus/slider_checks.json'))
other = dict(low=19, high=32, evening=None, humidity=22, rain=5.0, wind=45)
shot = dict(low=21, high=37, evening=19, humidity=21, rain=None, wind=30)
with sync_playwright() as p:
    for engine in (p.chromium, p.webkit):
        b = engine.launch(); pg = b.new_page(viewport={'width': 900, 'height': 1200})
        errors = []; pg.on('pageerror', lambda e: errors.append(str(e)))
        pg.goto('file:///Users/josh/code/nice-weather-finder/docs/product-direction.html'); pg.wait_for_function('window.lastResults', timeout=20000)
        js_default = pg.evaluate('window.lastResults'); js_other = pg.evaluate(f'window.niceDaysAt({json.dumps(other)})'); js_shot = pg.evaluate(f'window.niceDaysAt({json.dumps(shot)})')
        ms = pg.evaluate("(() => { const t0 = performance.now(); for (let i = 0; i < 10; i++) window.niceDaysAt({low: 21 + i/10, high: 30, evening: 12, humidity: 20, rain: 1, wind: 35}); return (performance.now() - t0) / 10; })()")
        diff = lambda a, b: {k: (a[k], b[k]) for k in b if a.get(k) != b[k]}
        print(engine.name, 'errors', errors, '| default mismatches', diff(js_default, checks['default']), '| other mismatches', diff(js_other, checks['other']), '| screenshot mismatches', diff(js_shot, checks['screenshot']), '| Singapore at screenshot settings', js_shot['Singapore'], f'| recount {ms:.1f} ms')
        # drag the evening slider to "don't mind" through the real input, check San Diego changes
        pg.eval_on_selector('#evening', "e => { e.value = 5; e.dispatchEvent(new Event('input')); }"); pg.wait_for_timeout(100)
        print('  evening off ->', pg.inner_text('#v-evening'), '| San Diego', pg.evaluate('window.lastResults["San Diego"]'), '| line:', pg.inner_text('#settings-line'))
        b.close()
