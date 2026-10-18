// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Exercise the actual Worker and browser transport against the full packed tables.
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import {handleRequest} from '../../hosting/sites/worker.mjs';
import {createNamedPlaceClient,namedPlaceNumbers,namedUncompressed,decodeNamedTable} from '../../hosting/sites/named-place.mjs';
const [overlay,queriesPath,output,labelsDirectory,forecastRoot]=process.argv.slice(2);
if(!output)throw Error('Usage: named_view_transport_proof.mjs <overlay> <queries.json> <report.json> [labels-directory|-] [forecast-root]');
const repo=process.cwd();
const read=async key=>{for(const root of [...(forecastRoot&&/^v6\/forecast\/[^/]+\/named\.bin\.gz$/.test(key)?[forecastRoot]:[]),overlay,repo+'/artifacts'])try{return new Uint8Array(await fs.readFile(path.join(root,key)));}catch(error){if(error.code!=='ENOENT')throw error;}return null;};
let enabled=true,failBatch=false;const bucket={async get(key){if(!enabled&&key==='v6/named/current.json')return null;const bytes=await read(key);if(!bytes)return null;return {arrayBuffer:async()=>bytes.buffer.slice(bytes.byteOffset,bytes.byteOffset+bytes.byteLength),json:async()=>JSON.parse(new TextDecoder().decode(bytes)),text:async()=>new TextDecoder().decode(bytes),body:new Blob([bytes]).stream(),size:bytes.length};}};
const inactiveBucket={get:async key=>key==='v6/named/current.json'?null:bucket.get(key)};
const requests=[];
globalThis.fetch=async(input,init)=>{const request=new Request(new URL(input,'https://nice.jjpcodes.com'),init),started=performance.now();if(failBatch&&new URL(request.url).pathname.endsWith('/points'))return new Response('Unavailable',{status:503});const response=await handleRequest(request,{WEATHER:enabled?bucket:inactiveBucket},()=>null);const bytes=new Uint8Array(await response.arrayBuffer());requests.push({path:new URL(request.url).pathname,method:request.method,bytes:bytes.length,ms:performance.now()-started});return new Response(bytes,{status:response.status,headers:response.headers});};
const configuration=JSON.parse(new TextDecoder().decode(await read('v6/named/current.json'))),forecastMeta=JSON.parse(new TextDecoder().decode(await read('v6/forecast/current.json')));
const fullTables={climate:decodeNamedTable(await namedUncompressed(await read(configuration.root+'/climate.bin.gz'))),forecast:decodeNamedTable(await namedUncompressed(await read(`v6/forecast/${forecastMeta.run}/named.bin.gz`)))};
const lookups=Object.fromEntries(Object.entries(fullTables).map(([kind,table])=>[kind,new Map(Array.from({length:table.count},(_,index)=>[`${table.view.getInt32(32+index*8,true)},${table.view.getInt32(36+index*8,true)}`,index]))]));
const client=createNamedPlaceClient('https://nice.jjpcodes.com/api/named');await client.ready();assert.equal(requests.length,1);assert.equal(client.enabled,true);
const queries=JSON.parse(await fs.readFile(queriesPath));
const points=queries.map(q=>({latitude:q.lat,longitude:q.lon}));points.push({latitude:-25,longitude:135});
let comparisons=0;
for(const kind of ['climate','forecast']) {
  const period={kind:kind==='climate'?'year':'forecast'};
  assert(client.pending(points[0].latitude,points[0].longitude,period));
  const first=client.ensure(points,period),duplicate=client.ensure(points,period);await Promise.all([first,duplicate]);
  for(const point of points) {
    assert.equal(client.pending(point.latitude,point.longitude,period),false);
    const index=lookups[kind].get(`${Math.round(point.latitude*1e5)},${Math.round(point.longitude*1e5)}`),table=fullTables[kind];
    const full=index===undefined?null:kind==='climate'?{climate:table,climateIndex:index}:{forecast:table,forecastIndex:index};
    const sliced=client.find(point.latitude,point.longitude,period);
    for(const period of kind==='climate'?[...Array.from({length:12},(_,i)=>({kind:'month',month:i+1})),{kind:'months',startMonth:11,endMonth:3},{kind:'year'}]:[...Array.from({length:14},(_,day)=>({kind:'forecast-day',day})),{kind:'forecast'}]) {
      const expected=full&&namedPlaceNumbers(full,period),actual=sliced&&namedPlaceNumbers(sliced,period);
      assert.equal(Boolean(actual),Boolean(expected));if(!actual)continue;
      for(const field of ['score','niceDayCount','perfectDayCount','awfulDayCount']){assert.equal(actual[field],expected[field]);comparisons++;}
    }
  }
}
const batchRequests=requests.filter(r=>r.path.endsWith('/points'));assert.equal(batchRequests.length,2,'Duplicate ensures issued duplicate requests');
const bad=await fetch('https://nice.jjpcodes.com/api/named/points',{method:'POST',body:JSON.stringify({kind:'climate',points:[[91,0]]})});assert.equal(bad.status,400);
let worldSummary;
if(labelsDirectory&&labelsDirectory!=='-') {
  const labels=[...JSON.parse(await fs.readFile(path.join(labelsDirectory,'labels-1.json'))),...JSON.parse(await fs.readFile(path.join(labelsDirectory,'labels-2.json')))];
  const world=labels.filter(row=>row[4]>=300000).map(row=>({latitude:row[2],longitude:row[3]}));
  const worldClient=createNamedPlaceClient('https://nice.jjpcodes.com/api/named');await worldClient.ready();
  const worldRequests=[];
  for(const kind of ['year','forecast']){await worldClient.ensure(world,{kind});worldRequests.push(requests.at(-1));}
  worldSummary={candidates:world.length,requests:worldRequests};
}
const failedClient=createNamedPlaceClient('https://nice.jjpcodes.com/api/named');await failedClient.ready();failBatch=true;
await assert.rejects(failedClient.ensure(points,{kind:'year'}));assert.equal(failedClient.pending(points[0].latitude,points[0].longitude,{kind:'year'}),false);failBatch=false;
enabled=false;const inactiveClient=createNamedPlaceClient('https://nice.jjpcodes.com/api/named');await inactiveClient.ready();assert.equal(inactiveClient.enabled,false);await inactiveClient.ensure(points,{kind:'year'});assert.equal(inactiveClient.pending(points[0].latitude,points[0].longitude,{kind:'year'}),false);
const report={written_by:'AI, GPT-6.1 Sol, 2026.10.18',timing:'Local Mac Worker fixture, including SSD reads; not live latency',passes:true,points:queries.length,exactScoreCountComparisons:comparisons,batchRequests,worldHeadline:worldSummary,noWholeDataRequest:requests.every(r=>!r.path.startsWith('/data/')),sources:Object.fromEntries(await Promise.all(['hosting/sites/named-place.mjs','hosting/sites/named-place.d.mts','hosting/sites/worker.mjs','research/fine/named_view_transport_proof.mjs'].map(async name=>[name,crypto.createHash('sha256').update(await fs.readFile(path.join(repo,name))).digest('hex')])))};
await fs.mkdir(path.dirname(output),{recursive:true});await fs.writeFile(output,JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify(report,null,2));
