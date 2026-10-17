# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
"""Exercise the built local site with headless Playwright and save visitor evidence.

Run with Playwright installed, while npm run serve listens on port 5198:
  python check_typical_days.py --out /tmp/nwf-typical-day-proof
"""
import argparse
import base64
import json
import pathlib
import re

from playwright.sync_api import sync_playwright

ROOT = pathlib.Path(__file__).resolve().parents[2]
SITE = 'http://127.0.0.1:5198'
CASES = [
    ('tenerife', '/@28.05,-16.72,7z/year', 'Perfect most days', 'Tenerife, south coast', 'year'),
    ('rotterdam', '/the-netherlands/rotterdam/january', 'Awful most days', 'Rotterdam', 'january'),
    ('malaga', '/spain/malaga/january', 'Fair most days', 'Málaga', 'january'),
    ('san-diego', '/united-states/san-diego/year', 'Perfect most days', 'San Diego', 'year'),
    ('cape-town', '/south-africa/cape-town/nov-mar', 'Perfect most days', 'Cape Town', 'novMar'),
]


def ready(page):
    page.wait_for_selector('.why-row', timeout=120000)
    page.wait_for_function("!document.querySelector('.headline-note')?.textContent.includes('Loading')", timeout=120000)
    page.wait_for_timeout(250)


def capture(page, out, name):
    page.wait_for_load_state('networkidle', timeout=30000)
    page.wait_for_timeout(500)
    page.screenshot(path=str(out / f'{name}.png'))
    metrics = page.evaluate('''() => {
      const rect = selector => document.querySelector(selector)?.getBoundingClientRect().toJSON();
      return {overflow:document.documentElement.scrollWidth>innerWidth,
        card:rect('.card'),bar:rect('.bar'),
        selects:[...document.querySelectorAll('select')].map(e=>({label:e.getAttribute('aria-label'),rect:e.getBoundingClientRect().toJSON()}))};
    }''')
    assert not metrics['overflow'], metrics
    assert all(field['rect']['right'] <= page.viewport_size['width'] for field in metrics['selects']), metrics
    assert metrics['card']['bottom'] < metrics['bar']['top'], metrics
    return metrics


def read_counts(page):
    sentence = page.locator('.verdict-days').inner_text()
    match = re.search(r'Nice or better on (\d+) of (\d+) days, perfect on (\d+), awful on (\d+)', sentence)
    assert match, sentence
    nice, total, perfect, awful = map(int, match.groups())
    return {'nice': nice, 'total': total, 'perfect': perfect, 'awful': awful}


def share_image(page, out):
    encoded = page.evaluate('''async () => {
      const blob = await window.niceWeatherShareCard(1200,630);
      let text='';for(const byte of new Uint8Array(await blob.arrayBuffer()))text+=String.fromCharCode(byte);
      return btoa(text);
    }''')
    out.write_bytes(base64.b64decode(encoded))


def proposal(page, out):
    page.goto((ROOT / '.agent/typical-day-proposal.html').as_uri())
    result = page.evaluate('''() => {
      const selected=[...months];const result={};
      for(const name of ['Tenerife, south coast','Rotterdam','Málaga','San Diego','Cape Town']) {
        const i=DATA.places.findIndex(p=>p[0]===name);result[name]={point:DATA.places[i].slice(2,4)};
        for(const [key,selection] of Object.entries({year:selected,january:[0],novMar:[10,11,0,1,2]})) {
          months=new Set(selection);const c=kinds(placeDays,i*96);
          result[name][key]={score:score(placeDays,i*96),nice:Math.round(c[3]+c[4]+c[5]),perfect:Math.round(c[5]),awful:Math.round(c[0])};
        }
      }
      months=new Set(selected);return result;
    }''')
    page.screenshot(path=str(out / 'proposal-year.png'), full_page=True)
    page.get_by_role('button', name='Nov to Mar', exact=True).click()
    page.screenshot(path=str(out / 'proposal-nov-mar.png'), full_page=True)
    return result


def run(browser_type, out, interactions_only=False):
    results = {'proposal': {}, 'cases': [], 'interactions': {}, 'errors': []}
    with sync_playwright() as playwright:
        browser = getattr(playwright, browser_type).launch(headless=True)
        if interactions_only:
            results = json.loads((out / 'visitor-checks.json').read_text())
        else:
            visual_checks(browser, browser_type, out, results)
        interaction_checks(browser, browser_type, out, results)
        browser.close()
    assert not results['errors'], results['errors']
    return results


def visual_checks(browser, browser_type, out, results):
    proposal_page = browser.new_page(viewport={'width': 1512, 'height': 982})
    results['proposal'] = proposal(proposal_page, out)
    proposal_page.close()
    for theme in ['light', 'dark']:
        for width, height in [(390, 844), (1512, 982)]:
            context = browser.new_context(viewport={'width': width, 'height': height}, color_scheme=theme, device_scale_factor=1)
            page = context.new_page()
            page.on('pageerror', lambda error: results['errors'].append(str(error)))
            for key, path, band, name, period in CASES:
                page.goto(SITE + path, wait_until='domcontentloaded', timeout=120000)
                ready(page)
                assert page.locator('.band-word').inner_text() == band
                counts = read_counts(page)
                expected = results['proposal'][name][period]
                results['cases'].append({
                    'place': key, 'theme': theme, 'viewport': [width, height],
                    'card': page.locator('.card').inner_text(), 'counts': counts,
                    'proposalDifference': {kind: counts[kind] - expected[kind] for kind in ['nice', 'perfect', 'awful']},
                    'description': page.locator('meta[name=description]').get_attribute('content'),
                    'layout': capture(page, out, f'{browser_type}-{theme}-{width}-{key}'),
                })
            picker = page.get_by_label('Choose a forecast or typical year period')
            picker.select_option('months')
            ready(page)
            page.get_by_label('First month', exact=True).select_option('10')
            ready(page)
            page.get_by_label('Last month', exact=True).select_option('4')
            ready(page)
            assert page.url.endswith('/oct-apr'), page.url
            capture(page, out, f'{browser_type}-{theme}-{width}-custom-run')
            picker.select_option('dec-feb')
            ready(page)
            assert page.url.endswith('/dec-feb')
            picker.select_option('next-14-days')
            ready(page)
            assert read_counts(page)['total'] == 14
            capture(page, out, f'{browser_type}-{theme}-{width}-forecast')
            picker.select_option('day')
            ready(page)
            assert 'most days' not in page.locator('.band-word').inner_text()
            assert '/day-' in page.url
            capture(page, out, f'{browser_type}-{theme}-{width}-day')
            context.close()
    context = browser.new_context(viewport={'width': 3840, 'height': 1080}, color_scheme='dark')
    page = context.new_page()
    page.goto(SITE + '/south-africa/cape-town/nov-mar', wait_until='domcontentloaded')
    ready(page)
    capture(page, out, f'{browser_type}-ultrawide')
    share_image(page, out / 'cape-town-share.png')
    context.close()
    (out / 'visitor-checks.json').write_text(json.dumps(results, indent=2))

def interaction_checks(browser, browser_type, out, results):
    context = browser.new_context(viewport={'width': 1512, 'height': 982}, color_scheme='light')
    page = context.new_page()
    page.goto(SITE + '/year', wait_until='domcontentloaded')
    page.get_by_label('Search a place', exact=True).fill('San Diego')
    page.locator('.search-results button').first.click()
    ready(page)
    page.wait_for_timeout(1200)
    assert page.locator('.card h2').inner_text() == 'San Diego'
    searched = {'counts': read_counts(page), 'score': page.locator('.score-badge').inner_text()}
    capture(page, out, f'{browser_type}-san-diego-search')
    page.get_by_role('button', name='Close', exact=True).click()
    page.mouse.move(756, 491)
    for _ in range(6):
        page.mouse.wheel(0, -500)
        page.wait_for_timeout(350)
    page.wait_for_timeout(1000)
    page.screenshot(path=str(out / 'san-diego-before-click.png'))
    page.mouse.click(756, 480)
    ready(page)
    page.screenshot(path=str(out / 'san-diego-after-click.png'))
    assert page.locator('.card h2').inner_text() == 'San Diego', page.locator('.card').inner_text()
    clicked = {'counts': read_counts(page), 'score': page.locator('.score-badge').inner_text()}
    assert searched == clicked, {'search': searched, 'map': clicked}
    assert '/united-states/san-diego/year' in page.url, page.url
    capture(page, out, f'{browser_type}-san-diego-map-click')
    results['interactions']['sanDiego'] = {'search': searched, 'map': clicked, 'link': page.url}
    share_image(page, out / 'san-diego-share.png')
    context.close()
    for width, height in [(1512, 982), (390, 844)]:
        context = browser.new_context(viewport={'width': width, 'height': height})
        page = context.new_page()
        page.goto(SITE + '/united-states/san-diego/year', wait_until='domcontentloaded')
        ready(page)
        page.get_by_role('button', name='Your taste', exact=True).click()
        timings = []
        for value in [22, 24, 21, 25, 23]:
            timings.append(page.locator('#slider-perfectFrom').evaluate('''(input,value) => new Promise(resolve => {
              const started=performance.now();
              const observer=new MutationObserver(()=> {
                if(!document.querySelector('.score-badge'))return;
                observer.disconnect();resolve(performance.now()-started);
              });
              observer.observe(document.querySelector('.slot-card'),{childList:true,subtree:true});
              input.value=String(value);input.dispatchEvent(new Event('input',{bubbles:true}));
            })''', value))
        limit = 200 if width == 1512 else 1000
        assert max(timings) < limit, timings
        results['interactions'][f'slider-{width}'] = timings
        context.close()

def first_view_checks(browser_type, out):
    result = []
    with sync_playwright() as playwright:
        browser = getattr(playwright, browser_type).launch(headless=True)
        for period, image in [('year', 'year'), ('nov-mar', 'nov-mar'), ('jan-dec', 'year')]:
            context = browser.new_context(viewport={'width': 1512, 'height': 982}, color_scheme='light')
            page = context.new_page()
            held = []
            blocking = True
            def hold_records(route):
                if blocking:
                    held.append(route)
                else:
                    route.continue_()
            page.route('**/data/v6/climate/month-*.bin', hold_records)
            with page.expect_response(lambda response: f'scores-{image}.png?statistic=typical-day' in response.url) as response:
                page.goto(SITE + f'/united-states/san-diego/{period}', wait_until='domcontentloaded')
            assert response.value.status == 200
            page.wait_for_timeout(2500)
            assert held, 'No weather records were held back'
            assert page.locator('.why-row').count() == 0, 'Raw weather unexpectedly loaded'
            page.screenshot(path=str(out / f'{browser_type}-first-view-{period}.png'))
            result.append({'period':period, 'scoreImage':response.value.url, 'heldWeatherRequests':len(held)})
            blocking = False
            for route in held:
                route.continue_()
            ready(page)
            context.close()
        browser.close()
    (out / 'first-view.json').write_text(json.dumps(result, indent=2))
    return result


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', type=pathlib.Path, default=pathlib.Path('/tmp/nwf-typical-day-proof'))
    parser.add_argument('--browser', choices=['webkit', 'chromium'], default='webkit')
    parser.add_argument('--interactions-only', action='store_true', help='Reuse a completed visual pass in the output folder')
    parser.add_argument('--first-view-only', action='store_true', help='Hold raw records to inspect precomputed first paint')
    arguments = parser.parse_args()
    arguments.out.mkdir(parents=True, exist_ok=True)
    if arguments.first_view_only:
        print(json.dumps(first_view_checks(arguments.browser, arguments.out), indent=2))
        raise SystemExit(0)
    result = run(arguments.browser, arguments.out, arguments.interactions_only)
    (arguments.out / 'visitor-checks.json').write_text(json.dumps(result, indent=2))
    print(json.dumps({'cases': len(result['cases']), 'errors': result['errors'], 'out': str(arguments.out)}, indent=2))
