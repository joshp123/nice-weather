# Written by AI on 2026.10.26 by model GPT-6.
# Run: devenv shell -- uv run --with playwright python -I research/site/island-whiteouts/check-fine-map.py
import json
from pathlib import Path
import subprocess
from playwright.sync_api import sync_playwright
payload=json.loads(subprocess.check_output(['node',str(Path(__file__).with_suffix('.mjs'))],text=True))
with sync_playwright() as p:
 browser=p.chromium.launch(headless=True)
 page=browser.new_page()
 page.set_content('<canvas width="1" height="1"></canvas>')
 result=page.evaluate('''input=>{
 const gl=document.querySelector('canvas').getContext('webgl2');
 const shader=(type,source)=>{const s=gl.createShader(type);gl.shaderSource(s,source);gl.compileShader(s);if(!gl.getShaderParameter(s,gl.COMPILE_STATUS))throw Error(gl.getShaderInfoLog(s));return s;};
 const program=gl.createProgram();
 gl.attachShader(program,shader(gl.VERTEX_SHADER,`#version 300 es
 precision highp float;
 in vec2 a_position;uniform vec2 u_sample;out vec2 v_mercator;
 void main(){v_mercator=u_sample;gl_Position=vec4(a_position,0.,1.);}`));
 gl.attachShader(program,shader(gl.FRAGMENT_SHADER,input.fragment));
 gl.bindAttribLocation(program,0,'a_position');gl.linkProgram(program);if(!gl.getProgramParameter(program,gl.LINK_STATUS))throw Error(gl.getProgramInfoLog(program));gl.useProgram(program);
 const buffer=gl.createBuffer();gl.bindBuffer(gl.ARRAY_BUFFER,buffer);gl.bufferData(gl.ARRAY_BUFFER,new Float32Array([-1,-1,3,-1,-1,3]),gl.STATIC_DRAW);gl.enableVertexAttribArray(0);gl.vertexAttribPointer(0,2,gl.FLOAT,false,0,0);
 const texture=gl.createTexture();gl.bindTexture(gl.TEXTURE_2D,texture);gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_MIN_FILTER,gl.NEAREST);gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_MAG_FILTER,gl.NEAREST);
 gl.uniform1i(gl.getUniformLocation(program,'u_scores'),0);
 gl.uniform2iv(gl.getUniformLocation(program,'u_pixel_origin'),input.pixelOrigin);
 const y=(1-Math.log(Math.tan(Math.PI/4+input.latitude*Math.PI/360))/Math.PI)/2;
 gl.uniform2f(gl.getUniformLocation(program,'u_sample'),(input.longitude+180)/360,y);
 gl.uniform4fv(gl.getUniformLocation(program,'u_band_colours'),new Float32Array(Array.from({length:6},()=>[.5,0,0,.5]).flat()));
 gl.uniform4f(gl.getUniformLocation(program,'u_land_colour'),1,1,1,1);gl.uniform1f(gl.getUniformLocation(program,'u_edge_pixels'),1);
 gl.enable(gl.BLEND);gl.blendFunc(gl.ONE,gl.ONE_MINUS_SRC_ALPHA);
 const sample=scores=>{gl.texImage2D(gl.TEXTURE_2D,0,gl.R32F,input.size,input.size,0,gl.RED,gl.FLOAT,scores);gl.clearColor(0,.25,.5,1);gl.clear(gl.COLOR_BUFFER_BIT);gl.drawArrays(gl.TRIANGLES,0,3);const pixel=new Uint8Array(4);gl.readPixels(0,0,1,1,gl.RGBA,gl.UNSIGNED_BYTE,pixel);return Array.from(pixel);};
 const scores=new Float32Array(input.size*input.size).fill(255);for(const [pixel,score] of input.validPixels)scores[pixel]=score;
 const complete=sample(scores);scores[input.first]=255;const partial=sample(scores);scores.fill(255);const absent=sample(scores);
 if(complete.join(',')!=='255,128,128,255')throw Error('Unexpected opaque fine overlay '+complete);
 for(const [name,value] of [['partial',partial],['absent',absent]])if(value.join(',')!=='0,64,128,255')throw Error(name+' changed underlying weather: '+value);
 return {cpu:input.cpu,complete,partial,absent,renderer:gl.getParameter(gl.RENDERER)};
}''',payload)
 print(json.dumps(result))
 browser.close()
