// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Replay actual forecast records through the old Worker and shared place core.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import {execFileSync} from 'node:child_process';
import {pathToFileURL} from 'node:url';
import {parseArgs} from 'node:util';

const {values}=parseArgs({options:{data:{type:'string'},queries:{type:'string'},output:{type:'string'},weighted:{type:'boolean',default:false}}});
for(const name of ['data','queries','output'])if(!values[name])throw new Error(`Missing --${name}`);
const root=path.resolve(new URL('../../',import.meta.url).pathname),hosting=path.join(root,'hosting/sites');
const before=execFileSync('git',['show','633d1ee:hosting/sites/worker.mjs'],{cwd:root,encoding:'utf8'});
const after=await fs.readFile(path.join(hosting,'worker.mjs'),'utf8');
async function importWorker(source,capture) {
  let rewritten=source.replace(/from '\.\/([^']+)'/g,(_,name)=>`from '${pathToFileURL(path.join(hosting,name)).href}'`);
  if(capture)rewritten=rewritten.replace("return placeNumbers({source: 'forecast'","return captureNumbers({source: 'forecast'");
  rewritten+='\nexport {defaultPeriodJudgement as parityJudgement};\n';
  if(capture)rewritten+='export let capturedPacket=null;function captureNumbers(packet,period){capturedPacket=packet;return placeNumbers(packet,period);}\n';
  return import(`data:text/javascript;base64,${Buffer.from(rewritten).toString('base64')}`);
}
const oldWorker=await importWorker(before,false),newWorker=await importWorker(after,true);
const consumed=new Map();
const bucket={async get(key,options){
  if(key==='v6/fine/current.json')return null;
  if(!consumed.has(key))try{consumed.set(key,await fs.readFile(path.join(values.data,key)));}catch(error){if(error.code==='ENOENT')return null;throw error;}
  const all=consumed.get(key),range=options?.range,bytes=range?all.subarray(range.offset,range.offset+range.length):all;
  return {arrayBuffer:async()=>bytes.buffer.slice(bytes.byteOffset,bytes.byteOffset+bytes.length),json:async()=>JSON.parse(bytes.toString()),text:async()=>bytes.toString()};
}};
const queries=JSON.parse(await fs.readFile(values.queries)),fields=['score','totalDays','niceDayCount','perfectDayCount','awfulDayCount'];
const rows=[],inputs=[],expected=[],maximumDifference=Object.fromEntries(fields.map(field=>[field,0]));
const cases=values.weighted?[['Dec-Feb',{kind:'climate',months:[12,1,2]}]]:[['tomorrow',{kind:'forecast',day:1}],['next-14-days',{kind:'forecast'}]];
for(const query of queries)for(const [word,period]of cases) {
  const original=await oldWorker.parityJudgement(bucket,query.lat,query.lon,period);
  const actual=await newWorker.parityJudgement(bucket,query.lat,query.lon,period);
  const id=`${query.slug}/${word}`;
  assert.equal(actual===null,original===null,`${id}: availability differs`);
  if(!actual){rows.push({id,available:false});continue;}
  const differences={};
  for(const field of fields) {
    const difference=Math.abs(actual[field]-original[field]);maximumDifference[field]=Math.max(maximumDifference[field],difference);differences[field]=difference;
    assert(difference<=1e-5,`${id}/${field}: old=${original[field]}, shared=${actual[field]}`);
  }
  const packet=newWorker.capturedPacket;assert(packet,`No actual record packet captured for ${id}`);
  inputs.push({id,packet:{...packet,records:packet.records.map(record=>Array.from(record))},periods:[{totalDays:actual.totalDays}]});
  expected.push({id,numbers:[actual],months:null});
  rows.push({id,available:true,differences});
}
await fs.mkdir(values.output,{recursive:true});
for(const [name,records]of [['input',inputs],['expected',expected]])await fs.writeFile(path.join(values.output,`${name}.jsonl`),records.map(row=>JSON.stringify(row)).join('\n')+'\n');
const sha=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
const report={written_by:'AI, GPT-6.1 Sol, 2026.10.18',passes:true,definition:values.weighted?'The old and shared compact-record calculation on actual retained Dec–Feb climate records, with weights 31/8, 31/8 and 28/8. This checks unequal calendar weights, not a new forecast.':'Actual existing forecast cell/point selection, retained weather records and offsets. The previous Worker judgement is compared with its shared placeNumbers replacement. A missing fine pointer deliberately keeps forecasts on their existing weather resolution.',
  queries:queries.length,compared:inputs.length,unavailable:rows.filter(row=>!row.available).length,maximum_absolute_difference:maximumDifference,
  baseline_worker:'633d1ee:hosting/sites/worker.mjs',source_sha256:{baseline:sha(before),candidate:sha(after)},
  consumed:Object.fromEntries([...consumed].map(([key,bytes])=>[key,{bytes:bytes.length,sha256:sha(bytes)}])),rows};
await fs.writeFile(path.join(values.output,'worker-parity.json'),JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify({passes:report.passes,queries:report.queries,compared:report.compared,unavailable:report.unavailable,maximum_absolute_difference:maximumDifference}));
