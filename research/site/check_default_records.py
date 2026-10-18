# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Capture cold default-route traffic and screenshots in headless WebKit.

With Playwright installed and the built app served on port 5198:
  python research/site/check_default_records.py /tmp/nwf-default-record-free after
"""
import asyncio
import json
import pathlib
import sys

from playwright.async_api import async_playwright

SITE = 'http://127.0.0.1:5198'


async def capture(browser, output, width, height, path):
    context = await browser.new_context(
        viewport={'width': width, 'height': height}, device_scale_factor=1,
        color_scheme='light',
    )
    page = await context.new_page()
    errors, requests, request_tasks = [], [], []
    page.on('pageerror', lambda error: errors.append(str(error)))

    async def record_response(request):
        try:
            sizes = await request.sizes()
            response = await request.response()
            requests.append({
                'url': request.url, 'method': request.method,
                'status': response.status if response else None, **sizes,
            })
        except Exception as error:
            requests.append({'url': request.url, 'error': str(error)})

    page.on('requestfinished', lambda request: request_tasks.append(
        asyncio.create_task(record_response(request))))
    page.on('requestfailed', lambda request: requests.append({
        'url': request.url, 'failure': request.failure,
    }))
    await page.goto(SITE + path, wait_until='domcontentloaded', timeout=120000)
    await page.wait_for_function("""() =>
      performance.getEntriesByName('nice-weather-redraw').some(entry => entry.duration > 0)
      && document.querySelector('.headline-sentence .inline-place')
      && !document.querySelector('.headline-note')?.textContent.includes('Loading')
    """, timeout=120000)
    answer_ready = await page.evaluate('performance.now()')
    await page.wait_for_load_state('networkidle', timeout=60000)
    await page.wait_for_timeout(500)
    await asyncio.gather(*request_tasks)
    metrics = await page.evaluate("""() => ({
      redraws: performance.getEntriesByName('nice-weather-redraw')
        .map(entry => ({start:entry.startTime, duration:entry.duration})),
      paints: performance.getEntriesByType('paint')
        .map(entry => ({name:entry.name, start:entry.startTime})),
      resources: performance.getEntriesByType('resource').map(entry => ({
        url:entry.name, encoded:entry.encodedBodySize, decoded:entry.decodedBodySize,
        transfer:entry.transferSize, start:entry.startTime, end:entry.responseEnd,
      })),
      headline: document.querySelector('.headline-sentence')?.innerText,
      overflow: document.documentElement.scrollWidth > innerWidth,
    })""")
    name = f'{width}-{path.strip("/") or "forecast"}'
    await page.screenshot(path=str(output / f'{name}.png'))
    records = [request for request in requests if
               '/data/v6/climate/month-' in request['url'] or
               '/data/v6/forecast/' in request['url'] and '/days.bin' in request['url']]
    network = [request for request in requests
               if request['url'].startswith(('http://', 'https://'))]
    row = {
        'path': path, 'viewport': [width, height], 'answerReadyMs': answer_ready,
        'firstWeatherMs': next(entry['start'] + entry['duration']
                               for entry in metrics['redraws'] if entry['duration'] > 0),
        'firstContentfulPaintMs': next((entry['start'] for entry in metrics['paints']
                                       if entry['name'] == 'first-contentful-paint'), None),
        'settledMs': max([entry['end'] for entry in metrics['resources']] +
                         [entry['start'] + entry['duration'] for entry in metrics['redraws']]),
        'bodyBytes': sum(max(0, request.get('responseBodySize', 0)) for request in network),
        'requestCount': len(network), 'dayRecordRequests': len(records),
        'dayRecordBytes': sum(request.get('responseBodySize', 0) for request in records),
        'requests': requests, 'networkRequests': network, 'errors': errors, **metrics,
    }
    assert not records, f'Default view requested day records: {records}'
    assert not errors and not metrics['overflow'], row
    await context.close()
    return row


async def main():
    output = pathlib.Path(sys.argv[1]) / sys.argv[2]
    output.mkdir(parents=True, exist_ok=True)
    results = []
    async with async_playwright() as playwright:
        browser = await playwright.webkit.launch(headless=True)
        for width, height in [(390, 844), (1512, 982)]:
            for path in ['/nov-mar', '/year', '/july', '/']:
                row = await capture(browser, output, width, height, path)
                results.append(row)
                (output / 'results.json').write_text(json.dumps(results, indent=2))
                print(json.dumps({key: row[key] for key in [
                    'path', 'viewport', 'settledMs', 'firstWeatherMs', 'bodyBytes',
                    'requestCount', 'dayRecordRequests', 'dayRecordBytes', 'errors',
                ]}), flush=True)
        await browser.close()


asyncio.run(main())
