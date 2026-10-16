# Written by AI on 2026.10.17 by model claude opus 5.5. Drags each thumb with the mouse, as a person would.
from playwright.sync_api import sync_playwright
with sync_playwright() as p:
    for engine in (p.chromium, p.webkit):
        b = engine.launch(); pg = b.new_page(viewport={'width': 900, 'height': 1200})
        pg.goto('file:///Users/josh/code/nice-weather-finder/docs/product-direction.html'); pg.wait_for_function('window.lastResults', timeout=20000)
        box = pg.locator('.range2').bounding_box(); x0, w, y = box['x'], box['width'], box['y'] + 20
        at = lambda c: x0 + 10 + (c - 12) / 26 * (w - 20)
        for name, start, end in (('low', 21, 17), ('high', 30, 35), ('low', 17, 34)):
            pg.mouse.move(at(start), y); pg.mouse.down(); pg.mouse.move(at(end), y, steps=8); pg.mouse.up(); pg.wait_for_timeout(80)
            print(engine.name, f'drag {name} {start}->{end}:', pg.inner_text('#v-range'), '| San Diego', pg.evaluate('window.lastResults["San Diego"]'))
        pg.mouse.move(at(25), pg.locator('#evening').bounding_box()['y'] + 14); pg.mouse.down(); pg.mouse.up(); pg.wait_for_timeout(80)
        pg.click('#reset'); pg.wait_for_timeout(80); print('  after reset:', pg.inner_text('#v-range'), '|', pg.inner_text('#settings-line'))
        b.close()
