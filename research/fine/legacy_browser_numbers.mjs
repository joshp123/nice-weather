// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Run the unchanged 25 km browser model over published files for rendered-card checks.
import fs from 'node:fs/promises';
import ts from 'typescript';

const moduleUrl = source => `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`;
const javascript = source => ts.transpileModule(source, {compilerOptions: {target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext}}).outputText;

export async function legacyBrowserNumbers(localFetch, month = 7, tasteOverride) {
  const tasteUrl = moduleUrl(javascript(await fs.readFile('src/engine/taste.ts', 'utf8')));
  const shadersUrl = moduleUrl(javascript(await fs.readFile('src/engine/shaders.ts', 'utf8')).replace("'./taste'", JSON.stringify(tasteUrl)));
  const samplingUrl = moduleUrl(javascript(await fs.readFile('src/engine/sampling.ts', 'utf8'))
    .replace("'./taste'", JSON.stringify(tasteUrl)).replace("'./shaders'", JSON.stringify(shadersUrl)));
  // The DOM's image decoder is replaced only for Node execution; use the Worker's PNG decoder.
  const worker = await fs.readFile('hosting/sites/worker.mjs', 'utf8');
  const decoder = worker.slice(worker.indexOf('async function decodeGreyscalePng(png) {'), worker.indexOf('\nconst monthlyOffsets'));
  const imageUrl = moduleUrl(`const FINE_WIDTH=1440,FINE_HEIGHT=721;\n${decoder}\nexport async function loadGreyscaleImage(url) {return decodeGreyscalePng(new Uint8Array(await (await fetch(url)).arrayBuffer()));}`);
  const dataUrl = moduleUrl(javascript(await fs.readFile('src/engine/data.ts', 'utf8')).replace("'./greyscaleImage'", JSON.stringify(imageUrl)));
  const [{PointSampler}, {WeatherData}, {DEFAULT_TASTE}] = await Promise.all([import(samplingUrl), import(dataUrl), import(tasteUrl)]);
  const originalFetch = globalThis.fetch;
  let cells, data;
  const period = typeof month === 'number' ? {kind:'month',month} : month;
  try {
    globalThis.fetch = localFetch;
    const source = new WeatherData('http://127.0.0.1:5198/data/v6');
    [cells, data] = await Promise.all([source.cells, source.load(period)]);
  } finally { globalThis.fetch = originalFetch; }
  const sampler = new PointSampler(cells, data, period, tasteOverride ?? DEFAULT_TASTE);
  return (latitude, longitude) => sampler.judge(latitude, longitude);
}
