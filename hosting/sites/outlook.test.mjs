// Written by AI on 2026.10.17 by model GPT-6.1 Sol.
import assert from 'node:assert/strict';
import test from 'node:test';
import { handleRequest } from './worker.mjs';

test('outlooks serve shareable posters for tomorrow and withhold stale or missing posters', async () => {
  const firstDate = new Date().toISOString().slice(0, 10);
  const tomorrow = new Date(`${firstDate}T12:00:00Z`);
  tomorrow.setUTCDate(tomorrow.getUTCDate() + 1);
  const date = tomorrow.toISOString().slice(0, 10);
  const files = new Map([['v6/forecast/current.json', { run: 'test-run', firstDate, days: 14, previousRun: 'previous-run' }]]);
  const bucket = {
    async get(key) {
      const value = files.get(key);
      return value ? { json: async () => value, body: value, httpEtag: '"test"' } : null;
    },
    async head(key) { return files.has(key) ? { size: 1 } : null; },
  };
  const request = (path, method = 'GET') => handleRequest(new Request(`https://nice.example${path}`, { method }), { WEATHER: bucket }, () => { throw new Error('Outlooks must serve their own page'); });

  for (const region of ['world', 'europe', 'usa']) {
    const key = `v6/forecast/test-run/outlook/${region}`;
    const sentence = `The nicest weather in ${region} tomorrow: Málaga, Chania and Antalya.`;
    files.set(`${key}.json`, { region, date, sentence, run: 'test-run', day: 1, fullImage: `/data/${key}-full.png` });
    files.set(`${key}.png`, 'poster');
    const response = await request(`/outlook/${region}`);
    assert.equal(response.status, 200);
    assert.equal(response.headers.get('Cache-Control'), 'no-store');
    const html = await response.text();
    assert.ok(html.includes(`<h1>${sentence}</h1>`));
    assert.ok(html.includes(`<meta property="og:image" content="https://nice.example/data/${key}.png"`));
    assert.ok(html.includes('name="twitter:card" content="summary_large_image"'));
    assert.ok(html.includes(`rel="canonical" href="https://nice.example/outlook/${region}?date=${date}"`));
    assert.ok(html.includes(`property="og:url" content="https://nice.example/outlook/${region}?date=${date}"`));
    assert.ok(html.includes(`href="/@`));
    assert.ok(html.includes('/day-1"'));
    assert.ok(html.includes('Copy post'));
    assert.ok(html.includes(`href="/data/${key}-full.png" download=`));
    const head = await request(`/outlook/${region}`, 'HEAD');
    assert.equal(head.status, 200);
    assert.equal(await head.text(), '');
    const poster = await request(`/data/${key}.png`);
    assert.equal(poster.status, 200);
    assert.equal(poster.headers.get('Content-Type'), 'image/png');
    assert.equal(poster.headers.get('Cache-Control'), 'public, max-age=60');
  }

  const key = 'v6/forecast/test-run/outlook/europe';
  const dated = await request(`/outlook/europe?date=${date}`);
  assert.equal(dated.status, 200);
  assert.ok((await dated.text()).includes(`rel="canonical" href="https://nice.example/outlook/europe?date=${date}"`));
  const oldLink = await request('/outlook/europe?date=2020-01-01');
  assert.equal(oldLink.status, 503);
  const oldLinkHtml = await oldLink.text();
  assert.ok(oldLinkHtml.includes('This dated outlook is unavailable.'));
  assert.ok(oldLinkHtml.includes('This link is for Wednesday, 1 January 2020.'));
  assert.ok(oldLinkHtml.includes('href="https://nice.example/outlook/europe?date=2020-01-01"'));
  assert.ok(!oldLinkHtml.includes('property="og:image"'));
  assert.ok(!oldLinkHtml.includes('Málaga'));
  const previousKey = 'v6/forecast/previous-run/outlook/europe';
  const previousSentence = 'The nicest weather in Europe tomorrow, Wednesday 1 January: Old Place, Another Place and Third Place.';
  files.set(`${previousKey}.json`, { region: 'europe', date: '2020-01-01', sentence: previousSentence, run: 'previous-run', day: 1 });
  files.set(`${previousKey}.png`, 'old poster');
  const retained = await request('/outlook/europe?date=2020-01-01');
  assert.equal(retained.status, 200);
  const retainedHtml = await retained.text();
  assert.ok(retainedHtml.includes('<h1>The nicest weather in Europe on Wednesday 1 January: Old Place, Another Place and Third Place.</h1>'));
  assert.ok(retainedHtml.includes(`property="og:image" content="https://nice.example/data/${previousKey}.png"`));
  assert.ok(retainedHtml.includes('property="og:url" content="https://nice.example/outlook/europe?date=2020-01-01"'));
  assert.ok(!retainedHtml.includes('tomorrow'));
  assert.ok(!retainedHtml.includes('Explore this date'));
  files.delete(`${previousKey}.json`);
  files.get('v6/forecast/test-run/outlook/europe.json').date = '2020-01-01';
  const retainedInCurrentRun = await request('/outlook/europe?date=2020-01-01');
  assert.equal(retainedInCurrentRun.status, 200);
  assert.ok((await retainedInCurrentRun.text()).includes('/data/v6/forecast/test-run/outlook/europe.png'));
  assert.equal((await request('/outlook/europe?date=2026-02-31')).status, 400);
  files.get(`${key}.json`).date = '2020-01-01';
  const stale = await request('/outlook/europe');
  assert.equal(stale.status, 503);
  const staleHtml = await stale.text();
  assert.ok(staleHtml.includes('The latest outlook was for 2020-01-01.'));
  assert.ok(!staleHtml.includes('property="og:image"'));
  assert.ok(!staleHtml.includes('class="poster"'));
  assert.ok(!staleHtml.includes('Málaga'));

  files.get(`${key}.json`).date = date;
  files.delete(`${key}.png`);
  assert.equal((await request('/outlook/europe')).status, 503);
  files.delete('v6/forecast/current.json');
  assert.equal((await request('/outlook/world')).status, 503);
  assert.equal((await request('/outlook/unknown')).status, 404);
  assert.equal((await request('/outlook/constructor')).status, 404);
});
