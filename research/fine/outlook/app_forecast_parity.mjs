// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Measure actual browser sampling against the Worker/shared forecast function.
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import { pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';
import { build } from 'esbuild';
const { values } = parseArgs({options:{data:{type:'string'},queries:{type:'string'},output:{type:'string'}}});
for (const key of ['data','queries','output']) if (!values[key]) throw new Error(`Missing --${key}`);
const root=path.resolve(new URL('../../../',import.meta.url).pathname),hosting=path.join(root,'hosting/sites');
const sha=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
async function bundled(file,append='') {
  const source=await fs.readFile(path.join(root,file),'utf8');
  const result=await build({stdin:{contents:source+append,resolveDir:path.dirname(path.join(root,file)),sourcefile:path.basename(file),loader:'ts'},bundle:true,platform:'node',format:'esm',write:false});
  return import(`data:text/javascript;base64,${Buffer.from(result.outputFiles[0].text).toString('base64')}`);
}
const [{PointSampler},{nearestLandOrdinals},{DEFAULT_TASTE},{placeNumbers,blendPlaceNumbers}]=await Promise.all([
  bundled('src/engine/sampling.ts'),bundled('src/engine/data.ts','\nexport {nearestLandOrdinals};'),bundled('src/engine/taste.ts'),import(pathToFileURL(path.join(hosting,'fine-place.mjs')).href)]);
let worker=await fs.readFile(path.join(hosting,'worker.mjs'),'utf8');
worker=worker.replace(/from '\.\/([^']+)'/g,(_,name)=>`from '${pathToFileURL(path.join(hosting,name)).href}'`)
 .replace("numbers: placeNumbers({source: 'forecast'","numbers: captureNumbers({source: 'forecast'");
worker+='\nexport {defaultPeriodJudgement,monthlyOffsets};export const capturedPackets=[];function captureNumbers(packet,period){capturedPackets.push(packet);return placeNumbers(packet,period);}\n';
const shared=await import(`data:text/javascript;base64,${Buffer.from(worker).toString('base64')}`),consumed=new Map();
const bucket={async get(key,options){
 if(key==='v6/fine/current.json')return null;
 if(!consumed.has(key))try{consumed.set(key,await fs.readFile(path.join(values.data,key)));}catch(error){if(error.code==='ENOENT')return null;throw error;}
 const all=consumed.get(key),range=options?.range,bytes=range?all.subarray(range.offset,range.offset+range.length):all;
 return {arrayBuffer:async()=>bytes.buffer.slice(bytes.byteOffset,bytes.byteOffset+bytes.length),json:async()=>JSON.parse(bytes.toString()),text:async()=>bytes.toString()};
}};
const pointer=await(await bucket.get('v6/forecast/current.json')).json(),cellBytes=await fs.readFile(path.join(values.data,'v6/cells.bin'));
const cells={count:cellBytes.length/4,ordinals:nearestLandOrdinals(new Uint16Array(cellBytes.buffer,cellBytes.byteOffset,cellBytes.length/2))};
const records={bytes:new Uint8Array(await(await bucket.get(`v6/forecast/${pointer.run}/days.bin`)).arrayBuffer()),samples:14};
const queries=(await fs.stat(values.queries)).isDirectory()
 ? (await Promise.all(['world','europe','usa'].map(async region=>JSON.parse(await fs.readFile(path.join(values.queries,`${region}.json`)))))).flatMap(poster=>poster.places.map(place=>({slug:place.slug,lat:place.latitude,lon:place.longitude})))
 : JSON.parse(await fs.readFile(values.queries));
const fields=['meanScore','niceDayCount','perfectDayCount','awfulDayCount','niceDayShare','perfectDayShare','awfulDayShare','totalDays','sampleDays',...['warmth','evening','rainFactor','windFactor','humidityFactor','sunFactor'].map(name=>`parts.${name}`),...['feelsInSunAt14C','feelsInShadeAt15C','feelsInShadeAt21C','dewPointC','rainMmPerDay','wetDayShare','windKmh','sunnyShare','daylightHours'].map(name=>`typical.${name}`)];
const read=(object,name)=>name.split('.').reduce((value,key)=>value?.[key],object);
const rows=[],inputs=[],expected=[],max=Object.fromEntries(fields.map(field=>[field,0]));
for(const [word,period,workerPeriod]of [['tomorrow',{kind:'forecast-day',day:1},{kind:'forecast',day:1}],['next-14-days',{kind:'forecast'},{kind:'forecast'}]]) {
 const days=period.kind==='forecast-day'?[period.day]:Array.from({length:14},(_,day)=>day),groups=[];
 for(const day of days){const date=new Date(`${pointer.firstDate}T12:00:00Z`);date.setUTCDate(date.getUTCDate()+day);const month=date.getUTCMonth()+1,previous=groups.at(-1);
  if(previous?.month===month){previous.dayCount++;previous.weight+=1/days.length;}
  else groups.push({records,firstDay:day,dayCount:1,month,weight:1/days.length});}
 const offsets=new Map(await Promise.all([...new Set(groups.map(group=>group.month))].map(async month=>[month,await shared.monthlyOffsets(bucket,month)])));
 const data={groups,offsets,sampleDays:days.length,totalDays:days.length};
 const tastes=[['default',DEFAULT_TASTE],['custom',{warmShift:2,heatShift:1,eveningShift:2,humidityShift:-1,windShift:-3,rainScale:1.3}]];
 const samplers=tastes.map(([,taste])=>new PointSampler(cells,data,period,taste));
 for(const query of queries){
  shared.capturedPackets.length=0;
  const workerNumbers=await shared.defaultPeriodJudgement(bucket,query.lat,query.lon,workerPeriod);
  const packets=new Map(shared.capturedPackets.map(packet=>[packet.fineIndex,packet]));
  const {mapPointWeights}=await import(pathToFileURL(path.join(hosting,'fine-place.mjs')).href);
  const footprint=mapPointWeights(query.lat,query.lon,index=>offsets.values().every(month=>month[index]!==255));
  for(let tasteIndex=0;tasteIndex<tastes.length;tasteIndex++) {
   const [tasteName,taste]=tastes[tasteIndex],id=`${query.slug}/${word}/${tasteName}`,actual=samplers[tasteIndex].judge(query.lat,query.lon);
   const candidate=tasteName==='default'?workerNumbers:blendPlaceNumbers(footprint.map(point=>({numbers:placeNumbers(packets.get(point.index),period,taste),weight:point.weight})));
   if(actual&&candidate){const differences=Object.fromEntries(fields.map(field=>[field,read(actual,field)-read(candidate,field)]));for(const field of fields)max[field]=Math.max(max[field],Math.abs(differences[field]));
    rows.push({id,latitude:query.lat,longitude:query.lon,app:actual,shared:candidate,differences,bandDiffers:actual.band!==candidate.band});
    inputs.push({id,latitude:query.lat,longitude:query.lon,packet:{source:'forecast'},spatialPackets:[...packets].map(([index,packet])=>({index,packet:{...packet,records:packet.records.map(record=>Array.from(record))}})),taste,periods:[{totalDays:candidate.totalDays}]});
    expected.push({id,numbers:[candidate],months:null});
   }else rows.push({id,appAvailable:Boolean(actual),sharedAvailable:Boolean(candidate)});
  }
 }
}
await fs.mkdir(values.output,{recursive:true});
for(const [name,records]of [['input',inputs],['expected',expected]])await fs.writeFile(path.join(values.output,`${name}.jsonl`),records.map(JSON.stringify).join('\n')+'\n');
const compared=rows.filter(row=>row.differences),report={written_by:'AI, GPT-6.1 Sol, 2026.10.18',definition:'Actual current browser PointSampler including its B-spline footprint, compared with current Worker/shared spatial forecast judgement. Existing ECMWF forecast records; no fine climate correction. Default and non-default tastes; default exercises the actual Worker path, custom exercises the same shared spatial function on captured actual Worker packets.',queries:queries.length,compared:compared.length,passes:compared.every(row=>Object.values(row.differences).every(value=>Number.isFinite(value)&&Math.abs(value)<=1e-7))&&rows.every(row=>row.differences||row.appAvailable===row.sharedAvailable),unavailable:rows.length-compared.length,maximum_absolute_difference:max,scoreMismatches:compared.filter(row=>Math.abs(row.differences.meanScore)>1e-5).length,wholeScoreMismatches:compared.filter(row=>Math.floor(row.app.meanScore)!==Math.floor(row.shared.meanScore)).length,bandMismatches:compared.filter(row=>row.bandDiffers).length,source_sha256:Object.fromEntries(await Promise.all(['src/engine/sampling.ts','src/engine/data.ts','src/engine/taste.ts','hosting/sites/worker.mjs','hosting/sites/fine-place.mjs'].map(async name=>[name,sha(await fs.readFile(path.join(root,name)))]))),consumed:Object.fromEntries([...consumed].map(([key,bytes])=>[key,{bytes:bytes.length,sha256:sha(bytes)}])),rows};
await fs.writeFile(path.join(values.output,'app-forecast-parity.json'),JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify({...report,source_sha256:undefined,consumed:undefined,rows:undefined}));
