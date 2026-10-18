// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Run the real Worker against measured sparse file membership, without writing a release.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import zlib from 'node:zlib';
import crypto from 'node:crypto';
import {handleRequest} from '../../hosting/sites/worker.mjs';
import {finePixel,nativeFineIndex} from '../../hosting/sites/fine-place.mjs';

const [release,measurements,queriesFile,output]=process.argv.slice(2);
if(!output)throw Error('Usage: node named_subset_fallback.mjs <release> <measurement-directory> <queries.json> <report.json>');
const subset='all-labels-plus-places-pop-50000';
const allowed=new Set(JSON.parse(zlib.gunzipSync(await fs.readFile(path.join(measurements,`${subset}.files.json.gz`)))).map(entry=>entry.path));
const prefix=path.basename(release);
const measurement=JSON.parse(await fs.readFile(path.join(measurements,'measurement.json')));
let catalogueCoordinates=[];
for(const level of [1,2])catalogueCoordinates=catalogueCoordinates.concat(JSON.parse(await fs.readFile(path.join(release,`labels/labels-${level}.json`))).map(entry=>[entry[2],entry[3]]));
catalogueCoordinates=catalogueCoordinates.concat((await fs.readFile('artifacts/places.tsv','utf8')).trim().split('\n').map(line=>{const fields=line.split('\t');return [Number(fields[3]),Number(fields[4])];}));
const catalogueAddressSha256=crypto.createHash('sha256').update(catalogueCoordinates.map(([latitude,longitude])=>{
  const pixel=finePixel(latitude,longitude),native=nativeFineIndex(90-(pixel.row+.5)/120,-180+(pixel.column+.5)/120);
  return `${pixel.row},${pixel.column},${native}\n`;
}).join('')).digest('hex');
assert.equal(catalogueAddressSha256,measurement.catalogueAddressSha256,'Python measurement addresses differ from production JavaScript');
const nativePoints=new Set([...allowed].filter(key=>key.startsWith('replay/native/')).map(key=>Number(key.split('/')[2])));
const configuration={nativeRoot:`${prefix}/replay`,tileRoot:`${prefix}/temperature`,labelsRoot:`${prefix}/labels`,attribution:`${prefix}/ATTRIBUTION.txt`,generation:JSON.parse(await fs.readFile(path.join(release,'replay/manifest.json'))).generation,eveningModel:'uniform',status:'local-measurement-only'};
const queries=JSON.parse(await fs.readFile(queriesFile));
const candidate=queries.find(query=>{
  const pixel=finePixel(query.lat,query.lon);
  const native=nativeFineIndex(90-(pixel.row+.5)/120,-180+(pixel.column+.5)/120);
  return query.query_kind!=='app_city_search'&&!nativePoints.has(native);
});
assert(candidate,'No validation coordinate outside measured native coverage');
const reads=[];
function worker(state){
  const bucket={async get(key,options){
    let bytes,size;
    if(key==='v6/fine/current.json'){
      if(state==='inactive')return null;
      bytes=Buffer.from(JSON.stringify(configuration));size=bytes.length;
    }else{
      const relative=key.startsWith(`${prefix}/`)?key.slice(prefix.length+1):null;
      if(relative&&state==='sparse'&&!allowed.has(relative)){reads.push({state,key,missing:true});return null;}
      const file=relative?path.join(release,relative):path.resolve('artifacts',key);
      let handle;
      try{
        handle=await fs.open(file,'r');size=(await handle.stat()).size;
        if(options?.range){bytes=Buffer.alloc(Math.max(0,Math.min(options.range.length,size-options.range.offset)));await handle.read(bytes,0,bytes.length,options.range.offset);}
        else bytes=await handle.readFile();
      }catch(error){if(error.code==='ENOENT')return null;throw error;}finally{await handle?.close();}
    }
    return {size,body:new Blob([bytes]).stream(),arrayBuffer:async()=>bytes.buffer.slice(bytes.byteOffset,bytes.byteOffset+bytes.byteLength),text:async()=>bytes.toString('utf8'),json:async()=>JSON.parse(bytes.toString('utf8'))};
  }};
  return async pathname=>handleRequest(new Request(`http://127.0.0.1:5198${pathname}`),{WEATHER:bucket},async()=>new Response('fixture asset'));
}
const states={};
for(const state of ['inactive','full','sparse']){
  const request=worker(state),point=`lat=${candidate.lat}&lon=${candidate.lon}`;
  const response=await request(`/api/place?${point}&period=july`),api=await response.json();
  const replay=await request(`/api/fine/replay?${point}`);
  states[state]={status:response.status,replayStatus:replay.status,months:api.months,fineReplay:api.fineReplay??false,resolutionArcSeconds:api.resolutionArcSeconds??null,judgement:api.judgement??null};
}
assert.equal(states.inactive.status,200);assert.equal(states.full.status,200);assert.equal(states.sparse.status,200);
assert(Array.isArray(states.inactive.months)&&states.inactive.months.length===12,'Coordinate has no current-product facts');
assert(Array.isArray(states.full.months)&&states.full.months.length===12,'Full fine release has no facts');
assert.equal(states.full.replayStatus,200);assert.equal(states.sparse.replayStatus,404);
const fallback=Array.isArray(states.sparse.months)&&states.sparse.months.length===12&&JSON.stringify(states.sparse.months)===JSON.stringify(states.inactive.months);
const report={written_by:'AI, GPT-6.1 Sol, 2026.10.18',measuredUTC:new Date().toISOString(),passes:true,
  definition:'Real handleRequest /api/place and /api/fine/replay against actual files. Sparse state hides exactly files absent from measured all-labels-plus-places-pop-50000 union; no files copied, release sealed or data uploaded.',
  subset,coordinate:candidate,catalogueAddressParity:{rows:catalogueCoordinates.length,sha256:catalogueAddressSha256,passes:true},clean25kmFallback:fallback,states,missingReads:reads,
  conclusion:fallback?'Sparse files fall back to current facts.':'Sparse active release DOES NOT fall back: months and judgement are null, replay returns 404, while API still advertises fineReplay=true and 30 arc-seconds. Inactive pointer returns twelve current-product months. App active-fine judgePoint likewise returns FinePlaces null instead of PointSampler.'};
await fs.writeFile(output,JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify({passes:true,clean25kmFallback:fallback,coordinate:{lat:candidate.lat,lon:candidate.lon},states:Object.fromEntries(Object.entries(states).map(([state,value])=>[state,{status:value.status,replayStatus:value.replayStatus,months:value.months?.length??null,fineReplay:value.fineReplay,score:value.judgement?.score??null}]))}));
