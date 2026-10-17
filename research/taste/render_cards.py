# Written by AI on 2026.10.17 by model claude opus 5.5.
# Pre-renders link-preview images (1200 x 630) with the real app in a headless browser, using the same share card
# as the Copy image button (window.niceWeatherShareCard). Writes artifacts/v6/cards/:
#   world.png (the year), world-<period>.png for each month and season, and <slug>.png for the biggest places.
# Needs the local server (npm run serve, http://127.0.0.1:5198) running the built app with artifacts/v6 data.
# Usage: render_cards.py <number of places>
import base64, pathlib, sys, unicodedata, re
from playwright.sync_api import sync_playwright

ROOT = pathlib.Path('/Users/josh/code/nice-weather-finder')
OUT = ROOT / 'artifacts/v6/cards'
SITE = 'http://127.0.0.1:5198'
MONTHS = ['january', 'february', 'march', 'april', 'may', 'june', 'july', 'august', 'september', 'october', 'november', 'december']
PERIODS = MONTHS + ['dec-feb', 'mar-may', 'jun-aug', 'sep-nov']

def kebab(text):
    text = unicodedata.normalize('NFD', text).encode('ascii', 'ignore').decode().lower()
    return re.sub(r'[^a-z0-9]+', '-', text).strip('-')

def biggest_places(count):
    """The biggest places whose slug is theirs (the Worker gives a shared slug to the biggest place)."""
    rows = [line.rstrip('\n').split('\t') for line in open(ROOT / 'artifacts/places.tsv', encoding='utf-8')]
    rows.sort(key=lambda row: -int(row[5]))
    seen, chosen = set(), []
    for name, plain, region, lat, lon, population in rows:
        slug = f"{kebab(plain)}-{kebab(region.split(',')[-1])}".strip('-')
        if slug in seen: continue
        seen.add(slug); chosen.append(slug)
        if len(chosen) == count: break
    return chosen

def render(page, path, out):
    page.goto(f'{SITE}{path}', wait_until='networkidle')
    page.wait_for_function('typeof window.niceWeatherShareCard === "function"', timeout=60000)
    page.wait_for_timeout(1500)  # basemap labels settle
    data = page.evaluate('''async () => {
        const blob = await window.niceWeatherShareCard(1200, 630);
        const bytes = new Uint8Array(await blob.arrayBuffer());
        let text = ''; for (const byte of bytes) text += String.fromCharCode(byte);
        return btoa(text);
    }''')
    out.write_bytes(base64.b64decode(data))
    print(out.name, out.stat().st_size, flush=True)

if __name__ == '__main__':
    OUT.mkdir(parents=True, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch(args=['--use-angle=metal', '--enable-gpu'])
        page = browser.new_page(viewport={'width': 1200, 'height': 630}, device_scale_factor=1)
        render(page, '/year', OUT / 'world.png')
        for period in PERIODS: render(page, f'/{period}', OUT / f'world-{period}.png')
        for slug in biggest_places(int(sys.argv[1])):
            try: render(page, f'/{slug}/year', OUT / f'{slug}.png')
            except Exception as error: print('failed', slug, error, flush=True)
        browser.close()
