# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
# Pre-renders link previews with the same share card as the app's Copy image button.
# Writes world.png, world-<period>.png, and <country>/<city>.png under artifacts/v6/cards.
# Needs the built app served with artifacts/v6 at http://127.0.0.1:5198.
# Usage from /tmp: uv run --quiet --with playwright python -I /path/to/research/taste/render_cards.py 150
import base64
import csv
import pathlib
import re
import sys
import unicodedata

from playwright.sync_api import sync_playwright

ROOT = pathlib.Path(__file__).resolve().parents[2]
OUT = ROOT / 'artifacts/v6/cards'
SITE = 'http://127.0.0.1:5198'
MONTHS = ['january', 'february', 'march', 'april', 'may', 'june', 'july', 'august', 'september', 'october', 'november', 'december']
PERIODS = MONTHS + ['dec-feb', 'mar-may', 'jun-aug', 'sep-nov']


def kebab(text):
    text = re.sub(r'[\u0300-\u036f]', '', unicodedata.normalize('NFD', text)).lower()
    return re.sub(r'[^a-z0-9]+', '-', text).strip('-')


def biggest_places(count):
    """Match the Worker's country/city slugs, where the biggest place owns a shared slug."""
    with (ROOT / 'artifacts/places.tsv').open(encoding='utf-8', newline='') as source:
        rows = sorted(csv.reader(source, delimiter='\t'), key=lambda row: -int(row[5]))
    seen, chosen = set(), []
    for row in rows:
        name, plain_name, region = row[:3]
        country = kebab(region.split(',')[-1])
        # Places in Palestine use their Arabic names, but have no pre-rendered preview cards.
        if country == 'palestine':
            continue
        slug = f'{country}/{kebab(plain_name)}'
        if slug in seen:
            continue
        seen.add(slug)
        chosen.append((slug, name))
        if len(chosen) == count:
            break
    return chosen


def render(page, path, out, place_name=None):
    response = page.goto(f'{SITE}{path}', wait_until='networkidle', timeout=120000)
    if not response.ok:
        raise RuntimeError(f'{path}: HTTP {response.status}')
    page.wait_for_function('typeof window.niceWeatherShareCard === "function"', timeout=60000)
    page.wait_for_function("document.querySelector('.headline-sentence')?.textContent.startsWith('Nicest ')", timeout=120000)
    if place_name is not None:
        page.locator('.card .score-badge').wait_for(timeout=120000)
        actual_name = page.locator('.card h2').inner_text()
        if actual_name != place_name:
            raise RuntimeError(f'{path}: expected {place_name!r}, saw {actual_name!r}')
    page.wait_for_timeout(1500)  # Let the map's labels settle before copying its canvas.
    data = page.evaluate('''async () => {
        const blob = await window.niceWeatherShareCard(1200, 630);
        const bytes = new Uint8Array(await blob.arrayBuffer());
        let text = ''; for (const byte of bytes) text += String.fromCharCode(byte);
        return btoa(text);
    }''')
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_bytes(base64.b64decode(data))
    print(f'{out.relative_to(OUT)}\t{out.stat().st_size}', flush=True)


if __name__ == '__main__':
    place_count = int(sys.argv[1])
    places = biggest_places(place_count)
    if len(places) != place_count:
        raise RuntimeError(f'Asked for {place_count} places, found {len(places)}')
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True, args=['--use-angle=metal', '--enable-gpu'])
        page = browser.new_page(viewport={'width': 1200, 'height': 630}, device_scale_factor=1, color_scheme='light')
        render(page, '/year', OUT / 'world.png')
        for period in PERIODS:
            render(page, f'/{period}', OUT / f'world-{period}.png')
        for slug, name in places:
            render(page, f'/{slug}/year', OUT / f'{slug}.png', name)
        browser.close()
    print(f'Finished: {len(PERIODS) + 1} world cards and {len(places)} place cards.', flush=True)
