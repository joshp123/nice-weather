# Written by AI on 2026.10.17 by model claude opus 5.5. Screenshots: MacBook viewport (light, dark), scrolled, and phone.
from playwright.sync_api import sync_playwright
U = 'file:///Users/josh/code/nice-weather-finder/docs/product-direction.html'
with sync_playwright() as p:
    b = p.webkit.launch()
    for scheme in ('light', 'dark'):
        pg = b.new_page(viewport={'width': 1440, 'height': 900}, color_scheme=scheme); pg.goto(U); pg.wait_for_function('window.lastResults', timeout=20000)
        pg.evaluate("document.querySelector('h2').scrollIntoView()"); pg.wait_for_timeout(100)
        pg.screenshot(path=f'/tmp/nwf-agents/round3/opus/d5_{scheme}_mac_top.png')
        pg.mouse.wheel(0, 700); pg.wait_for_timeout(200); pg.screenshot(path=f'/tmp/nwf-agents/round3/opus/d5_{scheme}_mac_scrolled.png')
        if scheme == 'light': pg.screenshot(path='/tmp/nwf-agents/round3/opus/d5_light_mac_full.png', full_page=True)
    pg = b.new_page(viewport={'width': 390, 'height': 844}, device_scale_factor=2); pg.goto(U); pg.wait_for_function('window.lastResults', timeout=20000)
    pg.screenshot(path='/tmp/nwf-agents/round3/opus/d5_phone_full.png', full_page=True)
    b.close()
