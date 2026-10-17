# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Compare the real map fragment's GPU result with the card's engine verdict.

Run against the built app on 127.0.0.1:5198 with headless Playwright:
cd /tmp && uv run --quiet --with playwright python -I /absolute/path/check-map-card.py --out /tmp/nwf-agents/round5/build-followup
The browser route exposes the existing engine instance only for this check.
"""
import argparse
import asyncio
import csv
import json
from pathlib import Path
from playwright.async_api import async_playwright

PLACES = [
 ('Chania',35.5138,24.018),('Málaga',36.7202,-4.4203),('Tenerife south',28.05,-16.72),('Cape Town',-33.9249,18.4241),('San Diego',32.7157,-117.1611),('Rotterdam',51.9225,4.4792),('Sydney',-33.8688,151.2093),('Bangkok',13.7563,100.5018),
 ('London',51.5074,-0.1278),('New York',40.7128,-74.006),('San Francisco',37.7749,-122.4194),('Lisbon',38.7223,-9.1393),('Agadir',30.4278,-9.5981),('Funchal',32.6669,-16.9241),('Santiago',-33.4489,-70.6693),('Valparaíso',-33.0472,-71.6127),('Buenos Aires',-34.6037,-58.3816),('Rio de Janeiro',-22.9068,-43.1729),('Melbourne',-37.8136,144.9631),('Perth',-31.9505,115.8605),('Tokyo',35.6762,139.6503),('Singapore',1.3521,103.8198),('Hong Kong',22.3193,114.1694),('Taipei',25.033,121.5654),('Auckland',-36.8485,174.7633),('Honolulu',21.3099,-157.8581),('Los Angeles',34.0522,-118.2437),('Athens',37.9838,23.7275),('Karachi',24.8607,67.0011),('Nicosia',35.1856,33.3823),
]
PERIODS = [('November to March',{'kind':'months','startMonth':11,'endMonth':3}),('Whole year',{'kind':'year'}),('July',{'kind':'month','month':7}),('Next 14 days',{'kind':'forecast'})]
PROBE = r'''({places,periodName}) => {
  const e=window.__weatherEngine,sourceGl=e.gl;
  e.readScores();
  const shader=sourceGl.getAttachedShaders(e.programs.map).find(shader=>sourceGl.getShaderParameter(shader,sourceGl.SHADER_TYPE)===sourceGl.FRAGMENT_SHADER);
  const source=sourceGl.getShaderSource(shader);
  const fragment=source.replace('fragColour = vec4(landComposite * landCoverage, landCoverage);','fragColour = vec4(score, landWeight, landCoverage, 1.0);');
  if(fragment===source)throw Error('The actual map fragment output could not be instrumented');
  // An isolated GL context avoids changing MapLibre's state. Input is an exact readback of its live resolved score texture.
  const gl=new OffscreenCanvas(1,1).getContext('webgl2');
  if(!gl.getExtension('EXT_color_buffer_float'))throw Error('No floating-point render targets');
  const vertex='#version 300 es\nin vec2 a_position;uniform vec2 u_point;out vec2 v_mercator;void main(){v_mercator=u_point;gl_Position=vec4(a_position*2.0-1.0,0.0,1.0);}';
  const program=gl.createProgram();
  for(const [type,text]of[[gl.VERTEX_SHADER,vertex],[gl.FRAGMENT_SHADER,fragment]]){const shader=gl.createShader(type);gl.shaderSource(shader,text);gl.compileShader(shader);if(!gl.getShaderParameter(shader,gl.COMPILE_STATUS))throw Error(gl.getShaderInfoLog(shader));gl.attachShader(program,shader);gl.deleteShader(shader);}
  gl.bindAttribLocation(program,0,'a_position');gl.linkProgram(program);if(!gl.getProgramParameter(program,gl.LINK_STATUS))throw Error(gl.getProgramInfoLog(program));
  const buffer=gl.createBuffer();gl.bindBuffer(gl.ARRAY_BUFFER,buffer);gl.bufferData(gl.ARRAY_BUFFER,new Float32Array([0,0,1,0,0,1,0,1,1,0,1,1]),gl.STATIC_DRAW);gl.enableVertexAttribArray(0);gl.vertexAttribPointer(0,2,gl.FLOAT,false,0,0);
  const grid=gl.createTexture();gl.activeTexture(gl.TEXTURE0);gl.bindTexture(gl.TEXTURE_2D,grid);gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_MIN_FILTER,gl.NEAREST);gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_MAG_FILTER,gl.NEAREST);gl.texImage2D(gl.TEXTURE_2D,0,gl.RGBA32F,1440,721,0,gl.RGBA,gl.FLOAT,e.fineScoreReadback);
  const output=gl.createTexture();gl.bindTexture(gl.TEXTURE_2D,output);gl.texImage2D(gl.TEXTURE_2D,0,gl.RGBA32F,1,1,0,gl.RGBA,gl.FLOAT,null);const framebuffer=gl.createFramebuffer();gl.bindFramebuffer(gl.FRAMEBUFFER,framebuffer);gl.framebufferTexture2D(gl.FRAMEBUFFER,gl.COLOR_ATTACHMENT0,gl.TEXTURE_2D,output,0);gl.bindTexture(gl.TEXTURE_2D,grid);
  gl.useProgram(program);gl.viewport(0,0,1,1);gl.uniform1i(gl.getUniformLocation(program,'u_grid'),0);gl.uniform1f(gl.getUniformLocation(program,'u_grid_scale'),1);gl.uniform1i(gl.getUniformLocation(program,'u_round_scores'),0);
  const band=score=>['Awful','Poor','Fair','Nice','Beautiful','Perfect'][[0,25,40,55,70,85].findLastIndex(minimum=>Math.round(score*1000)/1000>=minimum)];
  const rows=places.map(([name,latitude,longitude])=>{const card=e.judgePoint(latitude,longitude);if(!card)throw Error(`No card verdict for ${name}`);const pixel=new Float32Array(4);gl.uniform2f(gl.getUniformLocation(program,'u_point'),(longitude+180)/360,(1-Math.asinh(Math.tan(latitude*Math.PI/180))/Math.PI)/2);gl.drawArrays(gl.TRIANGLES,0,6);gl.readPixels(0,0,1,1,gl.RGBA,gl.FLOAT,pixel);return {place:name,latitude,longitude,period:periodName,mapScore:pixel[0],cardScore:card.meanScore,difference:pixel[0]-card.meanScore,mapBand:band(pixel[0]),cardBand:card.band,niceDays:card.niceDayCount,totalDays:card.totalDays,landWeight:pixel[1],coverage:pixel[2],previewLoaded:Boolean(e.scoreImage),previewStillOverrides:Boolean(e.scoreImage&&!e.data)};});
  gl.getExtension('WEBGL_lose_context')?.loseContext();
  return rows;
}'''

async def main(args):
 out=Path(args.out);out.mkdir(parents=True,exist_ok=True)
 errors=[];rows=[]
 async with async_playwright() as p:
  browser=await getattr(p,args.browser).launch(headless=True)
  page=await browser.new_page(viewport={'width':1512,'height':982},color_scheme='dark',timezone_id='Europe/Amsterdam')
  page.on('pageerror',lambda error:errors.append(str(error)))
  async def instrument(route):
   response=await route.fetch();source=await response.text()
   source=source.replace('this.layer={id:"weather-v6"','window.__weatherEngine=this,this.layer={id:"weather-v6"')
   await route.fulfill(response=response,body=source)
  await page.route('**/assets/main-*.js',instrument)
  await page.goto('http://127.0.0.1:5198/nov-mar')
  await page.wait_for_function('window.__weatherEngine?.data && !window.__weatherEngine.loading',timeout=120000)
  for name,period in PERIODS:
   await page.evaluate('(period)=>window.__weatherEngine.setPeriod(period)',period)
   rows.extend(await page.evaluate(PROBE,{'places':PLACES,'periodName':name}))
  await browser.close()
 summary={'browser':args.browser,'comparisons':len(rows),'maximumAbsoluteDifference':max(abs(row['difference'])for row in rows),'withinHalfScorePoint':all(abs(row['difference'])<.5 for row in rows),'sameBand':all(row['mapBand']==row['cardBand']for row in rows),'previewOverridesLiveRecords':any(row['previewStillOverrides']for row in rows),'errors':errors}
 (out/f'map-card-{args.browser}.json').write_text(json.dumps({'summary':summary,'rows':rows},indent=2))
 with (out/f'map-card-{args.browser}.csv').open('w')as f:
  writer=csv.DictWriter(f,fieldnames=rows[0].keys());writer.writeheader();writer.writerows(rows)
 print(json.dumps(summary,indent=2))
 assert summary['withinHalfScorePoint'] and summary['sameBand'] and not errors,summary

if __name__=='__main__':
 parser=argparse.ArgumentParser();parser.add_argument('--out',required=True);parser.add_argument('--browser',choices=['chromium','webkit'],default='chromium');asyncio.run(main(parser.parse_args()))
