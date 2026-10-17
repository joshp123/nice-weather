// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Compare the compact served response with the original full-hour JSON cohort.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import zlib from 'node:zlib';
import {parseArgs} from 'node:util';
import {createFinePlaceLoader,placeMonths,placeNumbers} from '../../hosting/sites/fine-place.mjs';
import {decodeFineReplay} from '../../hosting/sites/fine-replay.mjs';

const {values}=parseArgs({options:{'old-bundle':{type:'string'},compact:{type:'string'},output:{type:'string'},fixtures:{type:'string'}}});
for(const name of ['old-bundle','compact','output'])if(!values[name])throw new Error(`Missing --${name}`);
const hash=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
const readInputs=new Map();
const reader=async key=> {
  try {const bytes=await fs.readFile(path.join(values['old-bundle'],key));readInputs.set(key,hash(bytes));return new Uint8Array(bytes);}
  catch(error){if(error.code==='ENOENT')return null;throw error;}
};
const pointer=JSON.parse(new TextDecoder().decode(await reader('v6/fine/current.json')));
const oldManifest=JSON.parse(new TextDecoder().decode(await reader(`${pointer.nativeRoot}/manifest.json`)));
const compactManifest=JSON.parse(await fs.readFile(path.join(values.compact,'manifest.json')));
const loader=createFinePlaceLoader(reader),rows=[],maximumDifference={};
const monthsFields=['highC','feelsAt21C','dewPointC','wetDaysPercent','sunnyPercent','windKmh','score','niceDayShare'];
const judgementFields=['score','niceDayCount','perfectDayCount','awfulDayCount','sampleDays'];
const packets=[],fixtureInput=[],fixtureExpected=[];
for(const query of oldManifest.queries) {
  const full=await loader.load(query.lat,query.lon);assert(full);
  full.eveningModel='uniform';
  const zoneIndex=compactManifest.timezoneNames.indexOf(full.replay.zone);
  assert(zoneIndex>=0,`No compact timezone for ${query.slug}`);
  const filename=path.join(values.compact,`native/${full.replay.fineIndex}/${zoneIndex}.nwfp.gz`),bytes=await fs.readFile(filename);
  const replay=decodeFineReplay(new Uint8Array(zlib.gunzipSync(bytes)),compactManifest,full.replay.fineIndex,zoneIndex);
  const compact={...full,replay},fullMonths=placeMonths(full),compactMonths=placeMonths(compact);
  for(let month=1;month<=12;month++) {
    const fullJudgement=placeNumbers(full,{kind:'month',month}),compactJudgement=placeNumbers(compact,{kind:'month',month});
    const differences={};
    for(const [prefix,fields,expected,actual]of [['facts',monthsFields,fullMonths[month-1],compactMonths[month-1]],['judgement',judgementFields,fullJudgement,compactJudgement]])for(const field of fields) {
      const name=`${prefix}.${field}`,difference=Math.abs(actual[field]-expected[field]);
      maximumDifference[name]=Math.max(maximumDifference[name]??0,difference);differences[name]=difference;
      assert(difference<=1e-5,`${query.slug}/${month}/${name}: full=${expected[field]}, compact=${actual[field]}`);
    }
    rows.push({slug:query.slug,month,differences});
  }
  packets.push({slug:query.slug,path:filename,bytes:bytes.length,sha256:hash(bytes)});
  if(values.fixtures) {
    const periods=[...Array.from({length:12},(_,index)=>({months:[index+1]})),{months:Array.from({length:12},(_,index)=>index+1)},...[ [12,1,2],[3,4,5],[6,7,8],[9,10,11] ].map(months=>({months}))];
    fixtureInput.push({id:query.slug,packet:compact,periods});
    fixtureExpected.push({id:query.slug,numbers:periods.map(period=>placeNumbers(compact,period)),months:compactMonths});
  }
}
for(const [key,expected]of readInputs)assert.equal(hash(await fs.readFile(path.join(values['old-bundle'],key))),expected,`Input changed: ${key}`);
const report={written_by:'AI, GPT-6.1 Sol, 2026.10.18',passes:true,
  definition:'Actual gzip NWFP bytes decoded through the shared compact response path versus original full-hour JSON packets and frozen correction bytes. Uniform21 globally; native clocks are the same for the thirteen-city cohort.',
  monthly_rows:rows.length,maximum_absolute_difference:maximumDifference,packets,
  correction_tiles_verified_unchanged:[...readInputs.keys()].filter(key=>key.endsWith('.nwf.gz')).length,
  old_input_sha256:Object.fromEntries(readInputs),rows};
await fs.mkdir(path.dirname(values.output),{recursive:true});await fs.writeFile(values.output,JSON.stringify(report,null,2)+'\n');
if(values.fixtures) {
  await fs.mkdir(values.fixtures,{recursive:true});
  const replacer=(_key,value)=>ArrayBuffer.isView(value)?Array.from(value):value;
  for(const [name,records]of [['input',fixtureInput],['expected',fixtureExpected]])await fs.writeFile(path.join(values.fixtures,`${name}.jsonl`),records.map(row=>JSON.stringify(row,replacer)).join('\n')+'\n');
}
console.log(JSON.stringify({passes:true,monthly_rows:rows.length,maximum_absolute_difference:maximumDifference,correction_tiles_verified_unchanged:report.correction_tiles_verified_unchanged}));
