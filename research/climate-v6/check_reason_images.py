# Written by AI on 2026.10.26 by model GPT-6.
"""Compare local reason PNGs with the why-map candidate's actual WebGL2 shaders.

Start research/site/layout-audit/live_data_proxy.py dist 5199 first, then run:
devenv shell -- uv run --with playwright python -I research/climate-v6/check_reason_images.py

The candidate's shaders and taste module are extracted into a temporary directory;
the working engine files are never modified. Current WeatherData supplies the
same records, nearest-land ordinals and nearest-filled offsets as today's engine.
"""
import argparse
import asyncio
import json
import shutil
import subprocess
import tempfile
from pathlib import Path

from playwright.async_api import async_playwright

ROOT = Path(__file__).resolve().parents[2]
CANDIDATE = 'claude/audit-whymap-29ba59'
PERIODS = [
    ('day-00', {'kind': 'forecast-day', 'day': 0}),
    ('january', {'kind': 'month', 'month': 1}),
    ('year', {'kind': 'year'}),
    ('all', {'kind': 'forecast'}),
    ('nov-mar', {'kind': 'months', 'startMonth': 11, 'endMonth': 3}),
]

PROBE = r'''async ({name, period}) => {
  const {WeatherData, loadGreyscaleImage, fullscreenVertex, scoringFragment,
    resolveFragment, SHIFT_LEVELS, CELL_TEXTURE_WIDTH, DEFAULT_TASTE, SCORE_BANDS} = window.reasonProbe;
  const source = new WeatherData('/data/v6');
  const [cells, data] = await Promise.all([source.cells, source.load(period)]);
  const forecast = period.kind === 'forecast-day' || period.kind === 'forecast';
  const index = forecast ? await (await fetch('/data/v6/forecast/current.json')).json() : null;
  const reasonPath = forecast ? `forecast/${index.run}/reasons-typical-day-${name}.png`
    : `climate/reasons-typical-day-${name}.png`;
  const image = await loadGreyscaleImage(`/data/v6/${reasonPath}`);
  const rawOffsets = await Promise.all([...data.offsets.keys()].map(month =>
    loadGreyscaleImage(`/data/v6/climate/offsets-${String(month).padStart(2, '0')}.png`)));
  const pairs = new Uint16Array(await (await fetch('/data/v6/cells.bin')).arrayBuffer());
  const cellCentres = new Set();
  for (let i = 0; i < pairs.length; i += 2) {
    const row = pairs[i] * 4 + 2, column = pairs[i + 1] * 4 + 2;
    if (row <= 600) cellCentres.add(row * 1440 + column);
  }
  const gl = new OffscreenCanvas(1440, 721).getContext('webgl2');
  if (!gl || !gl.getExtension('EXT_color_buffer_float')) throw Error('WebGL2 float targets unavailable');
  const renderer = gl.getParameter(gl.RENDERER);
  function program(fragment) {
    const result = gl.createProgram();
    for (const [type, source] of [[gl.VERTEX_SHADER, fullscreenVertex], [gl.FRAGMENT_SHADER, fragment]]) {
      const shader = gl.createShader(type); gl.shaderSource(shader, source); gl.compileShader(shader);
      if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) throw Error(gl.getShaderInfoLog(shader));
      gl.attachShader(result, shader); gl.deleteShader(shader);
    }
    gl.bindAttribLocation(result, 0, 'a_position'); gl.linkProgram(result);
    if (!gl.getProgramParameter(result, gl.LINK_STATUS)) throw Error(gl.getProgramInfoLog(result));
    return result;
  }
  function texture(target) {
    const result = gl.createTexture(); gl.bindTexture(target, result);
    for (const setting of [gl.TEXTURE_MIN_FILTER, gl.TEXTURE_MAG_FILTER]) gl.texParameteri(target, setting, gl.NEAREST);
    gl.texParameteri(target, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(target, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    return result;
  }
  const scoreProgram = program(scoringFragment), resolveProgram = program(resolveFragment);
  const buffer = gl.createBuffer(); gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
  gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([0,0,1,0,0,1,0,1,1,0,1,1]), gl.STATIC_DRAW);
  gl.enableVertexAttribArray(0); gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0);
  gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1);
  const rows = Math.ceil(cells.count / CELL_TEXTURE_WIDTH);
  const ordinals = texture(gl.TEXTURE_2D);
  gl.texImage2D(gl.TEXTURE_2D,0,gl.R16UI,360,180,0,gl.RED_INTEGER,gl.UNSIGNED_SHORT,cells.ordinals);
  const offsets = texture(gl.TEXTURE_2D_ARRAY);
  gl.texStorage3D(gl.TEXTURE_2D_ARRAY,1,gl.R8UI,1440,721,12);
  for (const [month, bytes] of data.offsets) gl.texSubImage3D(gl.TEXTURE_2D_ARRAY,0,0,0,month-1,1440,721,1,gl.RED_INTEGER,gl.UNSIGNED_BYTE,bytes);
  const scores = texture(gl.TEXTURE_2D_ARRAY);
  gl.texStorage3D(gl.TEXTURE_2D_ARRAY,1,gl.RGBA32F,CELL_TEXTURE_WIDTH,rows*SHIFT_LEVELS,data.groups.length*4);
  const output = texture(gl.TEXTURE_2D);
  gl.texStorage2D(gl.TEXTURE_2D,1,gl.RGBA32F,1440,721);
  const framebuffer = gl.createFramebuffer(); gl.bindFramebuffer(gl.FRAMEBUFFER,framebuffer);
  const recordTextures = new Map();
  for (const {records} of data.groups) {
    if (recordTextures.has(records)) continue;
    const padded = new Uint8Array(CELL_TEXTURE_WIDTH*rows*records.samples*16);
    for (let cell=0; cell<cells.count; cell++) for (let day=0; day<records.samples; day++) {
      const start=(cell*records.samples+day)*15;
      const target=((Math.floor(cell/CELL_TEXTURE_WIDTH)*records.samples+day)*CELL_TEXTURE_WIDTH+cell%CELL_TEXTURE_WIDTH)*16;
      padded.set(records.bytes.subarray(start,start+15),target);
    }
    const recordTexture = texture(gl.TEXTURE_2D);
    gl.texImage2D(gl.TEXTURE_2D,0,gl.RGBA8UI,CELL_TEXTURE_WIDTH*4,rows*records.samples,0,gl.RGBA_INTEGER,gl.UNSIGNED_BYTE,padded);
    recordTextures.set(records, recordTexture);
  }
  const scoreStart = performance.now();
  gl.viewport(0,0,CELL_TEXTURE_WIDTH,rows*SHIFT_LEVELS); gl.useProgram(scoreProgram);
  const scoreUniform = name => gl.getUniformLocation(scoreProgram,name);
  gl.uniform1i(scoreUniform('u_records'),0); gl.uniform1i(scoreUniform('u_cell_rows'),rows);
  gl.uniform1i(scoreUniform('u_cell_count'),cells.count); gl.uniform1i(scoreUniform('u_pack_reasons'),1);
  const taste=DEFAULT_TASTE;
  gl.uniform4f(scoreUniform('u_thermal_taste'),taste.warmShift,taste.heatShift,taste.eveningShift,taste.humidityShift);
  gl.uniform2f(scoreUniform('u_weather_taste'),taste.windShift,taste.rainScale);
  for (let groupIndex=0; groupIndex<data.groups.length; groupIndex++) {
    const group=data.groups[groupIndex]; gl.activeTexture(gl.TEXTURE0);
    gl.bindTexture(gl.TEXTURE_2D,recordTextures.get(group.records));
    gl.uniform1i(scoreUniform('u_samples'),group.records.samples);
    gl.uniform1i(scoreUniform('u_first_day'),group.firstDay);
    gl.uniform1i(scoreUniform('u_day_count'),group.dayCount);
    for (let pack=0; pack<4; pack++) gl.framebufferTextureLayer(gl.FRAMEBUFFER,gl.COLOR_ATTACHMENT0+pack,scores,0,groupIndex*4+pack);
    gl.drawBuffers([gl.COLOR_ATTACHMENT0,gl.COLOR_ATTACHMENT1,gl.COLOR_ATTACHMENT2,gl.COLOR_ATTACHMENT3]);
    if(gl.checkFramebufferStatus(gl.FRAMEBUFFER)!==gl.FRAMEBUFFER_COMPLETE)throw Error('Incomplete scoring framebuffer');
    gl.drawArrays(gl.TRIANGLES,0,6);
  }
  for(let pack=1;pack<4;pack++)gl.framebufferTextureLayer(gl.FRAMEBUFFER,gl.COLOR_ATTACHMENT0+pack,null,0,0);
  gl.drawBuffers([gl.COLOR_ATTACHMENT0]); gl.viewport(0,0,1440,721); gl.useProgram(resolveProgram);
  function bind(name,unit,target,value) {
    gl.activeTexture(gl.TEXTURE0+unit);gl.bindTexture(target,value);
    gl.uniform1i(gl.getUniformLocation(resolveProgram,name),unit);
  }
  bind('u_scores',0,gl.TEXTURE_2D_ARRAY,scores);bind('u_offsets',1,gl.TEXTURE_2D_ARRAY,offsets);
  bind('u_cell_ordinals',2,gl.TEXTURE_2D,ordinals);
  const resolveUniform=name=>gl.getUniformLocation(resolveProgram,name);
  gl.uniform1i(resolveUniform('u_cell_rows'),rows);gl.uniform1i(resolveUniform('u_month_count'),data.groups.length);
  gl.uniform1iv(resolveUniform('u_month_layers'),data.groups.map(group=>group.month-1));
  gl.uniform1iv(resolveUniform('u_day_counts'),data.groups.map(group=>group.dayCount));
  gl.uniform1fv(resolveUniform('u_weights'),data.groups.map(group=>group.weight));
  gl.uniform1i(resolveUniform('u_single_day'),period.kind==='forecast-day'?1:0);gl.uniform1i(resolveUniform('u_pack_reasons'),1);
  gl.framebufferTexture2D(gl.FRAMEBUFFER,gl.COLOR_ATTACHMENT0,gl.TEXTURE_2D,output,0);
  if(gl.checkFramebufferStatus(gl.FRAMEBUFFER)!==gl.FRAMEBUFFER_COMPLETE)throw Error('Incomplete resolve framebuffer');
  gl.drawArrays(gl.TRIANGLES,0,6);
  const pixels=new Float32Array(1440*721*4);gl.readPixels(0,0,1440,721,gl.RGBA,gl.FLOAT,pixels);
  const scoreResolveReadbackMilliseconds = performance.now() - scoreStart;
  const error=gl.getError();if(error!==gl.NO_ERROR)throw Error(`WebGL error ${error}`);
  const poor=SCORE_BANDS.find(band=>band.name==='Poor').minimum;
  const severity=score=>score>=55?0:score>=40?1:score>=poor?2:3;
  const groups={drawingGrid:[],measuredLand:[],landCellCentres:[]},examples=[];
  for(let i=0;i<image.length;i++) {
    if(pixels[i*4]>100.5) {if(image[i]!==255)throw Error(`Mask mismatch at ${i}`);continue;}
    if(image[i]===255)throw Error(`Image masks a GPU drawing point at ${i}`);
    const expectedReason=Math.round(pixels[i*4+3]),expectedSeverity=severity(pixels[i*4]);
    const actualReason=Math.floor(image[i]/4),actualSeverity=image[i]%4;
    const result=[actualReason===expectedReason,actualSeverity===expectedSeverity];
    groups.drawingGrid.push(result);
    if(rawOffsets.every(offset=>offset[i]!==255))groups.measuredLand.push(result);
    if(cellCentres.has(i))groups.landCellCentres.push(result);
    if((!result[0]||!result[1])&&examples.length<12)examples.push({index:i,latitude:90-Math.floor(i/1440)/4,longitude:-180+(i%1440)/4,imageByte:image[i],gpuReason:expectedReason,gpuScore:pixels[i*4],gpuSeverity:expectedSeverity});
  }
  function summary(values) {
    const count=values.length;
    const reason=values.filter(value=>value[0]).length,severity=values.filter(value=>value[1]).length;
    const complete=values.filter(value=>value[0]&&value[1]).length;
    return {count,reasonMatches:reason,severityMatches:severity,fullByteMatches:complete,
      reasonPercent:100*reason/count,severityPercent:100*severity/count,fullBytePercent:100*complete/count};
  }
  const summaries=Object.fromEntries(Object.entries(groups).map(([key,values])=>[key,summary(values)]));
  gl.getExtension('WEBGL_lose_context').loseContext();
  return {period:name,path:reasonPath,renderer,poorThreshold:poor,scoreResolveReadbackMilliseconds,...summaries,mismatchExamples:examples};
}'''


def build_probe(directory):
    for name in ['src/engine/data.ts', 'src/engine/greyscaleImage.ts',
                 'src/engine/temperatureOffsets.ts', 'hosting/sites/score-bands.mjs',
                 'hosting/sites/awful-maximum.json']:
        target = directory / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(ROOT / name, target)
    for name in ['shaders.ts', 'taste.ts']:
        (directory / 'src/engine' / name).write_bytes(subprocess.check_output(
            ['git', 'show', f'{CANDIDATE}:src/engine/{name}'], cwd=ROOT))
    (directory / 'probe.ts').write_text("""
import { WeatherData } from './src/engine/data';
import { loadGreyscaleImage } from './src/engine/greyscaleImage';
import * as shaders from './src/engine/shaders';
import { DEFAULT_TASTE } from './src/engine/taste';
import { SCORE_BANDS } from './hosting/sites/score-bands.mjs';
window.reasonProbe={WeatherData,loadGreyscaleImage,DEFAULT_TASTE,SCORE_BANDS,...shaders};
""")
    subprocess.run([str(ROOT / 'node_modules/.bin/esbuild'), str(directory / 'probe.ts'),
                    '--bundle', '--format=esm', '--define:import.meta.env.VITE_CLIMATE_RELEASE=""',
                    f'--outfile={directory / "probe.js"}'], check=True, cwd=ROOT)


async def main(args):
    with tempfile.TemporaryDirectory(prefix='nice-reason-gpu-') as temporary:
        directory = Path(temporary)
        build_probe(directory)
        async with async_playwright() as playwright:
            browser = await getattr(playwright, args.browser).launch(headless=True)
            page = await browser.new_page()
            await page.route('**/__reason-comparison', lambda route: route.fulfill(
                content_type='text/html', body='<script type="module" src="/__reason-probe.js"></script>'))
            await page.route('**/__reason-probe.js', lambda route: route.fulfill(
                content_type='text/javascript', body=(directory / 'probe.js').read_text()))
            await page.goto(args.url + '/__reason-comparison')
            await page.wait_for_function('window.reasonProbe')
            results = []
            for name, period in PERIODS:
                result = await page.evaluate(PROBE, {'name': name, 'period': period})
                results.append(result)
                print(json.dumps(result), flush=True)
            await browser.close()
    report = {'browser': args.browser, 'candidate': subprocess.check_output(
        ['git', 'rev-parse', CANDIDATE], cwd=ROOT, text=True).strip(), 'periods': results}
    if args.out:
        Path(args.out).write_text(json.dumps(report, indent=2) + '\n')
    assert all(period[scope]['fullBytePercent'] >= 99.5 for period in results
               for scope in ['drawingGrid', 'measuredLand', 'landCellCentres']), report


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--url', default='http://127.0.0.1:5199')
    parser.add_argument('--browser', choices=['chromium', 'webkit'], default='chromium')
    parser.add_argument('--out')
    asyncio.run(main(parser.parse_args()))
