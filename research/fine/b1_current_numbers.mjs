// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Run the actual Worker's current-format calculation without enabling fine data.
import fs from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { createHash } from 'node:crypto';

const [dataRoot, queriesPath, output, liveOutput] = process.argv.slice(2);
if (!output) throw Error('Usage: node b1_current_numbers.mjs <data-root> <queries.json> <output.json> [live-output.json]');
const workerPath = path.resolve('hosting/sites/worker.mjs');
const source = await fs.readFile(workerPath, 'utf8');
const executable = source.replace("'./fine-place.mjs'", JSON.stringify(pathToFileURL(path.resolve('hosting/sites/fine-place.mjs')).href))
  + '\nexport { defaultPeriodJudgement };\n';
const { defaultPeriodJudgement } = await import(`data:text/javascript;base64,${Buffer.from(executable).toString('base64')}`);
const bucket = {
  async get(key, options) {
    if (key === 'v6/fine/current.json') return null;
    let bytes;
    try { bytes = await fs.readFile(path.join(dataRoot, key)); } catch (error) {
      if (error.code === 'ENOENT') return null;
      throw error;
    }
    const range = options?.range;
    if (range) bytes = bytes.subarray(range.offset, range.offset + range.length);
    return {
      arrayBuffer: async () => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength),
      json: async () => JSON.parse(bytes.toString('utf8')),
    };
  },
};
const queries = JSON.parse(await fs.readFile(queriesPath));
const rows = [];
for (const query of queries) {
  for (let month = 1; month <= 12; month++) {
    const judgement = await defaultPeriodJudgement(bucket, query.lat, query.lon, { kind: 'climate', months: [month] });
    rows.push({ slug: query.slug, month, ...judgement });
  }
}
await fs.mkdir(path.dirname(output), { recursive: true });
await fs.writeFile(output, JSON.stringify({
  capturedUtc: new Date().toISOString(),
  workerSha256: createHash('sha256').update(source).digest('hex'),
  definition: 'Actual current-format Worker calculation; fine activation pointer withheld. Original compact records and offsets. Scores before display floor, day counts before display rounding.',
  rows,
}, null, 2) + '\n');
console.log(`Current-format Worker: ${rows.length} monthly judgements`);
if (liveOutput) {
  const names = ['january','february','march','april','may','june','july','august','september','october','november','december'];
  const captures = [];
  for (const query of queries) {
    const slug = query.search_result?.slug ?? `@${query.lat},${query.lon},7z`;
    const months = await Promise.all(names.map(async (name, index) => {
      const url = `https://nice.jjpcodes.com/${slug}/${name}`;
      const response = await fetch(url);
      if (!response.ok) throw Error(`${url}: HTTP ${response.status}`);
      const html = await response.text();
      const description = html.match(/<meta name="description" content="([^"]*)"/)[1];
      const score = description.match(/nice score (\d+) out of 100/);
      if (!score) throw Error(`Current published score unavailable in ${url}: ${description}`);
      const counts = description.match(/Nice or better on (\d+) of (\d+) days, perfect on (\d+), awful on (\d+)/);
      const result = { slug: query.slug, month: index + 1, url, description, score: +score[1],
        ...(counts ? { niceDayCount: +counts[1], totalDays: +counts[2], perfectDayCount: +counts[3], awfulDayCount: +counts[4] } : {}) };
      const expected = rows.find(row => row.slug === result.slug && row.month === result.month);
      result.matchesTypicalDayScore = result.score === Math.floor(expected.score);
      for (const field of counts ? ['score','niceDayCount','perfectDayCount','awfulDayCount'] : []) {
        const value = field === 'score' ? Math.floor(expected[field]) : Math.round(expected[field]);
        if (result[field] !== value) throw Error(`Current files differ from published preview: ${query.slug}/${name}/${field}: ${value} versus ${result[field]}`);
      }
      return result;
    }));
    captures.push(...months);
  }
  await fs.writeFile(liveOutput, JSON.stringify({ capturedUtc: new Date().toISOString(), rows: captures }, null, 2) + '\n');
  console.log(`Live previews: ${captures.length} captured; ${captures.filter(row => row.matchesTypicalDayScore).length} scores match the current-source typical-day rule`);
}
