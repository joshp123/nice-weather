// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Actual fitted satellite packets through the JavaScript and Go place functions.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {parseArgs} from 'node:util';
import {execFileSync} from 'node:child_process';
import {openSync,closeSync} from 'node:fs';
import crypto from 'node:crypto';
import {createFinePlaceLoader,placeNumbers,placeMonths,placeSampleScores,encodeFinePlacePacket,decodeFinePlacePacket} from '../../hosting/sites/fine-place.mjs';
import {fitNamedSunshine,sunshineCoordinateKey} from './named_correction.mjs';

const {values}=parseArgs({options:{queries:{type:'string'},targets:{type:'string'},native:{type:'string'},tiles:{type:'string'},output:{type:'string'}}});
for(const key of ['queries','targets','native','tiles','output'])assert(values[key],`Missing --${key}`);
await fs.mkdir(values.output,{recursive:true});
const targetBytes=await fs.readFile(values.targets),targets=JSON.parse(targetBytes),indexed=new Map(targets.records.map(row=>[sunshineCoordinateKey(row.latitude,row.longitude),row]));
const manifest=JSON.parse(await fs.readFile(path.join(values.native,'manifest.json')));
const loader=createFinePlaceLoader(async key=>{
  if(key==='v6/fine/current.json')return Buffer.from(JSON.stringify({nativeRoot:'replay',tileRoot:'temperature',generation:manifest.generation,eveningModel:'uniform'}));
  const prefix=key.startsWith('replay/')?'replay/':'temperature/',folder=prefix==='replay/'?values.native:values.tiles;
  try{return await fs.readFile(path.join(folder,key.slice(prefix.length)));}catch(error){if(error.code==='ENOENT')return null;throw error;}
});
const periods=[...Array.from({length:12},(_,index)=>({months:[index+1]})),{months:Array.from({length:12},(_,index)=>index+1)},...[[12,1,2],[3,4,5],[6,7,8],[9,10,11],[11,12,1,2,3]].map(months=>({months}))];
const fields=['meanScore','score','band','niceDayShare','perfectDayShare','awfulDayShare','totalDays','sampleDays','parts','typical'];
const inputs=[],expected=[];let preservedPhysicalFields=0,transportRoundTrips=0;
for(const place of JSON.parse(await fs.readFile(values.queries))){
  const packet=await loader.load(place.latitude,place.longitude);assert(packet,`No native replay ${place.slug}`);
  const source=indexed.get(sunshineCoordinateKey(place.latitude,place.longitude));assert(source,`No satellite target ${place.slug}`);
  const originalMonths=placeMonths(packet);packet.sunshine=fitNamedSunshine(packet,source.months);
  const decoded=decodeFinePlacePacket(encodeFinePlacePacket(packet));assert.deepEqual(placeSampleScores(decoded),placeSampleScores(packet));transportRoundTrips++;
  for(const [scenario,current]of [['fitted',packet],['transported',decoded]]){
    const months=placeMonths(current),numbers=periods.map(period=>placeNumbers(current,period));
    const unchangedMonths=scenario==='fitted'?originalMonths:placeMonths({...current,sunshine:undefined});
    for(let month=0;month<12;month++)for(const field of ['highC','feelsAt21C','dewPointC','wetDaysPercent','windKmh']){assert.equal(months[month][field],unchangedMonths[month][field]);preservedPhysicalFields++;}
    const id=place.slug+'/'+scenario;inputs.push({id,packet:current,periods});
    expected.push({id,numbers:numbers.map(number=>Object.fromEntries(fields.map(field=>[field,number[field]]))),months,displayedCounts:numbers.map(number=>['niceDayCount','perfectDayCount','awfulDayCount'].map(field=>number[field]))});
  }
}
const inputFile=path.join(values.output,'input.jsonl'),expectedFile=path.join(values.output,'expected.jsonl'),goFile=path.join(values.output,'go-results.jsonl');
const replacer=(_,value)=>ArrayBuffer.isView(value)?Array.from(value):value;
await fs.writeFile(inputFile,inputs.map(value=>JSON.stringify(value,replacer)).join('\n')+'\n');await fs.writeFile(expectedFile,expected.map(value=>JSON.stringify(value)).join('\n')+'\n');
const outputHandle=openSync(goFile,'w');try{execFileSync('go',['run','./research/fine/outlook/port'],{input:await fs.readFile(inputFile),maxBuffer:128*1024*1024,stdio:['pipe',outputHandle,'inherit']});}finally{closeSync(outputHandle);}
const actual=(await fs.readFile(goFile,'utf8')).trim().split('\n').map(line=>{const row=JSON.parse(line);row.displayedCounts=row.numbers.map(number=>['niceDayCount','perfectDayCount','awfulDayCount'].map(field=>Math.round(number[field])));return row;});
const displayedFile=path.join(values.output,'go-displayed-results.jsonl');await fs.writeFile(displayedFile,actual.map(value=>JSON.stringify(value)).join('\n')+'\n');
execFileSync(process.execPath,['research/fine/outlook/compare_port.mjs',expectedFile,displayedFile,path.join(values.output,'parity.json')],{stdio:'inherit'});
const report={writtenBy:'AI, GPT-6.1 Sol, 2026-10-18',passes:true,purpose:'Actual fitted final satellite targets through native-pixel replay, shared JavaScript/Go functions and binary packet transport; separate from source accuracy.',places:inputs.length/2,periodsPerPacket:periods.length,transportRoundTrips,preservedPhysicalFields,satelliteSha256:crypto.createHash('sha256').update(targetBytes).digest('hex'),preexistingCountRepresentation:'Go fractional counts are rounded for the same displayed integer comparison used by JavaScript.'};
await fs.writeFile(path.join(values.output,'model-proof.json'),JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify(report));
