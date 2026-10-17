# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
"""Inspect the fresh runner forecast through the built local app, headless only.

Serve the runner's output with NICE_WEATHER_GENERATED_DATA, then run this script
with Playwright installed. Numeric CPU/GPU/image comparisons are recorded
separately in forecast-runner-proof.md.
"""
import argparse
import json
import pathlib

from playwright.sync_api import sync_playwright

from check_typical_days import SITE, capture, ready, read_counts


def check(out):
    result = {'views': [], 'firstView': [], 'errors': []}
    with sync_playwright() as playwright:
        browser = playwright.webkit.launch(headless=True)
        for width, height, theme, place, period in [
            (390, 844, 'light', 'spain/malaga', 'next-14-days'),
            (390, 844, 'dark', 'united-states/san-diego', 'day-6'),
            (1512, 982, 'light', 'south-africa/cape-town', 'next-14-days'),
            (1512, 982, 'dark', 'spain/malaga', 'day-13'),
        ]:
            context = browser.new_context(viewport={'width': width, 'height': height}, color_scheme=theme)
            page = context.new_page()
            page.on('pageerror', lambda error: result['errors'].append(str(error)))
            page.goto(f'{SITE}/{place}/{period}', wait_until='domcontentloaded')
            ready(page)
            name = f'webkit-{theme}-{width}-{place.split("/")[-1]}-{period}'
            row = {'place': place, 'period': period, 'url': page.url,
                   'score': page.locator('.score-badge').inner_text(),
                   'band': page.locator('.band-word').inner_text(),
                   'card': page.locator('.card').inner_text(),
                   'layout': capture(page, out, name)}
            if period == 'next-14-days':
                row['counts'] = read_counts(page)
                assert row['counts']['total'] == 14
                assert row['band'].endswith('most days')
            else:
                assert 'most days' not in row['band']
            result['views'].append(row)
            context.close()
        for period, image in [('next-14-days', 'all'), ('day-6', 'day-06')]:
            context = browser.new_context(viewport={'width': 1512, 'height': 982}, color_scheme='light')
            page = context.new_page()
            held = []
            blocking = True

            def hold_records(route):
                if blocking:
                    held.append(route)
                else:
                    route.continue_()

            page.route('**/data/v6/forecast/*/days.bin', hold_records)
            with page.expect_response(lambda response: f'scores-{image}.png?statistic=typical-day' in response.url) as response:
                page.goto(f'{SITE}/spain/malaga/{period}', wait_until='domcontentloaded')
            assert response.value.status == 200
            page.wait_for_timeout(2000)
            assert held
            assert page.locator('.why-row').count() == 0
            page.screenshot(path=str(out / f'webkit-first-view-{period}.png'))
            result['firstView'].append({'period': period, 'image': response.value.url, 'heldRecords': len(held)})
            blocking = False
            for route in held:
                route.continue_()
            ready(page)
            context.close()
        browser.close()
    assert not result['errors'], result['errors']
    (out / 'visitor-checks.json').write_text(json.dumps(result, indent=2))
    return result


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', type=pathlib.Path, required=True)
    arguments = parser.parse_args()
    arguments.out.mkdir(parents=True, exist_ok=True)
    results = check(arguments.out)
    print(json.dumps({'views': len(results['views']), 'firstViews': len(results['firstView']), 'errors': results['errors']}))
