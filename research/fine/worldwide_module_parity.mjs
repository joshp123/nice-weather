// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// The served compact carrier versus independently rebuilt original hourly weather.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import zlib from 'node:zlib';
import {parseArgs} from 'node:util';
import {createFinePlaceLoader,placeMonths,placeNumbers} from '../../hosting/sites/fine-place.mjs';

const {values}=parseArgs({options:{data:{type:'string'},pointer:{type:'string'},queries:{type:'string'},oracle:{type:'string'},output:{type:'string'}}});
for(const name of ['data','pointer','queries','oracle','output'])if(!values[name])throw new Error(`Missing --${name}`);
const started=performance.now(),sha=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
const pointerBytes=await fs.readFile(values.pointer),consumed=new Map();
const reader=async key=> {
  if(key==='v6/fine/current.json')return new Uint8Array(pointerBytes);
  try {const bytes=await fs.readFile(path.join(values.data,key));consumed.set(key,{bytes:bytes.length,sha256:sha(bytes)});return new Uint8Array(bytes);}
  catch(error){if(error.code==='ENOENT')return null;throw error;}
};
const queryBytes=await fs.readFile(values.queries),queries=JSON.parse(queryBytes),queryBySlug=new Map(queries.map(query=>[query.slug,query]));
const manifestBytes=await fs.readFile(path.join(values.oracle,'manifest.json')),oracle=JSON.parse(manifestBytes);
const cases=oracle.cases.filter(record=>queryBySlug.has(record.query.slug));
const loader=createFinePlaceLoader(reader),rows=[],maximumDifference={},inputOracles=[];
const periods=[...Array.from({length:12},(_,index)=>({months:[index+1]})),{months:Array.from({length:12},(_,index)=>index+1)},...[ [12,1,2],[3,4,5],[6,7,8],[9,10,11] ].map(months=>({months}))];
const physicalFields=['highC','feelsAt21C','dewPointC','wetDaysPercent','sunnyPercent','windKmh','score','niceDayShare'];
const scoreFields=['score','niceDayCount','perfectDayCount','awfulDayCount','sampleDays'];
let comparisons=0,targetClockVariants=0,maximumRetainedBytes=0;
const compare=(actual,expected,key)=> {
  const difference=Math.abs(actual-expected);assert(Number.isFinite(difference),`Nonfinite ${key}`);
  const field=key.split('/').at(-1);maximumDifference[field]=Math.max(maximumDifference[field]??0,difference);comparisons++;
  assert(difference<=1e-5,`${key}: compact=${actual}, original-hourly=${expected}`);
  return difference;
};
// Exercise the cold multi-label path, rather than only sequential place requests.
await Promise.all(cases.slice(0,20).map(async record=>{const packet=await loader.load(record.query.lat,record.query.lon);assert(packet,`Missing concurrent place ${record.query.slug}`);}));
for(const record of cases) {
  const query=record.query,packet=await loader.load(query.lat,query.lon);assert(packet,`No actual compact packet for ${query.slug}`);
  const filename=path.join(values.oracle,record.path),bytes=await fs.readFile(filename),full=JSON.parse(zlib.gunzipSync(bytes));
  inputOracles.push({slug:query.slug,path:filename,bytes:bytes.length,sha256:sha(bytes)});
  assert.equal(packet.replay.targetTimezone,full.zone,`${query.slug}: target local clock differs`);
  assert.equal(packet.replay.fineIndex,full.fineIndex,`${query.slug}: own native weather point differs`);
  assert.deepEqual(packet.replay.selectedDateIndices,full.selectedIndices,`${query.slug}: representative dates differ`);
  const referenceClock=full.clockBaseOffsetC??Array.from({length:12},()=>[0,0]);
  const corrections=packet.corrections.map((pair,month)=>pair.map((offset,side)=> {
    const clock=packet.replay.physicalResponse.clockOffsets[month*2+side];
    compare(clock,referenceClock[month][side],`${query.slug}/${month+1}/clockOffsetC`);
    return offset-clock+referenceClock[month][side];
  }));
  if(referenceClock.some(pair=>pair.some(value=>Math.abs(value)>1e-8)))targetClockVariants++;
  const reference={...packet,replay:full,corrections};
  const actualMonths=placeMonths(packet),referenceMonths=placeMonths(reference),differences={};
  for(let month=0;month<12;month++)for(const field of physicalFields)differences[`${month+1}/${field}`]=compare(actualMonths[month][field],referenceMonths[month][field],`${query.slug}/${month+1}/${field}`);
  for(const period of periods) {
    const actual=placeNumbers(packet,period),expected=placeNumbers(reference,period);
    for(const field of scoreFields)compare(actual[field],expected[field],`${query.slug}/${period.months.join('-')}/${field}`);
  }
  const usage=loader.cacheUsage();
  assert(usage.correctionBytes<=24*327712&&usage.timezoneBytes<=8*131104&&usage.maximumActiveTileReads<=4,'Decoded tile cache exceeded its operating budget');
  maximumRetainedBytes=Math.max(maximumRetainedBytes,usage.correctionBytes+usage.timezoneBytes);
  rows.push({slug:query.slug,nativeFine:full.fineIndex,targetTimezone:full.zone,monthly:actualMonths,differences});
}
const report={written_by:'AI, GPT-6.1 Sol, 2026.10.18',passes:true,coverage_complete:cases.length===queries.length,
  definition:'Actual createFinePlaceLoader with versioned NWZT/NWFP/NWFT bytes. Compared with original full-hour JSON rebuilt independently through b1_data.build_packet, including separately computed target-clock normal differences. Cold concurrent first20 place requests exercise the bounded tile cache.',
  requested_places:queries.length,compared_places:cases.length,uncompared:queries.filter(query=>!cases.some(record=>record.query.slug===query.slug)).map(query=>query.slug),
  target_clock_variants:targetClockVariants,numeric_comparisons:comparisons,maximum_absolute_difference:maximumDifference,
  cache:{...loader.cacheUsage(),maximum_retained_decoded_tile_bytes:maximumRetainedBytes},
  input_sha256:{pointer:sha(pointerBytes),queries:sha(queryBytes),oracle_manifest:sha(manifestBytes),shared_module:sha(await fs.readFile(new URL('../../hosting/sites/fine-place.mjs',import.meta.url))),decoder:sha(await fs.readFile(new URL('../../hosting/sites/fine-replay.mjs',import.meta.url)))},
  consumed:Object.fromEntries(consumed),oracles:inputOracles,seconds:(performance.now()-started)/1000,rows};
await fs.mkdir(path.dirname(values.output),{recursive:true});await fs.writeFile(values.output,JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify({passes:true,coverage_complete:report.coverage_complete,compared_places:cases.length,target_clock_variants:targetClockVariants,numeric_comparisons:comparisons,maximum_absolute_difference:maximumDifference,cache:report.cache,seconds:report.seconds}));
