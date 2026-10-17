// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// A read-only local Sites bucket for headless checks without changing another worker's server.
import fs from 'node:fs/promises';
import path from 'node:path';
import {handleRequest} from '../../hosting/sites/worker.mjs';

export function localWorkerFixture(overlay, finePointer) {
  const artifacts=path.resolve('artifacts'),dist=path.resolve('dist');
  const media={'.html':'text/html; charset=utf-8','.js':'text/javascript','.css':'text/css','.png':'image/png','.svg':'image/svg+xml'};
  const bucket={async get(key,options) {
    let bytes;
    if(key==='v6/fine/current.json'&&finePointer) {
      const value=finePointer();
      if(value===null)return null;
      bytes=Buffer.from(JSON.stringify(value));
    }
    for(const root of bytes?[]:[overlay,artifacts]) {
      try {bytes=await fs.readFile(path.join(root,key));break;}catch(error){if(error.code!=='ENOENT')throw error;}
    }
    if(!bytes)return null;
    const size=bytes.length;
    if(options?.range)bytes=bytes.subarray(options.range.offset,options.range.offset+options.range.length);
    return {size,httpEtag:`"${size}"`,body:new Blob([bytes]).stream(),
      arrayBuffer:async()=>bytes.buffer.slice(bytes.byteOffset,bytes.byteOffset+bytes.byteLength),
      text:async()=>bytes.toString('utf8'),json:async()=>JSON.parse(bytes.toString('utf8'))};
  },async head(key) {
    const object=await this.get(key);
    return object?{size:object.size,httpEtag:object.httpEtag}:null;
  }};
  const serveAsset=async pathname=> {
    const candidate=path.extname(pathname)?pathname.slice(1):pathname==='/'?'index.html':`${pathname.slice(1)}.html`;
    let name=candidate,bytes;
    try {bytes=await fs.readFile(path.join(dist,name));}catch(error) {
      if(error.code!=='ENOENT')throw error;
      name='index.html';bytes=await fs.readFile(path.join(dist,name));
    }
    return new Response(bytes,{headers:{'Content-Type':media[path.extname(name)]??'application/octet-stream'}});
  };
  return url=>handleRequest(new Request(url),{WEATHER:bucket},serveAsset);
}
