# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Render link previews from the app's Make a poster image, in headless Chromium."""
import argparse
import asyncio
import base64
import csv
import hashlib
import json
import pathlib
import re
import time
import unicodedata

from playwright.async_api import async_playwright

ROOT = pathlib.Path(__file__).resolve().parents[2]
MONTHS = ['january', 'february', 'march', 'april', 'may', 'june', 'july', 'august', 'september', 'october', 'november', 'december']
PERIODS = MONTHS + ['dec-feb', 'mar-may', 'jun-aug', 'sep-nov', 'nov-mar', 'year']
EXTRA_PLACES = ['spain/arona', 'portugal/funchal', 'spain/malaga', 'spain/seville', 'portugal/lisbon', 'greece/chania', 'south-africa/cape-town', 'united-states/san-diego', 'united-states/san-francisco', 'morocco/agadir']


def kebab(text):
    text = re.sub(r'[\u0300-\u036f]', '', unicodedata.normalize('NFD', text)).lower()
    return re.sub(r'[^a-z0-9]+', '-', text).strip('-')


def catalogue_places():
    """Match the Worker's slugs: the largest place owns each country/city slug."""
    with (ROOT / 'artifacts/places.tsv').open(encoding='utf-8', newline='') as source:
        rows = sorted(csv.reader(source, delimiter='\t'), key=lambda row: -int(row[5]))
    places = {}
    for name, plain_name, region, *_ in rows:
        country = kebab(region.split(',')[-1])
        if country != 'palestine':
            places.setdefault(f'{country}/{kebab(plain_name)}', name)
    return places


async def select_period(page, period):
    if page.url.rstrip('/').endswith('/' + period):
        return
    await page.get_by_role('button', name='Choose when', exact=True).click()
    if period in MONTHS:
        await page.get_by_role('button', name='Whole year', exact=True).click()
        await page.locator(f'.year-dial-month[data-month="{MONTHS.index(period) + 1}"]').click()
    else:
        label = {'year': 'Whole year', 'nov-mar': 'Your winter, Nov to Mar', 'dec-feb': 'Winter', 'mar-may': 'Spring', 'jun-aug': 'Summer', 'sep-nov': 'Autumn'}[period]
        await page.get_by_role('button', name=label, exact=True).click()
    await page.get_by_role('button', name='Close time picker', exact=True).click()
    await page.wait_for_url(lambda url: str(url).split('?')[0].endswith('/' + period))


async def render_period(page, period, destination, place_name):
    await select_period(page, period)
    await page.wait_for_function("!document.querySelector('.headline-note')?.textContent.startsWith('Loading weather') && document.querySelectorAll('.headline-sentence .inline-place').length > 0", timeout=120000)
    if place_name:
        await page.locator('.card .score-badge').wait_for(timeout=120000)
        actual_name = await page.locator('.card h2').inner_text()
        if actual_name != place_name and not actual_name.endswith(' ' + place_name):
            raise RuntimeError(f'{page.url}: expected {place_name!r}, saw {actual_name!r}')
    await page.get_by_role('button', name='Make a poster', exact=True).click()
    await page.wait_for_function("document.querySelector('.poster-preview')?.complete && document.querySelector('.poster-preview')?.naturalWidth === 1200", timeout=60000)
    encoded = await page.locator('.poster-preview').evaluate('''async image => {
        const blob = await fetch(image.src).then(response => response.blob());
        return new Promise(resolve => { const reader = new FileReader(); reader.onload = () => resolve(reader.result.split(',')[1]); reader.readAsDataURL(blob); });
    }''')
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_bytes(base64.b64decode(encoded))
    await page.get_by_role('button', name='Back to the map', exact=True).click()


async def main(arguments):
    catalogue = catalogue_places()
    if arguments.place_count < 0 or arguments.place_count > len(catalogue):
        raise ValueError('place_count is outside the catalogue')
    requested = list(catalogue)[:arguments.place_count] + arguments.include
    places = list(dict.fromkeys(requested))
    missing = [slug for slug in places if slug not in catalogue]
    if missing:
        raise ValueError(f'Unknown place slugs: {missing}')
    arguments.out.mkdir(parents=True, exist_ok=True)
    queue = asyncio.Queue()
    queue.put_nowait((None, None))
    for slug in places:
        queue.put_nowait((slug, catalogue[slug]))
    started = time.monotonic()
    completed = []
    async with async_playwright() as playwright:
        browser = await playwright.chromium.launch(headless=True, args=['--use-angle=metal', '--enable-gpu'])
        async def worker():
            context = await browser.new_context(viewport={'width': 1200, 'height': 630}, device_scale_factor=1, color_scheme='light', timezone_id='UTC')
            page = await context.new_page()
            try:
                while not queue.empty():
                    slug, name = queue.get_nowait()
                    response = await page.goto(f'{arguments.site}/{slug + "/" if slug else ""}year', wait_until='networkidle', timeout=120000)
                    if not response.ok:
                        raise RuntimeError(f'{page.url}: HTTP {response.status}')
                    for period in arguments.periods:
                        destination = arguments.out / slug / f'{period}.png' if slug else arguments.out / f'world-{period}.png'
                        await render_period(page, period, destination, name)
                        completed.append(destination)
                        if period == 'year':
                            alias = arguments.out / f'{slug}.png' if slug else arguments.out / 'world.png'
                            alias.parent.mkdir(parents=True, exist_ok=True)
                            alias.write_bytes(destination.read_bytes())
                            completed.append(alias)
                    print(f'{slug or "world"}\t{len(arguments.periods)} periods\t{time.monotonic() - started:.1f}s', flush=True)
                    queue.task_done()
            finally:
                await context.close()
        try:
            await asyncio.gather(*(worker() for _ in range(arguments.workers)))
        finally:
            await browser.close()
    files = []
    for path in sorted(completed):
        data = path.read_bytes()
        files.append({'file': str(path.relative_to(arguments.out)), 'bytes': len(data), 'sha256': hashlib.sha256(data).hexdigest()})
    manifest = {'places': len(places), 'periods': arguments.periods, 'files': files, 'bytes': sum(item['bytes'] for item in files), 'seconds': round(time.monotonic() - started, 2)}
    manifest_path = arguments.manifest or arguments.out.with_suffix('.manifest.json')
    manifest_path.write_text(json.dumps(manifest, indent=2) + '\n')
    print(json.dumps({**manifest, 'files': len(files), 'manifest': str(manifest_path)}, indent=2), flush=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('place_count', type=int, help='Largest places, before adding and deduplicating --include')
    parser.add_argument('--out', type=pathlib.Path, default=pathlib.Path('/tmp/nice-weather-cards/typical-day'))
    parser.add_argument('--periods', nargs='+', choices=PERIODS, default=PERIODS)
    parser.add_argument('--include', nargs='*', default=EXTRA_PLACES, help='Additional country/city slugs; default: the ten requested places')
    parser.add_argument('--site', default='http://127.0.0.1:5198')
    parser.add_argument('--workers', type=int, choices=range(1, 5), default=2)
    parser.add_argument('--manifest', type=pathlib.Path)
    asyncio.run(main(parser.parse_args()))
