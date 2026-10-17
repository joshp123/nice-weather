// Written by AI on 2026.10.17 by model GPT-6.1 Sol.
import assert from 'node:assert/strict';
import test from 'node:test';
import { handleRequest } from './worker.mjs';

test('outlooks serve shareable posters for tomorrow and withhold stale or missing posters', async () => {
  const firstDate = new Date().toISOString().slice(0, 10);
  const tomorrow = new Date(`${firstDate}T12:00:00Z`);
  tomorrow.setUTCDate(tomorrow.getUTCDate() + 1);
  const date = tomorrow.toISOString().slice(0, 10);
  const files = new Map([['v6/forecast/current.json', { run: 'test-run', firstDate }]]);
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
    assert.ok(html.includes(`rel="canonical" href="https://nice.example/outlook/${region}"`));
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
});
