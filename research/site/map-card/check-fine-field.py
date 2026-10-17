# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Exercise production fine-place transport and the batched native-pixel draw.
Run after make-fine-fixture.mjs with headless Playwright against port 5198.
"""
import argparse,asyncio,csv,json
from pathlib import Path
from playwright.async_api import async_playwright

PROBE=r'''(places)=>{
const e=window.__weatherEngine,live=e.gl;
const shaders=live.getAttachedShaders(e.programs.finePixel),source=type=>live.getShaderSource(shaders.find(shader=>live.getShaderParameter(shader,live.SHADER_TYPE)===type));
const vertex=source(live.VERTEX_SHADER),original=source(live.FRAGMENT_SHADER),fragment=original.replace('fragColour = vec4(colour.rgb + u_land_colour.rgb * (1.0 - colour.a), 1.0);','fragColour = vec4(v_score, 0.0, 0.0, 1.0);');
if(fragment===original)throw Error('Could not instrument actual native-pixel fragment');
const previous=live.getParameter(live.ARRAY_BUFFER_BINDING),vertices=new Float32Array(e.finePixelCount*3);live.bindBuffer(live.ARRAY_BUFFER,e.finePixelBuffer);live.getBufferSubData(live.ARRAY_BUFFER,0,vertices);live.bindBuffer(live.ARRAY_BUFFER,previous);
const gl=new OffscreenCanvas(257,257).getContext('webgl2');if(!gl.getExtension('EXT_color_buffer_float'))throw Error('No float render targets');
const program=gl.createProgram();for(const [type,text]of[[gl.VERTEX_SHADER,vertex],[gl.FRAGMENT_SHADER,fragment]]){const shader=gl.createShader(type);gl.shaderSource(shader,text);gl.compileShader(shader);if(!gl.getShaderParameter(shader,gl.COMPILE_STATUS))throw Error(gl.getShaderInfoLog(shader));gl.attachShader(program,shader);gl.deleteShader(shader);}gl.bindAttribLocation(program,0,'a_position');gl.bindAttribLocation(program,1,'a_score');gl.linkProgram(program);if(!gl.getProgramParameter(program,gl.LINK_STATUS))throw Error(gl.getProgramInfoLog(program));
const buffer=gl.createBuffer();gl.bindBuffer(gl.ARRAY_BUFFER,buffer);gl.bufferData(gl.ARRAY_BUFFER,vertices,gl.STATIC_DRAW);gl.enableVertexAttribArray(0);gl.vertexAttribPointer(0,2,gl.FLOAT,false,12,0);gl.enableVertexAttribArray(1);gl.vertexAttribPointer(1,1,gl.FLOAT,false,12,8);
const texture=gl.createTexture();gl.bindTexture(gl.TEXTURE_2D,texture);gl.texImage2D(gl.TEXTURE_2D,0,gl.RGBA32F,257,257,0,gl.RGBA,gl.FLOAT,null);const framebuffer=gl.createFramebuffer();gl.bindFramebuffer(gl.FRAMEBUFFER,framebuffer);gl.framebufferTexture2D(gl.FRAMEBUFFER,gl.COLOR_ATTACHMENT0,gl.TEXTURE_2D,texture,0);gl.useProgram(program);gl.viewport(0,0,257,257);gl.uniform1f(gl.getUniformLocation(program,'u_world_offset'),0);
// Read the actual containing cell at its centre. GPU edge rasterisation of a one-kilometre cell is subpixel at map overview scales; containment of the original place is checked independently.
const rows=places.map(place=>{const {latitude,longitude}=place,card=e.judgePoint(latitude,longitude),pixelLatitude=90-(place.pixel.row+.5)/120,pixelLongitude=-180+(place.pixel.column+.5)/120,x=(pixelLongitude+180)/360,y=(1-Math.asinh(Math.tan(pixelLatitude*Math.PI/180))/Math.PI)/2,scale=43200;const matrix=new Float32Array([scale,0,0,0,0,scale,0,0,0,0,1,0,-x*scale,-y*scale,0,1]);gl.uniformMatrix4fv(gl.getUniformLocation(program,'u_matrix'),false,matrix);gl.clearColor(-1,0,0,0);gl.clear(gl.COLOR_BUFFER_BIT);gl.drawArrays(gl.TRIANGLES,0,e.finePixelCount);const result=new Float32Array(4);gl.readPixels(128,128,1,1,gl.RGBA,gl.FLOAT,result);return {place:place.name,latitude,longitude,pixelRow:place.pixel.row,pixelColumn:place.pixel.column,pixelContainsPlace:Math.floor((90-latitude)*120)===place.pixel.row&&Math.floor((longitude+180)*120)===place.pixel.column,mapScore:result[0],cardScore:card.meanScore,difference:result[0]-card.meanScore,covered:result[3]===1,cardBand:card.band,niceDays:card.niceDayCount,totalDays:card.totalDays};});gl.getExtension('WEBGL_lose_context')?.loseContext();return rows;
}'''
PERIODS=[('November to March',{'kind':'months','startMonth':11,'endMonth':3}),('Whole year',{'kind':'year'}),('July',{'kind':'month','month':7})]
async def main(args):
 folder=Path(args.fixture);places=json.loads((folder/'index.json').read_text());out=Path(args.out);out.mkdir(parents=True,exist_ok=True);errors=[];rows=[]
 async with async_playwright()as p:
  browser=await getattr(p,args.browser).launch(headless=True);page=await browser.new_page(viewport={'width':1512,'height':982},color_scheme='dark')
  page.on('pageerror',lambda error:errors.append(str(error)))
  async def instrument(route):
   r=await route.fetch();s=(await r.text()).replace('this.layer={id:"weather-v6"','window.__weatherEngine=this,this.layer={id:"weather-v6"');await route.fulfill(response=r,body=s)
  async def fine(route):
   from urllib.parse import urlparse,parse_qs
   params=parse_qs(urlparse(route.request.url).query)
   if'lat'not in params:await route.fulfill(json={'enabled':False});return
   lat=float(params['lat'][0]);lon=float(params['lon'][0]);place=next((place for place in places if abs(place['latitude']-lat)<1e-8 and abs(place['longitude']-lon)<1e-8),None)
   if place is None:await route.fulfill(status=404);return
   await route.fulfill(body=(folder/place['filename']).read_bytes(),headers={'Content-Type':'application/octet-stream','X-Fine-Generation':place['generation'],'X-Fine-Evening-Model':place['eveningModel']})
  await page.route('**/assets/main-*.js',instrument);await page.route('**/api/fine/replay*',fine)
  await page.goto('http://127.0.0.1:5198/nov-mar');await page.wait_for_function('window.__weatherEngine?.data&&!window.__weatherEngine.loading',timeout=120000)
  await page.evaluate('''()=>{const e=window.__weatherEngine;e.options.onRedraw=undefined;e.options.onLoadingChange=undefined;e.finePlaces.availability='active';}''')
  for name,period in PERIODS:
   await page.evaluate('(period)=>window.__weatherEngine.setPeriod(period)',period)
   await page.evaluate('(places)=>{for(const p of places)window.__weatherEngine.judgePoint(p.latitude,p.longitude)}',places)
   await page.wait_for_function('(places)=>places.every(p=>window.__weatherEngine.judgePoint(p.latitude,p.longitude)!==null)',arg=places,timeout=120000)
   await page.evaluate('()=>window.__weatherEngine.requestFrame()')
   actual=await page.evaluate(PROBE,places)
   for row in actual:row['period']=name
   rows.extend(actual)
  await page.screenshot(path=str(out/f'fine-native-pixels-{args.browser}.png'))
  await browser.close()
 summary={'browser':args.browser,'places':len(places),'comparisons':len(rows),'maximumAbsoluteDifference':max(abs(row['difference'])for row in rows),'everyPlaceCoveredByItsNativePixel':all(row['covered']and row['pixelContainsPlace']for row in rows),'errors':errors}
 (out/f'fine-map-card-{args.browser}.json').write_text(json.dumps({'summary':summary,'rows':rows},indent=2))
 with(out/f'fine-map-card-{args.browser}.csv').open('w')as f:writer=csv.DictWriter(f,fieldnames=rows[0].keys());writer.writeheader();writer.writerows(rows)
 print(json.dumps(summary,indent=2));assert summary['everyPlaceCoveredByItsNativePixel']and summary['maximumAbsoluteDifference']<.001 and not errors,summary
if __name__=='__main__':
 parser=argparse.ArgumentParser();parser.add_argument('--fixture',required=True);parser.add_argument('--out',required=True);parser.add_argument('--browser',choices=['chromium','webkit'],default='chromium');asyncio.run(main(parser.parse_args()))
