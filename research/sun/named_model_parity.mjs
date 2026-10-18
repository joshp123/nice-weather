// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Runtime arithmetic proof only: synthetic coefficients are not satellite validation.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import {openSync,closeSync} from 'node:fs';
import {execFileSync} from 'node:child_process';
import {parseArgs} from 'node:util';
import {fileURLToPath} from 'node:url';
import {createFinePlaceLoader,placeNumbers,placeMonths,placeSampleScores,encodeFinePlacePacket,decodeFinePlacePacket} from '../../hosting/sites/fine-place.mjs';

const {values}=parseArgs({options:{input:{type:'string'},queries:{type:'string'},native:{type:'string'},tiles:{type:'string'},output:{type:'string'},baseline:{type:'string',default:'HEAD'}}});
for(const name of ['input','output'])assert(values[name],`Missing --${name}`);
await fs.mkdir(values.output,{recursive:true});
const root=fileURLToPath(new URL('../../',import.meta.url));
const baselineRevision=execFileSync('git',['rev-parse',values.baseline],{cwd:root,encoding:'utf8'}).trim();
const baselineSource=execFileSync('git',['show',`${baselineRevision}:hosting/sites/fine-place.mjs`],{cwd:root,encoding:'utf8'})
  .replaceAll("'./fine-replay.mjs'",JSON.stringify(new URL('../../hosting/sites/fine-replay.mjs',import.meta.url).href))
  .replaceAll("'./day-counts.mjs'",JSON.stringify(new URL('../../hosting/sites/day-counts.mjs',import.meta.url).href));
const baseline=await import(`data:text/javascript;base64,${Buffer.from(baselineSource).toString('base64')}`);
const sourceInputs=(await fs.readFile(values.input,'utf8')).trim().split('\n').map(line=>JSON.parse(line));
const retainedPlaces=sourceInputs.length;
let extraPlaces=0;
if(values.queries){
  for(const name of ['native','tiles'])assert(values[name],`--queries requires --${name}`);
  const manifest=JSON.parse(await fs.readFile(path.join(values.native,'manifest.json')));
  const loader=createFinePlaceLoader(async key=>{
    if(key==='v6/fine/current.json')return Buffer.from(JSON.stringify({nativeRoot:'replay',tileRoot:'temperature',generation:manifest.generation,eveningModel:'uniform',status:'parity-only'}));
    const prefix=key.startsWith('replay/')?'replay/':'temperature/',folder=prefix==='replay/'?values.native:values.tiles;
    try{return await fs.readFile(path.join(folder,key.slice(prefix.length)));}catch(error){if(error.code==='ENOENT')return null;throw error;}
  });
  for(const query of JSON.parse(await fs.readFile(values.queries,'utf8'))){
    const packet=await loader.load(query.lat??query.latitude,query.lon??query.longitude);
    assert(packet,`No retained replay at ${query.slug}`);
    sourceInputs.push({id:`target-${query.slug}`,packet,periods:sourceInputs[0].periods});extraPlaces++;
  }
}
const numberFields=['meanScore','score','band','niceDayShare','perfectDayShare','awfulDayShare','totalDays','sampleDays','parts','typical'];
const inputFile=path.join(values.output,'input.jsonl'),expectedFile=path.join(values.output,'expected.jsonl');
await fs.writeFile(inputFile,'');await fs.writeFile(expectedFile,'');
let unchanged=0,temperaturePreserved=0,transportRoundTrips=0;
const replacer=(_,value)=>ArrayBuffer.isView(value)?Array.from(value):value;
for(const input of sourceInputs){
  const original=input.packet,baselineMonths=baseline.placeMonths(original);
  const coefficients=Array.from({length:12},(_,month)=>month%4===0?null:{
    sunnyShareOffset:month%3===0?.18:-.55+month*.02,
    dniScale:Array.from({length:12},(_,hour)=>.2+((month+hour)%8)*.13),
    sunnyShare:.15+month*.05,
  });
  const scenarios=[['absent',original],['null',{...original,sunshine:Array(12).fill(null)}],['synthetic',{...original,sunshine:coefficients}]];
  if(extraPlaces&&input.id.startsWith('target-'))scenarios.push(['expanded',{...original,replay:{...original.replay,physicalResponse:undefined},sunshine:coefficients}]);
  for(const [scenario,packet]of scenarios){
    const months=placeMonths(packet),numbers=input.periods.map(period=>placeNumbers(packet,period));
    const unchangedMonths=scenario==='expanded'?baseline.placeMonths({...packet,sunshine:undefined}):baselineMonths;
    if(scenario==='absent'||scenario==='null'){
      assert.deepEqual(months,baselineMonths);
      assert.deepEqual(numbers,input.periods.map(period=>baseline.placeNumbers(original,period)));
      assert.deepEqual(placeSampleScores(packet),baseline.placeSampleScores(original));unchanged++;
    }
    for(let month=0;month<12;month++){
      for(const field of ['highC','feelsAt21C','dewPointC','wetDaysPercent','windKmh']){
        assert.equal(months[month][field],unchangedMonths[month][field]);
        temperaturePreserved++;
      }
      if(packet.sunshine?.[month])assert.equal(months[month].sunnyPercent,100*packet.sunshine[month].sunnyShare);
    }
    const transportPacket={...packet,replay:{...packet.replay,generation:packet.replay.generation??'parity-only'}};
    if(scenario==='absent')assert.deepEqual(encodeFinePlacePacket(transportPacket),baseline.encodeFinePlacePacket(transportPacket));
    const decoded=decodeFinePlacePacket(encodeFinePlacePacket(transportPacket));
    assert.deepEqual(placeSampleScores(decoded),placeSampleScores(packet));transportRoundTrips++;
    const expected={id:`${input.id}/${scenario}`,numbers:numbers.map(number=>Object.fromEntries(numberFields.map(field=>[field,number[field]]))),months,
      displayedCounts:numbers.map(number=>['niceDayCount','perfectDayCount','awfulDayCount'].map(field=>number[field]))};
    await fs.appendFile(inputFile,JSON.stringify({id:expected.id,packet,periods:input.periods},replacer)+'\n');
    await fs.appendFile(expectedFile,JSON.stringify(expected)+'\n');
  }
}
const goFile=path.join(values.output,'go-results.jsonl');
const goOutput=openSync(goFile,'w');
try{execFileSync('go',['run','./research/fine/outlook/port'],{cwd:root,input:await fs.readFile(inputFile),maxBuffer:128*1024*1024,stdio:['pipe',goOutput,'inherit']});}finally{closeSync(goOutput);}
const actualLines=(await fs.readFile(goFile,'utf8')).trim().split('\n').map(line=>{
  const value=JSON.parse(line);value.displayedCounts=value.numbers.map(number=>['niceDayCount','perfectDayCount','awfulDayCount'].map(field=>Math.round(number[field])));return JSON.stringify(value);
});
const displayedFile=path.join(values.output,'go-displayed-results.jsonl');await fs.writeFile(displayedFile,actualLines.join('\n')+'\n');
execFileSync(process.execPath,[path.join(root,'research/fine/outlook/compare_port.mjs'),expectedFile,displayedFile,path.join(values.output,'parity.json')],{stdio:'inherit'});
const report={writtenBy:'AI, GPT-6.1 Sol, 2026-10-18',passes:true,purpose:'Arithmetic parity only. Coefficients are fabricated to exercise negative and positive shifts, clamping, hourly DNI ratios and uncovered months; they are not observations or source validation.',retainedPlaces,extraPlaces,unchangedPacketScenarios:unchanged,preservedPhysicalFields:temperaturePreserved,transportRoundTrips,preexistingCountDifference:'Go stores fractional weighted counts; JS stores rounded displayed counts. This proof compares the displayed rounded counts and does not change that existing representation.',baselineRevision,inputSha256:crypto.createHash('sha256').update(await fs.readFile(values.input)).digest('hex')};
await fs.writeFile(path.join(values.output,'model-proof.json'),JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify(report));
