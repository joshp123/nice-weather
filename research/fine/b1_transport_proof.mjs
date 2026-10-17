// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Actual Worker transport against the unchanged direct exact-pixel loader.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import zlib from 'node:zlib';
import {createFinePlaceClient,createFinePlaceLoader,placeNumbers,DEFAULT_FINE_TASTE} from '../../hosting/sites/fine-place.mjs';
import {localWorkerFixture} from './local_worker_fixture.mjs';
const [data,overlay,queriesPath,output]=process.argv.slice(2);
if(!output)throw Error('Usage: node research/fine/b1_transport_proof.mjs <data-root> <overlay> <queries.json> <report.json>');
const started=performance.now(),sha=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
const queryBytes=await fs.readFile(queriesPath),queries=JSON.parse(queryBytes);
const pointerBytes=await fs.readFile(path.join(overlay,'v6/fine/current.json'));
const direct=createFinePlaceLoader(async key=> {
  if(key==='v6/fine/current.json')return new Uint8Array(pointerBytes);
  try{return new Uint8Array(await fs.readFile(path.join(data,key)));}catch(error){if(error.code==='ENOENT')return null;throw error;}
});
const request=localWorkerFixture(overlay),routeRows=[];
const client=createFinePlaceClient('http://127.0.0.1:5198/api/fine/replay',async url=> {
  const response=await request(url);
  if(new URL(url).searchParams.has('lat')&&response.ok) {
    const bytes=new Uint8Array(await response.arrayBuffer()),plain=zlib.gunzipSync(bytes);
    assert.equal(response.headers.get('Content-Encoding'),'gzip');
    assert.equal(response.headers.get('Cache-Control'),'public, max-age=60');
    routeRows.push({url,compressed_bytes:bytes.length,uncompressed_bytes:plain.length});
    return new Response(bytes,{status:response.status,headers:response.headers});
  }
  return response;
});
assert.equal(await client.enabled(),true);
const inactive=createFinePlaceClient('http://127.0.0.1:5198/api/fine/replay',localWorkerFixture(overlay,()=>null));
assert.equal(await inactive.enabled(),false);assert.equal(await inactive.load(32.6669,-16.9241),null);
for(const query of ['?lat=100&lon=0','?lat=1','?lon=1','?lat=nan&lon=0'])assert.equal((await request(`http://127.0.0.1:5198/api/fine/replay${query}`)).status,400);
const periods=[...Array.from({length:12},(_,i)=>({months:[i+1]})),{kind:'year'},...['Dec-Feb','Mar-May','Jun-Aug','Sep-Nov'].map(season=>({kind:'season',season}))];
const tastes=[DEFAULT_FINE_TASTE,{warmShift:1,heatShift:-1,eveningShift:2,humidityShift:1,windShift:-5,rainScale:.5}];
let numericValues=0,maximumDifference=0;
const compare=(actual,expected,label)=> {
  if(typeof actual==='number') {const difference=Math.abs(actual-expected);maximumDifference=Math.max(maximumDifference,difference);numericValues++;assert.equal(actual,expected,label);return;}
  if(actual&&typeof actual==='object') {for(const key of Object.keys(actual))compare(actual[key],expected[key],`${label}/${key}`);return;}
  assert.equal(actual,expected,label);
};
const rows=[];
for(const query of queries) {
  const expected=await direct.load(query.lat,query.lon),actual=await client.load(query.lat,query.lon);
  assert(expected&&actual,`Missing ${query.slug}`);
  for(const field of ['pixel','status','eveningModel','corrections'])compare(actual[field],expected[field],`${query.slug}/${field}`);
  for(const field of ['fineIndex','generation','targetTimezone','dates','selectedDateIndices','selectedIndices','highC','lowC','airC','dewC','sunC','shadeC','rainMm','windKmh','sunnyHours','daylightHours'])compare(actual.replay[field],expected.replay[field],`${query.slug}/${field}`);
  for(const period of periods)for(const taste of tastes)compare(placeNumbers(actual,period,taste),placeNumbers(expected,period,taste),`${query.slug}/${JSON.stringify(period)}`);
  rows.push({slug:query.slug,...routeRows.at(-1)});
}
const compressed=rows.map(row=>row.compressed_bytes).sort((a,b)=>a-b);
const sources={};
for(const filename of ['hosting/sites/fine-place.mjs','hosting/sites/fine-place.d.mts','hosting/sites/worker.mjs','src/engine/finePlaces.ts','research/fine/b1_transport_proof.mjs'])sources[filename]=sha(await fs.readFile(filename));
const report={written_by:'AI, GPT-6.1 Sol, 2026.10.18',passes:true,places:rows.length,numeric_values:numericValues,maximum_absolute_difference:maximumDifference,
 definition:'Actual gzip Worker /api/fine/replay response decoded by the production browser client versus direct createFinePlaceLoader. Exact weather, float64 corrections, pixel/generation/target clock/model and complete placeNumbers output for 17 periods and two tastes; inactive pointer and bad-coordinate paths exercised.',
 payload:{total_compressed_bytes:compressed.reduce((sum,n)=>sum+n,0),minimum_bytes:compressed[0],median_bytes:compressed[Math.floor(compressed.length/2)],maximum_bytes:compressed.at(-1),hypothetical_339_place_bytes:Math.ceil(compressed.reduce((sum,n)=>sum+n,0)/rows.length*339)},
 source_sha256:sources,input_sha256:{queries:sha(queryBytes),pointer:sha(pointerBytes)},seconds:(performance.now()-started)/1000,rows};
await fs.mkdir(path.dirname(output),{recursive:true});await fs.writeFile(output,JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify({...report,rows:undefined}));
