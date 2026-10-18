// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Cached all-native-day radiation reference; preserve the prior matched-date diagnostic.
import fs from 'node:fs/promises';
import path from 'node:path';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {parseArgs} from 'node:util';
import {createFinePlaceLoader,placeNumbers} from '../../hosting/sites/fine-place.mjs';
import {fitNamedSunshine,sunshineCoordinateKey} from './named_correction.mjs';
const {values}=parseArgs({options:{targets:{type:'string'},sarah:{type:'string'},goes:{type:'string'},'goes-normal':{type:'string'},native:{type:'string'},tiles:{type:'string'},output:{type:'string'},python:{type:'string'},ssd:{type:'string'}}});
for(const key of ['targets','sarah','goes','goes-normal','native','tiles','output','python','ssd'])if(!values[key])throw Error(`Missing --${key}`);
const targets=JSON.parse(await fs.readFile(values.targets)),indexed=new Map(targets.records.map(row=>[sunshineCoordinateKey(row.latitude,row.longitude),row]));
const controls=[...JSON.parse(await fs.readFile(path.join(values.sarah,'summary.json'))).places.map(row=>({...row,source:'sarah'})),...JSON.parse(await fs.readFile(values['goes-normal'])).places.map(row=>({...row,source:'goes'}))];
const manifest=JSON.parse(await fs.readFile(path.join(values.native,'manifest.json')));
const loader=createFinePlaceLoader(async key=>{
  if(key==='v6/fine/current.json')return Buffer.from(JSON.stringify({nativeRoot:'replay',tileRoot:'temperature',generation:manifest.generation,eveningModel:'uniform'}));
  const prefix=key.startsWith('replay/')?'replay/':'temperature/',folder=prefix==='replay/'?values.native:values.tiles;
  try{return await fs.readFile(path.join(folder,key.slice(prefix.length)));}catch(error){if(error.code==='ENOENT')return null;throw error;}
});
const inputs=[];
for(const control of controls){
  const place=control.place,packet=await loader.load(place.latitude,place.longitude);if(!packet)throw Error('Missing native '+place.slug);
  inputs.push({place,packet,source:control.source,query:{slug:place.slug,raw:path.join(values.sarah,place.slug.replace('/','-')+'-2005-2023.json'),...(control.source==='goes'?{goesRoot:values.goes,latitude:place.latitude,longitude:place.longitude}:{}),timezone:packet.replay.targetTimezone,dates:packet.replay.dates}});
}
const fullNative=JSON.parse(execFileSync(values.python,[fileURLToPath(new URL('./named_native_days.py',import.meta.url)),values.ssd],{input:JSON.stringify(inputs.map(({place,packet})=>({slug:place.slug,fineIndex:packet.replay.fineIndex,timezone:packet.replay.targetTimezone,chunks:manifest.chunks}))),maxBuffer:100*1024*1024}));
for(const row of inputs){
  const full=fullNative.find(value=>value.slug===row.place.slug),replay=row.packet.replay;row.originalPacket=row.packet;
  if(full.dates.length<=96||full.dates.length!==replay.physicalResponse.dates.length)throw Error('Full native daily coverage missing');
  let maxError=0;for(let selected=0;selected<replay.dates.length;selected++){const index=replay.selectedDateIndices[selected];if(full.dates[index]!==replay.dates[selected])throw Error('Date mismatch');for(const field of ['airC','dewC','sunC','shadeC'])for(let hour=0;hour<12;hour++)maxError=Math.max(maxError,Math.abs(full[field][index*12+hour]-replay[field][selected*12+hour]));for(const field of ['rainMm','windKmh','sunnyHours','daylightHours'])maxError=Math.max(maxError,Math.abs(full[field][index]-replay[field][selected]));}
  if(maxError>.0001)throw Error('Full native extraction differs from delivered bytes: '+maxError);row.extractionMaxError=maxError;
  const physical=replay.physicalResponse.values;row.packet={...row.packet,replay:{...replay,...full,highC:full.dates.map((_,i)=>physical[i*6]),lowC:full.dates.map((_,i)=>physical[i*6+1]),selectedIndices:replay.selectedDateIndices}};row.query.dates=full.dates;
}
const satelliteDays=JSON.parse(execFileSync(values.python,[fileURLToPath(new URL('./named_satellite_days.py',import.meta.url))],{input:JSON.stringify(inputs.map(row=>row.query)),maxBuffer:30*1024*1024}));
const fields=['score','niceDayCount','perfectDayCount'];
const compact=number=>Object.fromEntries(['score','niceDayCount','perfectDayCount','awfulDayCount','totalDays','sampleDays'].map(field=>[field,number[field]]));
const result=[];
for(const input of inputs){
  const {place,packet}=input,satellite=satelliteDays.find(row=>row.slug===place.slug),target=indexed.get(sunshineCoordinateKey(place.latitude,place.longitude));
  const available=packet.replay.dates.flatMap((_,index)=>satellite.days[index]?[index]:[]),matched=packet.replay.selectedIndices.filter(index=>satellite.days[index]);
  const monthCounts=indices=>Array.from({length:12},(_,month)=>indices.filter(index=>Number(packet.replay.dates[index].slice(5,7))===month+1).length);
  const counts=monthCounts(available),months=counts.flatMap((count,index)=>count?[index+1]:[]),periods=[{months},...Array.from({length:12},(_,index)=>counts[index]?{months:[index+1]}:null)];
  const predictions=packet.replay.selectedIndices.filter(index=>months.includes(Number(packet.replay.dates[index].slice(5,7))));
  const coefficients=fitNamedSunshine(input.originalPacket,target.months),subset=indices=>({...packet,replay:{...packet.replay,selectedIndices:indices}});
  const fullBefore=subset(predictions),matchedBefore=subset(matched),allBefore=subset(available),allReference={...allBefore,replay:{...allBefore.replay,sunC:Array.from(packet.replay.sunC),sunnyHours:Array.from(packet.replay.sunnyHours)}};
  for(const index of available){
    const hourly=satellite.days[index],daylight=Math.max(1,packet.replay.daylightHours[index]);allReference.replay.sunnyHours[index]=hourly.slice(0,daylight).filter(dni=>dni>=300).length;
    for(let hour=0;hour<12;hour++){
      const at=index*12+hour,air=packet.replay.airC[at],dew=Math.min(air,packet.replay.dewC[at]),vapour=6.105*Math.exp(17.27*dew/(237.7+dew)),wind=(air+.33*vapour-4-packet.replay.shadeC[at])/.7;
      allReference.replay.sunC[at]=packet.replay.shadeC[at]+.105*hourly[hour]/(wind+10);
    }
  }
  const packets={representativeBefore:fullBefore,representativeAfter:{...fullBefore,sunshine:coefficients},matchedRepresentativeBefore:matchedBefore,matchedRepresentativeAfter:{...matchedBefore,sunshine:coefficients},allNativeBefore:allBefore,allNativeAfter:{...allBefore,sunshine:coefficients},allNativeReference:allReference};
  const scenarios=Object.fromEntries(Object.entries(packets).map(([name,current])=>[name,periods.map(period=>period?compact(placeNumbers(current,period)):null)]));
  const errors=Object.fromEntries(Object.keys(scenarios).filter(name=>name!=='allNativeReference').map(name=>[name,Object.fromEntries(fields.map(field=>[field,months.reduce((sum,month)=>sum+Math.abs(scenarios[name][month][field]-scenarios.allNativeReference[month][field]),0)/months.length]))]));
  result.push({place,source:input.source,extractionMaxError:input.extractionMaxError,availableMonths:months,aggregateDays:scenarios.allNativeReference[0].totalDays,allNativeAvailableSamples:available.length,allNativeMonthlySamples:counts,representativeMonthlySamples:monthCounts(predictions),matchedRepresentativeMonthlySamples:monthCounts(matched),nativeDates:packet.replay.dates,availableIndices:available,scenarios,monthlyMeanAbsoluteErrorsAgainstAllNativeReference:errors});
}
const report={writtenBy:'AI, GPT-6.1 Sol, 2026-10-18',inputs:values,purpose:'Sensitivity diagnostic, not a new fit or full observed weather. All native days with cached satellite radiation define the reference. Native air, temperature anchors, dew point, rain and wind are identical. Monthly coefficients remain fitted to the original eight representative days. All-native before/after/reference use identical available dates; published representative predictions use all eight dates per available month and therefore differ in year selection. Matched representative predictions use only exact available dates as in the earlier diagnostic. SARAH source covers2005–2023; GOES only2022/2024, with native coverageJan–Sep. The score field is typicalScore (weighted-median-day band rule), not an arithmetic mean.',places:result};
await fs.writeFile(values.output,JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify(result.map(row=>({name:row.place.validationName,days:row.aggregateDays,samples:row.allNativeAvailableSamples,errors:row.monthlyMeanAbsoluteErrorsAgainstAllNativeReference})),null,2));
