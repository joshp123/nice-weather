# Written by AI on 2026.10.18 by model claude opus 5.5.
# Every screen and state of Nice Weather at Josh's sizes, light and dark, headless, with the layout audit
# (layout_audit.js): overlapping elements, text off the window, control heights, shared edges and baselines,
# the outer margin, contrast, and map labels that collide or sit under a bar. A good build reports zeros.
#
#   npm run build; python3 research/site/layout-audit/live_data_proxy.py dist &    # the local build on :5198, live data
#   cd /tmp && uv run --quiet --with playwright python -I <repo>/research/site/layout-audit/check_layout.py \
#       http://127.0.0.1:5198 /tmp/layout [webkit|chromium] [phone,mbp14,...] [01-first,...]
import json, os, sys, time
from playwright.sync_api import sync_playwright

BASE = sys.argv[1].rstrip('/')
OUT = sys.argv[2]
ENGINE = sys.argv[3] if len(sys.argv) > 3 else 'webkit'
ALL_SIZES = {
    'phone': (390, 844), 'mbp14': (1512, 982), 'mbp16': (1728, 1117), 'safari': (1950, 1100),
    'ultra-half': (1920, 1080), 'ultra-full': (3840, 1080),
}
SIZES = {name: ALL_SIZES[name] for name in (sys.argv[4].split(',') if len(sys.argv) > 4 and sys.argv[4] else ALL_SIZES)}
ONLY = set(sys.argv[5].split(',')) if len(sys.argv) > 5 and sys.argv[5] else None
INVENTORY_JS = open(os.path.join(os.path.dirname(__file__), 'layout_audit.js')).read()
os.makedirs(OUT, exist_ok=True)


def settle(page, seconds=40):
    deadline = time.time() + seconds
    while time.time() < deadline:
        text = page.evaluate("() => document.querySelector('.answer-places')?.textContent || ''")
        if text and not any(word in text for word in ('Finding', 'Loading', 'Where is it nice')):
            break
        page.wait_for_timeout(400)
    page.wait_for_timeout(1800)


def visit(page, path):
    page.goto(BASE + path, wait_until='load')
    settle(page)


def click(page, selector):
    page.locator(selector).first.click()
    page.wait_for_timeout(1200)


STATES = [
    ('01-first', lambda page: visit(page, '/')),
    ('02-picker-forecast', lambda page: click(page, '.time-button')),
    ('03-one-day', lambda page: (click(page, '.forecast-calendar-day >> nth=2'), page.keyboard.press('Escape'), settle(page, 20))),
    ('04-winter', lambda page: visit(page, '/dec-feb')),
    ('05-picker-year', lambda page: (click(page, '.time-button'), page.wait_for_timeout(1500))),
    ('06-month', lambda page: visit(page, '/november')),
    ('07-stretch', lambda page: visit(page, '/nov-mar')),
    ('08-year', lambda page: visit(page, '/year')),
    ('09-list', lambda page: click(page, '.answer .text-button')),
    ('10-card', lambda page: (visit(page, '/spain/malaga/dec-feb'), page.wait_for_timeout(2500))),
    ('11-taste', lambda page: ((page.keyboard.press('Escape'), page.wait_for_timeout(500)) if page.viewport_size['width'] < 720 else None, click(page, 'button[aria-label="Your taste"]:visible'), page.wait_for_timeout(800))),
    ('12-search', lambda page: (page.keyboard.press('Escape'), page.keyboard.press('Escape'), page.wait_for_timeout(300),
                                click(page, '.search-open-button') if page.locator('.search-open-button').is_visible() else None,
                                page.locator('.search-input').click(), page.keyboard.type('Lisb'), page.wait_for_timeout(2500))),
    ('13-location', lambda page: (page.keyboard.press('Escape'), click(page, 'button[aria-label="My location"]:visible'), page.wait_for_timeout(6000))),
    ('14-poster', lambda page: (click(page, 'button[aria-label="Share"]:visible'), page.wait_for_selector('.poster-preview', timeout=40000), page.wait_for_timeout(1500))),
    ('15-method', lambda page: (page.goto(BASE + '/method'), page.wait_for_timeout(2500))),
    ('16-not-found', lambda page: visit(page, '/atlantis/nowhere')),
]


def main():
    results = {}
    with sync_playwright() as p:
        browser = getattr(p, ENGINE).launch()
        for scheme in ('light', 'dark'):
            for size_name, (width, height) in SIZES.items():
                phone = width < 720
                context = browser.new_context(viewport={'width': width, 'height': height}, color_scheme=scheme,
                                              device_scale_factor=2 if width <= 2000 else 1, has_touch=phone,
                                              geolocation={'latitude': 38.72, 'longitude': -9.14}, permissions=['geolocation'])
                page = context.new_page()
                errors = []
                page.on('pageerror', lambda error: errors.append(str(error)[:200]))
                for name, action in STATES:
                    if ONLY and name not in ONLY and name.split('-', 1)[1] not in ONLY:
                        continue
                    key = f'{size_name}-{scheme}-{name}'
                    try:
                        action(page)
                    except Exception as error:
                        print(key, 'ACTION FAILED', str(error)[:160], flush=True)
                    page.screenshot(path=f'{OUT}/{key}.png')
                    inventory = page.evaluate(INVENTORY_JS)
                    inventory['errors'] = list(errors)
                    results[key] = inventory
                    print(key, 'overlaps', len(inventory['overlaps']), 'alignment', len(inventory['alignment']), 'lowContrast', len(inventory['lowContrast']), 'labels', inventory['mapLabels'], 'errors', errors[:1], flush=True)
                context.close()
        browser.close()
    with open(f'{OUT}/audit-{ENGINE}.json', 'w') as handle:
        json.dump(results, handle, indent=1)


main()
